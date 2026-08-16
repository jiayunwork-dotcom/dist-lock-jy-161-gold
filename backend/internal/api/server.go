package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/distributed-lock/backend/internal/config"
	"github.com/distributed-lock/backend/internal/lock"
	"github.com/distributed-lock/backend/internal/ratelimit"
	raftpkg "github.com/distributed-lock/backend/internal/raft"
	"github.com/distributed-lock/backend/internal/metrics"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

type Server struct {
	router        *mux.Router
	lockMgr       *lock.LockManager
	raftMgr       *raftpkg.RaftManager
	rateLimitMgr  *ratelimit.RateLimitManager
	metrics       *metrics.PrometheusCollector
	cfg           *config.ServerConfig
	logger        *zap.Logger
	adminOpsLog   []AdminOperation
	alertMgr      *AlertManager
	mu            sync.RWMutex
}

type AdminOperation struct {
	Timestamp   time.Time
	Operator    string
	Action      string
	LockID      string
	Details     string
}

type APIError struct {
	Error string `json:"error"`
}

func NewServer(lockMgr *lock.LockManager, raftMgr *raftpkg.RaftManager,
	rateLimitMgr *ratelimit.RateLimitManager, metrics *metrics.PrometheusCollector, cfg *config.ServerConfig, logger *zap.Logger) *Server {

	s := &Server{
		router:        mux.NewRouter(),
		lockMgr:       lockMgr,
		raftMgr:       raftMgr,
		rateLimitMgr:  rateLimitMgr,
		metrics:       metrics,
		cfg:           cfg,
		logger:        logger,
		adminOpsLog:   make([]AdminOperation, 0, 1000),
	}

	s.alertMgr = NewAlertManager(cfg, lockMgr, raftMgr, logger, metrics)
	s.setupRoutes()

	return s
}

func (s *Server) setupRoutes() {
	api := s.router.PathPrefix("/api/v1").Subrouter()

	api.HandleFunc("/health", s.handleHealth).Methods("GET")
	api.HandleFunc("/leader", s.handleGetLeader).Methods("GET")
	api.HandleFunc("/cluster", s.handleClusterStatus).Methods("GET")

	lockApi := api.PathPrefix("/locks").Subrouter()
	lockApi.HandleFunc("", s.handleListLocks).Methods("GET")
	lockApi.HandleFunc("/acquire", s.handleAcquire).Methods("POST")
	lockApi.HandleFunc("/release", s.handleRelease).Methods("POST")
	lockApi.HandleFunc("/heartbeat", s.handleHeartbeat).Methods("POST")
	lockApi.HandleFunc("/{namespace}/{name}", s.handleGetLock).Methods("GET")
	lockApi.HandleFunc("/{namespace}/{name}/adjust-lease", s.handleAdjustLease).Methods("POST")
	lockApi.HandleFunc("/{namespace}/{name}/adjust-capacity", s.handleAdjustCapacity).Methods("POST")
	lockApi.HandleFunc("/{namespace}/{name}/clear-queue", s.handleClearQueue).Methods("POST")
	lockApi.HandleFunc("/{namespace}/{name}/force-release", s.handleForceRelease).Methods("POST")

	depApi := api.PathPrefix("/dependencies").Subrouter()
	depApi.HandleFunc("", s.handleGetDependencies).Methods("GET")
	depApi.HandleFunc("/register", s.handleRegisterDependency).Methods("POST")
	depApi.HandleFunc("/remove", s.handleRemoveDependency).Methods("POST")
	depApi.HandleFunc("/graph", s.handleGetDependencyGraph).Methods("GET")

	groupApi := api.PathPrefix("/groups").Subrouter()
	groupApi.HandleFunc("", s.handleListGroups).Methods("GET")
	groupApi.HandleFunc("/create", s.handleCreateGroup).Methods("POST")
	groupApi.HandleFunc("/{name}", s.handleGetGroup).Methods("GET")
	groupApi.HandleFunc("/{name}/delete", s.handleDeleteGroup).Methods("POST")
	groupApi.HandleFunc("/{name}/add-lock", s.handleAddLockToGroup).Methods("POST")
	groupApi.HandleFunc("/{name}/remove-lock", s.handleRemoveLockFromGroup).Methods("POST")
	groupApi.HandleFunc("/{name}/batch-acquire", s.handleBatchAcquire).Methods("POST")
	groupApi.HandleFunc("/{name}/batch-release", s.handleBatchRelease).Methods("POST")

	adminApi := api.PathPrefix("/admin").Subrouter()
	adminApi.Use(s.adminAuthMiddleware)
	adminApi.HandleFunc("/operations", s.handleListOperations).Methods("GET")
	adminApi.HandleFunc("/alerts", s.handleListAlerts).Methods("GET")
	adminApi.HandleFunc("/alerts/configure", s.handleConfigureAlerts).Methods("POST")
	adminApi.HandleFunc("/cluster/add-peer", s.handleAddPeer).Methods("POST")
	adminApi.HandleFunc("/cluster/remove-peer", s.handleRemovePeer).Methods("POST")

	api.HandleFunc("/ratelimit/rules", s.handleListRateLimitRules).Methods("GET")
	api.HandleFunc("/ratelimit/rules", s.handleCreateRateLimitRule).Methods("POST")
	api.HandleFunc("/ratelimit/rules/{key}", s.handleGetRateLimitRule).Methods("GET")
	api.HandleFunc("/ratelimit/rules/{key}", s.handleUpdateRateLimitRule).Methods("PUT")
	api.HandleFunc("/ratelimit/rules/{key}", s.handleDeleteRateLimitRule).Methods("DELETE")
	api.HandleFunc("/ratelimit/rules/{key}/adjust-quota", s.handleRateLimitAdjustQuota).Methods("POST")
	api.HandleFunc("/ratelimit/rules/{key}/adaptive-history", s.handleGetAdaptiveHistory).Methods("GET")
	api.HandleFunc("/ratelimit/rules/{key}/quota-multiplier", s.handleGetQuotaMultiplier).Methods("GET")
	api.HandleFunc("/ratelimit/rules/{key}/check-adaptive", s.handleCheckAndAdjustQuota).Methods("POST")
	api.HandleFunc("/ratelimit/check", s.handleRateLimitCheck).Methods("POST")
	api.HandleFunc("/ratelimit/events", s.handleListRateLimitEvents).Methods("GET")
	api.HandleFunc("/ratelimit/monitor/stats", s.handleRateLimitMonitorStats).Methods("GET")
	api.HandleFunc("/ratelimit/monitor/top-rejected", s.handleRateLimitTopRejected).Methods("GET")
	api.HandleFunc("/ratelimit/latency", s.handleReportLatency).Methods("POST")
	api.HandleFunc("/ratelimit/borrow", s.handleBorrowTokens).Methods("POST")
	api.HandleFunc("/ratelimit/repay", s.handleRepayTokens).Methods("POST")
	api.HandleFunc("/ratelimit/namespaces/{namespace}/borrow-flow", s.handleGetBorrowFlow).Methods("GET")
	api.HandleFunc("/ratelimit/namespaces/{namespace}/rules", s.handleGetNamespaceRules).Methods("GET")

	s.router.Handle("/metrics", promhttp.Handler()).Methods("GET")
}

func (s *Server) adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token != s.cfg.AdminToken {
			s.writeError(w, http.StatusUnauthorized, "invalid admin token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Start(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.router,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	go s.alertMgr.Start(ctx)
	go s.startLeaseChecker(ctx)
	go s.startQueueLengthReporter(ctx)

	s.logger.Info("HTTP server starting", zap.String("addr", addr))
	return srv.ListenAndServe()
}

func (s *Server) startLeaseChecker(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expired := s.lockMgr.CheckLeases()
			for lockID, clients := range expired {
				s.logger.Info("Expired leases detected",
					zap.String("lock", lockID),
					zap.Strings("clients", clients))
			}
		}
	}
}

func (s *Server) startQueueLengthReporter(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			locks := s.lockMgr.ListLocks("")
			for _, l := range locks {
				l.Mu.RLock()
				s.metrics.SetQueueLength(l.ID.String(), string(l.Type), len(l.WaitQueue))
				l.Mu.RUnlock()
			}
		}
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"node_id":   s.cfg.NodeID,
		"is_leader": s.raftMgr.IsLeader(),
		"leader":    s.raftMgr.GetLeader(),
		"state":     s.raftMgr.GetState(),
	})
}

func (s *Server) handleGetLeader(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"leader": s.raftMgr.GetLeader(),
		"is_leader": s.raftMgr.IsLeader(),
	})
}

func (s *Server) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.raftMgr.GetConfiguration()
	servers := make([]map[string]interface{}, 0)
	for _, svr := range cfg.Servers {
		servers = append(servers, map[string]interface{}{
			"id":      string(svr.ID),
			"address": string(svr.Address),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"node_id":       s.cfg.NodeID,
		"state":         s.raftMgr.GetState(),
		"leader":        s.raftMgr.GetLeader(),
		"last_index":    s.raftMgr.GetLastIndex(),
		"applied_index": s.raftMgr.GetAppliedIndex(),
		"servers":       servers,
	})
}

type AcquireRequest struct {
	Namespace   string        `json:"namespace"`
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Mode        string        `json:"mode"`
	ClientID    string        `json:"client_id"`
	LeaseTime   time.Duration `json:"lease_time"`
	WaitTimeout time.Duration `json:"wait_timeout"`
	QueueMode   string        `json:"queue_mode"`
	Priority    int           `json:"priority"`
	TryLock     bool          `json:"try_lock"`
	Capacity    int           `json:"capacity"`
}

func (s *Server) handleAcquire(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req AcquireRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	acquireReq := &lock.AcquireRequest{
		LockID: lock.LockIdentifier{
			Namespace: req.Namespace,
			Name:      req.Name,
		},
		Type:        lock.LockType(req.Type),
		Mode:        lock.LockMode(req.Mode),
		ClientID:    req.ClientID,
		LeaseTime:   req.LeaseTime,
		WaitTimeout: req.WaitTimeout,
		QueueMode:   lock.QueueMode(req.QueueMode),
		Priority:    req.Priority,
		TryLock:     req.TryLock,
		Capacity:    req.Capacity,
	}

	payload, err := json.Marshal(acquireReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdAcquire,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 30*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response from raft")
		return
	}

	if resErr, ok := resMap["err"].(error); ok && resErr != nil {
		s.writeError(w, http.StatusInternalServerError, resErr.Error())
		return
	}

	resp, ok := resMap["resp"].(*lock.LockResponse)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response type")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type ReleaseRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	ClientID  string `json:"client_id"`
	Token     uint64 `json:"token"`
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req ReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	releaseReq := &lock.ReleaseRequest{
		LockID: lock.LockIdentifier{
			Namespace: req.Namespace,
			Name:      req.Name,
		},
		ClientID: req.ClientID,
		Token:    req.Token,
	}

	payload, err := json.Marshal(releaseReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRelease,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response from raft")
		return
	}

	if resErr, ok := resMap["err"].(error); ok && resErr != nil {
		s.writeError(w, http.StatusInternalServerError, resErr.Error())
		return
	}

	resp, ok := resMap["resp"].(*lock.LockResponse)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response type")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type HeartbeatRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	ClientID  string `json:"client_id"`
	Token     uint64 `json:"token"`
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	heartbeatReq := &lock.HeartbeatRequest{
		LockID: lock.LockIdentifier{
			Namespace: req.Namespace,
			Name:      req.Name,
		},
		ClientID: req.ClientID,
		Token:    req.Token,
	}

	payload, err := json.Marshal(heartbeatReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdHeartbeat,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response from raft")
		return
	}

	if resErr, ok := resMap["err"].(error); ok && resErr != nil {
		s.writeError(w, http.StatusInternalServerError, resErr.Error())
		return
	}

	resp, ok := resMap["resp"].(*lock.LockResponse)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response type")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleListLocks(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	search := r.URL.Query().Get("search")
	sortBy := r.URL.Query().Get("sort_by")

	locks := s.lockMgr.ListLocks(namespace)

	if search != "" {
		filtered := make([]*lock.Lock, 0)
		for _, l := range locks {
			if strings.Contains(l.ID.Name, search) || strings.Contains(l.ID.String(), search) {
				filtered = append(filtered, l)
			}
		}
		locks = filtered
	}

	if sortBy == "contention" {
		sort.Slice(locks, func(i, j int) bool {
			locks[i].Mu.RLock()
			locks[j].Mu.RLock()
			defer locks[i].Mu.RUnlock()
			defer locks[j].Mu.RUnlock()
			return len(locks[i].WaitQueue) > len(locks[j].WaitQueue)
		})
	}

	response := make([]map[string]interface{}, 0, len(locks))
	for _, l := range locks {
		l.Mu.RLock()
		holders := make([]map[string]interface{}, 0, len(l.Holders))
		for _, h := range l.Holders {
			holders = append(holders, map[string]interface{}{
				"client_id": h.ClientID,
				"token": h.Token,
				"mode": h.Mode,
				"acquired_at": h.AcquiredAt,
				"lease_expiry": h.LeaseExpiry,
				"remaining_lease": time.Until(h.LeaseExpiry),
			})
		}
		waitQueue := make([]map[string]interface{}, 0, len(l.WaitQueue))
		for _, wr := range l.WaitQueue {
			waitQueue = append(waitQueue, map[string]interface{}{
				"client_id": wr.ClientID,
				"mode": wr.Mode,
				"requested_at": wr.RequestedAt,
				"priority": wr.Priority,
				"wait_time": time.Since(wr.RequestedAt),
			})
		}
		response = append(response, map[string]interface{}{
			"id":           l.ID.String(),
			"namespace":    l.ID.Namespace,
			"name":         l.ID.Name,
			"type":         l.Type,
			"state":        l.State,
			"holders":      holders,
			"wait_queue":   waitQueue,
			"wait_length":  len(l.WaitQueue),
			"queue_mode":   l.QueueMode,
			"lease_time":   l.LeaseTime,
			"capacity":     l.Capacity,
			"created_at":   l.CreatedAt,
			"max_token":    l.MaxToken,
		})
		l.Mu.RUnlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleGetLock(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	lockID := lock.LockIdentifier{
		Namespace: vars["namespace"],
		Name:      vars["name"],
	}

	lock, exists := s.lockMgr.GetLock(lockID)
	if !exists {
		s.writeError(w, http.StatusNotFound, "lock not found")
		return
	}

	lock.Mu.RLock()
	defer lock.Mu.RUnlock()

	holders := make([]map[string]interface{}, 0, len(lock.Holders))
	for _, h := range lock.Holders {
		holders = append(holders, map[string]interface{}{
			"client_id":      h.ClientID,
			"token":          h.Token,
			"mode":           h.Mode,
			"acquired_at":    h.AcquiredAt,
			"lease_expiry":   h.LeaseExpiry,
			"remaining_lease": time.Until(h.LeaseExpiry),
		})
	}

	waitQueue := make([]map[string]interface{}, 0, len(lock.WaitQueue))
	for _, wr := range lock.WaitQueue {
		waitQueue = append(waitQueue, map[string]interface{}{
			"client_id":   wr.ClientID,
			"mode":        wr.Mode,
			"requested_at": wr.RequestedAt,
			"priority":    wr.Priority,
			"wait_time":   time.Since(wr.RequestedAt),
		})
	}

	history := make([]map[string]interface{}, 0, len(lock.History))
	for _, e := range lock.History {
		history = append(history, map[string]interface{}{
			"timestamp": e.Timestamp,
			"event":     e.Event,
			"client_id": e.ClientID,
			"mode":      e.Mode,
			"token":     e.Token,
		})
	}

	response := map[string]interface{}{
		"id":          lock.ID.String(),
		"namespace":   lock.ID.Namespace,
		"name":        lock.ID.Name,
		"type":        lock.Type,
		"state":       lock.State,
		"holders":     holders,
		"wait_queue":  waitQueue,
		"wait_length": len(lock.WaitQueue),
		"queue_mode":  lock.QueueMode,
		"lease_time":  lock.LeaseTime,
		"capacity":    lock.Capacity,
		"created_at":  lock.CreatedAt,
		"max_token":   lock.MaxToken,
		"history":     history,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleAdjustLease(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	lockID := lock.LockIdentifier{
		Namespace: vars["namespace"],
		Name:      vars["name"],
	}

	var body struct {
		LeaseTime time.Duration `json:"lease_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload := struct {
		LockID    lock.LockIdentifier `json:"lock_id"`
		LeaseTime time.Duration    `json:"lease_time"`
		OldLease  time.Duration    `json:"old_lease"`
	}{
		LockID:    lockID,
		LeaseTime: body.LeaseTime,
	}

	if l, exists := s.lockMgr.GetLock(lockID); exists {
		l.Mu.RLock()
		payload.OldLease = l.LeaseTime
		l.Mu.RUnlock()
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdAdjustLease,
		Payload: payloadBytes,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	s.logAdminOp("system", "adjust_lease", lockID.String(), body.LeaseTime.String())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) handleAdjustCapacity(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	lockID := lock.LockIdentifier{
		Namespace: vars["namespace"],
		Name:      vars["name"],
	}

	var body struct {
		Capacity int `json:"capacity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload := struct {
		LockID      lock.LockIdentifier `json:"lock_id"`
		NewCapacity int              `json:"new_capacity"`
		OldCapacity int              `json:"old_capacity"`
	}{
		LockID:      lockID,
		NewCapacity: body.Capacity,
	}

	if l, exists := s.lockMgr.GetLock(lockID); exists {
		l.Mu.RLock()
		payload.OldCapacity = l.Capacity
		l.Mu.RUnlock()
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdAdjustCapacity,
		Payload: payloadBytes,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	s.logAdminOp("system", "adjust_capacity", lockID.String(), fmt.Sprintf("%d", body.Capacity))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) handleClearQueue(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	lockID := lock.LockIdentifier{
		Namespace: vars["namespace"],
		Name:      vars["name"],
	}

	payload := struct {
		LockID    lock.LockIdentifier `json:"lock_id"`
		QueueSize int              `json:"queue_size"`
	}{
		LockID: lockID,
	}

	if l, exists := s.lockMgr.GetLock(lockID); exists {
		l.Mu.RLock()
		payload.QueueSize = len(l.WaitQueue)
		l.Mu.RUnlock()
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdClearQueue,
		Payload: payloadBytes,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	s.logAdminOp("system", "clear_queue", lockID.String(), fmt.Sprintf("queue_size=%d", payload.QueueSize))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

type ForceReleaseRequest struct {
	Confirm bool `json:"confirm"`
}

func (s *Server) handleForceRelease(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	lockID := lock.LockIdentifier{
		Namespace: vars["namespace"],
		Name:      vars["name"],
	}

	var body ForceReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm {
		s.writeError(w, http.StatusBadRequest, "confirmation required")
		return
	}

	releaseReq := &lock.ReleaseRequest{
		LockID:   lockID,
		ClientID: "admin",
		Token:    0,
		Force:    true,
	}

	payload, err := json.Marshal(releaseReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRelease,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response from raft")
		return
	}

	if resErr, ok := resMap["err"].(error); ok && resErr != nil {
		s.writeError(w, http.StatusInternalServerError, resErr.Error())
		return
	}

	resp, ok := resMap["resp"].(*lock.LockResponse)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response type")
		return
	}

	s.logAdminOp("admin", "force_release", lockID.String(), fmt.Sprintf("token=%d", resp.Token))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleListOperations(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	response := make([]map[string]interface{}, 0, len(s.adminOpsLog))
	for _, op := range s.adminOpsLog {
		response = append(response, map[string]interface{}{
			"timestamp": op.Timestamp,
			"operator":  op.Operator,
			"action":    op.Action,
			"lock_id":   op.LockID,
			"details":   op.Details,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	alerts := s.alertMgr.GetActiveAlerts()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(alerts)
}

func (s *Server) handleConfigureAlerts(w http.ResponseWriter, r *http.Request) {
	var rules []config.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.cfg.AlertRules = rules
	s.alertMgr.UpdateRules(rules)

	s.logAdminOp("admin", "configure_alerts", "", "rules_updated")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

type PeerRequest struct {
	NodeID string `json:"node_id"`
	Addr   string `json:"addr"`
}

func (s *Server) handleAddPeer(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req PeerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.raftMgr.AddPeer(req.NodeID, req.Addr); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.logAdminOp("admin", "add_peer", "", req.NodeID+"="+req.Addr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) handleRemovePeer(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req PeerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.raftMgr.RemovePeer(req.NodeID); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.logAdminOp("admin", "remove_peer", "", req.NodeID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) redirectToLeader(w http.ResponseWriter, r *http.Request) {
	leader := s.raftMgr.GetLeader()
	if leader == "" {
		s.writeError(w, http.StatusServiceUnavailable, "no leader elected")
		return
	}

	host := strings.Split(leader, ":")[0]
	port := "8080"

	w.Header().Set("X-Leader-Address", host+":"+port)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  false,
		"error":    "not leader",
		"leader":   host + ":" + port,
		"redirect": true,
	})
}

func (s *Server) writeError(w http.ResponseWriter, code int, err string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(APIError{Error: err})
}

func (s *Server) logAdminOp(operator, action, lockID, details string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adminOpsLog = append(s.adminOpsLog, AdminOperation{
		Timestamp: time.Now(),
		Operator:  operator,
		Action:    action,
		LockID:    lockID,
		Details:   details,
	})
	if len(s.adminOpsLog) > 1000 {
		s.adminOpsLog = s.adminOpsLog[len(s.adminOpsLog)-1000:]
	}
}

type AlertManager struct {
	cfg         *config.ServerConfig
	lockMgr     *lock.LockManager
	raftMgr     *raftpkg.RaftManager
	logger      *zap.Logger
	metrics     *metrics.PrometheusCollector
	alerts      []ActiveAlert
	mu          sync.RWMutex
	deadlockCount int
	lastDeadlockCheck time.Time
}

type ActiveAlert struct {
	ID        string
	Name      string
	Message   string
	Severity  string
	Timestamp time.Time
	Value     float64
	Threshold float64
}

func NewAlertManager(cfg *config.ServerConfig, lockMgr *lock.LockManager,
	raftMgr *raftpkg.RaftManager, logger *zap.Logger, metrics *metrics.PrometheusCollector) *AlertManager {
	return &AlertManager{
		cfg:         cfg,
		lockMgr:     lockMgr,
		raftMgr:     raftMgr,
		logger:      logger,
		metrics:     metrics,
		alerts:      make([]ActiveAlert, 0),
		lastDeadlockCheck: time.Now(),
	}
}

func (am *AlertManager) UpdateRules(rules []config.AlertRule) {
	am.cfg.AlertRules = rules
}

func (am *AlertManager) GetActiveAlerts() []ActiveAlert {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return append([]ActiveAlert{}, am.alerts...)
}

func (am *AlertManager) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			am.CheckAlerts()
		}
	}
}

func (am *AlertManager) CheckAlerts() {
	newAlerts := make([]ActiveAlert, 0)
	now := time.Now()

	locks := am.lockMgr.ListLocks("")

	for _, l := range locks {
		l.Mu.RLock()
		for _, h := range l.Holders {
			holdTime := now.Sub(h.AcquiredAt).Seconds()
			for _, rule := range am.cfg.AlertRules {
				if rule.Condition == "lock_hold_time" && rule.Enabled && holdTime > rule.Threshold {
					newAlerts = append(newAlerts, ActiveAlert{
						ID:        rule.ID + "_" + l.ID.String(),
						Name:      rule.Name,
						Message:   "Lock " + l.ID.String() + " held by " + h.ClientID + " for " + string(rune(holdTime)) + "s",
						Severity:  "warning",
						Timestamp: now,
						Value:     holdTime,
						Threshold: rule.Threshold,
					})
				}
			}
		}

		for _, rule := range am.cfg.AlertRules {
			if rule.Condition == "wait_queue_length" && rule.Enabled && float64(len(l.WaitQueue)) > rule.Threshold {
				newAlerts = append(newAlerts, ActiveAlert{
					ID:        rule.ID + "_" + l.ID.String(),
					Name:      rule.Name,
					Message:   "Lock " + l.ID.String() + " has wait queue length " + string(rune(len(l.WaitQueue))),
					Severity:  "warning",
					Timestamp: now,
					Value:     float64(len(l.WaitQueue)),
					Threshold: rule.Threshold,
				})
			}
		}
		l.Mu.RUnlock()
	}

	if now.Sub(am.lastDeadlockCheck) >= time.Minute {
		am.deadlockCount = 0
		am.lastDeadlockCheck = now
	}

	for _, rule := range am.cfg.AlertRules {
		if rule.Condition == "deadlock_frequency" && rule.Enabled && float64(am.deadlockCount) > rule.Threshold {
			newAlerts = append(newAlerts, ActiveAlert{
				ID:        rule.ID,
				Name:      rule.Name,
				Message:   "Deadlock frequency high: " + string(rune(am.deadlockCount)) + " in last minute",
				Severity:  "critical",
				Timestamp: now,
				Value:     float64(am.deadlockCount),
				Threshold: rule.Threshold,
			})
		}
	}

	am.mu.Lock()
	am.alerts = newAlerts
	am.mu.Unlock()

	if len(newAlerts) > 0 {
		am.sendWebhookAlerts(newAlerts)
	}
}

func (am *AlertManager) sendWebhookAlerts(alerts []ActiveAlert) {
	if len(am.cfg.WebhookURLs) == 0 {
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"alerts":    alerts,
		"node_id":   am.cfg.NodeID,
		"timestamp": time.Now(),
	})

	for _, url := range am.cfg.WebhookURLs {
		go func(u string) {
			resp, err := http.Post(u, "application/json", bytes.NewReader(payload))
			if err != nil {
				am.logger.Error("Failed to send webhook alert", zap.String("url", u), zap.Error(err))
				return
			}
			defer resp.Body.Close()
		}(url)
	}
}

type RegisterDependencyRequest struct {
	ParentNamespace string `json:"parent_namespace"`
	ParentName      string `json:"parent_name"`
	ChildNamespace  string `json:"child_namespace"`
	ChildName       string `json:"child_name"`
}

func (s *Server) handleRegisterDependency(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req RegisterDependencyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	depReq := lock.RegisterDependencyRequest{
		ParentLock: lock.LockIdentifier{
			Namespace: req.ParentNamespace,
			Name:      req.ParentName,
		},
		ChildLock: lock.LockIdentifier{
			Namespace: req.ChildNamespace,
			Name:      req.ChildName,
		},
	}

	payload, err := json.Marshal(depReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRegisterDep,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

type RemoveDependencyRequest struct {
	ParentNamespace string `json:"parent_namespace"`
	ParentName      string `json:"parent_name"`
	ChildNamespace  string `json:"child_namespace"`
	ChildName       string `json:"child_name"`
}

func (s *Server) handleRemoveDependency(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req RemoveDependencyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	depReq := lock.RemoveDependencyRequest{
		ParentLock: lock.LockIdentifier{
			Namespace: req.ParentNamespace,
			Name:      req.ParentName,
		},
		ChildLock: lock.LockIdentifier{
			Namespace: req.ChildNamespace,
			Name:      req.ChildName,
		},
	}

	payload, err := json.Marshal(depReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRemoveDep,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) handleGetDependencies(w http.ResponseWriter, r *http.Request) {
	graph := s.lockMgr.GetDependencyGraph()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(graph)
}

func (s *Server) handleGetDependencyGraph(w http.ResponseWriter, r *http.Request) {
	graph := s.lockMgr.GetDependencyGraphWithState()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(graph)
}

type CreateGroupRequest struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Timeout     time.Duration `json:"timeout"`
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	groupReq := lock.CreateGroupRequest{
		Name:        req.Name,
		Description: req.Description,
		Timeout:     req.Timeout,
	}

	payload, err := json.Marshal(groupReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdCreateGroup,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	name := vars["name"]

	payload := struct {
		Name string `json:"name"`
	}{Name: name}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdDeleteGroup,
		Payload: payloadBytes,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

type AddLockToGroupRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (s *Server) handleAddLockToGroup(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	groupName := vars["name"]

	var req AddLockToGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	addReq := lock.AddLockToGroupRequest{
		GroupName: groupName,
		LockID: lock.LockIdentifier{
			Namespace: req.Namespace,
			Name:      req.Name,
		},
	}

	payload, err := json.Marshal(addReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdAddToGroup,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

type RemoveLockFromGroupRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (s *Server) handleRemoveLockFromGroup(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	groupName := vars["name"]

	var req RemoveLockFromGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	removeReq := lock.RemoveLockFromGroupRequest{
		GroupName: groupName,
		LockID: lock.LockIdentifier{
			Namespace: req.Namespace,
			Name:      req.Name,
		},
	}

	payload, err := json.Marshal(removeReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRemoveFromGroup,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups := s.lockMgr.ListGroups()

	response := make([]map[string]interface{}, 0, len(groups))
	for _, g := range groups {
		locks := make([]map[string]interface{}, 0, len(g.Locks))
		for _, l := range g.Locks {
			l.Mu.RLock()
			locks = append(locks, map[string]interface{}{
				"id":    l.ID.String(),
				"name":  l.ID.Name,
				"type":  l.Type,
				"state": l.State,
			})
			l.Mu.RUnlock()
		}

		lockIDs := make([]string, 0, len(g.LockIDs))
		for _, id := range g.LockIDs {
			lockIDs = append(lockIDs, id.String())
		}

		response = append(response, map[string]interface{}{
			"name":        g.Name,
			"description": g.Description,
			"lock_ids":    lockIDs,
			"locks":       locks,
			"created_at":  g.CreatedAt,
			"timeout":     g.Timeout,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	group, exists := s.lockMgr.GetGroup(name)
	if !exists {
		s.writeError(w, http.StatusNotFound, "group not found")
		return
	}

	locks := make([]map[string]interface{}, 0, len(group.Locks))
	for _, l := range group.Locks {
		l.Mu.RLock()
		holders := make([]string, 0, len(l.Holders))
		for _, h := range l.Holders {
			holders = append(holders, h.ClientID)
		}
		locks = append(locks, map[string]interface{}{
			"id":       l.ID.String(),
			"name":     l.ID.Name,
			"type":     l.Type,
			"state":    l.State,
			"holders":  holders,
			"waiters":  len(l.WaitQueue),
		})
		l.Mu.RUnlock()
	}

	lockIDs := make([]string, 0, len(group.LockIDs))
	for _, id := range group.LockIDs {
		lockIDs = append(lockIDs, id.String())
	}

	response := map[string]interface{}{
		"name":        group.Name,
		"description": group.Description,
		"lock_ids":    lockIDs,
		"locks":       locks,
		"created_at":  group.CreatedAt,
		"timeout":     group.Timeout,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

type BatchAcquireRequest struct {
	ClientID  string        `json:"client_id"`
	LeaseTime time.Duration `json:"lease_time"`
	Mode      string        `json:"mode"`
}

func (s *Server) handleBatchAcquire(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	groupName := vars["name"]

	var req BatchAcquireRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	mode := lock.LockModeWrite
	if req.Mode == "read" {
		mode = lock.LockModeRead
	}

	batchReq := lock.BatchAcquireRequest{
		GroupName: groupName,
		ClientID:  req.ClientID,
		LeaseTime: req.LeaseTime,
		Mode:      mode,
	}

	payload, err := json.Marshal(batchReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdBatchAcquire,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 60*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response from raft")
		return
	}

	if resErr, ok := resMap["err"].(error); ok && resErr != nil {
		s.writeError(w, http.StatusInternalServerError, resErr.Error())
		return
	}

	resp, ok := resMap["resp"].(*lock.BatchAcquireResponse)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response type")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type BatchReleaseRequest struct {
	ClientID string `json:"client_id"`
}

func (s *Server) handleBatchRelease(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	groupName := vars["name"]

	var req BatchReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	batchReq := lock.BatchReleaseRequest{
		GroupName: groupName,
		ClientID:  req.ClientID,
	}

	payload, err := json.Marshal(batchReq)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdBatchRelease,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 30*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response from raft")
		return
	}

	if resErr, ok := resMap["err"].(error); ok && resErr != nil {
		s.writeError(w, http.StatusInternalServerError, resErr.Error())
		return
	}

	resp, ok := resMap["resp"].(*lock.BatchReleaseResponse)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response type")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleListRateLimitRules(w http.ResponseWriter, r *http.Request) {
	rules := s.rateLimitMgr.ListRules()

	response := make([]map[string]interface{}, 0, len(rules))
	for _, rule := range rules {
		response = append(response, s.ruleToMap(rule))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleCreateRateLimitRule(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req ratelimit.CreateRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload, err := json.Marshal(req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRateLimitCreateRule,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	rule, _ := s.rateLimitMgr.GetRule(req.Key)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.ruleToMap(rule))
}

func (s *Server) handleGetRateLimitRule(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]

	rule, exists := s.rateLimitMgr.GetRule(key)
	if !exists {
		s.writeError(w, http.StatusNotFound, "rule not found")
		return
	}

	state, _ := s.rateLimitMgr.GetRuleState(key)
	response := s.ruleToMap(rule)
	if state != nil {
		response["state"] = s.stateToMap(state, rule)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleUpdateRateLimitRule(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	key := vars["key"]

	var req ratelimit.UpdateRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload := struct {
		Key string                       `json:"key"`
		Req ratelimit.UpdateRuleRequest  `json:"req"`
	}{
		Key: key,
		Req: req,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRateLimitUpdateRule,
		Payload: payloadBytes,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	rule, _ := s.rateLimitMgr.GetRule(key)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.ruleToMap(rule))
}

func (s *Server) handleDeleteRateLimitRule(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	key := vars["key"]

	payload := struct {
		Key string `json:"key"`
	}{Key: key}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRateLimitDeleteRule,
		Payload: payloadBytes,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) handleRateLimitCheck(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req ratelimit.CheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload, err := json.Marshal(req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRateLimitCheck,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response from raft")
		return
	}

	resultRaw := resMap["result"]
	var rlResult *ratelimit.RateLimitResult
	if castResult, ok := resultRaw.(*ratelimit.RateLimitResult); ok {
		rlResult = castResult
	} else if resultMap, ok := resultRaw.(map[string]interface{}); ok {
		rlResult = &ratelimit.RateLimitResult{
			Allowed:    resultMap["allowed"].(bool),
			Remaining:  resultMap["remaining"].(float64),
			RetryAfter: resultMap["retry_after"].(float64),
			RuleKey:    resultMap["rule_key"].(string),
			Algorithm:  ratelimit.Algorithm(resultMap["algorithm"].(string)),
			Reason:     resultMap["reason"].(string),
		}
	} else {
		s.writeError(w, http.StatusInternalServerError, "invalid result type")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%.1f", rlResult.Remaining))
	w.Header().Set("X-RateLimit-Limit", rlResult.RuleKey)

	if !rlResult.Allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%.1f", rlResult.RetryAfter))
		w.WriteHeader(http.StatusTooManyRequests)
	}
	json.NewEncoder(w).Encode(rlResult)
}

func (s *Server) handleListRateLimitEvents(w http.ResponseWriter, r *http.Request) {
	filterKey := r.URL.Query().Get("rule_key")
	filterClient := r.URL.Query().Get("client_id")
	var filterStart, filterEnd time.Time
	if start := r.URL.Query().Get("start"); start != "" {
		if t, err := time.Parse(time.RFC3339, start); err == nil {
			filterStart = t
		}
	}
	if end := r.URL.Query().Get("end"); end != "" {
		if t, err := time.Parse(time.RFC3339, end); err == nil {
			filterEnd = t
		}
	}

	events := s.rateLimitMgr.GetEvents(filterKey, filterClient, filterStart, filterEnd)

	response := make([]map[string]interface{}, 0, len(events))
	for _, event := range events {
		response = append(response, map[string]interface{}{
			"timestamp":   event.Timestamp,
			"client_id":   event.ClientID,
			"rule_key":    event.RuleKey,
			"algorithm":   event.Algorithm,
			"reason":      event.Reason,
			"request_key": event.RequestKey,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleRateLimitMonitorStats(w http.ResponseWriter, r *http.Request) {
	rules := s.rateLimitMgr.ListRules()

	stats := make([]map[string]interface{}, 0, len(rules))
	for _, rule := range rules {
		state, _ := s.rateLimitMgr.GetRuleState(rule.Key)
		entry := map[string]interface{}{
			"key":       rule.Key,
			"algorithm": rule.Algorithm,
		}
		if state != nil {
			entry["state"] = s.stateToMap(state, rule)
		}
		stats = append(stats, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleRateLimitTopRejected(w http.ResponseWriter, r *http.Request) {
	topN := 10
	if n := r.URL.Query().Get("n"); n != "" {
		if val, err := fmt.Sscanf(n, "%d", &topN); err != nil || val != 1 {
			topN = 10
		}
	}

	stats := s.rateLimitMgr.GetTopRejectedRules(topN)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) ruleToMap(rule *ratelimit.RateLimitRule) map[string]interface{} {
	m := map[string]interface{}{
		"key":         rule.Key,
		"algorithm":   rule.Algorithm,
		"capacity":    rule.Capacity,
		"rate":        rule.Rate,
		"window":      rule.Window,
		"max_requests": rule.MaxRequests,
		"queue_depth": rule.QueueDepth,
		"per_client":  rule.PerClient,
		"parent_key":  rule.ParentKey,
		"created_at":  rule.CreatedAt,
		"updated_at":  rule.UpdatedAt,
		"original_capacity": rule.OriginalCapacity,
		"original_rate":     rule.OriginalRate,
		"original_max_requests": rule.OriginalMaxRequests,
	}
	if rule.ActiveStart != "" {
		m["active_start"] = rule.ActiveStart
	}
	if rule.ActiveEnd != "" {
		m["active_end"] = rule.ActiveEnd
	}
	if len(rule.ActiveDays) > 0 {
		m["active_days"] = rule.ActiveDays
	}
	if rule.AdaptiveConfig != nil {
		m["adaptive_config"] = rule.AdaptiveConfig
	}
	if rule.ShapingConfig != nil {
		m["shaping_config"] = rule.ShapingConfig
	}
	return m
}

func (s *Server) stateToMap(state *ratelimit.RuleState, rule *ratelimit.RateLimitRule) map[string]interface{} {
	m := map[string]interface{}{}

	cs := state.GlobalState
	if cs != nil {
		switch rule.Algorithm {
		case ratelimit.AlgorithmTokenBucket:
			if cs.TokenBucket != nil {
				m["tokens"] = cs.TokenBucket.Tokens
				m["last_refill"] = cs.TokenBucket.LastRefill
			}
		case ratelimit.AlgorithmSlidingWindow:
			if cs.SlidingWindow != nil {
				m["window_count"] = len(cs.SlidingWindow.Logs)
			}
		case ratelimit.AlgorithmLeakyBucket:
			if cs.LeakyBucket != nil {
				m["queue_depth"] = cs.LeakyBucket.CurrentDepth
			}
		}
	}

	if rule.PerClient && len(state.ClientStates) > 0 {
		clients := make(map[string]interface{})
		for clientID, clientState := range state.ClientStates {
			cm := map[string]interface{}{}
			switch rule.Algorithm {
			case ratelimit.AlgorithmTokenBucket:
				if clientState.TokenBucket != nil {
					cm["tokens"] = clientState.TokenBucket.Tokens
					cm["last_refill"] = clientState.TokenBucket.LastRefill
				}
			case ratelimit.AlgorithmSlidingWindow:
				if clientState.SlidingWindow != nil {
					cm["window_count"] = len(clientState.SlidingWindow.Logs)
				}
			case ratelimit.AlgorithmLeakyBucket:
				if clientState.LeakyBucket != nil {
					cm["queue_depth"] = clientState.LeakyBucket.CurrentDepth
				}
			}
			clients[clientID] = cm
		}
		m["clients"] = clients
	}

	if state.AdaptiveState != nil {
		m["adaptive_state"] = state.AdaptiveState
		m["effective_quota_percent"] = state.AdaptiveState.CurrentQuotaMultiplier * 100
	}

	if state.WarmUpState != nil {
		m["warm_up_state"] = state.WarmUpState
	}

	if len(state.PriorityQueue) > 0 {
		m["priority_queue_length"] = len(state.PriorityQueue)
	}

	m["borrowed_tokens"] = state.BorrowedTokens
	m["lent_tokens"] = state.LentTokens

	return m
}

func (s *Server) handleReportLatency(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req ratelimit.ReportLatencyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload, err := json.Marshal(req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRateLimitReportLatency,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) handleRateLimitAdjustQuota(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	key := vars["key"]

	var body struct {
		NewMultiplier float64 `json:"new_multiplier"`
		Reason        string  `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req := ratelimit.AdjustQuotaRequest{
		RuleKey:       key,
		NewMultiplier: body.NewMultiplier,
		Reason:        body.Reason,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRateLimitAdjustQuota,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if ok {
		if resErr, ok := resMap["err"].(error); ok && resErr != nil {
			s.writeError(w, http.StatusBadRequest, resErr.Error())
			return
		}
	}

	rule, _ := s.rateLimitMgr.GetRule(key)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.ruleToMap(rule))
}

func (s *Server) handleGetAdaptiveHistory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]

	history, err := s.rateLimitMgr.GetAdaptiveHistory(key)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (s *Server) handleGetQuotaMultiplier(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]

	multiplier, err := s.rateLimitMgr.GetEffectiveQuotaMultiplier(key)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	rule, exists := s.rateLimitMgr.GetRule(key)
	if !exists {
		s.writeError(w, http.StatusNotFound, "rule not found")
		return
	}

	state, _ := s.rateLimitMgr.GetRuleState(key)

	response := map[string]interface{}{
		"rule_key":             key,
		"current_multiplier":   multiplier,
		"current_percent":      multiplier * 100,
		"original_capacity":    rule.OriginalCapacity,
		"original_rate":        rule.OriginalRate,
		"current_capacity":     rule.Capacity,
		"current_rate":         rule.Rate,
	}

	if state != nil && state.AdaptiveState != nil {
		response["adaptive_state"] = state.AdaptiveState
	}

	if state != nil && state.WarmUpState != nil {
		response["warm_up_state"] = state.WarmUpState
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleCheckAndAdjustQuota(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	vars := mux.Vars(r)
	key := vars["key"]

	rule, exists := s.rateLimitMgr.GetRule(key)
	if !exists {
		s.writeError(w, http.StatusNotFound, "rule not found")
		return
	}

	if rule.AdaptiveConfig == nil || !rule.AdaptiveConfig.Enabled {
		s.writeError(w, http.StatusBadRequest, "adaptive rate limiting not enabled for this rule")
		return
	}

	err := s.rateLimitMgr.CheckAndAdjustQuota(key)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	history, _ := s.rateLimitMgr.GetAdaptiveHistory(key)
	state, _ := s.rateLimitMgr.GetRuleState(key)

	response := map[string]interface{}{
		"success":         true,
		"rule_key":        key,
		"current_state":   state.AdaptiveState,
		"latest_adjustment": nil,
	}

	if len(history) > 0 {
		response["latest_adjustment"] = history[len(history)-1]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleBorrowTokens(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req ratelimit.BorrowTokensRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload, err := json.Marshal(req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRateLimitBorrowTokens,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response from raft")
		return
	}

	if resErr, ok := resMap["err"].(error); ok && resErr != nil {
		s.writeError(w, http.StatusBadRequest, resErr.Error())
		return
	}

	amount, _ := resMap["amount"].(float64)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"amount":  amount,
	})
}

func (s *Server) handleRepayTokens(w http.ResponseWriter, r *http.Request) {
	if !s.raftMgr.IsLeader() {
		s.redirectToLeader(w, r)
		return
	}

	var req ratelimit.RepayTokensRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload, err := json.Marshal(req)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cmd := raftpkg.RaftCommand{
		Type:    raftpkg.CmdRateLimitRepayTokens,
		Payload: payload,
	}

	result, err := s.raftMgr.ApplyCommand(cmd, 10*time.Second)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resMap, ok := result.(map[string]interface{})
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "invalid response from raft")
		return
	}

	if resErr, ok := resMap["err"].(error); ok && resErr != nil {
		s.writeError(w, http.StatusBadRequest, resErr.Error())
		return
	}

	amount, _ := resMap["amount"].(float64)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"amount":  amount,
	})
}

func (s *Server) handleGetBorrowFlow(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]

	flow, err := s.rateLimitMgr.GetBorrowFlow(namespace)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(flow)
}

func (s *Server) handleGetNamespaceRules(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]

	rules := s.rateLimitMgr.GetNamespaceRules(namespace)

	result := make([]map[string]interface{}, 0, len(rules))
	for _, key := range rules {
		rule, exists := s.rateLimitMgr.GetRule(key)
		if exists {
			result = append(result, s.ruleToMap(rule))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
