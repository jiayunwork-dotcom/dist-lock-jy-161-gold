import axios from 'axios';
import { LockInfo, ClusterStatus, Alert, AdminOperation, AlertRule, LockGroupInfo, DependencyGraphData, LockDependency, RateLimitRule, RateLimitEvent, RateLimitRuleRejectStats, RateLimitMonitorStats, AdaptiveHistoryEntry, TokenBorrowRecord, QuotaMultiplierResponse, AdaptiveConfig, TrafficShapingConfig } from '../types';

const API_BASE = '/api/v1';
const ADMIN_TOKEN = 'admin-secret';

const api = axios.create({
  baseURL: API_BASE,
  timeout: 10000,
});

const adminHeaders = {
  'X-Admin-Token': ADMIN_TOKEN,
};

export async function getHealth() {
  const response = await api.get('/health');
  return response.data;
}

export async function getClusterStatus(): Promise<ClusterStatus> {
  const response = await api.get('/cluster');
  return response.data;
}

export async function listLocks(namespace?: string, search?: string, sortBy?: string): Promise<LockInfo[]> {
  const params: Record<string, string> = {};
  if (namespace) params.namespace = namespace;
  if (search) params.search = search;
  if (sortBy) params.sort_by = sortBy;

  const response = await api.get('/locks', { params });
  return response.data;
}

export async function getLock(namespace: string, name: string): Promise<LockInfo> {
  const response = await api.get(`/locks/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`);
  return response.data;
}

export async function forceReleaseLock(namespace: string, name: string): Promise<void> {
  await api.post(`/locks/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/force-release`, {
    confirm: true,
  });
}

export async function adjustLease(namespace: string, name: string, leaseTime: number): Promise<void> {
  await api.post(`/locks/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/adjust-lease`, {
    lease_time: leaseTime,
  });
}

export async function adjustCapacity(namespace: string, name: string, capacity: number): Promise<void> {
  await api.post(`/locks/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/adjust-capacity`, {
    capacity,
  });
}

export async function clearQueue(namespace: string, name: string): Promise<void> {
  await api.post(`/locks/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/clear-queue`);
}

export async function listAdminOperations(): Promise<AdminOperation[]> {
  const response = await api.get('/admin/operations', { headers: adminHeaders });
  return response.data;
}

export async function listAlerts(): Promise<Alert[]> {
  const response = await api.get('/admin/alerts', { headers: adminHeaders });
  return response.data;
}

export async function configureAlerts(rules: AlertRule[]): Promise<void> {
  await api.post('/admin/alerts/configure', rules, { headers: adminHeaders });
}

export async function addPeer(nodeId: string, addr: string): Promise<void> {
  await api.post('/admin/cluster/add-peer', { node_id: nodeId, addr }, { headers: adminHeaders });
}

export async function removePeer(nodeId: string): Promise<void> {
  await api.post('/admin/cluster/remove-peer', { node_id: nodeId }, { headers: adminHeaders });
}

export async function getDependencyGraph(): Promise<DependencyGraphData> {
  const response = await api.get('/dependencies/graph');
  return response.data;
}

export async function registerDependency(parentNamespace: string, parentName: string, childNamespace: string, childName: string): Promise<void> {
  await api.post('/dependencies/register', {
    parent_namespace: parentNamespace,
    parent_name: parentName,
    child_namespace: childNamespace,
    child_name: childName,
  });
}

export async function removeDependency(parentNamespace: string, parentName: string, childNamespace: string, childName: string): Promise<void> {
  await api.post('/dependencies/remove', {
    parent_namespace: parentNamespace,
    parent_name: parentName,
    child_namespace: childNamespace,
    child_name: childName,
  });
}

export async function listGroups(): Promise<LockGroupInfo[]> {
  const response = await api.get('/groups');
  return response.data;
}

export async function getGroup(name: string): Promise<LockGroupInfo> {
  const response = await api.get(`/groups/${encodeURIComponent(name)}`);
  return response.data;
}

export async function createGroup(name: string, description: string, timeout: number): Promise<void> {
  await api.post('/groups/create', {
    name,
    description,
    timeout,
  });
}

export async function deleteGroup(name: string): Promise<void> {
  await api.post(`/groups/${encodeURIComponent(name)}/delete`);
}

export async function addLockToGroup(groupName: string, namespace: string, name: string): Promise<void> {
  await api.post(`/groups/${encodeURIComponent(groupName)}/add-lock`, {
    namespace,
    name,
  });
}

export async function removeLockFromGroup(groupName: string, namespace: string, name: string): Promise<void> {
  await api.post(`/groups/${encodeURIComponent(groupName)}/remove-lock`, {
    namespace,
    name,
  });
}

export async function batchAcquire(groupName: string, clientId: string, leaseTime: number, mode: string): Promise<any> {
  const response = await api.post(`/groups/${encodeURIComponent(groupName)}/batch-acquire`, {
    client_id: clientId,
    lease_time: leaseTime,
    mode,
  });
  return response.data;
}

export async function batchRelease(groupName: string, clientId: string): Promise<any> {
  const response = await api.post(`/groups/${encodeURIComponent(groupName)}/batch-release`, {
    client_id: clientId,
  });
  return response.data;
}

export async function listRateLimitRules(): Promise<RateLimitRule[]> {
  const response = await api.get('/ratelimit/rules');
  return response.data;
}

export async function getRateLimitRule(key: string): Promise<RateLimitRule> {
  const response = await api.get(`/ratelimit/rules/${encodeURIComponent(key)}`);
  return response.data;
}

export async function createRateLimitRule(rule: Partial<RateLimitRule>): Promise<RateLimitRule> {
  const response = await api.post('/ratelimit/rules', rule);
  return response.data;
}

export async function updateRateLimitRule(key: string, rule: Partial<RateLimitRule>): Promise<RateLimitRule> {
  const response = await api.put(`/ratelimit/rules/${encodeURIComponent(key)}`, rule);
  return response.data;
}

export async function deleteRateLimitRule(key: string): Promise<void> {
  await api.delete(`/ratelimit/rules/${encodeURIComponent(key)}`);
}

export async function checkRateLimit(key: string, clientId: string, tokens?: number): Promise<any> {
  const response = await api.post('/ratelimit/check', {
    key,
    client_id: clientId,
    tokens: tokens || 1,
  });
  return response.data;
}

export async function listRateLimitEvents(params?: {
  rule_key?: string;
  client_id?: string;
  start?: string;
  end?: string;
}): Promise<RateLimitEvent[]> {
  const response = await api.get('/ratelimit/events', { params });
  return response.data;
}

export async function getRateLimitMonitorStats(): Promise<RateLimitMonitorStats[]> {
  const response = await api.get('/ratelimit/monitor/stats');
  return response.data;
}

export async function getRateLimitTopRejected(n?: number): Promise<RateLimitRuleRejectStats[]> {
  const params: Record<string, string> = {};
  if (n) params.n = String(n);
  const response = await api.get('/ratelimit/monitor/top-rejected', { params });
  return response.data;
}

export async function reportLatency(ruleKey: string, clientId: string, latencyMs: number): Promise<void> {
  await api.post('/ratelimit/latency', {
    rule_key: ruleKey,
    client_id: clientId,
    latency_ms: latencyMs,
  });
}

export async function adjustQuota(ruleKey: string, newMultiplier: number, reason: string): Promise<RateLimitRule> {
  const response = await api.post(`/ratelimit/rules/${encodeURIComponent(ruleKey)}/adjust-quota`, {
    new_multiplier: newMultiplier,
    reason,
  });
  return response.data;
}

export async function getAdaptiveHistory(ruleKey: string): Promise<AdaptiveHistoryEntry[]> {
  const response = await api.get(`/ratelimit/rules/${encodeURIComponent(ruleKey)}/adaptive-history`);
  return response.data;
}

export async function getQuotaMultiplier(ruleKey: string): Promise<QuotaMultiplierResponse> {
  const response = await api.get(`/ratelimit/rules/${encodeURIComponent(ruleKey)}/quota-multiplier`);
  return response.data;
}

export async function checkAndAdjustQuota(ruleKey: string): Promise<any> {
  const response = await api.post(`/ratelimit/rules/${encodeURIComponent(ruleKey)}/check-adaptive`);
  return response.data;
}

export async function borrowTokens(fromRuleKey: string, toRuleKey: string, amount: number): Promise<{ success: boolean; amount: number }> {
  const response = await api.post('/ratelimit/borrow', {
    from_rule_key: fromRuleKey,
    to_rule_key: toRuleKey,
    amount,
  });
  return response.data;
}

export async function repayTokens(ruleKey: string, amount: number): Promise<{ success: boolean; amount: number }> {
  const response = await api.post('/ratelimit/repay', {
    rule_key: ruleKey,
    amount,
  });
  return response.data;
}

export async function getBorrowFlow(namespace: string): Promise<TokenBorrowRecord[]> {
  const response = await api.get(`/ratelimit/namespaces/${encodeURIComponent(namespace)}/borrow-flow`);
  return response.data;
}

export async function getNamespaceRules(namespace: string): Promise<RateLimitRule[]> {
  const response = await api.get(`/ratelimit/namespaces/${encodeURIComponent(namespace)}/rules`);
  return response.data;
}
