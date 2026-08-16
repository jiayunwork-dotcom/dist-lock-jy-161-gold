package lock

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

type LockManager struct {
	locks            map[string]*Lock
	config           Config
	logger           *zap.Logger
	tokenGenerator   TokenGenerator
	mu               sync.RWMutex
	metrics          MetricsCollector
	dependencyGraph  *DependencyGraph
	groups           map[string]*LockGroup
	groupsMu         sync.RWMutex
}

type TokenGenerator interface {
	NextToken() uint64
}

type MetricsCollector interface {
	RecordLockAcquire(lockType LockType, duration time.Duration)
	RecordLockWait(lockType LockType, duration time.Duration)
	RecordLockRelease(lockType LockType)
	RecordHeartbeat(success bool)
	IncrementActiveLocks()
	DecrementActiveLocks()
	RecordDeadlockDetected()
	RecordDeadlockResolved()
}

type SimpleTokenGenerator struct {
	mu     sync.Mutex
	nextID uint64
}

func NewSimpleTokenGenerator() *SimpleTokenGenerator {
	return &SimpleTokenGenerator{nextID: 1}
}

func (g *SimpleTokenGenerator) NextToken() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID++
	return g.nextID
}

func CloneKeys(keys []string) []string {
	return keys
}

func ClientName(name *string) string {
	return *name
}

func (g *SimpleTokenGenerator) SetCurrentToken(token uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID = token + 1
}

func NewLockManager(config Config, logger *zap.Logger, tokenGen TokenGenerator, metrics MetricsCollector) *LockManager {
	return &LockManager{
		locks:           make(map[string]*Lock),
		config:          config,
		logger:          logger,
		tokenGenerator:  tokenGen,
		metrics:         metrics,
		dependencyGraph: NewDependencyGraph(),
		groups:          make(map[string]*LockGroup),
	}
}

func (lm *LockManager) Acquire(ctx context.Context, req *AcquireRequest) (*LockResponse, error) {
	if req.LeaseTime == 0 {
		req.LeaseTime = lm.config.DefaultLeaseTime
	}
	if req.LeaseTime < lm.config.MinLeaseTime {
		req.LeaseTime = lm.config.MinLeaseTime
	}
	if req.LeaseTime > lm.config.MaxLeaseTime {
		req.LeaseTime = lm.config.MaxLeaseTime
	}

	depCheck := lm.CheckDependencies(req.LockID, req.ClientID)
	if len(depCheck.MissingDependencies) > 0 {
		missing := make([]string, 0, len(depCheck.MissingDependencies))
		for _, dep := range depCheck.MissingDependencies {
			missing = append(missing, dep.String())
		}
		return &LockResponse{
			Success: false,
			Error:   "missing dependencies: " + strings.Join(missing, ", "),
		}, nil
	}

	lockKey := req.LockID.String()
	startWait := time.Now()

	lm.mu.Lock()
	lock, exists := lm.locks[lockKey]
	if !exists {
		lock = &Lock{
			ID:        req.LockID,
			Type:      req.Type,
			State:     LockStateFree,
			Holders:   make([]*LockHolder, 0),
			WaitQueue: make([]*WaitRequest, 0),
			QueueMode: req.QueueMode,
			LeaseTime: req.LeaseTime,
			CreatedAt: time.Now(),
			History:   make([]LockEvent, 0, 100),
			Capacity:  req.Capacity,
		}
		if req.Type == LockTypeSemaphore && req.Capacity <= 0 {
			lock.Capacity = 1
		}
		if req.Type == LockTypeBarrier {
			lock.BarrierCount = 0
		}
		lm.locks[lockKey] = lock
	}
	lock.Mu.Lock()
	lm.mu.Unlock()

	if lock.Type != req.Type {
		lock.Mu.Unlock()
		return &LockResponse{Success: false, Error: "lock type mismatch"}, nil
	}

	if lock.QueueMode == "" {
		lock.QueueMode = req.QueueMode
	}
	if lock.QueueMode == "" {
		lock.QueueMode = QueueModeFIFO
	}

	resp := lm.tryAcquireInternal(lock, req)
	if resp.Success {
		lock.Mu.Unlock()
		waitDur := time.Since(startWait)
		lm.metrics.RecordLockWait(req.Type, waitDur)
		lm.metrics.RecordLockAcquire(req.Type, req.LeaseTime)
		return resp, nil
	}

	if req.TryLock || req.WaitTimeout == 0 {
		lock.Mu.Unlock()
		return resp, nil
	}

	notifier := make(chan *LockResponse, 1)
	waitReq := &WaitRequest{
		ClientID:      req.ClientID,
		Mode:          req.Mode,
		RequestedAt:   time.Now(),
		Priority:      req.Priority,
		Timeout:       req.WaitTimeout,
		LastHeartbeat: time.Now(),
		Notifier:      notifier,
		Ctx:           ctx,
	}

	lm.addToWaitQueue(lock, waitReq)
	lock.Mu.Unlock()

	select {
	case <-ctx.Done():
		lm.removeFromWaitQueue(lock, waitReq)
		return &LockResponse{Success: false, Error: "context canceled"}, nil
	case <-time.After(req.WaitTimeout):
		lm.removeFromWaitQueue(lock, waitReq)
		return &LockResponse{Success: false, Error: "timeout"}, nil
	case resp := <-notifier:
		waitDur := time.Since(startWait)
		lm.metrics.RecordLockWait(req.Type, waitDur)
		if resp.Success {
			lm.metrics.RecordLockAcquire(req.Type, req.LeaseTime)
		}
		return resp, nil
	}
}

func (lm *LockManager) tryAcquireInternal(lock *Lock, req *AcquireRequest) *LockResponse {
	now := time.Now()
	token := lm.tokenGenerator.NextToken()

	switch lock.Type {
	case LockTypeMutex:
		if len(lock.Holders) == 0 {
			holder := &LockHolder{
				ClientID:      req.ClientID,
				Token:         token,
				Mode:          LockModeWrite,
				AcquiredAt:    now,
				LeaseExpiry:   now.Add(req.LeaseTime),
				LastHeartbeat: now,
			}
			lock.Holders = append(lock.Holders, holder)
			lock.State = LockStateHeld
			lock.MaxToken = token
			lm.addHistory(lock, "acquired", req.ClientID, LockModeWrite, token)
			lm.metrics.IncrementActiveLocks()
			return &LockResponse{Success: true, Token: token, ExpiresAt: holder.LeaseExpiry}
		}

	case LockTypeRWLock:
		hasWriter := false
		hasUpgradeRequest := false
		for _, h := range lock.Holders {
			if h.Mode == LockModeWrite {
				hasWriter = true
			}
			if h.ClientID == req.ClientID && h.Mode == LockModeRead && req.Mode == LockModeWrite {
				hasUpgradeRequest = true
			}
		}

		if req.Mode == LockModeRead && !hasWriter {
			for _, h := range lock.Holders {
				if h.ClientID == req.ClientID {
					return &LockResponse{Success: true, Token: h.Token, ExpiresAt: h.LeaseExpiry}
				}
			}
			holder := &LockHolder{
				ClientID:      req.ClientID,
				Token:         token,
				Mode:          LockModeRead,
				AcquiredAt:    now,
				LeaseExpiry:   now.Add(req.LeaseTime),
				LastHeartbeat: now,
			}
			lock.Holders = append(lock.Holders, holder)
			lock.State = LockStateHeld
			lock.MaxToken = token
			lm.addHistory(lock, "acquired", req.ClientID, LockModeRead, token)
			lm.metrics.IncrementActiveLocks()
			return &LockResponse{Success: true, Token: token, ExpiresAt: holder.LeaseExpiry}
		}

		if req.Mode == LockModeWrite && len(lock.Holders) == 0 {
			holder := &LockHolder{
				ClientID:      req.ClientID,
				Token:         token,
				Mode:          LockModeWrite,
				AcquiredAt:    now,
				LeaseExpiry:   now.Add(req.LeaseTime),
				LastHeartbeat: now,
			}
			lock.Holders = append(lock.Holders, holder)
			lock.State = LockStateHeld
			lock.MaxToken = token
			lm.addHistory(lock, "acquired", req.ClientID, LockModeWrite, token)
			lm.metrics.IncrementActiveLocks()
			return &LockResponse{Success: true, Token: token, ExpiresAt: holder.LeaseExpiry}
		}

		if hasUpgradeRequest {
			for i, h := range lock.Holders {
				if h.ClientID == req.ClientID && h.Mode == LockModeRead {
					hasOtherReaders := false
					for _, other := range lock.Holders {
						if other.ClientID != req.ClientID {
							hasOtherReaders = true
						}
					}
					if !hasOtherReaders {
						lock.Holders[i].Mode = LockModeWrite
						lock.Holders[i].Token = token
						lock.Holders[i].LeaseExpiry = now.Add(req.LeaseTime)
						lock.MaxToken = token
						lm.addHistory(lock, "upgraded", req.ClientID, LockModeWrite, token)
						return &LockResponse{Success: true, Token: token, ExpiresAt: lock.Holders[i].LeaseExpiry}
					}
				}
			}
		}

	case LockTypeSemaphore:
		capacity := lock.Capacity
		if capacity <= 0 {
			capacity = 1
		}
		if len(lock.Holders) < capacity {
			holder := &LockHolder{
				ClientID:      req.ClientID,
				Token:         token,
				Mode:          LockModeWrite,
				AcquiredAt:    now,
				LeaseExpiry:   now.Add(req.LeaseTime),
				LastHeartbeat: now,
			}
			lock.Holders = append(lock.Holders, holder)
			lock.State = LockStateHeld
			lock.MaxToken = token
			lm.addHistory(lock, "acquired", req.ClientID, LockModeWrite, token)
			lm.metrics.IncrementActiveLocks()
			return &LockResponse{Success: true, Token: token, ExpiresAt: holder.LeaseExpiry}
		}

	case LockTypeBarrier:
		lock.BarrierCount++
		barrierCapacity := lock.Capacity
		if barrierCapacity <= 0 {
			barrierCapacity = 1
		}
		if lock.BarrierCount >= barrierCapacity {
			holder := &LockHolder{
				ClientID:      req.ClientID,
				Token:         token,
				Mode:          LockModeWrite,
				AcquiredAt:    now,
				LeaseExpiry:   now.Add(req.LeaseTime),
				LastHeartbeat: now,
			}
			lock.Holders = append(lock.Holders, holder)
			lock.State = LockStateHeld
			lock.MaxToken = token
			lm.addHistory(lock, "barrier_released", req.ClientID, LockModeWrite, token)
			lm.metrics.IncrementActiveLocks()
			lock.BarrierCount = 0
			lm.wakeWaiters(lock)
			return &LockResponse{Success: true, Token: token, ExpiresAt: holder.LeaseExpiry}
		}
	}

	return &LockResponse{Success: false, Error: "lock not available"}
}

func (lm *LockManager) addToWaitQueue(lock *Lock, req *WaitRequest) {
	if lock.QueueMode == QueueModePriority {
		insertIdx := len(lock.WaitQueue)
		for i, existing := range lock.WaitQueue {
			if req.Priority > existing.Priority {
				insertIdx = i
				break
			}
		}
		lock.WaitQueue = append(lock.WaitQueue[:insertIdx], append([]*WaitRequest{req}, lock.WaitQueue[insertIdx:]...)...)
	} else {
		lock.WaitQueue = append(lock.WaitQueue, req)
	}
	lock.State = LockStateWaiting
}

func (lm *LockManager) removeFromWaitQueue(lock *Lock, req *WaitRequest) {
	lock.Mu.Lock()
	defer lock.Mu.Unlock()
	for i, wr := range lock.WaitQueue {
		if wr == req {
			lock.WaitQueue = append(lock.WaitQueue[:i], lock.WaitQueue[i+1:]...)
			break
		}
	}
	if len(lock.WaitQueue) == 0 && len(lock.Holders) == 0 {
		lock.State = LockStateFree
	}
}

func (lm *LockManager) wakeWaiters(lock *Lock) {
	lock.Mu.Lock()
	defer lock.Mu.Unlock()

	if len(lock.WaitQueue) == 0 {
		return
	}

	newQueue := make([]*WaitRequest, 0)
	for _, wr := range lock.WaitQueue {
		if time.Since(wr.LastHeartbeat) > lm.config.WaitQueueHeartbeatTimeout {
			continue
		}
		newQueue = append(newQueue, wr)
	}
	lock.WaitQueue = newQueue

	if lock.Type == LockTypeBarrier {
		barrierCapacity := lock.Capacity
		if barrierCapacity <= 0 {
			barrierCapacity = 1
		}
		for len(lock.WaitQueue) >= barrierCapacity {
			token := lm.tokenGenerator.NextToken()
			now := time.Now()
			for i := 0; i < barrierCapacity && i < len(lock.WaitQueue); i++ {
				wr := lock.WaitQueue[i]
				holder := &LockHolder{
					ClientID:      wr.ClientID,
					Token:         token,
					Mode:          wr.Mode,
					AcquiredAt:    now,
					LeaseExpiry:   now.Add(lock.LeaseTime),
					LastHeartbeat: now,
				}
				lock.Holders = append(lock.Holders, holder)
				lock.MaxToken = token
				lm.addHistory(lock, "barrier_released", wr.ClientID, wr.Mode, token)
				lm.metrics.IncrementActiveLocks()
				select {
				case wr.Notifier <- &LockResponse{Success: true, Token: token, ExpiresAt: holder.LeaseExpiry}:
				default:
				}
			}
			lock.WaitQueue = lock.WaitQueue[barrierCapacity:]
		}
		return
	}

	for len(lock.WaitQueue) > 0 {
		wr := lock.WaitQueue[0]

		var resp *LockResponse
		switch lock.Type {
		case LockTypeMutex:
			if len(lock.Holders) == 0 {
				token := lm.tokenGenerator.NextToken()
				now := time.Now()
				holder := &LockHolder{
					ClientID:      wr.ClientID,
					Token:         token,
					Mode:          LockModeWrite,
					AcquiredAt:    now,
					LeaseExpiry:   now.Add(lock.LeaseTime),
					LastHeartbeat: now,
				}
				lock.Holders = append(lock.Holders, holder)
				lock.MaxToken = token
				lm.addHistory(lock, "acquired", wr.ClientID, LockModeWrite, token)
				lm.metrics.IncrementActiveLocks()
				resp = &LockResponse{Success: true, Token: token, ExpiresAt: holder.LeaseExpiry}
			}

		case LockTypeRWLock:
			hasWriter := false
			hasUpgradePending := false
			for _, h := range lock.Holders {
				if h.Mode == LockModeWrite {
					hasWriter = true
				}
				if h.ClientID == wr.ClientID && h.Mode == LockModeRead && wr.Mode == LockModeWrite {
					hasUpgradePending = true
				}
			}

			if wr.Mode == LockModeWrite && len(lock.Holders) == 0 {
				token := lm.tokenGenerator.NextToken()
				now := time.Now()
				holder := &LockHolder{
					ClientID:      wr.ClientID,
					Token:         token,
					Mode:          LockModeWrite,
					AcquiredAt:    now,
					LeaseExpiry:   now.Add(lock.LeaseTime),
					LastHeartbeat: now,
				}
				lock.Holders = append(lock.Holders, holder)
				lock.MaxToken = token
				lm.addHistory(lock, "acquired", wr.ClientID, LockModeWrite, token)
				lm.metrics.IncrementActiveLocks()
				resp = &LockResponse{Success: true, Token: token, ExpiresAt: holder.LeaseExpiry}
			} else if wr.Mode == LockModeRead && !hasWriter && !hasUpgradePending {
				exists := false
				var existingToken uint64
				var existingExpiry time.Time
				for _, h := range lock.Holders {
					if h.ClientID == wr.ClientID {
						exists = true
						existingToken = h.Token
						existingExpiry = h.LeaseExpiry
					}
				}
				if !exists {
					token := lm.tokenGenerator.NextToken()
					now := time.Now()
					holder := &LockHolder{
						ClientID:      wr.ClientID,
						Token:         token,
						Mode:          LockModeRead,
						AcquiredAt:    now,
						LeaseExpiry:   now.Add(lock.LeaseTime),
						LastHeartbeat: now,
					}
					lock.Holders = append(lock.Holders, holder)
					lock.MaxToken = token
					lm.addHistory(lock, "acquired", wr.ClientID, LockModeRead, token)
					lm.metrics.IncrementActiveLocks()
					resp = &LockResponse{Success: true, Token: token, ExpiresAt: holder.LeaseExpiry}
				} else {
					resp = &LockResponse{Success: true, Token: existingToken, ExpiresAt: existingExpiry}
				}
			} else if hasUpgradePending {
				for i, h := range lock.Holders {
					if h.ClientID == wr.ClientID && h.Mode == LockModeRead {
						hasOtherReaders := false
						for _, other := range lock.Holders {
							if other.ClientID != wr.ClientID {
								hasOtherReaders = true
							}
						}
						if !hasOtherReaders {
							token := lm.tokenGenerator.NextToken()
							now := time.Now()
							lock.Holders[i].Mode = LockModeWrite
							lock.Holders[i].Token = token
							lock.Holders[i].LeaseExpiry = now.Add(lock.LeaseTime)
							lock.MaxToken = token
							lm.addHistory(lock, "upgraded", wr.ClientID, LockModeWrite, token)
							resp = &LockResponse{Success: true, Token: token, ExpiresAt: lock.Holders[i].LeaseExpiry}
						}
					}
				}
			}

		case LockTypeSemaphore:
			capacity := lock.Capacity
			if capacity <= 0 {
				capacity = 1
			}
			if len(lock.Holders) < capacity {
				token := lm.tokenGenerator.NextToken()
				now := time.Now()
				holder := &LockHolder{
					ClientID:      wr.ClientID,
					Token:         token,
					Mode:          LockModeWrite,
					AcquiredAt:    now,
					LeaseExpiry:   now.Add(lock.LeaseTime),
					LastHeartbeat: now,
				}
				lock.Holders = append(lock.Holders, holder)
				lock.MaxToken = token
				lm.addHistory(lock, "acquired", wr.ClientID, LockModeWrite, token)
				lm.metrics.IncrementActiveLocks()
				resp = &LockResponse{Success: true, Token: token, ExpiresAt: holder.LeaseExpiry}
			}
		}

		if resp != nil {
			lock.WaitQueue = lock.WaitQueue[1:]
			select {
			case wr.Notifier <- resp:
			default:
			}
		} else {
			break
		}
	}

	if len(lock.WaitQueue) == 0 && len(lock.Holders) == 0 {
		lock.State = LockStateFree
	}
}

func (lm *LockManager) Release(ctx context.Context, req *ReleaseRequest) (*LockResponse, error) {
	return lm.releaseInternal(ctx, req, false, "")
}

func (lm *LockManager) releaseInternal(ctx context.Context, req *ReleaseRequest, isCascade bool, cascadeParent string) (*LockResponse, error) {
	lockKey := req.LockID.String()

	lm.mu.RLock()
	lock, exists := lm.locks[lockKey]
	lm.mu.RUnlock()

	if !exists {
		return &LockResponse{Success: false, Error: "lock not found"}, nil
	}

	lock.Mu.Lock()
	defer lock.Mu.Unlock()

	found := false
	holderIdx := -1
	clientID := req.ClientID
	for i, h := range lock.Holders {
		if (req.Force && req.ClientID == h.ClientID) || (h.ClientID == req.ClientID && (req.Token == 0 || req.Token == h.Token)) {
			found = true
			holderIdx = i
			clientID = h.ClientID
			break
		}
	}

	if req.Force && req.ClientID == "admin" {
		if len(lock.Holders) > 0 {
			found = true
			holderIdx = 0
			clientID = lock.Holders[0].ClientID
		}
	}

	if !found {
		return &LockResponse{Success: false, Error: "not holder or invalid token"}, nil
	}

	holder := lock.Holders[holderIdx]
	newToken := uint64(0)
	if req.Force {
		newToken = lock.MaxToken + 1000
		if newToken > lm.tokenGenerator.NextToken() {
			if tg, ok := lm.tokenGenerator.(*SimpleTokenGenerator); ok {
				tg.SetCurrentToken(newToken)
			}
		}
		lock.MaxToken = newToken
	}

	event := LockEvent{
		Timestamp:       time.Now(),
		Event:           "released",
		ClientID:        holder.ClientID,
		Mode:            holder.Mode,
		Token:           holder.Token,
		CascadeReleased: isCascade,
		CascadeParent:   cascadeParent,
	}
	lock.History = append(lock.History, event)
	if len(lock.History) > 100 {
		lock.History = lock.History[len(lock.History)-100:]
	}

	lock.Holders = append(lock.Holders[:holderIdx], lock.Holders[holderIdx+1:]...)
	lm.metrics.DecrementActiveLocks()
	lm.metrics.RecordLockRelease(lock.Type)

	if len(lock.Holders) == 0 {
		lock.State = LockStateFree
	}

	go lm.wakeWaiters(lock)

	if !isCascade {
		go lm.CascadeRelease(req.LockID, clientID)
	}

	return &LockResponse{Success: true, Token: newToken}, nil
}

func (lm *LockManager) Heartbeat(ctx context.Context, req *HeartbeatRequest) (*LockResponse, error) {
	lockKey := req.LockID.String()

	lm.mu.RLock()
	lock, exists := lm.locks[lockKey]
	lm.mu.RUnlock()

	if !exists {
		lm.metrics.RecordHeartbeat(false)
		return &LockResponse{Success: false, Error: "lock not found"}, nil
	}

	lock.Mu.Lock()
	defer lock.Mu.Unlock()

	now := time.Now()
	for _, h := range lock.Holders {
		if h.ClientID == req.ClientID && (req.Token == 0 || req.Token == h.Token) {
			h.LastHeartbeat = now
			h.LeaseExpiry = now.Add(lock.LeaseTime)
			lm.metrics.RecordHeartbeat(true)
			return &LockResponse{Success: true, Token: h.Token, ExpiresAt: h.LeaseExpiry}, nil
		}
	}

	for _, wr := range lock.WaitQueue {
		if wr.ClientID == req.ClientID {
			wr.LastHeartbeat = now
			lm.metrics.RecordHeartbeat(true)
			return &LockResponse{Success: true}, nil
		}
	}

	lm.metrics.RecordHeartbeat(false)
	return &LockResponse{Success: false, Error: "not holder or in wait queue"}, nil
}

func (lm *LockManager) CheckLeases() map[string][]string {
	expired := make(map[string][]string)
	now := time.Now()

	lm.mu.RLock()
	locks := make([]*Lock, 0, len(lm.locks))
	for _, lock := range lm.locks {
		locks = append(locks, lock)
	}
	lm.mu.RUnlock()

	cascadeReleases := make([]struct {
		lockID   LockIdentifier
		clientID string
	}, 0)

	for _, lock := range locks {
		lock.Mu.Lock()
		expiredClients := make([]string, 0)
		remaining := make([]*LockHolder, 0)
		for _, h := range lock.Holders {
			if now.After(h.LeaseExpiry) {
				expiredClients = append(expiredClients, h.ClientID)
				event := LockEvent{
					Timestamp: time.Now(),
					Event:     "expired",
					ClientID:  h.ClientID,
					Mode:      h.Mode,
					Token:     h.Token,
				}
				lock.History = append(lock.History, event)
				if len(lock.History) > 100 {
					lock.History = lock.History[len(lock.History)-100:]
				}
				lm.metrics.DecrementActiveLocks()
				lm.metrics.RecordLockRelease(lock.Type)
				cascadeReleases = append(cascadeReleases, struct {
					lockID   LockIdentifier
					clientID string
				}{lock.ID, h.ClientID})
			} else {
				remaining = append(remaining, h)
			}
		}
		lock.Holders = remaining
		if len(expiredClients) > 0 {
			expired[lock.ID.String()] = expiredClients
			if len(lock.Holders) == 0 {
				lock.State = LockStateFree
			}
			go lm.wakeWaiters(lock)
		}

		cleanQueue := make([]*WaitRequest, 0)
		for _, wr := range lock.WaitQueue {
			if now.Sub(wr.LastHeartbeat) <= lm.config.WaitQueueHeartbeatTimeout {
				cleanQueue = append(cleanQueue, wr)
			}
		}
	lock.WaitQueue = cleanQueue

		lock.Mu.Unlock()
	}

	for _, cr := range cascadeReleases {
		go lm.CascadeRelease(cr.lockID, cr.clientID)
	}

	return expired
}

func (lm *LockManager) GetLock(lockID LockIdentifier) (*Lock, bool) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	lock, exists := lm.locks[lockID.String()]
	return lock, exists
}

func (lm *LockManager) ListLocks(namespace string) []*Lock {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	result := make([]*Lock, 0)
	for _, lock := range lm.locks {
		if namespace == "" || lock.ID.Namespace == namespace {
			result = append(result, lock)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID.String() < result[j].ID.String()
	})
	return result
}

func (lm *LockManager) AdjustSemaphoreCapacity(lockID LockIdentifier, newCapacity int) error {
	lockKey := lockID.String()
	lm.mu.RLock()
	lock, exists := lm.locks[lockKey]
	lm.mu.RUnlock()

	if !exists {
		return errors.New("lock not found")
	}
	if lock.Type != LockTypeSemaphore {
		return errors.New("not a semaphore")
	}

	lock.Mu.Lock()
	defer lock.Mu.Unlock()

	oldCapacity := lock.Capacity
	lock.Capacity = newCapacity

	if newCapacity > oldCapacity {
		go lm.wakeWaiters(lock)
	}

	return nil
}

func (lm *LockManager) AdjustLeaseTime(lockID LockIdentifier, newLease time.Duration) error {
	if newLease < lm.config.MinLeaseTime || newLease > lm.config.MaxLeaseTime {
		return errors.New("invalid lease time")
	}

	lockKey := lockID.String()
	lm.mu.RLock()
	lock, exists := lm.locks[lockKey]
	lm.mu.RUnlock()

	if !exists {
		return errors.New("lock not found")
	}

	lock.Mu.Lock()
	defer lock.Mu.Unlock()
	lock.LeaseTime = newLease

	return nil
}

func (lm *LockManager) ClearWaitQueue(lockID LockIdentifier) error {
	lockKey := lockID.String()
	lm.mu.RLock()
	lock, exists := lm.locks[lockKey]
	lm.mu.RUnlock()

	if !exists {
		return errors.New("lock not found")
	}

	lock.Mu.Lock()
	defer lock.Mu.Unlock()

	for _, wr := range lock.WaitQueue {
		select {
		case wr.Notifier <- &LockResponse{Success: false, Error: "queue cleared"}:
		default:
		}
	}
	lock.WaitQueue = make([]*WaitRequest, 0)
	if len(lock.Holders) == 0 {
		lock.State = LockStateFree
	}

	return nil
}

func (lm *LockManager) GetWaitForGraph() map[string][]string {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	graph := make(map[string][]string)
	for _, lock := range lm.locks {
		lock.Mu.RLock()
		for _, h := range lock.Holders {
			for _, wr := range lock.WaitQueue {
				if wr.ClientID != h.ClientID {
					graph[wr.ClientID] = append(graph[wr.ClientID], h.ClientID)
				}
			}
		}
		lock.Mu.RUnlock()
	}
	return graph
}

func (lm *LockManager) GetAllLocks() map[string]*Lock {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	result := make(map[string]*Lock)
	for k, v := range lm.locks {
		result[k] = v
	}
	return result
}

func (lm *LockManager) addHistory(lock *Lock, event string, clientID string, mode LockMode, token uint64) {
	lock.History = append(lock.History, LockEvent{
		Timestamp: time.Now(),
		Event:     event,
		ClientID:  clientID,
		Mode:      mode,
		Token:     token,
	})
	if len(lock.History) > 100 {
		lock.History = lock.History[len(lock.History)-100:]
	}
}

func (lm *LockManager) RestoreFromSnapshot(locks map[string]*Lock) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	maxToken := uint64(0)
	for _, lock := range locks {
		if lock.MaxToken > maxToken {
			maxToken = lock.MaxToken
		}
		for _, wr := range lock.WaitQueue {
			wr.Notifier = make(chan *LockResponse, 1)
		}
	}

	lm.locks = locks

	if tg, ok := lm.tokenGenerator.(*SimpleTokenGenerator); ok && maxToken > 0 {
		tg.SetCurrentToken(maxToken)
	}

	activeCount := 0
	for _, lock := range locks {
		activeCount += len(lock.Holders)
	}
	for i := 0; i < activeCount; i++ {
		lm.metrics.IncrementActiveLocks()
	}
}

func (lm *LockManager) CheckDependencies(lockID LockIdentifier, clientID string) *DependencyCheckResult {
	result := &DependencyCheckResult{
		MissingDependencies: make([]LockIdentifier, 0),
	}

	lockKey := lockID.String()
	lm.dependencyGraph.mu.RLock()
	defer lm.dependencyGraph.mu.RUnlock()

	visited := make(map[string]bool)
	var dfs func(string)
	dfs = func(current string) {
		if visited[current] {
			return
		}
		visited[current] = true

		for parent := range lm.dependencyGraph.Edges {
			if children, ok := lm.dependencyGraph.Edges[parent]; ok {
				if children[current] {
					ns, name := parseLockKey(parent)
					parentID := LockIdentifier{Namespace: ns, Name: name}
					if !lm.clientHoldsLock(parentID, clientID) {
						result.MissingDependencies = append(result.MissingDependencies, parentID)
					}
					dfs(parent)
				}
			}
		}
	}
	dfs(lockKey)

	return result
}

func parseLockKey(key string) (string, string) {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", parts[0]
}

func (lm *LockManager) clientHoldsLock(lockID LockIdentifier, clientID string) bool {
	lm.mu.RLock()
	lock, exists := lm.locks[lockID.String()]
	lm.mu.RUnlock()

	if !exists {
		return false
	}

	lock.Mu.RLock()
	defer lock.Mu.RUnlock()
	for _, h := range lock.Holders {
		if h.ClientID == clientID {
			return true
		}
	}
	return false
}

func (lm *LockManager) RegisterDependency(parent, child LockIdentifier) error {
	parentKey := parent.String()
	childKey := child.String()

	if parentKey == childKey {
		return errors.New("cannot create self-dependency")
	}

	lm.dependencyGraph.mu.Lock()
	defer lm.dependencyGraph.mu.Unlock()

	if lm.hasCycle(parentKey, childKey) {
		return errors.New("dependency would create a cycle")
	}

	if _, ok := lm.dependencyGraph.Edges[parentKey]; !ok {
		lm.dependencyGraph.Edges[parentKey] = make(map[string]bool)
	}
	lm.dependencyGraph.Edges[parentKey][childKey] = true

	lm.logger.Info("Dependency registered",
		zap.String("parent", parentKey),
		zap.String("child", childKey))
	return nil
}

func (lm *LockManager) RemoveDependency(parent, child LockIdentifier) error {
	parentKey := parent.String()
	childKey := child.String()

	lm.dependencyGraph.mu.Lock()
	defer lm.dependencyGraph.mu.Unlock()

	if children, ok := lm.dependencyGraph.Edges[parentKey]; ok {
		delete(children, childKey)
		if len(children) == 0 {
			delete(lm.dependencyGraph.Edges, parentKey)
		}
	}

	lm.logger.Info("Dependency removed",
		zap.String("parent", parentKey),
		zap.String("child", childKey))
	return nil
}

func (lm *LockManager) hasCycle(parent, child string) bool {
	visited := make(map[string]bool)
	var dfs func(string) bool
	dfs = func(current string) bool {
		if current == child {
			return true
		}
		if visited[current] {
			return false
		}
		visited[current] = true
		if children, ok := lm.dependencyGraph.Edges[current]; ok {
			for c := range children {
				if dfs(c) {
					return true
				}
			}
		}
		return false
	}
	return dfs(child)
}

func (lm *LockManager) GetDependencyGraph() *DependencyGraph {
	lm.dependencyGraph.mu.RLock()
	defer lm.dependencyGraph.mu.RUnlock()

	edges := make(map[string]map[string]bool)
	for k, v := range lm.dependencyGraph.Edges {
		edges[k] = make(map[string]bool)
		for k2, v2 := range v {
			edges[k][k2] = v2
		}
	}
	return &DependencyGraph{Edges: edges}
}

func (lm *LockManager) CascadeRelease(rootLock LockIdentifier, clientID string) {
	rootKey := rootLock.String()

	downstream := lm.getDownstreamLocks(rootKey, clientID)
	if len(downstream) == 0 {
		return
	}

	sorted := lm.topologicalSort(downstream, rootKey)

	ctx := context.Background()
	for _, lockKey := range sorted {
		ns, name := parseLockKey(lockKey)
		lockID := LockIdentifier{Namespace: ns, Name: name}

		req := &ReleaseRequest{
			LockID:   lockID,
			ClientID: clientID,
			Token:    0,
		}

		_, _ = lm.releaseInternal(ctx, req, true, rootKey)

		lm.logger.Info("Cascade released lock",
			zap.String("lock", lockKey),
			zap.String("client", clientID),
			zap.String("root", rootKey))
	}
}

func (lm *LockManager) getDownstreamLocks(rootKey string, clientID string) []string {
	lm.dependencyGraph.mu.RLock()
	defer lm.dependencyGraph.mu.RUnlock()

	result := make([]string, 0)
	visited := make(map[string]bool)

	var dfs func(string)
	dfs = func(current string) {
		if visited[current] {
			return
		}
		visited[current] = true

		if children, ok := lm.dependencyGraph.Edges[current]; ok {
			for child := range children {
				ns, name := parseLockKey(child)
				childID := LockIdentifier{Namespace: ns, Name: name}
				if lm.clientHoldsLock(childID, clientID) {
					result = append(result, child)
					dfs(child)
				}
			}
		}
	}
	dfs(rootKey)

	return result
}

func (lm *LockManager) topologicalSort(locks []string, rootKey string) []string {
	lockSet := make(map[string]bool)
	for _, l := range locks {
		lockSet[l] = true
	}
	lockSet[rootKey] = true

	inDegree := make(map[string]int)
	for _, l := range locks {
		inDegree[l] = 0
	}

	lm.dependencyGraph.mu.RLock()
	defer lm.dependencyGraph.mu.RUnlock()

	for parent, children := range lm.dependencyGraph.Edges {
		if lockSet[parent] {
			for child := range children {
				if lockSet[child] {
					inDegree[child]++
				}
			}
		}
	}

	result := make([]string, 0)
	for len(locks) > 0 {
		found := false
		for i, l := range locks {
			if inDegree[l] == 0 {
				result = append(result, l)
				locks = append(locks[:i], locks[i+1:]...)

				if children, ok := lm.dependencyGraph.Edges[l]; ok {
					for child := range children {
						if lockSet[child] {
							inDegree[child]--
						}
					}
				}
				found = true
				break
			}
		}
		if !found {
			break
		}
	}

	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result
}

func (lm *LockManager) CreateGroup(req *CreateGroupRequest) error {
	lm.groupsMu.Lock()
	defer lm.groupsMu.Unlock()

	if _, exists := lm.groups[req.Name]; exists {
		return fmt.Errorf("group %s already exists", req.Name)
	}

	group := &LockGroup{
		Name:        req.Name,
		Description: req.Description,
		LockIDs:     make([]LockIdentifier, 0),
		CreatedAt:   time.Now(),
		Timeout:     req.Timeout,
	}

	lm.groups[req.Name] = group
	lm.logger.Info("Lock group created", zap.String("group", req.Name))
	return nil
}

func (lm *LockManager) DeleteGroup(name string) error {
	lm.groupsMu.Lock()
	defer lm.groupsMu.Unlock()

	if _, exists := lm.groups[name]; !exists {
		return fmt.Errorf("group %s not found", name)
	}

	delete(lm.groups, name)
	lm.logger.Info("Lock group deleted", zap.String("group", name))
	return nil
}

func (lm *LockManager) AddLockToGroup(groupName string, lockID LockIdentifier) error {
	lm.groupsMu.Lock()
	defer lm.groupsMu.Unlock()

	group, exists := lm.groups[groupName]
	if !exists {
		return fmt.Errorf("group %s not found", groupName)
	}

	group.mu.Lock()
	defer group.mu.Unlock()

	for _, id := range group.LockIDs {
		if id.String() == lockID.String() {
			return fmt.Errorf("lock %s already in group", lockID.String())
		}
	}

	group.LockIDs = append(group.LockIDs, lockID)
	lm.logger.Info("Lock added to group",
		zap.String("group", groupName),
		zap.String("lock", lockID.String()))
	return nil
}

func (lm *LockManager) RemoveLockFromGroup(groupName string, lockID LockIdentifier) error {
	lm.groupsMu.Lock()
	defer lm.groupsMu.Unlock()

	group, exists := lm.groups[groupName]
	if !exists {
		return fmt.Errorf("group %s not found", groupName)
	}

	group.mu.Lock()
	defer group.mu.Unlock()

	foundIdx := -1
	for i, id := range group.LockIDs {
		if id.String() == lockID.String() {
			foundIdx = i
			break
		}
	}

	if foundIdx == -1 {
		return fmt.Errorf("lock %s not in group", lockID.String())
	}

	group.LockIDs = append(group.LockIDs[:foundIdx], group.LockIDs[foundIdx+1:]...)
	lm.logger.Info("Lock removed from group",
		zap.String("group", groupName),
		zap.String("lock", lockID.String()))
	return nil
}

func (lm *LockManager) ListGroups() []*LockGroupInfo {
	lm.groupsMu.RLock()
	defer lm.groupsMu.RUnlock()

	result := make([]*LockGroupInfo, 0, len(lm.groups))
	for _, group := range lm.groups {
		group.mu.RLock()
		info := &LockGroupInfo{
			Name:        group.Name,
			Description: group.Description,
			LockIDs:     append([]LockIdentifier{}, group.LockIDs...),
			CreatedAt:   group.CreatedAt,
			Timeout:     group.Timeout,
			Locks:       make([]*Lock, 0, len(group.LockIDs)),
		}

		for _, lockID := range group.LockIDs {
			if lock, exists := lm.GetLock(lockID); exists {
				info.Locks = append(info.Locks, lock)
			}
		}
		group.mu.RUnlock()
		result = append(result, info)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func (lm *LockManager) GetGroup(name string) (*LockGroupInfo, bool) {
	lm.groupsMu.RLock()
	group, exists := lm.groups[name]
	lm.groupsMu.RUnlock()

	if !exists {
		return nil, false
	}

	group.mu.RLock()
	defer group.mu.RUnlock()

	info := &LockGroupInfo{
		Name:        group.Name,
		Description: group.Description,
		LockIDs:     append([]LockIdentifier{}, group.LockIDs...),
		CreatedAt:   group.CreatedAt,
		Timeout:     group.Timeout,
		Locks:       make([]*Lock, 0, len(group.LockIDs)),
	}

	for _, lockID := range group.LockIDs {
		if lock, exists := lm.GetLock(lockID); exists {
			info.Locks = append(info.Locks, lock)
		}
	}

	return info, true
}

func (lm *LockManager) BatchAcquire(ctx context.Context, req *BatchAcquireRequest) (*BatchAcquireResponse, error) {
	lm.groupsMu.RLock()
	group, exists := lm.groups[req.GroupName]
	lm.groupsMu.RUnlock()

	if !exists {
		return &BatchAcquireResponse{
			Success: false,
			Error:   fmt.Sprintf("group %s not found", req.GroupName),
		}, nil
	}

	group.mu.RLock()
	lockIDs := append([]LockIdentifier{}, group.LockIDs...)
	timeout := group.Timeout
	group.mu.RUnlock()

	if len(lockIDs) == 0 {
		return &BatchAcquireResponse{
			Success: false,
			Error:   "group is empty",
		}, nil
	}

	if timeout == 0 {
		timeout = 30 * time.Second
	}

	acquireCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	acquired := make([]LockIdentifier, 0)
	tokens := make(map[string]uint64)
	expiresAt := make(map[string]time.Time)

	for _, lockID := range lockIDs {
		acquireReq := &AcquireRequest{
			LockID:     lockID,
			Type:       LockTypeMutex,
			Mode:       req.Mode,
			ClientID:   req.ClientID,
			LeaseTime:  req.LeaseTime,
			WaitTimeout: timeout,
			QueueMode:  QueueModeFIFO,
			TryLock:    false,
		}

		lm.mu.RLock()
		if l, ok := lm.locks[lockID.String()]; ok {
			acquireReq.Type = l.Type
		}
		lm.mu.RUnlock()

		resp, err := lm.Acquire(acquireCtx, acquireReq)
		if err != nil {
			lm.rollbackAcquired(acquired, req.ClientID)
			return &BatchAcquireResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to acquire %s: %v", lockID.String(), err),
			}, nil
		}

		if !resp.Success {
			lm.rollbackAcquired(acquired, req.ClientID)
			return &BatchAcquireResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to acquire %s: %s", lockID.String(), resp.Error),
			}, nil
		}

		acquired = append(acquired, lockID)
		tokens[lockID.String()] = resp.Token
		expiresAt[lockID.String()] = resp.ExpiresAt
	}

	return &BatchAcquireResponse{
		Success:   true,
		Tokens:    tokens,
		ExpiresAt: expiresAt,
	}, nil
}

func (lm *LockManager) rollbackAcquired(locks []LockIdentifier, clientID string) {
	ctx := context.Background()
	for i := len(locks) - 1; i >= 0; i-- {
		req := &ReleaseRequest{
			LockID:   locks[i],
			ClientID: clientID,
			Token:    0,
			Force:    true,
		}
		resp, err := lm.releaseInternal(ctx, req, false, "")
		if err != nil || !resp.Success {
			lm.logger.Warn("Failed to rollback lock during batch acquire",
				zap.String("lock", locks[i].String()),
				zap.String("client", clientID),
				zap.Error(err),
				zap.String("respError", resp.Error))
		} else {
			lm.logger.Info("Rolled back lock during batch acquire",
				zap.String("lock", locks[i].String()),
				zap.String("client", clientID))
		}
	}
}

func (lm *LockManager) BatchRelease(ctx context.Context, req *BatchReleaseRequest) (*BatchReleaseResponse, error) {
	lm.groupsMu.RLock()
	group, exists := lm.groups[req.GroupName]
	lm.groupsMu.RUnlock()

	if !exists {
		return &BatchReleaseResponse{
			Success: false,
			Error:   fmt.Sprintf("group %s not found", req.GroupName),
		}, nil
	}

	group.mu.RLock()
	lockIDs := append([]LockIdentifier{}, group.LockIDs...)
	group.mu.RUnlock()

	released := make([]LockIdentifier, 0)
	for i := len(lockIDs) - 1; i >= 0; i-- {
		releaseReq := &ReleaseRequest{
			LockID:   lockIDs[i],
			ClientID: req.ClientID,
			Token:    0,
		}

		resp, err := lm.Release(ctx, releaseReq)
		if err != nil {
			return &BatchReleaseResponse{
				Success:       false,
				Error:         fmt.Sprintf("failed to release %s: %v", lockIDs[i].String(), err),
				ReleasedLocks: released,
			}, nil
		}

		if !resp.Success && resp.Error != "lock not found" && resp.Error != "not holder or invalid token" {
			return &BatchReleaseResponse{
				Success:       false,
				Error:         fmt.Sprintf("failed to release %s: %s", lockIDs[i].String(), resp.Error),
				ReleasedLocks: released,
			}, nil
		}

		released = append(released, lockIDs[i])
	}

	return &BatchReleaseResponse{
		Success:       true,
		ReleasedLocks: released,
	}, nil
}

func (lm *LockManager) GetDependencyGraphWithState() map[string]interface{} {
	graph := lm.GetDependencyGraph()

	lockStates := make(map[string]string)
	lockClients := make(map[string][]string)

	lm.mu.RLock()
	for key, lock := range lm.locks {
		lock.Mu.RLock()
		lockStates[key] = string(lock.State)
		clients := make([]string, 0, len(lock.Holders))
		for _, h := range lock.Holders {
			clients = append(clients, h.ClientID)
		}
		lockClients[key] = clients
		lock.Mu.RUnlock()
	}
	lm.mu.RUnlock()

	return map[string]interface{}{
		"edges":   graph.Edges,
		"states":  lockStates,
		"clients": lockClients,
	}
}

func (lm *LockManager) RestoreDependencies(edges map[string]map[string]bool) {
	lm.dependencyGraph.mu.Lock()
	defer lm.dependencyGraph.mu.Unlock()
	lm.dependencyGraph.Edges = edges
}

func (lm *LockManager) RestoreGroups(groups map[string]*LockGroup) {
	lm.groupsMu.Lock()
	defer lm.groupsMu.Unlock()
	lm.groups = groups
}

func (lm *LockManager) GetAllDependencies() *DependencyGraph {
	return lm.GetDependencyGraph()
}

func (lm *LockManager) GetAllGroups() map[string]*LockGroup {
	lm.groupsMu.RLock()
	defer lm.groupsMu.RUnlock()
	result := make(map[string]*LockGroup)
	for k, v := range lm.groups {
		result[k] = v
	}
	return result
}
