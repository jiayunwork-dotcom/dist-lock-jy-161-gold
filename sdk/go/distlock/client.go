package distlock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Client struct {
	servers    []string
	leaderAddr string
	clientID   string
	httpClient *http.Client
	mu         sync.RWMutex
	locks      map[string]*LockHandle
	heartbeatCh chan struct{}
	fallbackMode bool
	maxRetries int
}

type LockHandle struct {
	Namespace string
	Name      string
	Token     uint64
	LeaseTime time.Duration
	stopHeartbeat context.CancelFunc
	valid     bool
	mu        sync.RWMutex
}

type ClientConfig struct {
	Servers      []string
	ClientID     string
	FallbackMode bool
	MaxRetries   int
	HTTPClient   *http.Client
}

type AcquireOptions struct {
	LeaseTime   time.Duration
	WaitTimeout time.Duration
	QueueMode   string
	Priority    int
	TryLock     bool
	Capacity    int
	Mode        string
}

type response struct {
	Success   bool      `json:"success"`
	Token     uint64    `json:"token"`
	Error     string    `json:"error"`
	ExpiresAt time.Time `json:"expires_at"`
	Leader    string    `json:"leader"`
	Redirect  bool      `json:"redirect"`
}

func NewClient(config ClientConfig) *Client {
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	c := &Client{
		servers:    config.Servers,
		clientID:   config.ClientID,
		httpClient: config.HTTPClient,
		locks:      make(map[string]*LockHandle),
		heartbeatCh: make(chan struct{}),
		fallbackMode: config.FallbackMode,
		maxRetries: config.MaxRetries,
	}

	go c.discoverLeader()
	return c
}

func (c *Client) discoverLeader() {
	for _, server := range c.servers {
		resp, err := c.httpClient.Get(server + "/api/v1/leader")
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		var leaderResp struct {
			Leader   string `json:"leader"`
			IsLeader bool   `json:"is_leader"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&leaderResp); err == nil {
			if leaderResp.IsLeader {
				c.mu.Lock()
				c.leaderAddr = server
				c.mu.Unlock()
				return
			} else if leaderResp.Leader != "" {
				c.mu.Lock()
				c.leaderAddr = leaderResp.Leader
				c.mu.Unlock()
				return
			}
		}
	}

	c.mu.Lock()
	if len(c.servers) > 0 {
		c.leaderAddr = c.servers[0]
	}
	c.mu.Unlock()
}

func (c *Client) getLeader() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.leaderAddr
}

func (c *Client) setLeader(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leaderAddr = addr
}

func (c *Client) request(method, path string, body interface{}) (*response, error) {
	var bodyBytes []byte
	var err error
	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	var lastErr error
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		leader := c.getLeader()
		if leader == "" {
			if c.fallbackMode {
				return &response{Success: true, Token: 1}, nil
			}
			return nil, fmt.Errorf("no leader available")
		}

		url := leader + path
		var req *http.Request
		if bodyBytes != nil {
			req, err = http.NewRequest(method, url, bytes.NewReader(bodyBytes))
		} else {
			req, err = http.NewRequest(method, url, nil)
		}
		if err != nil {
			lastErr = err
			c.discoverLeader()
			time.Sleep(time.Duration(attempt*attempt) * 100 * time.Millisecond)
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			c.discoverLeader()
			time.Sleep(time.Duration(attempt*attempt) * 100 * time.Millisecond)
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result response
		if err := json.Unmarshal(respBody, &result); err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt*attempt) * 100 * time.Millisecond)
			continue
		}

		if result.Redirect && result.Leader != "" {
			c.setLeader(result.Leader)
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error: %d", resp.StatusCode)
			c.discoverLeader()
			time.Sleep(time.Duration(attempt*attempt) * 100 * time.Millisecond)
			continue
		}

		return &result, nil
	}

	if c.fallbackMode {
		return &response{Success: true, Token: 1}, nil
	}

	return nil, fmt.Errorf("all attempts failed: %v", lastErr)
}

func (c *Client) Acquire(ctx context.Context, namespace, name, lockType string, opts *AcquireOptions) (*LockHandle, error) {
	if opts == nil {
		opts = &AcquireOptions{
			LeaseTime: 30 * time.Second,
			Mode:      "write",
		}
	}

	body := map[string]interface{}{
		"namespace":   namespace,
		"name":        name,
		"type":        lockType,
		"mode":        opts.Mode,
		"client_id":   c.clientID,
		"lease_time":  opts.LeaseTime,
		"wait_timeout": opts.WaitTimeout,
		"queue_mode":  opts.QueueMode,
		"priority":    opts.Priority,
		"try_lock":    opts.TryLock,
		"capacity":    opts.Capacity,
	}

	resp, err := c.request("POST", "/api/v1/locks/acquire", body)
	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("failed to acquire lock: %s", resp.Error)
	}

	handle := &LockHandle{
		Namespace: namespace,
		Name:      name,
		Token:     resp.Token,
		LeaseTime: opts.LeaseTime,
		valid:     true,
	}

	heartbeatCtx, cancel := context.WithCancel(ctx)
	handle.stopHeartbeat = cancel

	go c.startHeartbeat(heartbeatCtx, handle)

	key := namespace + "/" + name
	c.mu.Lock()
	c.locks[key] = handle
	c.mu.Unlock()

	return handle, nil
}

func (c *Client) startHeartbeat(ctx context.Context, handle *LockHandle) {
	interval := handle.LeaseTime / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			handle.mu.RLock()
			if !handle.valid {
				handle.mu.RUnlock()
				return
			}
			token := handle.Token
			handle.mu.RUnlock()

			body := map[string]interface{}{
				"namespace": handle.Namespace,
				"name":      handle.Name,
				"client_id": c.clientID,
				"token":     token,
			}

			resp, err := c.request("POST", "/api/v1/locks/heartbeat", body)
			if err != nil {
				c.discoverLeader()
				continue
			}

			if !resp.Success {
				handle.mu.Lock()
				handle.valid = false
				handle.mu.Unlock()
				return
			}
		}
	}
}

func (c *Client) Release(ctx context.Context, handle *LockHandle) error {
	handle.mu.Lock()
	if !handle.valid {
		handle.mu.Unlock()
		return fmt.Errorf("lock already released or invalid")
	}
	token := handle.Token
	handle.valid = false
	if handle.stopHeartbeat != nil {
		handle.stopHeartbeat()
	}
	handle.mu.Unlock()

	body := map[string]interface{}{
		"namespace": handle.Namespace,
		"name":      handle.Name,
		"client_id": c.clientID,
		"token":     token,
	}

	resp, err := c.request("POST", "/api/v1/locks/release", body)
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to release lock: %s", resp.Error)
	}

	key := handle.Namespace + "/" + handle.Name
	c.mu.Lock()
	delete(c.locks, key)
	c.mu.Unlock()

	return nil
}

func (h *LockHandle) IsValid() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.valid
}

func (h *LockHandle) GetToken() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Token
}

func (h *LockHandle) ValidateToken(token uint64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return token >= h.Token
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, handle := range c.locks {
		if handle.stopHeartbeat != nil {
			handle.stopHeartbeat()
		}
		handle.mu.Lock()
		handle.valid = false
		handle.mu.Unlock()
	}
	c.locks = make(map[string]*LockHandle)
}
