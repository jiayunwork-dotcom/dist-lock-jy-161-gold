package ratelimit

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func NewRateLimitManager(metrics MetricsRecorder) *RateLimitManager {
	return &RateLimitManager{
		rules:              make(map[string]*RateLimitRule),
		states:             make(map[string]*RuleState),
		events:             make([]RateLimitEvent, 0, 1000),
		adaptiveHistory:    make(map[string][]AdaptiveHistoryEntry),
		borrowStates:       make(map[string]*NamespaceBorrowState),
		namespaceGroups:    make(map[string][]string),
		metrics:            metrics,
		maxEvents:          1000,
		maxAdaptiveHistory: 200,
	}
}

func (m *RateLimitManager) CreateRule(req *CreateRuleRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.rules[req.Key]; exists {
		return nil
	}

	parentKey := ""
	parts := strings.Split(req.Key, "/")
	if len(parts) > 2 {
		parentKey = strings.Join(parts[:len(parts)-1], "/")
		if _, parentExists := m.rules[parentKey]; !parentExists {
			parentKey = ""
		}
	}

	namespace := ""
	if len(parts) >= 2 {
		namespace = parts[1]
	}

	now := time.Now()
	rule := &RateLimitRule{
		Key:                req.Key,
		Algorithm:          req.Algorithm,
		Capacity:           req.Capacity,
		Rate:               req.Rate,
		Window:             req.Window,
		MaxRequests:        req.MaxRequests,
		QueueDepth:         req.QueueDepth,
		ActiveStart:        req.ActiveStart,
		ActiveEnd:          req.ActiveEnd,
		ActiveDays:         req.ActiveDays,
		PerClient:          req.PerClient,
		ParentKey:          parentKey,
		CreatedAt:          now,
		UpdatedAt:          now,
		OriginalCapacity:   req.Capacity,
		OriginalRate:       req.Rate,
		OriginalMaxRequests: req.MaxRequests,
		AdaptiveConfig:     req.AdaptiveConfig,
		ShapingConfig:      req.ShapingConfig,
	}

	m.rules[req.Key] = rule
	m.states[req.Key] = m.initRuleState(rule)
	m.adaptiveHistory[req.Key] = make([]AdaptiveHistoryEntry, 0)

	if namespace != "" {
		if _, exists := m.namespaceGroups[namespace]; !exists {
			m.namespaceGroups[namespace] = make([]string, 0)
		}
		m.namespaceGroups[namespace] = append(m.namespaceGroups[namespace], req.Key)
	}

	return nil
}

func (m *RateLimitManager) UpdateRule(key string, req *UpdateRuleRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule, exists := m.rules[key]
	if !exists {
		return fmt.Errorf("rule %s not found", key)
	}

	rule.Algorithm = req.Algorithm
	rule.Capacity = req.Capacity
	rule.Rate = req.Rate
	rule.Window = req.Window
	rule.MaxRequests = req.MaxRequests
	rule.QueueDepth = req.QueueDepth
	rule.ActiveStart = req.ActiveStart
	rule.ActiveEnd = req.ActiveEnd
	rule.ActiveDays = req.ActiveDays
	rule.PerClient = req.PerClient
	rule.UpdatedAt = time.Now()

	if req.AdaptiveConfig != nil {
		rule.AdaptiveConfig = req.AdaptiveConfig
		state, exists := m.states[key]
		if exists && state.AdaptiveState == nil && req.AdaptiveConfig.Enabled {
			state.AdaptiveState = &AdaptiveStateData{
				CurrentQuotaMultiplier: 1.0,
				State:                  AdaptiveStateNormal,
				LastCheckTime:          time.Now(),
				LatencyHistory:         make([]float64, 0, 100),
			}
		}
	}

	if req.ShapingConfig != nil {
		rule.ShapingConfig = req.ShapingConfig
		state, exists := m.states[key]
		if exists {
			if req.ShapingConfig.WarmUpEnabled && state.WarmUpState == nil {
				state.WarmUpState = &WarmUpState{
					Enabled:         true,
					StartTime:       time.Now(),
					Duration:        req.ShapingConfig.WarmUpDuration,
					InitialPercent:  req.ShapingConfig.WarmUpInitialPercent,
					CurrentMultiplier: req.ShapingConfig.WarmUpInitialPercent / 100.0,
				}
			} else if !req.ShapingConfig.WarmUpEnabled {
				state.WarmUpState = nil
			}
		}
	}

	m.states[key] = m.initRuleState(rule)
	return nil
}

func (m *RateLimitManager) DeleteRule(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.rules[key]; !exists {
		return fmt.Errorf("rule %s not found", key)
	}

	delete(m.rules, key)
	delete(m.states, key)
	return nil
}

func (m *RateLimitManager) Check(req *CheckRequest) *RateLimitResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	start := time.Now()
	applicableRules := m.getApplicableRules(req.Key)

	if len(applicableRules) == 0 {
		return &RateLimitResult{
			Allowed:   true,
			Remaining: -1,
			RetryAfter: 0,
			RuleKey:   "",
			Algorithm: "",
			Reason:    "no_rules",
		}
	}

	if req.Latency > 0 {
		for _, rule := range applicableRules {
			if rule.AdaptiveConfig != nil && rule.AdaptiveConfig.Enabled {
				m.reportLatencyInternal(rule.Key, req.Latency)
			}
		}
	}

	for _, rule := range applicableRules {
		m.updateWarmUpStateInternal(rule.Key)
	}

	strictestResult := &RateLimitResult{
		Allowed:   true,
		Remaining: float64(1<<63 - 1),
	}

	var totalSmoothDelay time.Duration = 0

	for _, rule := range applicableRules {
		if !m.isRuleActive(rule) {
			continue
		}

		smoothDelay := m.processSmoothingDelayInternal(rule.Key)
		if smoothDelay > totalSmoothDelay {
			totalSmoothDelay = smoothDelay
		}

		if rule.ShapingConfig != nil && rule.ShapingConfig.PriorityEnabled {
			priority := req.Priority
			if priority == "" {
				priority = PriorityMedium
			}

			availableTokens := 0.0
			state, exists := m.states[rule.Key]
			if exists && state.GlobalState != nil && state.GlobalState.TokenBucket != nil {
				availableTokens = state.GlobalState.TokenBucket.Tokens
			}

			entry, tokensUsed := m.dequeuePriorityRequestInternal(rule.Key, availableTokens)
			if entry != nil {
				req.Tokens = tokensUsed
			} else if !resultWouldAllow(rule, req) {
				queueEntry := &PriorityQueueEntry{
					ClientID:   req.ClientID,
					Priority:   priority,
					Timestamp:  time.Now(),
					Tokens:     req.Tokens,
					RequestKey: req.Key,
				}
				m.enqueuePriorityRequestInternal(rule.Key, queueEntry)
				return &RateLimitResult{
					Allowed:   false,
					Remaining: 0,
					RetryAfter: float64(rule.ShapingConfig.SmoothingInterval.Seconds()),
					RuleKey:   rule.Key,
					Algorithm: rule.Algorithm,
					Reason:    "queued_by_priority",
				}
			}
		}

		var result *RateLimitResult
		switch rule.Algorithm {
		case AlgorithmTokenBucket:
			result = m.checkTokenBucket(rule, req)
		case AlgorithmSlidingWindow:
			result = m.checkSlidingWindow(rule, req)
		case AlgorithmLeakyBucket:
			result = m.checkLeakyBucket(rule, req)
		default:
			continue
		}

		if totalSmoothDelay > 0 && result.Allowed {
			result.RetryAfter = float64(totalSmoothDelay.Milliseconds()) / 1000.0
		}

		m.metrics.RecordRateLimitCheck(rule.Key, string(rule.Algorithm), result.Allowed, time.Since(start))

		if !result.Allowed {
			m.recordEvent(req, rule, result.Reason)
			m.metrics.RecordRateLimitReject(rule.Key, string(rule.Algorithm))
		}

		if !result.Allowed && strictestResult.Allowed {
			strictestResult = result
		} else if !result.Allowed && !strictestResult.Allowed {
			if result.RetryAfter > strictestResult.RetryAfter {
				strictestResult = result
			}
		}

		if result.Allowed && result.Remaining < strictestResult.Remaining {
			strictestResult.Remaining = result.Remaining
			strictestResult.RuleKey = result.RuleKey
			strictestResult.Algorithm = result.Algorithm
			strictestResult.RetryAfter = result.RetryAfter
		}
	}

	return strictestResult
}

func (m *RateLimitManager) reportLatencyInternal(ruleKey string, latencyMs float64) {
	state, exists := m.states[ruleKey]
	if !exists {
		return
	}

	if state.AdaptiveState == nil {
		state.AdaptiveState = &AdaptiveStateData{
			CurrentQuotaMultiplier: 1.0,
			State:                  AdaptiveStateNormal,
			LastCheckTime:          time.Now(),
			LatencyHistory:         make([]float64, 0, 100),
		}
	}

	state.AdaptiveState.LatencyHistory = append(state.AdaptiveState.LatencyHistory, latencyMs)
	if len(state.AdaptiveState.LatencyHistory) > 100 {
		state.AdaptiveState.LatencyHistory = state.AdaptiveState.LatencyHistory[len(state.AdaptiveState.LatencyHistory)-100:]
	}
}

func (m *RateLimitManager) updateWarmUpStateInternal(ruleKey string) {
	rule, exists := m.rules[ruleKey]
	if !exists {
		return
	}

	state, exists := m.states[ruleKey]
	if !exists || state.WarmUpState == nil || !state.WarmUpState.Enabled {
		return
	}

	warmUp := state.WarmUpState
	elapsed := time.Since(warmUp.StartTime)

	if elapsed >= warmUp.Duration {
		warmUp.CurrentMultiplier = 1.0
		warmUp.Enabled = false
		return
	}

	progress := elapsed.Seconds() / warmUp.Duration.Seconds()
	warmUp.CurrentMultiplier = (warmUp.InitialPercent / 100.0) + (1.0 - warmUp.InitialPercent/100.0) * (1 - math.Exp(-3*progress))
	m.applyQuotaMultiplier(rule, warmUp.CurrentMultiplier)
}

func (m *RateLimitManager) processSmoothingDelayInternal(ruleKey string) time.Duration {
	state, exists := m.states[ruleKey]
	if !exists {
		return 0
	}

	rule, exists := m.rules[ruleKey]
	if !exists || rule.ShapingConfig == nil || !rule.ShapingConfig.SmoothingEnabled {
		return 0
	}

	now := time.Now()
	interval := rule.ShapingConfig.SmoothingInterval
	if interval <= 0 {
		interval = 10 * time.Millisecond
	}

	if state.LastProcessTime.IsZero() {
		state.LastProcessTime = now
		return 0
	}

	elapsed := now.Sub(state.LastProcessTime)
	if elapsed < interval {
		delay := interval - elapsed
		state.CurrentSmoothDelay = delay
		state.LastProcessTime = now.Add(delay)
		return delay
	}

	state.LastProcessTime = now
	state.CurrentSmoothDelay = 0
	return 0
}

func (m *RateLimitManager) enqueuePriorityRequestInternal(ruleKey string, entry *PriorityQueueEntry) {
	state, exists := m.states[ruleKey]
	if !exists {
		return
	}

	state.PriorityQueue = append(state.PriorityQueue, *entry)
	sort.SliceStable(state.PriorityQueue, func(i, j int) bool {
		pi := priorityToInt(state.PriorityQueue[i].Priority)
		pj := priorityToInt(state.PriorityQueue[j].Priority)
		if pi != pj {
			return pi > pj
		}
		return state.PriorityQueue[i].Timestamp.Before(state.PriorityQueue[j].Timestamp)
	})
}

func (m *RateLimitManager) dequeuePriorityRequestInternal(ruleKey string, availableTokens float64) (*PriorityQueueEntry, float64) {
	state, exists := m.states[ruleKey]
	if !exists || len(state.PriorityQueue) == 0 {
		return nil, 0
	}

	rule, exists := m.rules[ruleKey]
	if !exists {
		return nil, 0
	}

	lowReserve := 0.0
	if rule.ShapingConfig != nil && rule.ShapingConfig.PriorityEnabled {
		lowReserve = rule.ShapingConfig.LowPriorityReserve
	}

	var selectedIndex int = -1
	var tokensUsed float64 = 0

	tokensForHigh := availableTokens * (1 - lowReserve)
	tokensForLow := availableTokens * lowReserve

	for i, entry := range state.PriorityQueue {
		tokensNeeded := entry.Tokens
		if tokensNeeded <= 0 {
			tokensNeeded = 1
		}

		if entry.Priority == PriorityLow {
			if tokensNeeded <= tokensForLow {
				selectedIndex = i
				tokensUsed = tokensNeeded
				break
			}
		} else {
			if tokensNeeded <= tokensForHigh {
				selectedIndex = i
				tokensUsed = tokensNeeded
				break
			}
		}
	}

	if selectedIndex >= 0 {
		entry := state.PriorityQueue[selectedIndex]
		state.PriorityQueue = append(state.PriorityQueue[:selectedIndex], state.PriorityQueue[selectedIndex+1:]...)
		return &entry, tokensUsed
	}

	return nil, 0
}

func resultWouldAllow(rule *RateLimitRule, req *CheckRequest) bool {
	return true
}

func (m *RateLimitManager) getApplicableRules(key string) []*RateLimitRule {
	rules := make([]*RateLimitRule, 0)

	directRule, exists := m.rules[key]
	if exists {
		rules = append(rules, directRule)
	}

	parts := strings.Split(key, "/")
	for i := len(parts) - 1; i > 1; i-- {
		parentKey := strings.Join(parts[:i], "/")
		if parentRule, pExists := m.rules[parentKey]; pExists {
			isOverridden := false
			for _, r := range rules {
				if r.Algorithm == parentRule.Algorithm {
					isOverridden = true
					break
				}
			}
			if !isOverridden {
				rules = append(rules, parentRule)
			}
		}
	}

	return rules
}

func (m *RateLimitManager) isRuleActive(rule *RateLimitRule) bool {
	if rule.ActiveStart == "" || rule.ActiveEnd == "" {
		return true
	}

	now := time.Now()
	if len(rule.ActiveDays) > 0 {
		weekday := int(now.Weekday())
		found := false
		for _, d := range rule.ActiveDays {
			if d == weekday {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	currentTime := now.Format("15:04")
	if currentTime < rule.ActiveStart || currentTime > rule.ActiveEnd {
		return false
	}

	return true
}

func (m *RateLimitManager) checkTokenBucket(rule *RateLimitRule, req *CheckRequest) *RateLimitResult {
	state := m.getClientState(rule, req.ClientID)
	if state.TokenBucket == nil {
		state.TokenBucket = &TokenBucketState{
			Tokens:     rule.Capacity,
			LastRefill: time.Now(),
		}
	}

	tb := state.TokenBucket
	now := time.Now()
	elapsed := now.Sub(tb.LastRefill).Seconds()
	tb.Tokens += elapsed * rule.Rate
	if tb.Tokens > rule.Capacity {
		tb.Tokens = rule.Capacity
	}
	tb.LastRefill = now

	tokens := req.Tokens
	if tokens <= 0 {
		tokens = 1
	}

	if tb.Tokens >= tokens {
		tb.Tokens -= tokens
		m.metrics.SetRateLimitTokens(rule.Key, tb.Tokens)
		return &RateLimitResult{
			Allowed:   true,
			Remaining: tb.Tokens,
			RetryAfter: 0,
			RuleKey:   rule.Key,
			Algorithm: AlgorithmTokenBucket,
		}
	}

	retryAfter := (tokens - tb.Tokens) / rule.Rate
	m.metrics.SetRateLimitTokens(rule.Key, tb.Tokens)
	return &RateLimitResult{
		Allowed:   false,
		Remaining: tb.Tokens,
		RetryAfter: retryAfter,
		RuleKey:   rule.Key,
		Algorithm: AlgorithmTokenBucket,
		Reason:    "token_bucket_exhausted",
	}
}

func (m *RateLimitManager) checkSlidingWindow(rule *RateLimitRule, req *CheckRequest) *RateLimitResult {
	state := m.getClientState(rule, req.ClientID)
	if state.SlidingWindow == nil {
		state.SlidingWindow = &SlidingWindowState{
			Logs: make([]SlidingWindowEntry, 0),
		}
	}

	sw := state.SlidingWindow
	now := time.Now()
	windowStart := now.Add(-rule.Window)

	filtered := make([]SlidingWindowEntry, 0, len(sw.Logs))
	for _, entry := range sw.Logs {
		if entry.Timestamp.After(windowStart) {
			filtered = append(filtered, entry)
		}
	}
	sw.Logs = filtered

	m.metrics.SetRateLimitWindowCount(rule.Key, int64(len(sw.Logs)))

	if int64(len(sw.Logs)) >= rule.MaxRequests {
		if len(sw.Logs) > 0 {
			oldest := sw.Logs[0].Timestamp
			retryAfter := rule.Window.Seconds() - now.Sub(oldest).Seconds()
			if retryAfter < 0 {
				retryAfter = 0
			}
			return &RateLimitResult{
				Allowed:   false,
				Remaining: 0,
				RetryAfter: retryAfter,
				RuleKey:   rule.Key,
				Algorithm: AlgorithmSlidingWindow,
				Reason:    "sliding_window_exhausted",
			}
		}
		return &RateLimitResult{
			Allowed:   false,
			Remaining: 0,
			RetryAfter: rule.Window.Seconds(),
			RuleKey:   rule.Key,
			Algorithm: AlgorithmSlidingWindow,
			Reason:    "sliding_window_exhausted",
		}
	}

	sw.Logs = append(sw.Logs, SlidingWindowEntry{
		Timestamp: now,
		ClientID:  req.ClientID,
	})
	remaining := float64(rule.MaxRequests - int64(len(sw.Logs)))

	return &RateLimitResult{
		Allowed:   true,
		Remaining: remaining,
		RetryAfter: 0,
		RuleKey:   rule.Key,
		Algorithm: AlgorithmSlidingWindow,
	}
}

func (m *RateLimitManager) checkLeakyBucket(rule *RateLimitRule, req *CheckRequest) *RateLimitResult {
	state := m.getClientState(rule, req.ClientID)
	if state.LeakyBucket == nil {
		state.LeakyBucket = &LeakyBucketState{
			Queue:        make([]LeakyBucketEntry, 0),
			LastLeak:     time.Now(),
			CurrentDepth: 0,
		}
	}

	lb := state.LeakyBucket
	now := time.Now()
	elapsed := now.Sub(lb.LastLeak).Seconds()
	leaked := int(elapsed * rule.Rate)
	if leaked > 0 {
		if leaked > lb.CurrentDepth {
			leaked = lb.CurrentDepth
		}
		lb.Queue = lb.Queue[leaked:]
		lb.CurrentDepth -= leaked
		lb.LastLeak = now
	}

	m.metrics.SetRateLimitQueueDepth(rule.Key, lb.CurrentDepth)

	if lb.CurrentDepth >= rule.QueueDepth {
		retryAfter := 1.0 / rule.Rate
		return &RateLimitResult{
			Allowed:   false,
			Remaining: 0,
			RetryAfter: retryAfter,
			RuleKey:   rule.Key,
			Algorithm: AlgorithmLeakyBucket,
			Reason:    "leaky_bucket_full",
		}
	}

	lb.Queue = append(lb.Queue, LeakyBucketEntry{
		ClientID: req.ClientID,
		AddedAt:  now,
	})
	lb.CurrentDepth++
	remaining := float64(rule.QueueDepth - lb.CurrentDepth)

	return &RateLimitResult{
		Allowed:   true,
		Remaining: remaining,
		RetryAfter: 0,
		RuleKey:   rule.Key,
		Algorithm: AlgorithmLeakyBucket,
	}
}

func (m *RateLimitManager) getClientState(rule *RateLimitRule, clientID string) *ClientState {
	ruleState, exists := m.states[rule.Key]
	if !exists {
		ruleState = &RuleState{
			Key:          rule.Key,
			GlobalState:  m.initClientState(rule),
			ClientStates: make(map[string]*ClientState),
		}
		m.states[rule.Key] = ruleState
	}

	if rule.PerClient && clientID != "" {
		clientState, exists := ruleState.ClientStates[clientID]
		if !exists {
			clientState = m.initClientState(rule)
			ruleState.ClientStates[clientID] = clientState
		}
		return clientState
	}

	return ruleState.GlobalState
}

func (m *RateLimitManager) initClientState(rule *RateLimitRule) *ClientState {
	cs := &ClientState{}
	switch rule.Algorithm {
	case AlgorithmTokenBucket:
		cs.TokenBucket = &TokenBucketState{
			Tokens:     rule.Capacity,
			LastRefill: time.Now(),
		}
	case AlgorithmSlidingWindow:
		cs.SlidingWindow = &SlidingWindowState{
			Logs: make([]SlidingWindowEntry, 0),
		}
	case AlgorithmLeakyBucket:
		cs.LeakyBucket = &LeakyBucketState{
			Queue:        make([]LeakyBucketEntry, 0),
			LastLeak:     time.Now(),
			CurrentDepth: 0,
		}
	}
	return cs
}

func (m *RateLimitManager) initRuleState(rule *RateLimitRule) *RuleState {
	state := &RuleState{
		Key:           rule.Key,
		GlobalState:   m.initClientState(rule),
		ClientStates:  make(map[string]*ClientState),
		PriorityQueue: make([]PriorityQueueEntry, 0),
	}

	if rule.AdaptiveConfig != nil && rule.AdaptiveConfig.Enabled {
		state.AdaptiveState = &AdaptiveStateData{
			CurrentQuotaMultiplier: 1.0,
			State:                  AdaptiveStateNormal,
			LastCheckTime:          time.Now(),
			LatencyHistory:         make([]float64, 0, 100),
		}
	}

	if rule.ShapingConfig != nil && rule.ShapingConfig.WarmUpEnabled {
		state.WarmUpState = &WarmUpState{
			Enabled:          true,
			StartTime:        time.Now(),
			Duration:         rule.ShapingConfig.WarmUpDuration,
			InitialPercent:   rule.ShapingConfig.WarmUpInitialPercent,
			CurrentMultiplier: rule.ShapingConfig.WarmUpInitialPercent / 100.0,
		}
	}

	return state
}

func (m *RateLimitManager) recordEvent(req *CheckRequest, rule *RateLimitRule, reason string) {
	event := RateLimitEvent{
		Timestamp:  time.Now(),
		ClientID:   req.ClientID,
		RuleKey:    rule.Key,
		Algorithm:  rule.Algorithm,
		Reason:     reason,
		RequestKey: req.Key,
	}
	m.events = append(m.events, event)
	if len(m.events) > m.maxEvents {
		m.events = m.events[len(m.events)-m.maxEvents:]
	}
}

func (m *RateLimitManager) GetRule(key string) (*RateLimitRule, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rule, exists := m.rules[key]
	return rule, exists
}

func (m *RateLimitManager) ListRules() []*RateLimitRule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*RateLimitRule, 0, len(m.rules))
	for _, rule := range m.rules {
		result = append(result, rule)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Key < result[j].Key
	})
	return result
}

func (m *RateLimitManager) GetEvents(filterKey string, filterClient string, filterStart, filterEnd time.Time) []RateLimitEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]RateLimitEvent, 0)
	for _, event := range m.events {
		if filterKey != "" && event.RuleKey != filterKey && event.RequestKey != filterKey {
			continue
		}
		if filterClient != "" && event.ClientID != filterClient {
			continue
		}
		if !filterStart.IsZero() && event.Timestamp.Before(filterStart) {
			continue
		}
		if !filterEnd.IsZero() && event.Timestamp.After(filterEnd) {
			continue
		}
		result = append(result, event)
	}
	return result
}

func (m *RateLimitManager) GetRuleState(key string) (*RuleState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, exists := m.states[key]
	return state, exists
}

func (m *RateLimitManager) GetSnapshot() *RuleSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rules := make(map[string]*RateLimitRule)
	for k, v := range m.rules {
		ruleCopy := *v
		rules[k] = &ruleCopy
	}

	states := make(map[string]*RuleState)
	for k, v := range m.states {
		states[k] = v
	}

	adaptiveHistory := make(map[string][]AdaptiveHistoryEntry)
	for k, v := range m.adaptiveHistory {
		historyCopy := make([]AdaptiveHistoryEntry, len(v))
		copy(historyCopy, v)
		adaptiveHistory[k] = historyCopy
	}

	borrowStates := make(map[string]*NamespaceBorrowState)
	for k, v := range m.borrowStates {
		borrowStates[k] = v
	}

	namespaceGroups := make(map[string][]string)
	for k, v := range m.namespaceGroups {
		groupsCopy := make([]string, len(v))
		copy(groupsCopy, v)
		namespaceGroups[k] = groupsCopy
	}

	return &RuleSnapshot{
		Rules:           rules,
		States:          states,
		AdaptiveHistory: adaptiveHistory,
		BorrowStates:    borrowStates,
		NamespaceGroups: namespaceGroups,
	}
}

func (m *RateLimitManager) RestoreFromSnapshot(snapshot *RuleSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rules = make(map[string]*RateLimitRule)
	for k, v := range snapshot.Rules {
		ruleCopy := *v
		m.rules[k] = &ruleCopy
	}

	m.states = make(map[string]*RuleState)
	for k, v := range snapshot.States {
		m.states[k] = v
	}

	if snapshot.AdaptiveHistory != nil {
		m.adaptiveHistory = make(map[string][]AdaptiveHistoryEntry)
		for k, v := range snapshot.AdaptiveHistory {
			m.adaptiveHistory[k] = v
		}
	}

	if snapshot.BorrowStates != nil {
		m.borrowStates = make(map[string]*NamespaceBorrowState)
		for k, v := range snapshot.BorrowStates {
			m.borrowStates[k] = v
		}
	}

	if snapshot.NamespaceGroups != nil {
		m.namespaceGroups = make(map[string][]string)
		for k, v := range snapshot.NamespaceGroups {
			m.namespaceGroups[k] = v
		}
	}
}

func (m *RateLimitManager) GetTopRejectedRules(n int) []RuleRejectStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counts := make(map[string]int)
	for _, event := range m.events {
		counts[event.RuleKey]++
	}

	stats := make([]RuleRejectStats, 0, len(counts))
	for key, count := range counts {
		stats = append(stats, RuleRejectStats{
			RuleKey:     key,
			RejectCount: count,
		})
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].RejectCount > stats[j].RejectCount
	})

	if len(stats) > n {
		stats = stats[:n]
	}
	return stats
}

type RuleRejectStats struct {
	RuleKey     string `json:"rule_key"`
	RejectCount int    `json:"reject_count"`
}

func (m *RateLimitManager) GetAllRules() map[string]*RateLimitRule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*RateLimitRule)
	for k, v := range m.rules {
		result[k] = v
	}
	return result
}

func (m *RateLimitManager) ReportLatency(req *ReportLatencyRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, exists := m.states[req.RuleKey]
	if !exists {
		return fmt.Errorf("rule %s not found", req.RuleKey)
	}

	rule, exists := m.rules[req.RuleKey]
	if !exists {
		return fmt.Errorf("rule %s not found", req.RuleKey)
	}

	if rule.AdaptiveConfig == nil || !rule.AdaptiveConfig.Enabled {
		return fmt.Errorf("adaptive rate limiting not enabled for rule %s", req.RuleKey)
	}

	if state.AdaptiveState == nil {
		state.AdaptiveState = &AdaptiveStateData{
			CurrentQuotaMultiplier: 1.0,
			State:                  AdaptiveStateNormal,
			LastCheckTime:          time.Now(),
			LatencyHistory:         make([]float64, 0, 100),
		}
	}

	state.AdaptiveState.LatencyHistory = append(state.AdaptiveState.LatencyHistory, req.LatencyMs)
	if len(state.AdaptiveState.LatencyHistory) > 100 {
		state.AdaptiveState.LatencyHistory = state.AdaptiveState.LatencyHistory[len(state.AdaptiveState.LatencyHistory)-100:]
	}

	return nil
}

func (m *RateLimitManager) CheckAndAdjustQuota(ruleKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule, exists := m.rules[ruleKey]
	if !exists {
		return fmt.Errorf("rule %s not found", ruleKey)
	}

	if rule.AdaptiveConfig == nil || !rule.AdaptiveConfig.Enabled {
		return fmt.Errorf("adaptive rate limiting not enabled for rule %s", ruleKey)
	}

	state, exists := m.states[ruleKey]
	if !exists || state.AdaptiveState == nil {
		return fmt.Errorf("adaptive state not initialized for rule %s", ruleKey)
	}

	adaptiveState := state.AdaptiveState
	config := rule.AdaptiveConfig

	now := time.Now()
	if now.Sub(adaptiveState.LastCheckTime) < config.CheckInterval {
		return nil
	}

	adaptiveState.LastCheckTime = now

	if len(adaptiveState.LatencyHistory) < 10 {
		return nil
	}

	sorted := make([]float64, len(adaptiveState.LatencyHistory))
	copy(sorted, adaptiveState.LatencyHistory)
	sort.Float64s(sorted)

	p99Index := int(float64(len(sorted)) * 0.99)
	if p99Index >= len(sorted) {
		p99Index = len(sorted) - 1
	}
	p99Latency := sorted[p99Index]

	var sum float64
	for _, v := range adaptiveState.LatencyHistory {
		sum += v
	}
	avgLatency := sum / float64(len(adaptiveState.LatencyHistory))

	thresholdMs := float64(config.LatencyThresholdP99) / float64(time.Millisecond)
	oldMultiplier := adaptiveState.CurrentQuotaMultiplier
	var newMultiplier float64
	var direction string
	var reason string

	if p99Latency > thresholdMs {
		newMultiplier = oldMultiplier * config.AIMDMultiplyFactor
		direction = "decrease"
		reason = fmt.Sprintf("P99 latency %.2fms exceeded threshold %.2fms", p99Latency, thresholdMs)

		minQuota := config.MinQuotaPercent / 100.0
		if newMultiplier < minQuota {
			newMultiplier = minQuota
		}
		adaptiveState.State = AdaptiveStateTightening
		if newMultiplier == minQuota {
			adaptiveState.State = AdaptiveStateMin
		}
	} else {
		newMultiplier = oldMultiplier * (1 + config.AIMDAddPercent/100.0)
		direction = "increase"
		reason = fmt.Sprintf("P99 latency %.2fms within threshold %.2fms", p99Latency, thresholdMs)

		maxQuota := config.MaxQuotaPercent / 100.0
		if newMultiplier > maxQuota {
			newMultiplier = maxQuota
		}
		if newMultiplier >= 1.0 {
			adaptiveState.State = AdaptiveStateNormal
			newMultiplier = 1.0
		}
	}

	if newMultiplier != oldMultiplier {
		oldQuota := m.getEffectiveQuota(rule)
		m.applyQuotaMultiplier(rule, newMultiplier)
		newQuota := m.getEffectiveQuota(rule)

		adaptiveState.LastAdjustTime = now
		adaptiveState.CurrentQuotaMultiplier = newMultiplier

		historyEntry := AdaptiveHistoryEntry{
			Timestamp:        now,
			Direction:        direction,
			BeforeMultiplier: oldMultiplier,
			AfterMultiplier:  newMultiplier,
			BeforeQuota:      oldQuota,
			AfterQuota:       newQuota,
			Reason:           reason,
			AverageLatency:   avgLatency,
			P99Latency:       p99Latency,
		}

		if _, exists := m.adaptiveHistory[ruleKey]; !exists {
			m.adaptiveHistory[ruleKey] = make([]AdaptiveHistoryEntry, 0)
		}
		m.adaptiveHistory[ruleKey] = append(m.adaptiveHistory[ruleKey], historyEntry)
		if len(m.adaptiveHistory[ruleKey]) > m.maxAdaptiveHistory {
			m.adaptiveHistory[ruleKey] = m.adaptiveHistory[ruleKey][len(m.adaptiveHistory[ruleKey])-m.maxAdaptiveHistory:]
		}
	}

	return nil
}

func (m *RateLimitManager) AdjustQuota(req *AdjustQuotaRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule, exists := m.rules[req.RuleKey]
	if !exists {
		return fmt.Errorf("rule %s not found", req.RuleKey)
	}

	state, exists := m.states[req.RuleKey]
	if !exists {
		return fmt.Errorf("state for rule %s not found", req.RuleKey)
	}

	oldQuota := m.getEffectiveQuota(rule)
	oldMultiplier := 1.0
	if state.AdaptiveState != nil {
		oldMultiplier = state.AdaptiveState.CurrentQuotaMultiplier
	}

	newMultiplier := req.NewMultiplier
	if rule.AdaptiveConfig != nil {
		minQuota := rule.AdaptiveConfig.MinQuotaPercent / 100.0
		maxQuota := rule.AdaptiveConfig.MaxQuotaPercent / 100.0
		if newMultiplier < minQuota {
			newMultiplier = minQuota
		}
		if newMultiplier > maxQuota {
			newMultiplier = maxQuota
		}
	}

	if state.AdaptiveState == nil {
		state.AdaptiveState = &AdaptiveStateData{
			CurrentQuotaMultiplier: newMultiplier,
			State:                  AdaptiveStateNormal,
			LastCheckTime:          time.Now(),
			LatencyHistory:         make([]float64, 0, 100),
		}
	} else {
		state.AdaptiveState.CurrentQuotaMultiplier = newMultiplier
	}

	m.applyQuotaMultiplier(rule, newMultiplier)
	newQuota := m.getEffectiveQuota(rule)

	direction := "manual"
	if newMultiplier > oldMultiplier {
		direction = "increase"
	} else if newMultiplier < oldMultiplier {
		direction = "decrease"
	}

	historyEntry := AdaptiveHistoryEntry{
		Timestamp:        time.Now(),
		Direction:        direction,
		BeforeMultiplier: oldMultiplier,
		AfterMultiplier:  newMultiplier,
		BeforeQuota:      oldQuota,
		AfterQuota:       newQuota,
		Reason:           req.Reason,
	}

	if _, exists := m.adaptiveHistory[req.RuleKey]; !exists {
		m.adaptiveHistory[req.RuleKey] = make([]AdaptiveHistoryEntry, 0)
	}
	m.adaptiveHistory[req.RuleKey] = append(m.adaptiveHistory[req.RuleKey], historyEntry)
	if len(m.adaptiveHistory[req.RuleKey]) > m.maxAdaptiveHistory {
		m.adaptiveHistory[req.RuleKey] = m.adaptiveHistory[req.RuleKey][len(m.adaptiveHistory[req.RuleKey])-m.maxAdaptiveHistory:]
	}

	return nil
}

func (m *RateLimitManager) GetAdaptiveHistory(ruleKey string) ([]AdaptiveHistoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	history, exists := m.adaptiveHistory[ruleKey]
	if !exists {
		return make([]AdaptiveHistoryEntry, 0), nil
	}

	result := make([]AdaptiveHistoryEntry, len(history))
	copy(result, history)
	return result, nil
}

func (m *RateLimitManager) getEffectiveQuota(rule *RateLimitRule) float64 {
	switch rule.Algorithm {
	case AlgorithmTokenBucket:
		return rule.Rate
	case AlgorithmSlidingWindow:
		return float64(rule.MaxRequests)
	case AlgorithmLeakyBucket:
		return rule.Rate
	default:
		return rule.Rate
	}
}

func (m *RateLimitManager) applyQuotaMultiplier(rule *RateLimitRule, multiplier float64) {
	switch rule.Algorithm {
	case AlgorithmTokenBucket:
		rule.Capacity = rule.OriginalCapacity * multiplier
		rule.Rate = rule.OriginalRate * multiplier
	case AlgorithmSlidingWindow:
		rule.MaxRequests = int64(float64(rule.OriginalMaxRequests) * multiplier)
	case AlgorithmLeakyBucket:
		rule.Rate = rule.OriginalRate * multiplier
		rule.QueueDepth = int(float64(rule.QueueDepth) * multiplier)
	}
}

func (m *RateLimitManager) UpdateWarmUpState(ruleKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule, exists := m.rules[ruleKey]
	if !exists {
		return fmt.Errorf("rule %s not found", ruleKey)
	}

	state, exists := m.states[ruleKey]
	if !exists || state.WarmUpState == nil || !state.WarmUpState.Enabled {
		return nil
	}

	warmUp := state.WarmUpState
	elapsed := time.Since(warmUp.StartTime)

	if elapsed >= warmUp.Duration {
		warmUp.CurrentMultiplier = 1.0
		warmUp.Enabled = false
		return nil
	}

	progress := elapsed.Seconds() / warmUp.Duration.Seconds()
	warmUp.CurrentMultiplier = (warmUp.InitialPercent / 100.0) + (1.0 - warmUp.InitialPercent/100.0) * (1 - math.Exp(-3*progress))

	m.applyQuotaMultiplier(rule, warmUp.CurrentMultiplier)

	return nil
}

func (m *RateLimitManager) EnqueuePriorityRequest(ruleKey string, entry *PriorityQueueEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, exists := m.states[ruleKey]
	if !exists {
		return fmt.Errorf("rule %s not found", ruleKey)
	}

	rule, exists := m.rules[ruleKey]
	if !exists {
		return fmt.Errorf("rule %s not found", ruleKey)
	}

	if rule.ShapingConfig == nil || !rule.ShapingConfig.PriorityEnabled {
		return fmt.Errorf("priority queue not enabled for rule %s", ruleKey)
	}

	state.PriorityQueue = append(state.PriorityQueue, *entry)

	sort.SliceStable(state.PriorityQueue, func(i, j int) bool {
		pi := priorityToInt(state.PriorityQueue[i].Priority)
		pj := priorityToInt(state.PriorityQueue[j].Priority)
		if pi != pj {
			return pi > pj
		}
		return state.PriorityQueue[i].Timestamp.Before(state.PriorityQueue[j].Timestamp)
	})

	return nil
}

func priorityToInt(p Priority) int {
	switch p {
	case PriorityHigh:
		return 3
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 1
	default:
		return 2
	}
}

func (m *RateLimitManager) DequeuePriorityRequest(ruleKey string, availableTokens float64) (*PriorityQueueEntry, float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, exists := m.states[ruleKey]
	if !exists {
		return nil, 0, fmt.Errorf("rule %s not found", ruleKey)
	}

	rule, exists := m.rules[ruleKey]
	if !exists {
		return nil, 0, fmt.Errorf("rule %s not found", ruleKey)
	}

	if len(state.PriorityQueue) == 0 {
		return nil, 0, nil
	}

	lowReserve := 0.0
	if rule.ShapingConfig != nil && rule.ShapingConfig.PriorityEnabled {
		lowReserve = rule.ShapingConfig.LowPriorityReserve
	}

	var selectedIndex int = -1
	var tokensUsed float64 = 0

	tokensForHigh := availableTokens * (1 - lowReserve)
	tokensForLow := availableTokens * lowReserve

	for i, entry := range state.PriorityQueue {
		tokensNeeded := entry.Tokens
		if tokensNeeded <= 0 {
			tokensNeeded = 1
		}

		if entry.Priority == PriorityLow {
			if tokensNeeded <= tokensForLow {
				selectedIndex = i
				tokensUsed = tokensNeeded
				break
			}
		} else {
			if tokensNeeded <= tokensForHigh {
				selectedIndex = i
				tokensUsed = tokensNeeded
				break
			}
		}
	}

	if selectedIndex >= 0 {
		entry := state.PriorityQueue[selectedIndex]
		state.PriorityQueue = append(state.PriorityQueue[:selectedIndex], state.PriorityQueue[selectedIndex+1:]...)
		return &entry, tokensUsed, nil
	}

	return nil, 0, nil
}

func (m *RateLimitManager) BorrowTokens(req *BorrowTokensRequest) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fromRule, exists := m.rules[req.FromRuleKey]
	if !exists {
		return 0, fmt.Errorf("source rule %s not found", req.FromRuleKey)
	}

	toRule, exists := m.rules[req.ToRuleKey]
	if !exists {
		return 0, fmt.Errorf("target rule %s not found", req.ToRuleKey)
	}

	if fromRule.ShapingConfig == nil || !fromRule.ShapingConfig.BorrowEnabled ||
		toRule.ShapingConfig == nil || !toRule.ShapingConfig.BorrowEnabled {
		return 0, fmt.Errorf("token borrowing not enabled for one or both rules")
	}

	fromParts := strings.Split(req.FromRuleKey, "/")
	toParts := strings.Split(req.ToRuleKey, "/")
	if len(fromParts) < 2 || len(toParts) < 2 || fromParts[1] != toParts[1] {
		return 0, fmt.Errorf("rules must be in the same namespace to borrow tokens")
	}

	fromState, exists := m.states[req.FromRuleKey]
	if !exists {
		return 0, fmt.Errorf("source state not found")
	}

	toState, exists := m.states[req.ToRuleKey]
	if !exists {
		return 0, fmt.Errorf("target state not found")
	}

	usageRatio := 0.0
	if fromRule.Capacity > 0 && fromState.GlobalState != nil && fromState.GlobalState.TokenBucket != nil {
		usageRatio = 1.0 - (fromState.GlobalState.TokenBucket.Tokens / fromRule.Capacity)
	}

	if usageRatio > fromRule.ShapingConfig.BorrowThreshold {
		return 0, fmt.Errorf("source rule usage %.2f exceeds borrow threshold %.2f", usageRatio, fromRule.ShapingConfig.BorrowThreshold)
	}

	maxBorrow := fromRule.Capacity * fromRule.ShapingConfig.BorrowMaxPercent / 100.0
	available := 0.0
	if fromState.GlobalState != nil && fromState.GlobalState.TokenBucket != nil {
		available = fromState.GlobalState.TokenBucket.Tokens
	}

	borrowAmount := math.Min(math.Min(req.Amount, maxBorrow), available)
	if borrowAmount <= 0 {
		return 0, fmt.Errorf("no tokens available to borrow")
	}

	fromState.GlobalState.TokenBucket.Tokens -= borrowAmount
	toState.BorrowedTokens += borrowAmount
	fromState.LentTokens += borrowAmount

	if toState.GlobalState != nil && toState.GlobalState.TokenBucket != nil {
		toState.GlobalState.TokenBucket.Tokens += borrowAmount
	}

	namespace := fromParts[1]
	if _, exists := m.borrowStates[namespace]; !exists {
		m.borrowStates[namespace] = &NamespaceBorrowState{
			Namespace:     namespace,
			BorrowRecords: make([]TokenBorrowRecord, 0),
		}
	}

	record := TokenBorrowRecord{
		FromRuleKey: req.FromRuleKey,
		ToRuleKey:   req.ToRuleKey,
		Amount:      borrowAmount,
		Timestamp:   time.Now(),
		Repaid:      false,
	}
	m.borrowStates[namespace].BorrowRecords = append(m.borrowStates[namespace].BorrowRecords, record)

	return borrowAmount, nil
}

func (m *RateLimitManager) RepayTokens(req *RepayTokensRequest) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rule, exists := m.rules[req.RuleKey]
	if !exists {
		return 0, fmt.Errorf("rule %s not found", req.RuleKey)
	}

	if rule.ShapingConfig == nil || !rule.ShapingConfig.BorrowEnabled {
		return 0, fmt.Errorf("token borrowing not enabled for rule %s", req.RuleKey)
	}

	state, exists := m.states[req.RuleKey]
	if !exists {
		return 0, fmt.Errorf("state for rule %s not found", req.RuleKey)
	}

	if state.BorrowedTokens <= 0 {
		return 0, nil
	}

	parts := strings.Split(req.RuleKey, "/")
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid rule key format")
	}
	namespace := parts[1]

	borrowState, exists := m.borrowStates[namespace]
	if !exists {
		return 0, nil
	}

	repayAmount := math.Min(req.Amount, state.BorrowedTokens)
	if repayAmount <= 0 {
		return 0, nil
	}

	if state.GlobalState != nil && state.GlobalState.TokenBucket != nil {
		state.GlobalState.TokenBucket.Tokens -= repayAmount
	}

	remainingRepay := repayAmount
	for i := range borrowState.BorrowRecords {
		if borrowState.BorrowRecords[i].ToRuleKey == req.RuleKey && !borrowState.BorrowRecords[i].Repaid {
			repayToThis := math.Min(remainingRepay, borrowState.BorrowRecords[i].Amount)
			fromState, exists := m.states[borrowState.BorrowRecords[i].FromRuleKey]
			if exists && fromState.GlobalState != nil && fromState.GlobalState.TokenBucket != nil {
				fromState.GlobalState.TokenBucket.Tokens += repayToThis
				fromState.LentTokens -= repayToThis
			}

			borrowState.BorrowRecords[i].Amount -= repayToThis
			if borrowState.BorrowRecords[i].Amount <= 0.001 {
				borrowState.BorrowRecords[i].Repaid = true
				borrowState.BorrowRecords[i].RepaidAt = time.Now()
			}

			remainingRepay -= repayToThis
			if remainingRepay <= 0 {
				break
			}
		}
	}

	state.BorrowedTokens -= (repayAmount - remainingRepay)
	borrowState.LastRepayTime = time.Now()

	return repayAmount - remainingRepay, nil
}

func (m *RateLimitManager) GetBorrowFlow(namespace string) ([]TokenBorrowRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	borrowState, exists := m.borrowStates[namespace]
	if !exists {
		return make([]TokenBorrowRecord, 0), nil
	}

	result := make([]TokenBorrowRecord, len(borrowState.BorrowRecords))
	copy(result, borrowState.BorrowRecords)
	return result, nil
}

func (m *RateLimitManager) ProcessSmoothingDelay(ruleKey string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, exists := m.states[ruleKey]
	if !exists {
		return 0
	}

	rule, exists := m.rules[ruleKey]
	if !exists || rule.ShapingConfig == nil || !rule.ShapingConfig.SmoothingEnabled {
		return 0
	}

	now := time.Now()
	interval := rule.ShapingConfig.SmoothingInterval
	if interval <= 0 {
		interval = 10 * time.Millisecond
	}

	if state.LastProcessTime.IsZero() {
		state.LastProcessTime = now
		return 0
	}

	elapsed := now.Sub(state.LastProcessTime)
	if elapsed < interval {
		delay := interval - elapsed
		state.CurrentSmoothDelay = delay
		state.LastProcessTime = now.Add(delay)
		return delay
	}

	state.LastProcessTime = now
	state.CurrentSmoothDelay = 0
	return 0
}

func (m *RateLimitManager) GetEffectiveQuotaMultiplier(ruleKey string) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, exists := m.states[ruleKey]
	if !exists {
		return 1.0, fmt.Errorf("rule %s not found", ruleKey)
	}

	multiplier := 1.0

	if state.AdaptiveState != nil {
		multiplier = state.AdaptiveState.CurrentQuotaMultiplier
	}

	if state.WarmUpState != nil && state.WarmUpState.Enabled {
		multiplier = math.Min(multiplier, state.WarmUpState.CurrentMultiplier)
	}

	return multiplier, nil
}

func (m *RateLimitManager) GetNamespaceRules(namespace string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rules, exists := m.namespaceGroups[namespace]
	if !exists {
		return make([]string, 0)
	}
	result := make([]string, len(rules))
	copy(result, rules)
	return result
}
