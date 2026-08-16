package metrics

import (
	"time"

	"github.com/distributed-lock/backend/internal/lock"
	"github.com/distributed-lock/backend/internal/ratelimit"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type PrometheusCollector struct {
	lockHoldTime        *prometheus.HistogramVec
	lockWaitTime        *prometheus.HistogramVec
	locksActive         prometheus.Gauge
	lockQueueLength     *prometheus.GaugeVec
	heartbeatSuccess    prometheus.Counter
	heartbeatFailure    prometheus.Counter
	deadlockDetected    prometheus.Counter
	deadlockResolved    prometheus.Counter
	raftElections       prometheus.Counter
	raftReplicationLag  prometheus.Histogram
	lockReleasesTotal   *prometheus.CounterVec
	lockAcquiresTotal   *prometheus.CounterVec

	rateLimitCheckDuration *prometheus.HistogramVec
	rateLimitRejectsTotal  *prometheus.CounterVec
	rateLimitTokens        *prometheus.GaugeVec
	rateLimitWindowCount   *prometheus.GaugeVec
	rateLimitQueueDepth    *prometheus.GaugeVec
}

func NewPrometheusCollector() *PrometheusCollector {
	return &PrometheusCollector{
		lockHoldTime: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "distlock_lock_hold_time_seconds",
			Help:    "Time locks are held in seconds",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
		}, []string{"lock_type"}),
		lockWaitTime: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "distlock_lock_wait_time_seconds",
			Help:    "Time waiting to acquire lock in seconds",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 15),
		}, []string{"lock_type"}),
		locksActive: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "distlock_active_locks",
			Help: "Current number of active locks",
		}),
		lockQueueLength: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "distlock_wait_queue_length",
			Help: "Length of wait queue per lock",
		}, []string{"lock_name", "lock_type"}),
		heartbeatSuccess: promauto.NewCounter(prometheus.CounterOpts{
			Name: "distlock_heartbeat_success_total",
			Help: "Total successful heartbeats",
		}),
		heartbeatFailure: promauto.NewCounter(prometheus.CounterOpts{
			Name: "distlock_heartbeat_failure_total",
			Help: "Total failed heartbeats",
		}),
		deadlockDetected: promauto.NewCounter(prometheus.CounterOpts{
			Name: "distlock_deadlock_detected_total",
			Help: "Total deadlocks detected",
		}),
		deadlockResolved: promauto.NewCounter(prometheus.CounterOpts{
			Name: "distlock_deadlock_resolved_total",
			Help: "Total deadlocks resolved",
		}),
		raftElections: promauto.NewCounter(prometheus.CounterOpts{
			Name: "distlock_raft_elections_total",
			Help: "Total Raft elections",
		}),
		raftReplicationLag: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "distlock_raft_replication_lag_ms",
			Help:    "Raft log replication lag in milliseconds",
			Buckets: prometheus.LinearBuckets(1, 5, 20),
		}),
		lockReleasesTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "distlock_lock_releases_total",
			Help: "Total lock releases",
		}, []string{"lock_type"}),
		lockAcquiresTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "distlock_lock_acquires_total",
			Help: "Total lock acquires",
		}, []string{"lock_type"}),

		rateLimitCheckDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "distlock_ratelimit_check_duration_seconds",
			Help:    "Rate limit check duration in seconds",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 15),
		}, []string{"rule_key", "algorithm"}),
		rateLimitRejectsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "distlock_ratelimit_rejects_total",
			Help: "Total rate limit rejections",
		}, []string{"rule_key", "algorithm"}),
		rateLimitTokens: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "distlock_ratelimit_tokens",
			Help: "Current token bucket tokens",
		}, []string{"rule_key"}),
		rateLimitWindowCount: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "distlock_ratelimit_window_count",
			Help: "Current sliding window request count",
		}, []string{"rule_key"}),
		rateLimitQueueDepth: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "distlock_ratelimit_queue_depth",
			Help: "Current leaky bucket queue depth",
		}, []string{"rule_key"}),
	}
}

func (p *PrometheusCollector) RecordLockAcquire(lockType lock.LockType, duration time.Duration) {
	p.lockHoldTime.WithLabelValues(string(lockType)).Observe(duration.Seconds())
	p.lockAcquiresTotal.WithLabelValues(string(lockType)).Inc()
}

func (p *PrometheusCollector) RecordLockWait(lockType lock.LockType, duration time.Duration) {
	p.lockWaitTime.WithLabelValues(string(lockType)).Observe(duration.Seconds())
}

func (p *PrometheusCollector) RecordLockRelease(lockType lock.LockType) {
	p.lockReleasesTotal.WithLabelValues(string(lockType)).Inc()
}

func (p *PrometheusCollector) RecordHeartbeat(success bool) {
	if success {
		p.heartbeatSuccess.Inc()
	} else {
		p.heartbeatFailure.Inc()
	}
}

func (p *PrometheusCollector) IncrementActiveLocks() {
	p.locksActive.Inc()
}

func (p *PrometheusCollector) DecrementActiveLocks() {
	p.locksActive.Dec()
}

func (p *PrometheusCollector) RecordDeadlockDetected() {
	p.deadlockDetected.Inc()
}

func (p *PrometheusCollector) RecordDeadlockResolved() {
	p.deadlockResolved.Inc()
}

func (p *PrometheusCollector) RecordRaftElection() {
	p.raftElections.Inc()
}

func (p *PrometheusCollector) RecordRaftReplicationLag(lagMs float64) {
	p.raftReplicationLag.Observe(lagMs)
}

func (p *PrometheusCollector) SetQueueLength(lockName string, lockType string, length int) {
	p.lockQueueLength.WithLabelValues(lockName, lockType).Set(float64(length))
}

func (p *PrometheusCollector) RecordRateLimitCheck(ruleKey string, algorithm string, allowed bool, duration time.Duration) {
	p.rateLimitCheckDuration.WithLabelValues(ruleKey, algorithm).Observe(duration.Seconds())
}

func (p *PrometheusCollector) RecordRateLimitReject(ruleKey string, algorithm string) {
	p.rateLimitRejectsTotal.WithLabelValues(ruleKey, algorithm).Inc()
}

func (p *PrometheusCollector) SetRateLimitTokens(ruleKey string, tokens float64) {
	p.rateLimitTokens.WithLabelValues(ruleKey).Set(tokens)
}

func (p *PrometheusCollector) SetRateLimitWindowCount(ruleKey string, count int64) {
	p.rateLimitWindowCount.WithLabelValues(ruleKey).Set(float64(count))
}

func (p *PrometheusCollector) SetRateLimitQueueDepth(ruleKey string, depth int) {
	p.rateLimitQueueDepth.WithLabelValues(ruleKey).Set(float64(depth))
}

var _ ratelimit.MetricsRecorder = (*PrometheusCollector)(nil)
