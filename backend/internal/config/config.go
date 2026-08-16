package config

import (
	"os"
	"strconv"
	"time"

	"github.com/distributed-lock/backend/internal/lock"
)

type ServerConfig struct {
	NodeID       string
	HTTPAddr     string
	RaftAddr     string
	DataDir      string
	Peers        map[string]string
	Bootstrap    bool
	LockConfig   lock.Config
	AlertRules   []AlertRule
	WebhookURLs  []string
	AdminToken   string
}

type AlertRule struct {
	ID           string
	Name         string
	Condition    string
	Threshold    float64
	Duration     time.Duration
	Enabled      bool
}

func Load() (*ServerConfig, error) {
	cfg := &ServerConfig{
		NodeID:    getEnv("NODE_ID", "node1"),
		HTTPAddr:  getEnv("HTTP_ADDR", ":8080"),
		RaftAddr:  getEnv("RAFT_ADDR", ":7000"),
		DataDir:   getEnv("DATA_DIR", "./data"),
		Bootstrap: getEnvBool("BOOTSTRAP", false),
		Peers:     make(map[string]string),
		LockConfig: lock.DefaultConfig(),
		AdminToken: getEnv("ADMIN_TOKEN", "admin-secret"),
	}

	peers := os.Getenv("PEERS")
	if peers == "" {
		peers = os.Getenv("RAFT_PEERS")
	}
	if peers != "" {
		for i, p := range splitAndTrim(peers, ",") {
			parts := splitAndTrim(p, "=")
			if len(parts) == 2 {
				cfg.Peers[parts[0]] = parts[1]
			} else if len(parts) == 1 {
				addr := parts[0]
				var nodeID string
				addrParts := splitAndTrim(addr, ":")
				if len(addrParts) >= 1 {
					host := addrParts[0]
					hostParts := splitAndTrim(host, ".")
					nodeID = hostParts[0]
				}
				if nodeID == "" {
					nodeID = "node" + strconv.Itoa(i+1)
				}
				cfg.Peers[nodeID] = addr
			}
		}
	}

	if leaseTime := getEnvDuration("DEFAULT_LEASE_TIME", 0); leaseTime > 0 {
		cfg.LockConfig.DefaultLeaseTime = leaseTime
	} else if leaseTime := getEnvDuration("LEASE_TTL", 0); leaseTime > 0 {
		cfg.LockConfig.DefaultLeaseTime = leaseTime
	}
	if minLease := getEnvDuration("MIN_LEASE_TIME", 0); minLease > 0 {
		cfg.LockConfig.MinLeaseTime = minLease
	}
	if maxLease := getEnvDuration("MAX_LEASE_TIME", 0); maxLease > 0 {
		cfg.LockConfig.MaxLeaseTime = maxLease
	}
	if deadlockInterval := getEnvDuration("DEADLOCK_CHECK_INTERVAL", 0); deadlockInterval > 0 {
		cfg.LockConfig.DeadlockCheckInterval = deadlockInterval
	} else if deadlockInterval := getEnvDuration("DEADLOCK_DETECT_INTERVAL", 0); deadlockInterval > 0 {
		cfg.LockConfig.DeadlockCheckInterval = deadlockInterval
	}
	if strategy := os.Getenv("DEADLOCK_STRATEGY"); strategy != "" {
		cfg.LockConfig.DeadlockStrategy = lock.DeadlockStrategy(strategy)
	}

	cfg.AlertRules = []AlertRule{
		{
			ID:        "lock_hold_timeout",
			Name:      "Lock Hold Time Exceeded",
			Condition: "lock_hold_time",
			Threshold: getEnvFloat("ALERT_LOCK_HOLD_THRESHOLD", 300),
			Duration:  getEnvDuration("ALERT_LOCK_HOLD_DURATION", 60*time.Second),
			Enabled:   true,
		},
		{
			ID:        "wait_queue_length",
			Name:      "Wait Queue Too Long",
			Condition: "wait_queue_length",
			Threshold: getEnvFloat("ALERT_QUEUE_THRESHOLD", 10),
			Duration:  getEnvDuration("ALERT_QUEUE_DURATION", 30*time.Second),
			Enabled:   true,
		},
		{
			ID:        "deadlock_frequency",
			Name:      "Deadlock Frequency High",
			Condition: "deadlock_frequency",
			Threshold: getEnvFloat("ALERT_DEADLOCK_THRESHOLD", 5),
			Duration:  getEnvDuration("ALERT_DEADLOCK_DURATION", 5*time.Minute),
			Enabled:   true,
		},
		{
			ID:        "node_offline",
			Name:      "Node Offline",
			Condition: "node_offline",
			Threshold: 1,
			Duration:  getEnvDuration("ALERT_NODE_OFFLINE_DURATION", 10*time.Second),
			Enabled:   true,
		},
	}

	if webhooks := os.Getenv("WEBHOOK_URLS"); webhooks != "" {
		cfg.WebhookURLs = splitAndTrim(webhooks, ",")
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultValue
}

func splitAndTrim(s, sep string) []string {
	var result []string
	for _, part := range split(s, sep) {
		result = append(result, part)
	}
	return result
}

func split(s, sep string) []string {
	if s == "" {
		return nil
	}
	result := make([]string, 0)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep[0] {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		result = append(result, s[start:])
	}
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
