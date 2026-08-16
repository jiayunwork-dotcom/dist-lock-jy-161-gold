export type LockType = 'mutex' | 'rwlock' | 'semaphore' | 'barrier';
export type LockMode = 'read' | 'write';
export type LockState = 'held' | 'waiting' | 'free';
export type QueueMode = 'fifo' | 'priority';
export type RaftState = 'Leader' | 'Follower' | 'Candidate';

export interface LockHolder {
  client_id: string;
  token: number;
  mode: LockMode;
  acquired_at: string;
  lease_expiry: string;
  remaining_lease: number;
}

export interface WaitRequest {
  client_id: string;
  mode: LockMode;
  requested_at: string;
  priority: number;
  wait_time: number;
}

export interface LockEvent {
  timestamp: string;
  event: string;
  client_id: string;
  mode: LockMode;
  token: number;
  cascade_released?: boolean;
  cascade_parent?: string;
}

export interface LockDependency {
  parent_namespace: string;
  parent_name: string;
  child_namespace: string;
  child_name: string;
}

export interface DependencyGraphData {
  edges: Record<string, Record<string, boolean>>;
  states: Record<string, LockState>;
  clients: Record<string, string[]>;
}

export interface LockGroupInfo {
  name: string;
  description: string;
  lock_ids: string[];
  locks: LockInfo[];
  created_at: string;
  timeout: number;
}

export interface GraphNode {
  id: string;
  label: string;
  state: LockState;
  clients: string[];
}

export interface GraphEdge {
  source: string;
  target: string;
}

export interface LockInfo {
  id: string;
  namespace: string;
  name: string;
  type: LockType;
  state: LockState;
  holders: LockHolder[];
  wait_queue: WaitRequest[];
  wait_length: number;
  queue_mode: QueueMode;
  lease_time: number;
  capacity: number;
  created_at: string;
  max_token: number;
  history?: LockEvent[];
}

export interface ClusterNode {
  id: string;
  address: string;
  state?: RaftState;
}

export interface ClusterStatus {
  node_id: string;
  state: string;
  leader: string;
  last_index: number;
  applied_index: number;
  servers: ClusterNode[];
}

export interface Alert {
  ID: string;
  Name: string;
  Message: string;
  Severity: string;
  Timestamp: string;
  Value: number;
  Threshold: number;
}

export interface AlertRule {
  id: string;
  name: string;
  condition: string;
  threshold: number;
  duration: number;
  enabled: boolean;
}

export interface AdminOperation {
  timestamp: string;
  operator: string;
  action: string;
  lock_id: string;
  details: string;
}

export interface MetricPoint {
  time: string;
  value: number;
}

export interface TopLock {
  name: string;
  contention: number;
}

export type RateLimitAlgorithm = 'token_bucket' | 'sliding_window' | 'leaky_bucket';

export interface RateLimitRule {
  key: string;
  algorithm: RateLimitAlgorithm;
  capacity: number;
  rate: number;
  window: number;
  max_requests: number;
  queue_depth: number;
  active_start?: string;
  active_end?: string;
  active_days?: number[];
  per_client: boolean;
  parent_key: string;
  created_at: string;
  updated_at: string;
  state?: RateLimitRuleState;
}

export interface RateLimitRuleState {
  tokens?: number;
  last_refill?: string;
  window_count?: number;
  queue_depth?: number;
  clients?: Record<string, {
    tokens?: number;
    last_refill?: string;
    window_count?: number;
    queue_depth?: number;
  }>;
}

export interface RateLimitCheckResult {
  allowed: boolean;
  remaining: number;
  retry_after: number;
  rule_key: string;
  algorithm: RateLimitAlgorithm;
  reason: string;
}

export interface RateLimitEvent {
  timestamp: string;
  client_id: string;
  rule_key: string;
  algorithm: RateLimitAlgorithm;
  reason: string;
  request_key: string;
}

export interface RateLimitRuleRejectStats {
  rule_key: string;
  reject_count: number;
}

export interface RateLimitMonitorStats {
  key: string;
  algorithm: RateLimitAlgorithm;
  state?: RateLimitRuleState;
}

export type Priority = 'high' | 'medium' | 'low';
export type AdaptiveState = 'normal' | 'tightening' | 'min';

export interface AdaptiveConfig {
  enabled: boolean;
  latency_threshold_p99: number;
  check_interval: number;
  aimd_add_percent: number;
  aimd_multiply_factor: number;
  min_quota_percent: number;
  max_quota_percent: number;
}

export interface AdaptiveStateData {
  current_quota_multiplier: number;
  state: AdaptiveState;
  last_check_time: string;
  last_adjust_time: string;
  latency_history: number[];
}

export interface AdaptiveHistoryEntry {
  timestamp: string;
  direction: string;
  before_multiplier: number;
  after_multiplier: number;
  before_quota: number;
  after_quota: number;
  reason: string;
  average_latency: number;
  p99_latency: number;
}

export interface TrafficShapingConfig {
  smoothing_enabled: boolean;
  smoothing_interval: number;
  priority_enabled: boolean;
  low_priority_reserve: number;
  warm_up_enabled: boolean;
  warm_up_duration: number;
  warm_up_initial_percent: number;
  borrow_enabled: boolean;
  borrow_threshold: number;
  borrow_max_percent: number;
}

export interface WarmUpState {
  enabled: boolean;
  start_time: string;
  duration: number;
  initial_percent: number;
  current_multiplier: number;
}

export interface TokenBorrowRecord {
  from_rule_key: string;
  to_rule_key: string;
  amount: number;
  timestamp: string;
  repaid: boolean;
  repaid_at?: string;
}

export interface RateLimitRule {
  key: string;
  algorithm: RateLimitAlgorithm;
  capacity: number;
  rate: number;
  window: number;
  max_requests: number;
  queue_depth: number;
  active_start?: string;
  active_end?: string;
  active_days?: number[];
  per_client: boolean;
  parent_key: string;
  created_at: string;
  updated_at: string;
  state?: RateLimitRuleState;
  original_capacity: number;
  original_rate: number;
  original_max_requests: number;
  adaptive_config?: AdaptiveConfig;
  shaping_config?: TrafficShapingConfig;
}

export interface QuotaMultiplierResponse {
  rule_key: string;
  current_multiplier: number;
  current_percent: number;
  original_capacity: number;
  original_rate: number;
  current_capacity: number;
  current_rate: number;
  adaptive_state?: AdaptiveStateData;
  warm_up_state?: WarmUpState;
}
