package ratelimit

import (
	"sync"
	"time"
)

type Algorithm string

const (
	AlgorithmTokenBucket   Algorithm = "token_bucket"
	AlgorithmSlidingWindow Algorithm = "sliding_window"
	AlgorithmLeakyBucket   Algorithm = "leaky_bucket"
)

type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
)

type AdaptiveState string

const (
	AdaptiveStateNormal   AdaptiveState = "normal"
	AdaptiveStateTightening AdaptiveState = "tightening"
	AdaptiveStateMin      AdaptiveState = "min"
)

type AdaptiveConfig struct {
	Enabled             bool          `json:"enabled"`
	LatencyThresholdP99 time.Duration `json:"latency_threshold_p99"`
	CheckInterval       time.Duration `json:"check_interval"`
	AIMDAddPercent      float64       `json:"aimd_add_percent"`
	AIMDMultiplyFactor  float64       `json:"aimd_multiply_factor"`
	MinQuotaPercent     float64       `json:"min_quota_percent"`
	MaxQuotaPercent     float64       `json:"max_quota_percent"`
}

type AdaptiveStateData struct {
	CurrentQuotaMultiplier float64       `json:"current_quota_multiplier"`
	State                  AdaptiveState `json:"state"`
	LastCheckTime          time.Time     `json:"last_check_time"`
	LastAdjustTime         time.Time     `json:"last_adjust_time"`
	LatencyHistory         []float64     `json:"latency_history"`
}

type AdaptiveHistoryEntry struct {
	Timestamp         time.Time `json:"timestamp"`
	Direction         string    `json:"direction"`
	BeforeMultiplier  float64   `json:"before_multiplier"`
	AfterMultiplier   float64   `json:"after_multiplier"`
	BeforeQuota       float64   `json:"before_quota"`
	AfterQuota        float64   `json:"after_quota"`
	Reason            string    `json:"reason"`
	AverageLatency    float64   `json:"average_latency"`
	P99Latency        float64   `json:"p99_latency"`
}

type TrafficShapingConfig struct {
	SmoothingEnabled  bool          `json:"smoothing_enabled"`
	SmoothingInterval time.Duration `json:"smoothing_interval"`
	PriorityEnabled   bool          `json:"priority_enabled"`
	LowPriorityReserve float64      `json:"low_priority_reserve"`
	WarmUpEnabled     bool          `json:"warm_up_enabled"`
	WarmUpDuration    time.Duration `json:"warm_up_duration"`
	WarmUpInitialPercent float64    `json:"warm_up_initial_percent"`
	BorrowEnabled     bool          `json:"borrow_enabled"`
	BorrowThreshold   float64       `json:"borrow_threshold"`
	BorrowMaxPercent  float64       `json:"borrow_max_percent"`
}

type WarmUpState struct {
	Enabled     bool      `json:"enabled"`
	StartTime   time.Time `json:"start_time"`
	Duration    time.Duration `json:"duration"`
	InitialPercent float64 `json:"initial_percent"`
	CurrentMultiplier float64 `json:"current_multiplier"`
}

type PriorityQueueEntry struct {
	ClientID  string    `json:"client_id"`
	Priority  Priority  `json:"priority"`
	Timestamp time.Time `json:"timestamp"`
	Tokens    float64   `json:"tokens"`
	RequestKey string   `json:"request_key"`
}

type TokenBorrowRecord struct {
	FromRuleKey string    `json:"from_rule_key"`
	ToRuleKey   string    `json:"to_rule_key"`
	Amount      float64   `json:"amount"`
	Timestamp   time.Time `json:"timestamp"`
	Repaid      bool      `json:"repaid"`
	RepaidAt    time.Time `json:"repaid_at,omitempty"`
}

type NamespaceBorrowState struct {
	Namespace    string                    `json:"namespace"`
	BorrowRecords []TokenBorrowRecord       `json:"borrow_records"`
	LastRepayTime time.Time                 `json:"last_repay_time"`
}

type RateLimitRule struct {
	Key              string              `json:"key"`
	Algorithm        Algorithm           `json:"algorithm"`
	Capacity         float64             `json:"capacity"`
	Rate             float64             `json:"rate"`
	Window           time.Duration       `json:"window"`
	MaxRequests      int64               `json:"max_requests"`
	QueueDepth       int                 `json:"queue_depth"`
	ActiveStart      string              `json:"active_start"`
	ActiveEnd        string              `json:"active_end"`
	ActiveDays       []int               `json:"active_days"`
	PerClient        bool                `json:"per_client"`
	ParentKey        string              `json:"parent_key"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
	AdaptiveConfig   *AdaptiveConfig     `json:"adaptive_config,omitempty"`
	ShapingConfig    *TrafficShapingConfig `json:"shaping_config,omitempty"`
	OriginalCapacity float64             `json:"original_capacity"`
	OriginalRate     float64             `json:"original_rate"`
	OriginalMaxRequests int64            `json:"original_max_requests"`
}

type TokenBucketState struct {
	Tokens     float64   `json:"tokens"`
	LastRefill time.Time `json:"last_refill"`
}

type SlidingWindowState struct {
	Logs []SlidingWindowEntry `json:"logs"`
}

type SlidingWindowEntry struct {
	Timestamp time.Time `json:"timestamp"`
	ClientID  string    `json:"client_id"`
}

type LeakyBucketState struct {
	Queue       []LeakyBucketEntry `json:"queue"`
	LastLeak    time.Time          `json:"last_leak"`
	CurrentDepth int               `json:"current_depth"`
}

type LeakyBucketEntry struct {
	ClientID  string    `json:"client_id"`
	AddedAt   time.Time `json:"added_at"`
}

type ClientState struct {
	TokenBucket   *TokenBucketState   `json:"token_bucket,omitempty"`
	SlidingWindow *SlidingWindowState `json:"sliding_window,omitempty"`
	LeakyBucket   *LeakyBucketState   `json:"leaky_bucket,omitempty"`
}

type RuleState struct {
	Key                string                  `json:"key"`
	GlobalState        *ClientState            `json:"global_state"`
	ClientStates       map[string]*ClientState `json:"client_states"`
	AdaptiveState      *AdaptiveStateData      `json:"adaptive_state,omitempty"`
	WarmUpState        *WarmUpState            `json:"warm_up_state,omitempty"`
	PriorityQueue      []PriorityQueueEntry    `json:"priority_queue,omitempty"`
	BorrowedTokens     float64                 `json:"borrowed_tokens,omitempty"`
	LentTokens         float64                 `json:"lent_tokens,omitempty"`
	LastProcessTime    time.Time               `json:"last_process_time,omitempty"`
	CurrentSmoothDelay time.Duration           `json:"current_smooth_delay,omitempty"`
}

type RateLimitResult struct {
	Allowed   bool    `json:"allowed"`
	Remaining float64 `json:"remaining"`
	RetryAfter float64 `json:"retry_after"`
	RuleKey   string  `json:"rule_key"`
	Algorithm Algorithm `json:"algorithm"`
	Reason    string  `json:"reason"`
}

type RateLimitEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	ClientID   string    `json:"client_id"`
	RuleKey    string    `json:"rule_key"`
	Algorithm  Algorithm `json:"algorithm"`
	Reason     string    `json:"reason"`
	RequestKey string    `json:"request_key"`
}

type CheckRequest struct {
	Key        string   `json:"key"`
	ClientID   string   `json:"client_id"`
	Tokens     float64  `json:"tokens"`
	Priority   Priority `json:"priority,omitempty"`
	RequestKey string   `json:"request_key,omitempty"`
	Latency    float64  `json:"latency_ms,omitempty"`
}

type ReportLatencyRequest struct {
	RuleKey   string  `json:"rule_key"`
	ClientID  string  `json:"client_id"`
	LatencyMs float64 `json:"latency_ms"`
}

type AdjustQuotaRequest struct {
	RuleKey       string  `json:"rule_key"`
	NewMultiplier float64 `json:"new_multiplier"`
	Reason        string  `json:"reason"`
}

type BorrowTokensRequest struct {
	FromRuleKey string  `json:"from_rule_key"`
	ToRuleKey   string  `json:"to_rule_key"`
	Amount      float64 `json:"amount"`
}

type RepayTokensRequest struct {
	RuleKey string  `json:"rule_key"`
	Amount  float64 `json:"amount"`
}

type CreateRuleRequest struct {
	Key            string              `json:"key"`
	Algorithm      Algorithm           `json:"algorithm"`
	Capacity       float64             `json:"capacity"`
	Rate           float64             `json:"rate"`
	Window         time.Duration       `json:"window"`
	MaxRequests    int64               `json:"max_requests"`
	QueueDepth     int                 `json:"queue_depth"`
	ActiveStart    string              `json:"active_start"`
	ActiveEnd      string              `json:"active_end"`
	ActiveDays     []int               `json:"active_days"`
	PerClient      bool                `json:"per_client"`
	AdaptiveConfig *AdaptiveConfig     `json:"adaptive_config,omitempty"`
	ShapingConfig  *TrafficShapingConfig `json:"shaping_config,omitempty"`
}

type UpdateRuleRequest struct {
	Algorithm      Algorithm           `json:"algorithm"`
	Capacity       float64             `json:"capacity"`
	Rate           float64             `json:"rate"`
	Window         time.Duration       `json:"window"`
	MaxRequests    int64               `json:"max_requests"`
	QueueDepth     int                 `json:"queue_depth"`
	ActiveStart    string              `json:"active_start"`
	ActiveEnd      string              `json:"active_end"`
	ActiveDays     []int               `json:"active_days"`
	PerClient      bool                `json:"per_client"`
	AdaptiveConfig *AdaptiveConfig     `json:"adaptive_config,omitempty"`
	ShapingConfig  *TrafficShapingConfig `json:"shaping_config,omitempty"`
}

type RuleSnapshot struct {
	Rules            map[string]*RateLimitRule        `json:"rules"`
	States           map[string]*RuleState            `json:"states"`
	AdaptiveHistory  map[string][]AdaptiveHistoryEntry `json:"adaptive_history,omitempty"`
	BorrowStates     map[string]*NamespaceBorrowState  `json:"borrow_states,omitempty"`
	NamespaceGroups  map[string][]string              `json:"namespace_groups,omitempty"`
}

type MetricsRecorder interface {
	RecordRateLimitCheck(ruleKey string, algorithm string, allowed bool, duration time.Duration)
	RecordRateLimitReject(ruleKey string, algorithm string)
	SetRateLimitTokens(ruleKey string, tokens float64)
	SetRateLimitWindowCount(ruleKey string, count int64)
	SetRateLimitQueueDepth(ruleKey string, depth int)
}

type RateLimitManager struct {
	rules           map[string]*RateLimitRule
	states          map[string]*RuleState
	events          []RateLimitEvent
	adaptiveHistory map[string][]AdaptiveHistoryEntry
	borrowStates    map[string]*NamespaceBorrowState
	namespaceGroups map[string][]string
	mu              sync.RWMutex
	metrics         MetricsRecorder
	maxEvents       int
	maxAdaptiveHistory int
}
