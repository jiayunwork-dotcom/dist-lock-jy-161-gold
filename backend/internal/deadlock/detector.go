package deadlock

import (
	"context"
	"sync"
	"time"

	"github.com/distributed-lock/backend/internal/lock"
	"go.uber.org/zap"
)

type Detector struct {
	lockManager *lock.LockManager
	config      lock.Config
	logger      *zap.Logger
	metrics     lock.MetricsCollector
	notifiers   map[string]chan string
	mu          sync.RWMutex
}

func NewDetector(lm *lock.LockManager, config lock.Config, logger *zap.Logger, metrics lock.MetricsCollector) *Detector {
	return &Detector{
		lockManager: lm,
		config:      config,
		logger:      logger,
		metrics:     metrics,
		notifiers:   make(map[string]chan string),
	}
}

func (d *Detector) Subscribe(clientID string) chan string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch := make(chan string, 1)
	d.notifiers[clientID] = ch
	return ch
}

func (d *Detector) Unsubscribe(clientID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ch, exists := d.notifiers[clientID]; exists {
		close(ch)
		delete(d.notifiers, clientID)
	}
}

func (d *Detector) notify(clientID string, message string) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if ch, exists := d.notifiers[clientID]; exists {
		select {
		case ch <- message:
		default:
		}
	}
}

func (d *Detector) Start(ctx context.Context) {
	ticker := time.NewTicker(d.config.DeadlockCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.DetectAndResolve()
		}
	}
}

func (d *Detector) DetectAndResolve() [][]string {
	graph := d.lockManager.GetWaitForGraph()
	cycles := d.findCycles(graph)

	if len(cycles) > 0 {
		d.metrics.RecordDeadlockDetected()
		d.logger.Warn("Deadlock detected", zap.Int("cycle_count", len(cycles)), zap.Any("cycles", cycles))

		for _, cycle := range cycles {
			victim := d.chooseVictim(cycle)
			if victim != "" {
				d.forceReleaseLocks(victim)
				d.metrics.RecordDeadlockResolved()
				d.logger.Info("Deadlock resolved by releasing locks", zap.String("victim", victim), zap.Strings("cycle", cycle))
				d.notify(victim, "Your locks were released due to deadlock detection")
			}
		}
	}

	return cycles
}

func (d *Detector) findCycles(graph map[string][]string) [][]string {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	cycles := make([][]string, 0)
	path := make([]string, 0)

	var dfs func(node string) bool
	dfs = func(node string) bool {
		visited[node] = true
		recStack[node] = true
		path = append(path, node)

		for _, neighbor := range graph[node] {
			if !visited[neighbor] {
				if dfs(neighbor) {
					return true
				}
			} else if recStack[neighbor] {
				cycleStart := -1
				for i, p := range path {
					if p == neighbor {
						cycleStart = i
						break
					}
				}
				if cycleStart != -1 {
					cycle := make([]string, len(path)-cycleStart)
					copy(cycle, path[cycleStart:])
					cycles = append(cycles, cycle)
				}
				return true
			}
		}

		recStack[node] = false
		path = path[:len(path)-1]
		return false
	}

	for node := range graph {
		if !visited[node] {
			dfs(node)
		}
	}

	return cycles
}

func (d *Detector) chooseVictim(cycle []string) string {
	if len(cycle) == 0 {
		return ""
	}

	allLocks := d.lockManager.GetAllLocks()

	switch d.config.DeadlockStrategy {
	case lock.DeadlockStrategyLowestPriority:
		lowestPriority := 1000
		victim := cycle[0]
		for _, clientID := range cycle {
			for _, l := range allLocks {
				l.Mu.RLock()
				for _, wr := range l.WaitQueue {
					if wr.ClientID == clientID && wr.Priority < lowestPriority {
						lowestPriority = wr.Priority
						victim = clientID
					}
				}
				l.Mu.RUnlock()
			}
		}
		return victim

	default:
		oldestTime := time.Now()
		victim := cycle[0]
		for _, clientID := range cycle {
			for _, l := range allLocks {
				l.Mu.RLock()
				for _, h := range l.Holders {
					if h.ClientID == clientID && h.AcquiredAt.Before(oldestTime) {
						oldestTime = h.AcquiredAt
						victim = clientID
					}
				}
				l.Mu.RUnlock()
			}
		}
		return victim
	}
}

func (d *Detector) forceReleaseLocks(clientID string) {
	allLocks := d.lockManager.GetAllLocks()
	for _, l := range allLocks {
		l.Mu.RLock()
		hasHolder := false
		for _, h := range l.Holders {
			if h.ClientID == clientID {
				hasHolder = true
				break
			}
		}
		l.Mu.RUnlock()

		if hasHolder {
			_, err := d.lockManager.Release(context.Background(), &lock.ReleaseRequest{
				LockID:   l.ID,
				ClientID: clientID,
				Token:    0,
				Force:    true,
			})
			if err != nil {
				d.logger.Error("Failed to release lock during deadlock resolution",
					zap.String("lock", l.ID.String()),
					zap.String("client", clientID),
					zap.Error(err))
			}
		}
	}
}
