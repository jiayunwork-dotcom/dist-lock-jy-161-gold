package lock

import (
	"context"
	"sync"
	"time"
)

type LockType string

const (
	LockTypeMutex     LockType = "mutex"
	LockTypeRWLock    LockType = "rwlock"
	LockTypeSemaphore LockType = "semaphore"
	LockTypeBarrier   LockType = "barrier"
)

type LockMode string

const (
	LockModeRead  LockMode = "read"
	LockModeWrite LockMode = "write"
)

type QueueMode string

const (
	QueueModeFIFO       QueueMode = "fifo"
	QueueModePriority   QueueMode = "priority"
)

type LockState string

const (
	LockStateHeld    LockState = "held"
	LockStateWaiting LockState = "waiting"
	LockStateFree    LockState = "free"
)

type LockIdentifier struct {
	Namespace string
	Name      string
}

func (id LockIdentifier) String() string {
	return id.Namespace + "/" + id.Name
}

type Lock struct {
	ID           LockIdentifier
	Type         LockType
	Mode         LockMode
	Capacity     int
	State        LockState
	Holders      []*LockHolder
	WaitQueue    []*WaitRequest
	QueueMode    QueueMode
	LeaseTime    time.Duration
	CreatedAt    time.Time
	MaxToken     uint64
	History      []LockEvent
	Mu           sync.RWMutex `json:"-"`
	BarrierCount int
}

type LockHolder struct {
	ClientID    string
	Token       uint64
	Mode        LockMode
	AcquiredAt  time.Time
	LeaseExpiry time.Time
	LastHeartbeat time.Time
}

type WaitRequest struct {
	ClientID      string
	Mode          LockMode
	RequestedAt   time.Time
	Priority      int
	Timeout       time.Duration
	LastHeartbeat time.Time
	Notifier      chan *LockResponse `json:"-"`
	Ctx           context.Context   `json:"-"`
}

type LockResponse struct {
	Success   bool
	Token     uint64
	Error     string
	ExpiresAt time.Time
}

type LockEvent struct {
	Timestamp        time.Time
	Event            string
	ClientID         string
	Mode             LockMode
	Token            uint64
	CascadeReleased  bool   `json:",omitempty"`
	CascadeParent    string `json:",omitempty"`
}

type AcquireRequest struct {
	LockID     LockIdentifier
	Type       LockType
	Mode       LockMode
	ClientID   string
	LeaseTime  time.Duration
	WaitTimeout time.Duration
	QueueMode  QueueMode
	Priority   int
	TryLock    bool
	Capacity   int
}

type ReleaseRequest struct {
	LockID   LockIdentifier
	ClientID string
	Token    uint64
	Force    bool
}

type HeartbeatRequest struct {
	LockID   LockIdentifier
	ClientID string
	Token    uint64
}

type DeadlockStrategy string

const (
	DeadlockStrategyOldest    DeadlockStrategy = "oldest"
	DeadlockStrategyLowestPriority DeadlockStrategy = "lowest_priority"
)

type Config struct {
	DefaultLeaseTime     time.Duration
	MinLeaseTime         time.Duration
	MaxLeaseTime         time.Duration
	WaitQueueHeartbeatTimeout time.Duration
	DeadlockCheckInterval   time.Duration
	DeadlockStrategy        DeadlockStrategy
	RaftElectionTimeoutMin  time.Duration
	RaftElectionTimeoutMax  time.Duration
	RaftSnapshotInterval    time.Duration
}

func DefaultConfig() Config {
	return Config{
		DefaultLeaseTime:        30 * time.Second,
		MinLeaseTime:            5 * time.Second,
		MaxLeaseTime:            300 * time.Second,
		WaitQueueHeartbeatTimeout: 60 * time.Second,
		DeadlockCheckInterval:   5 * time.Second,
		DeadlockStrategy:        DeadlockStrategyOldest,
		RaftElectionTimeoutMin:  150 * time.Millisecond,
		RaftElectionTimeoutMax:  300 * time.Millisecond,
		RaftSnapshotInterval:    10 * time.Minute,
	}
}

type LockDependency struct {
	ParentLock LockIdentifier
	ChildLock  LockIdentifier
}

type DependencyGraph struct {
	Edges map[string]map[string]bool
	mu    sync.RWMutex `json:"-"`
}

func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		Edges: make(map[string]map[string]bool),
	}
}

type RegisterDependencyRequest struct {
	ParentLock LockIdentifier
	ChildLock  LockIdentifier
}

type RemoveDependencyRequest struct {
	ParentLock LockIdentifier
	ChildLock  LockIdentifier
}

type DependencyCheckResult struct {
	MissingDependencies []LockIdentifier
}

type CascadeReleaseInfo struct {
	RootLock     LockIdentifier
	ReleasedLocks []LockIdentifier
	ClientID     string
}

type LockGroup struct {
	Name        string
	Description string
	LockIDs     []LockIdentifier
	CreatedAt   time.Time
	Timeout     time.Duration
	mu          sync.RWMutex `json:"-"`
}

type LockGroupInfo struct {
	Name        string
	Description string
	LockIDs     []LockIdentifier
	CreatedAt   time.Time
	Timeout     time.Duration
	Locks       []*Lock
}

type CreateGroupRequest struct {
	Name        string
	Description string
	Timeout     time.Duration
}

type AddLockToGroupRequest struct {
	GroupName string
	LockID    LockIdentifier
}

type RemoveLockFromGroupRequest struct {
	GroupName string
	LockID    LockIdentifier
}

type BatchAcquireRequest struct {
	GroupName  string
	ClientID   string
	LeaseTime  time.Duration
	Mode       LockMode
}

type BatchReleaseRequest struct {
	GroupName string
	ClientID  string
}

type BatchAcquireResponse struct {
	Success     bool
	Error       string
	Tokens      map[string]uint64
	ExpiresAt   map[string]time.Time
}

type BatchReleaseResponse struct {
	Success       bool
	Error         string
	ReleasedLocks []LockIdentifier
}
