import React, { useState, useEffect, useCallback, useRef } from 'react';
import { Card, Row, Col, Statistic, Table, Tag, Progress, Spin, Badge, List } from 'antd';
import {
  BarChart, Bar, LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, PieChart, Pie, Cell, AreaChart, Area,
} from 'recharts';
import { getRateLimitMonitorStats, getRateLimitTopRejected, listRateLimitRules, getQuotaMultiplier, getAdaptiveHistory } from '../api/client';
import { RateLimitMonitorStats, RateLimitRuleRejectStats, RateLimitAlgorithm, RateLimitRule, AdaptiveHistoryEntry } from '../types';
import dayjs from 'dayjs';

const algorithmColors: Record<RateLimitAlgorithm, string> = {
  token_bucket: '#1677ff',
  sliding_window: '#52c41a',
  leaky_bucket: '#fa8c16',
};

const algorithmLabels: Record<RateLimitAlgorithm, string> = {
  token_bucket: 'Token Bucket',
  sliding_window: 'Sliding Window',
  leaky_bucket: 'Leaky Bucket',
};

interface HistoryPoint {
  time: string;
  tokens: number;
  windowCount: number;
  queueDepth: number;
}

const RateLimitMonitor: React.FC = () => {
  const [stats, setStats] = useState<RateLimitMonitorStats[]>([]);
  const [topRejected, setTopRejected] = useState<RateLimitRuleRejectStats[]>([]);
  const [rules, setRules] = useState<RateLimitRule[]>([]);
  const [quotaMultipliers, setQuotaMultipliers] = useState<Record<string, any>>({});
  const [loading, setLoading] = useState(false);
  const [history, setHistory] = useState<HistoryPoint[]>([]);
  const historyRef = useRef<HistoryPoint[]>([]);
  const [allAdaptiveHistory, setAllAdaptiveHistory] = useState<Record<string, AdaptiveHistoryEntry[]>>({});

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const [statsData, topData, rulesData] = await Promise.all([
        getRateLimitMonitorStats(),
        getRateLimitTopRejected(10),
        listRateLimitRules(),
      ]);
      setStats(statsData || []);
      setTopRejected(topData || []);
      setRules(rulesData || []);

      const multipliers: Record<string, any> = {};
      const histories: Record<string, AdaptiveHistoryEntry[]> = {};
      for (const rule of rulesData || []) {
        try {
          const mult = await getQuotaMultiplier(rule.key);
          multipliers[rule.key] = mult;
        } catch (e) {
          // Ignore
        }
        if (rule.adaptive_config?.enabled) {
          try {
            const h = await getAdaptiveHistory(rule.key);
            histories[rule.key] = h;
          } catch (e) {
            // Ignore
          }
        }
      }
      setQuotaMultipliers(multipliers);
      setAllAdaptiveHistory(histories);

      const now = new Date().toLocaleTimeString();
      const point: HistoryPoint = {
        time: now,
        tokens: 0,
        windowCount: 0,
        queueDepth: 0,
      };

      for (const s of statsData || []) {
        if (s.state?.tokens !== undefined) point.tokens += s.state.tokens;
        if (s.state?.window_count !== undefined) point.windowCount += s.state.window_count;
        if (s.state?.queue_depth !== undefined) point.queueDepth += s.state.queue_depth;
      }

      const newHistory = [...historyRef.current, point].slice(-30);
      historyRef.current = newHistory;
      setHistory(newHistory);
    } catch (e) {
      // ignore
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 3000);
    return () => clearInterval(interval);
  }, [fetchData]);

  const totalTokens = stats.reduce((sum, s) => sum + (s.state?.tokens || 0), 0);
  const totalWindowCount = stats.reduce((sum, s) => sum + (s.state?.window_count || 0), 0);
  const totalQueueDepth = stats.reduce((sum, s) => sum + (s.state?.queue_depth || 0), 0);
  const totalRules = stats.length;

  const adaptiveRules = rules.filter(r => r.adaptive_config?.enabled);
  const tighteningRules = adaptiveRules.filter(r => {
    const mult = quotaMultipliers[r.key];
    return mult && mult.current_percent < 100 && mult.current_percent > (r.adaptive_config?.min_quota_percent || 10);
  });
  const minQuotaRules = adaptiveRules.filter(r => {
    const mult = quotaMultipliers[r.key];
    return mult && mult.current_percent <= (r.adaptive_config?.min_quota_percent || 10) * 1.2;
  });

  const algorithmDistribution = Object.entries(
    stats.reduce((acc, s) => {
      acc[s.algorithm] = (acc[s.algorithm] || 0) + 1;
      return acc;
    }, {} as Record<string, number>)
  ).map(([name, value]) => ({ name: algorithmLabels[name as RateLimitAlgorithm] || name, value }));

  const getStatusBadge = (state: string) => {
    switch (state) {
      case 'normal': return <Badge status="success" text="Normal" />;
      case 'tightening': return <Badge status="warning" text="Tightening" />;
      case 'min': return <Badge status="error" text="Min Quota" />;
      default: return <Badge status="default" text="Unknown" />;
    }
  };

  const recentAdjustments = Object.entries(allAdaptiveHistory)
    .flatMap(([ruleKey, entries]) => 
      entries.slice(-5).map(e => ({ ...e, rule_key: ruleKey }))
    )
    .sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())
    .slice(0, 10);

  const topRejectedChartData = topRejected.map(r => ({
    name: r.rule_key.length > 20 ? '...' + r.rule_key.slice(-18) : r.rule_key,
    fullName: r.rule_key,
    rejections: r.reject_count,
  }));

  const waterLevelColumns = [
    {
      title: 'Rule Key',
      dataIndex: 'key',
      key: 'key',
      render: (key: string) => <Tag color="purple">{key}</Tag>,
    },
    {
      title: 'Algorithm',
      dataIndex: 'algorithm',
      key: 'algorithm',
      render: (algo: RateLimitAlgorithm) => (
        <Tag color={algorithmColors[algo]}>{algorithmLabels[algo]}</Tag>
      ),
    },
    {
      title: 'Water Level',
      key: 'level',
      render: (_: any, record: RateLimitMonitorStats) => {
        if (record.algorithm === 'token_bucket' && record.state?.tokens !== undefined) {
          const rule = stats.find(s => s.key === record.key);
          const capacity = 100;
          const pct = Math.min(100, (record.state.tokens / capacity) * 100);
          return <Progress percent={Math.round(pct)} size="small" status={pct < 20 ? 'exception' : 'active'} />;
        }
        if (record.algorithm === 'sliding_window' && record.state?.window_count !== undefined) {
          return <span>{record.state.window_count} requests in window</span>;
        }
        if (record.algorithm === 'leaky_bucket' && record.state?.queue_depth !== undefined) {
          return <span>Queue: {record.state.queue_depth}</span>;
        }
        return '-';
      },
    },
    {
      title: 'Remaining',
      key: 'remaining',
      render: (_: any, record: RateLimitMonitorStats) => {
        if (record.algorithm === 'token_bucket' && record.state?.tokens !== undefined) {
          return <Statistic value={record.state.tokens.toFixed(1)} valueStyle={{ fontSize: 14 }} />;
        }
        if (record.algorithm === 'sliding_window' && record.state?.window_count !== undefined) {
          return <Statistic value={record.state.window_count} valueStyle={{ fontSize: 14 }} />;
        }
        if (record.algorithm === 'leaky_bucket' && record.state?.queue_depth !== undefined) {
          return <Statistic value={record.state.queue_depth} valueStyle={{ fontSize: 14 }} />;
        }
        return '-';
      },
    },
  ];

  return (
    <div>
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}>
          <Card>
            <Statistic title="Total Rules" value={totalRules} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Total Tokens" value={totalTokens.toFixed(1)} valueStyle={{ color: '#1677ff' }} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Window Requests" value={totalWindowCount} valueStyle={{ color: '#52c41a' }} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Queue Depth" value={totalQueueDepth} valueStyle={{ color: '#fa8c16' }} />
          </Card>
        </Col>
      </Row>

      {adaptiveRules.length > 0 && (
        <Card 
          title="Adaptive Rate Limiting Dashboard" 
          size="small" 
          style={{ marginBottom: 16 }}
          extra={<Spin spinning={loading} size="small" />}
        >
          <Row gutter={16} style={{ marginBottom: 16 }}>
            <Col span={8}>
              <Card size="small">
                <Statistic 
                  title="Adaptive Rules" 
                  value={adaptiveRules.length} 
                  valueStyle={{ color: '#722ed1' }}
                  suffix={`/ ${totalRules}`}
                />
              </Card>
            </Col>
            <Col span={8}>
              <Card size="small">
                <Statistic 
                  title="Tightening" 
                  value={tighteningRules.length} 
                  valueStyle={{ color: '#faad14' }}
                />
              </Card>
            </Col>
            <Col span={8}>
              <Card size="small">
                <Statistic 
                  title="Min Quota" 
                  value={minQuotaRules.length} 
                  valueStyle={{ color: '#ff4d4f' }}
                />
              </Card>
            </Col>
          </Row>

          <Row gutter={16} style={{ marginBottom: 16 }}>
            <Col span={24}>
              <Table
                dataSource={adaptiveRules}
                rowKey="key"
                pagination={false}
                size="small"
                columns={[
                  {
                    title: 'Rule Key',
                    dataIndex: 'key',
                    key: 'key',
                    render: (key: string) => <Tag color="purple">{key}</Tag>,
                  },
                  {
                    title: 'Status',
                    key: 'status',
                    render: (_: any, record: RateLimitRule) => {
                      const mult = quotaMultipliers[record.key];
                      return getStatusBadge(mult?.adaptive_state?.state || 'normal');
                    },
                  },
                  {
                    title: 'Current Quota',
                    key: 'quota',
                    render: (_: any, record: RateLimitRule) => {
                      const mult = quotaMultipliers[record.key];
                      if (!mult) return '-';
                      return (
                        <Progress 
                          percent={Math.round(mult.current_percent)} 
                          size="small"
                          strokeColor={mult.current_percent < 50 ? '#ff4d4f' : mult.current_percent < 100 ? '#faad14' : '#52c41a'}
                        />
                      );
                    },
                  },
                  {
                    title: 'P99 Latency',
                    key: 'latency',
                    render: (_: any, record: RateLimitRule) => {
                      const mult = quotaMultipliers[record.key];
                      if (!mult?.adaptive_state?.current_p99) return '-';
                      const p99 = mult.adaptive_state.current_p99 / 1e6;
                      const threshold = (record.adaptive_config?.latency_threshold_p99 || 0) / 1e6;
                      const color = p99 > threshold ? '#ff4d4f' : '#52c41a';
                      return <span style={{ color, fontWeight: 'bold' }}>{p99.toFixed(1)} ms</span>;
                    },
                  },
                  {
                    title: 'Quota Range',
                    key: 'range',
                    render: (_: any, record: RateLimitRule) => {
                      const min = record.adaptive_config?.min_quota_percent || 10;
                      const max = record.adaptive_config?.max_quota_percent || 200;
                      return `${min}% - ${max}%`;
                    },
                  },
                ]}
              />
            </Col>
          </Row>

          <Row gutter={16}>
            <Col span={12}>
              <Card title="Recent Adjustments" size="small">
                {recentAdjustments.length > 0 ? (
                  <List
                    size="small"
                    dataSource={recentAdjustments}
                    renderItem={(item: any) => (
                      <List.Item>
                        <List.Item.Meta
                          title={
                            <Row align="middle" justify="space-between" style={{ width: '100%' }}>
                              <Col>
                                <Tag color="purple">{item.rule_key}</Tag>
                                <Tag color={item.direction === 'increase' ? 'green' : 'red'}>
                                  {item.direction.toUpperCase()}
                                </Tag>
                                <span style={{ fontWeight: 'bold' }}>
                                  {(item.before_multiplier * 100).toFixed(0)}% → {(item.after_multiplier * 100).toFixed(0)}%
                                </span>
                              </Col>
                              <Col>
                                <span style={{ fontSize: '11px', color: '#8c8c8c' }}>
                                  {dayjs(item.timestamp).format('HH:mm:ss')}
                                </span>
                              </Col>
                            </Row>
                          }
                          description={item.reason}
                        />
                      </List.Item>
                    )}
                  />
                ) : (
                  <div style={{ textAlign: 'center', padding: '20px 0', color: '#8c8c8c' }}>
                    No recent adjustments
                  </div>
                )}
              </Card>
            </Col>
            <Col span={12}>
              <Card title="Quota Multiplier Trends" size="small">
                {Object.keys(allAdaptiveHistory).length > 0 ? (
                  <ResponsiveContainer width="100%" height={250}>
                    <LineChart>
                      <CartesianGrid strokeDasharray="3 3" />
                      <XAxis 
                        tick={{ fontSize: 10 }}
                        tickFormatter={(v) => dayjs(v).format('HH:mm:ss')}
                      />
                      <YAxis 
                        domain={[0, 2.5]}
                        tickFormatter={(v) => `${(v * 100).toFixed(0)}%`}
                      />
                      <Tooltip 
                        formatter={(value: number) => [`${(value * 100).toFixed(1)}%`, 'Quota']}
                        labelFormatter={(v) => dayjs(v).format('YYYY-MM-DD HH:mm:ss')}
                      />
                      {Object.entries(allAdaptiveHistory).slice(0, 3).map(([ruleKey, history], idx) => {
                        const colors = ['#1890ff', '#52c41a', '#722ed1'];
                        return (
                          <Line
                            key={ruleKey}
                            type="monotone"
                            data={history}
                            dataKey="after_multiplier"
                            name={ruleKey}
                            stroke={colors[idx % colors.length]}
                            strokeWidth={2}
                            dot={false}
                          />
                        );
                      })}
                    </LineChart>
                  </ResponsiveContainer>
                ) : (
                  <div style={{ textAlign: 'center', padding: '40px 0', color: '#8c8c8c' }}>
                    No history data
                  </div>
                )}
              </Card>
            </Col>
          </Row>
        </Card>
      )}

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={12}>
          <Card title="Token Level Trend" size="small">
            <ResponsiveContainer width="100%" height={200}>
              <LineChart data={history}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="time" tick={{ fontSize: 10 }} />
                <YAxis />
                <Tooltip />
                <Line type="monotone" dataKey="tokens" stroke="#1677ff" strokeWidth={2} name="Tokens" dot={false} />
              </LineChart>
            </ResponsiveContainer>
          </Card>
        </Col>
        <Col span={12}>
          <Card title="Window Count & Queue Depth Trend" size="small">
            <ResponsiveContainer width="100%" height={200}>
              <LineChart data={history}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="time" tick={{ fontSize: 10 }} />
                <YAxis />
                <Tooltip />
                <Line type="monotone" dataKey="windowCount" stroke="#52c41a" strokeWidth={2} name="Window Count" dot={false} />
                <Line type="monotone" dataKey="queueDepth" stroke="#fa8c16" strokeWidth={2} name="Queue Depth" dot={false} />
              </LineChart>
            </ResponsiveContainer>
          </Card>
        </Col>
      </Row>

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={12}>
          <Card title="Top 10 Rejected Rules" size="small">
            <ResponsiveContainer width="100%" height={200}>
              <BarChart data={topRejectedChartData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="name" tick={{ fontSize: 9 }} angle={-30} textAnchor="end" height={50} />
                <YAxis />
                <Tooltip formatter={(v: number, n: string, p: any) => [v, p.payload?.fullName || n]} />
                <Bar dataKey="rejections" fill="#ff4d4f" radius={[4, 4, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          </Card>
        </Col>
        <Col span={12}>
          <Card title="Algorithm Distribution" size="small">
            <ResponsiveContainer width="100%" height={200}>
              <PieChart>
                <Pie
                  data={algorithmDistribution}
                  cx="50%"
                  cy="50%"
                  outerRadius={70}
                  dataKey="value"
                  label={({ name, value }) => `${name}: ${value}`}
                >
                  {algorithmDistribution.map((entry, i) => (
                    <Cell key={i} fill={['#1677ff', '#52c41a', '#fa8c16'][i % 3]} />
                  ))}
                </Pie>
                <Tooltip />
              </PieChart>
            </ResponsiveContainer>
          </Card>
        </Col>
      </Row>

      <Card title="Rule Water Levels" extra={<Spin spinning={loading} size="small" />}>
        <Table
          dataSource={stats}
          columns={waterLevelColumns}
          rowKey="key"
          pagination={{ pageSize: 10 }}
          size="small"
        />
      </Card>
    </div>
  );
};

export default RateLimitMonitor;
