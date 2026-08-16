import React, { useState, useEffect, useCallback } from 'react';
import {
  Card, Table, Button, Modal, Form, Input, InputNumber, Select, Switch, Tag, Space, message, TimePicker, Checkbox, Row, Col, Tabs, Badge, Progress, List, Timeline, Tooltip as AntTooltip,
} from 'antd';
import { PlusOutlined, EditOutlined, DeleteOutlined, LineChartOutlined, RocketOutlined, ThunderboltOutlined, ExperimentOutlined, SwapOutlined, HistoryOutlined, SafetyOutlined } from '@ant-design/icons';
import { LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, AreaChart, Area } from 'recharts';
import { listRateLimitRules, createRateLimitRule, updateRateLimitRule, deleteRateLimitRule, getAdaptiveHistory, getQuotaMultiplier, checkAndAdjustQuota, getBorrowFlow } from '../api/client';
import { RateLimitRule, RateLimitAlgorithm, AdaptiveHistoryEntry, TokenBorrowRecord } from '../types';
import dayjs from 'dayjs';

const algorithmColors: Record<RateLimitAlgorithm, string> = {
  token_bucket: 'blue',
  sliding_window: 'green',
  leaky_bucket: 'orange',
};

const algorithmLabels: Record<RateLimitAlgorithm, string> = {
  token_bucket: 'Token Bucket',
  sliding_window: 'Sliding Window',
  leaky_bucket: 'Leaky Bucket',
};

const dayLabels = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

const RateLimitRules: React.FC = () => {
  const [rules, setRules] = useState<RateLimitRule[]>([]);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [editingRule, setEditingRule] = useState<RateLimitRule | null>(null);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [previewData, setPreviewData] = useState<any[]>([]);
  const [previewRule, setPreviewRule] = useState<Partial<RateLimitRule> | null>(null);
  const [adaptiveHistory, setAdaptiveHistory] = useState<AdaptiveHistoryEntry[]>([]);
  const [adaptiveHistoryModalOpen, setAdaptiveHistoryModalOpen] = useState(false);
  const [selectedRuleKey, setSelectedRuleKey] = useState<string>('');
  const [quotaMultipliers, setQuotaMultipliers] = useState<Record<string, any>>({});
  const [borrowFlow, setBorrowFlow] = useState<TokenBorrowRecord[]>([]);
  const [borrowFlowModalOpen, setBorrowFlowModalOpen] = useState(false);
  const [form] = Form.useForm();

  const fetchRules = useCallback(async () => {
    setLoading(true);
    try {
      const data = await listRateLimitRules();
      setRules(data || []);

      const multipliers: Record<string, any> = {};
      for (const rule of data || []) {
        try {
          const mult = await getQuotaMultiplier(rule.key);
          multipliers[rule.key] = mult;
        } catch (e) {
          // Ignore errors for individual rules
        }
      }
      setQuotaMultipliers(multipliers);
    } catch (e) {
      message.error('Failed to fetch rate limit rules');
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    fetchRules();
  }, [fetchRules]);

  const handleCreate = () => {
    setEditingRule(null);
    form.resetFields();
    form.setFieldsValue({
      algorithm: 'token_bucket',
      capacity: 100,
      rate: 10,
      window: 60000000000,
      max_requests: 100,
      queue_depth: 10,
      per_client: false,
      active_days: [1, 2, 3, 4, 5],
      adaptive_enabled: false,
      adaptive_latency_threshold: 500,
      adaptive_check_interval: 5000000000,
      adaptive_aimd_add: 5,
      adaptive_aimd_multiply: 0.5,
      adaptive_min_quota: 10,
      adaptive_max_quota: 200,
      shaping_smoothing_enabled: false,
      shaping_smoothing_interval: 10000000,
      shaping_priority_enabled: false,
      shaping_low_reserve: 10,
      shaping_warmup_enabled: false,
      shaping_warmup_duration: 30000000000,
      shaping_warmup_initial: 10,
      shaping_borrow_enabled: false,
      shaping_borrow_threshold: 50,
      shaping_borrow_max: 20,
    });
    setModalOpen(true);
  };

  const handleViewAdaptiveHistory = async (ruleKey: string) => {
    setSelectedRuleKey(ruleKey);
    try {
      const history = await getAdaptiveHistory(ruleKey);
      setAdaptiveHistory(history);
      setAdaptiveHistoryModalOpen(true);
    } catch (e) {
      message.error('Failed to fetch adaptive history');
    }
  };

  const handleCheckAndAdjust = async (ruleKey: string) => {
    try {
      const result = await checkAndAdjustQuota(ruleKey);
      if (result.latest_adjustment) {
        message.success(`Quota adjusted: ${result.latest_adjustment.direction} to ${(result.latest_adjustment.after_multiplier * 100).toFixed(1)}%`);
      } else {
        message.info('No adjustment needed');
      }
      fetchRules();
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to check and adjust quota');
    }
  };

  const handleViewBorrowFlow = async (namespace: string) => {
    try {
      const flow = await getBorrowFlow(namespace);
      setBorrowFlow(flow);
      setBorrowFlowModalOpen(true);
    } catch (e) {
      message.error('Failed to fetch borrow flow');
    }
  };

  const handleEdit = (rule: RateLimitRule) => {
    setEditingRule(rule);
    const adaptiveConfig = rule.adaptive_config;
    const shapingConfig = rule.shaping_config;

    form.setFieldsValue({
      key: rule.key,
      algorithm: rule.algorithm,
      capacity: rule.capacity,
      rate: rule.rate,
      window: rule.window,
      max_requests: rule.max_requests,
      queue_depth: rule.queue_depth,
      per_client: rule.per_client,
      active_start: rule.active_start ? dayjs(rule.active_start, 'HH:mm') : undefined,
      active_end: rule.active_end ? dayjs(rule.active_end, 'HH:mm') : undefined,
      active_days: rule.active_days || [],
      adaptive_enabled: adaptiveConfig?.enabled || false,
      adaptive_latency_threshold: adaptiveConfig ? adaptiveConfig.latency_threshold_p99 / 1e6 : 500,
      adaptive_check_interval: adaptiveConfig?.check_interval || 5000000000,
      adaptive_aimd_add: adaptiveConfig?.aimd_add_percent || 5,
      adaptive_aimd_multiply: adaptiveConfig?.aimd_multiply_factor || 0.5,
      adaptive_min_quota: adaptiveConfig?.min_quota_percent || 10,
      adaptive_max_quota: adaptiveConfig?.max_quota_percent || 200,
      shaping_smoothing_enabled: shapingConfig?.smoothing_enabled || false,
      shaping_smoothing_interval: shapingConfig?.smoothing_interval || 10000000,
      shaping_priority_enabled: shapingConfig?.priority_enabled || false,
      shaping_low_reserve: shapingConfig?.low_priority_reserve ? shapingConfig.low_priority_reserve * 100 : 10,
      shaping_warmup_enabled: shapingConfig?.warm_up_enabled || false,
      shaping_warmup_duration: shapingConfig?.warm_up_duration || 30000000000,
      shaping_warmup_initial: shapingConfig?.warm_up_initial_percent || 10,
      shaping_borrow_enabled: shapingConfig?.borrow_enabled || false,
      shaping_borrow_threshold: shapingConfig?.borrow_threshold ? shapingConfig.borrow_threshold * 100 : 50,
      shaping_borrow_max: shapingConfig?.borrow_max_percent || 20,
    });
    setModalOpen(true);
  };

  const handleDelete = async (key: string) => {
    Modal.confirm({
      title: 'Delete Rule',
      content: `Are you sure you want to delete rule "${key}"?`,
      onOk: async () => {
        try {
          await deleteRateLimitRule(key);
          message.success('Rule deleted');
          fetchRules();
        } catch (e) {
          message.error('Failed to delete rule');
        }
      },
    });
  };

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      const payload: Partial<RateLimitRule> = {
        key: values.key,
        algorithm: values.algorithm,
        capacity: values.capacity,
        rate: values.rate,
        window: values.window,
        max_requests: values.max_requests,
        queue_depth: values.queue_depth,
        per_client: values.per_client,
        active_start: values.active_start ? values.active_start.format('HH:mm') : '',
        active_end: values.active_end ? values.active_end.format('HH:mm') : '',
        active_days: values.active_days || [],
        adaptive_config: values.adaptive_enabled ? {
          enabled: true,
          latency_threshold_p99: values.adaptive_latency_threshold * 1e6,
          check_interval: values.adaptive_check_interval,
          aimd_add_percent: values.adaptive_aimd_add,
          aimd_multiply_factor: values.adaptive_aimd_multiply,
          min_quota_percent: values.adaptive_min_quota,
          max_quota_percent: values.adaptive_max_quota,
        } : undefined,
        shaping_config: (values.shaping_smoothing_enabled || values.shaping_priority_enabled || values.shaping_warmup_enabled || values.shaping_borrow_enabled) ? {
          smoothing_enabled: values.shaping_smoothing_enabled || false,
          smoothing_interval: values.shaping_smoothing_interval,
          priority_enabled: values.shaping_priority_enabled || false,
          low_priority_reserve: values.shaping_low_reserve / 100,
          warm_up_enabled: values.shaping_warmup_enabled || false,
          warm_up_duration: values.shaping_warmup_duration,
          warm_up_initial_percent: values.shaping_warmup_initial,
          borrow_enabled: values.shaping_borrow_enabled || false,
          borrow_threshold: values.shaping_borrow_threshold / 100,
          borrow_max_percent: values.shaping_borrow_max,
        } : undefined,
      };

      if (editingRule) {
        await updateRateLimitRule(editingRule.key, payload);
        message.success('Rule updated');
      } else {
        await createRateLimitRule(payload);
        message.success('Rule created');
      }
      setModalOpen(false);
      fetchRules();
    } catch (e: any) {
      if (e.response?.data?.error) {
        message.error(e.response.data.error);
      }
    }
  };

  const generatePreviewData = (rule: Partial<RateLimitRule>) => {
    if (rule.algorithm !== 'token_bucket' || !rule.capacity || !rule.rate) return;

    const data = [];
    const capacity = rule.capacity;
    const rate = rule.rate;
    let tokens = capacity;

    const burstPoints = [10, 25, 45];
    const burstAmount = capacity * 0.8;

    for (let t = 0; t <= 60; t += 0.5) {
      if (burstPoints.includes(Math.floor(t)) && t === Math.floor(t)) {
        tokens = Math.max(0, tokens - burstAmount);
      }
      tokens = Math.min(tokens + rate * 0.5, capacity);

      data.push({
        time: t,
        tokens: Math.min(tokens, capacity),
        burst: capacity,
        refill_rate: rate,
      });
    }

    setPreviewData(data);
    setPreviewRule(rule);
    setPreviewOpen(true);
  };

  const getAdaptiveStatusColor = (state: string) => {
    switch (state) {
      case 'normal': return 'green';
      case 'tightening': return 'gold';
      case 'min': return 'red';
      default: return 'default';
    }
  };

  const getAdaptiveStatusIcon = (state: string) => {
    switch (state) {
      case 'normal': return <span style={{ color: '#52c41a' }}>●</span>;
      case 'tightening': return <span style={{ color: '#faad14' }}>●</span>;
      case 'min': return <span style={{ color: '#ff4d4f' }}>●</span>;
      default: return <span>○</span>;
    }
  };

  const columns = [
    {
      title: 'Key',
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
      title: 'Capacity/Rate',
      key: 'config',
      render: (_: any, record: RateLimitRule) => {
        if (record.algorithm === 'token_bucket') {
          return `${record.capacity} tokens / ${record.rate}/s`;
        }
        if (record.algorithm === 'sliding_window') {
          return `${record.max_requests} req / ${(record.window / 1e9).toFixed(0)}s`;
        }
        return `depth ${record.queue_depth} / ${record.rate}/s`;
      },
    },
    {
      title: 'Effective Quota',
      key: 'effective_quota',
      render: (_: any, record: RateLimitRule) => {
        const mult = quotaMultipliers[record.key];
        if (!mult) return <Tag>100%</Tag>;
        
        const percent = mult.current_percent;
        let color = 'green';
        if (percent < 100) color = 'gold';
        if (percent <= 20) color = 'red';
        
        return (
          <Space direction="vertical" size={0} style={{ width: '100%' }}>
            <Progress 
              percent={Math.round(percent)} 
              size="small" 
              strokeColor={{ '0%': '#108ee9', '100%': color }}
              style={{ marginBottom: 0 }}
            />
            <Space size={4}>
              {record.adaptive_config?.enabled && getAdaptiveStatusIcon(mult.adaptive_state?.state || 'normal')}
              <span style={{ fontSize: '12px', color: '#8c8c8c' }}>
                {percent.toFixed(1)}%
              </span>
            </Space>
          </Space>
        );
      },
    },
    {
      title: 'Features',
      key: 'features',
      render: (_: any, record: RateLimitRule) => {
        const features = [];
        if (record.adaptive_config?.enabled) {
          features.push(
            <AntTooltip key="adaptive" title="Adaptive Rate Limiting">
              <Tag color="geekblue" icon={<ThunderboltOutlined />}>Adaptive</Tag>
            </AntTooltip>
          );
        }
        if (record.shaping_config?.smoothing_enabled) {
          features.push(
            <AntTooltip key="smooth" title="Traffic Smoothing">
              <Tag color="cyan" icon={<SafetyOutlined />}>Smooth</Tag>
            </AntTooltip>
          );
        }
        if (record.shaping_config?.priority_enabled) {
          features.push(
            <AntTooltip key="priority" title="Priority Queue">
              <Tag color="purple" icon={<RocketOutlined />}>Priority</Tag>
            </AntTooltip>
          );
        }
        if (record.shaping_config?.warm_up_enabled) {
          features.push(
            <AntTooltip key="warmup" title="Warm Up">
              <Tag color="orange" icon={<ExperimentOutlined />}>WarmUp</Tag>
            </AntTooltip>
          );
        }
        if (record.shaping_config?.borrow_enabled) {
          features.push(
            <AntTooltip key="borrow" title="Token Borrowing">
              <Tag color="green" icon={<SwapOutlined />}>Borrow</Tag>
            </AntTooltip>
          );
        }
        return <Space wrap>{features}</Space>;
      },
    },
    {
      title: 'Per Client',
      dataIndex: 'per_client',
      key: 'per_client',
      render: (v: boolean) => v ? <Tag color="cyan">Yes</Tag> : <Tag>No</Tag>,
    },
    {
      title: 'Active Time',
      key: 'active',
      render: (_: any, record: RateLimitRule) => {
        if (!record.active_start || !record.active_end) return <Tag>Always</Tag>;
        const days = record.active_days?.map((d: number) => dayLabels[d]).join(', ') || '';
        return <Tag color="gold">{record.active_start}-{record.active_end} {days}</Tag>;
      },
    },
    {
      title: 'Actions',
      key: 'actions',
      render: (_: any, record: RateLimitRule) => {
        const namespace = record.key.split('/')[1] || '';
        return (
          <Space wrap>
            {record.algorithm === 'token_bucket' && (
              <Button
                size="small"
                icon={<LineChartOutlined />}
                onClick={() => generatePreviewData(record)}
              >
                Preview
              </Button>
            )}
            {record.adaptive_config?.enabled && (
              <>
                <Button
                  size="small"
                  icon={<HistoryOutlined />}
                  onClick={() => handleViewAdaptiveHistory(record.key)}
                >
                  History
                </Button>
                <Button
                  size="small"
                  type="primary"
                  ghost
                  onClick={() => handleCheckAndAdjust(record.key)}
                >
                  Check
                </Button>
              </>
            )}
            {record.shaping_config?.borrow_enabled && namespace && (
              <Button
                size="small"
                icon={<SwapOutlined />}
                onClick={() => handleViewBorrowFlow(namespace)}
              >
                Flow
              </Button>
            )}
            <Button size="small" icon={<EditOutlined />} onClick={() => handleEdit(record)} />
            <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(record.key)} />
          </Space>
        );
      },
    },
  ];

  return (
    <div>
      <Card
        title="Rate Limit Rules"
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={handleCreate}>
            Create Rule
          </Button>
        }
      >
        <Table
          dataSource={rules}
          columns={columns}
          rowKey="key"
          loading={loading}
          pagination={{ pageSize: 10 }}
        />
      </Card>

      <Modal
        title={editingRule ? 'Edit Rule' : 'Create Rule'}
        open={modalOpen}
        onOk={handleSubmit}
        onCancel={() => setModalOpen(false)}
        width={720}
      >
        <Form form={form} layout="vertical">
          <Tabs
            defaultActiveKey="basic"
            items={[
              {
                key: 'basic',
                label: 'Basic Config',
                children: (
                  <>
                    <Form.Item name="key" label="Rule Key (e.g. /api/user/login)" rules={[{ required: true }]}>
                      <Input disabled={!!editingRule} placeholder="/api/user/login" />
                    </Form.Item>
                    <Form.Item name="algorithm" label="Algorithm" rules={[{ required: true }]}>
                      <Select>
                        <Select.Option value="token_bucket">Token Bucket</Select.Option>
                        <Select.Option value="sliding_window">Sliding Window Log</Select.Option>
                        <Select.Option value="leaky_bucket">Leaky Bucket</Select.Option>
                      </Select>
                    </Form.Item>
                    <Row gutter={16}>
                      <Col span={12}>
                        <Form.Item name="capacity" label="Bucket Capacity">
                          <InputNumber min={1} style={{ width: '100%' }} />
                        </Form.Item>
                      </Col>
                      <Col span={12}>
                        <Form.Item name="rate" label="Fill/Process Rate (per sec)">
                          <InputNumber min={0.001} step={0.1} style={{ width: '100%' }} />
                        </Form.Item>
                      </Col>
                    </Row>
                    <Row gutter={16}>
                      <Col span={12}>
                        <Form.Item name="max_requests" label="Max Requests (Sliding Window)">
                          <InputNumber min={1} style={{ width: '100%' }} />
                        </Form.Item>
                      </Col>
                      <Col span={12}>
                        <Form.Item name="window" label="Window (nanoseconds)">
                          <InputNumber min={1000000} style={{ width: '100%' }} />
                        </Form.Item>
                      </Col>
                    </Row>
                    <Form.Item name="queue_depth" label="Queue Depth (Leaky Bucket)">
                      <InputNumber min={1} style={{ width: '100%' }} />
                    </Form.Item>
                    <Form.Item name="per_client" label="Per-Client Quotas" valuePropName="checked">
                      <Switch />
                    </Form.Item>
                    <Row gutter={16}>
                      <Col span={12}>
                        <Form.Item name="active_start" label="Active Start Time">
                          <TimePicker format="HH:mm" style={{ width: '100%' }} />
                        </Form.Item>
                      </Col>
                      <Col span={12}>
                        <Form.Item name="active_end" label="Active End Time">
                          <TimePicker format="HH:mm" style={{ width: '100%' }} />
                        </Form.Item>
                      </Col>
                    </Row>
                    <Form.Item name="active_days" label="Active Days">
                      <Checkbox.Group>
                        <Row>
                          {dayLabels.map((day, i) => (
                            <Checkbox key={i} value={i}>{day}</Checkbox>
                          ))}
                        </Row>
                      </Checkbox.Group>
                    </Form.Item>
                  </>
                ),
              },
              {
                key: 'adaptive',
                label: 'Adaptive Limiting',
                children: (
                  <>
                    <Card type="inner" title="Adaptive Rate Limiting" size="small" style={{ marginBottom: 16 }}>
                      <Form.Item name="adaptive_enabled" valuePropName="checked">
                        <Switch checkedChildren="ON" unCheckedChildren="OFF" />
                        <span style={{ marginLeft: 8, color: '#8c8c8c' }}>
                          Enable auto-adjustment based on downstream latency
                        </span>
                      </Form.Item>
                    </Card>
                    
                    <Row gutter={16}>
                      <Col span={12}>
                        <Form.Item name="adaptive_latency_threshold" label="P99 Latency Threshold (ms)">
                          <InputNumber min={1} style={{ width: '100%' }} />
                        </Form.Item>
                      </Col>
                      <Col span={12}>
                        <Form.Item name="adaptive_check_interval" label="Check Interval (ns)">
                          <InputNumber min={1000000000} style={{ width: '100%' }} />
                        </Form.Item>
                      </Col>
                    </Row>
                    
                    <Card type="inner" title="AIMD Parameters" size="small" style={{ marginBottom: 16 }}>
                      <Row gutter={16}>
                        <Col span={12}>
                          <Form.Item name="adaptive_aimd_add" label="Additive Increase (%)">
                            <InputNumber min={1} max={50} style={{ width: '100%' }} />
                          </Form.Item>
                        </Col>
                        <Col span={12}>
                          <Form.Item name="adaptive_aimd_multiply" label="Multiplicative Decrease">
                            <InputNumber min={0.1} max={0.9} step={0.1} style={{ width: '100%' }} />
                          </Form.Item>
                        </Col>
                      </Row>
                    </Card>
                    
                    <Row gutter={16}>
                      <Col span={12}>
                        <Form.Item name="adaptive_min_quota" label="Min Quota (%)">
                          <InputNumber min={1} max={100} style={{ width: '100%' }} />
                        </Form.Item>
                      </Col>
                      <Col span={12}>
                        <Form.Item name="adaptive_max_quota" label="Max Quota (%)">
                          <InputNumber min={100} max={500} style={{ width: '100%' }} />
                        </Form.Item>
                      </Col>
                    </Row>
                  </>
                ),
              },
              {
                key: 'shaping',
                label: 'Traffic Shaping',
                children: (
                  <>
                    <Card type="inner" title="Traffic Smoothing" size="small" style={{ marginBottom: 16 }}>
                      <Form.Item name="shaping_smoothing_enabled" valuePropName="checked">
                        <Switch checkedChildren="ON" unCheckedChildren="OFF" />
                        <span style={{ marginLeft: 8, color: '#8c8c8c' }}>
                          Spread burst traffic evenly over time
                        </span>
                      </Form.Item>
                      <Form.Item name="shaping_smoothing_interval" label="Smoothing Interval (ns)">
                        <InputNumber min={1000000} style={{ width: '100%' }} />
                      </Form.Item>
                    </Card>
                    
                    <Card type="inner" title="Priority Queue" size="small" style={{ marginBottom: 16 }}>
                      <Form.Item name="shaping_priority_enabled" valuePropName="checked">
                        <Switch checkedChildren="ON" unCheckedChildren="OFF" />
                        <span style={{ marginLeft: 8, color: '#8c8c8c' }}>
                          High priority requests get tokens first
                        </span>
                      </Form.Item>
                      <Form.Item name="shaping_low_reserve" label="Low Priority Reserve (%)">
                        <InputNumber min={0} max={50} style={{ width: '100%' }} />
                      </Form.Item>
                    </Card>
                    
                    <Card type="inner" title="Warm Up" size="small" style={{ marginBottom: 16 }}>
                      <Form.Item name="shaping_warmup_enabled" valuePropName="checked">
                        <Switch checkedChildren="ON" unCheckedChildren="OFF" />
                        <span style={{ marginLeft: 8, color: '#8c8c8c' }}>
                          Gradually increase quota after cold start
                        </span>
                      </Form.Item>
                      <Row gutter={16}>
                        <Col span={12}>
                          <Form.Item name="shaping_warmup_duration" label="Warm Up Duration (ns)">
                            <InputNumber min={1000000000} style={{ width: '100%' }} />
                          </Form.Item>
                        </Col>
                        <Col span={12}>
                          <Form.Item name="shaping_warmup_initial" label="Initial Quota (%)">
                            <InputNumber min={1} max={90} style={{ width: '100%' }} />
                          </Form.Item>
                        </Col>
                      </Row>
                    </Card>
                    
                    <Card type="inner" title="Token Borrowing" size="small" style={{ marginBottom: 16 }}>
                      <Form.Item name="shaping_borrow_enabled" valuePropName="checked">
                        <Switch checkedChildren="ON" unCheckedChildren="OFF" />
                        <span style={{ marginLeft: 8, color: '#8c8c8c' }}>
                          Borrow tokens from other rules in same namespace
                        </span>
                      </Form.Item>
                      <Row gutter={16}>
                        <Col span={12}>
                          <Form.Item name="shaping_borrow_threshold" label="Lender Usage Threshold (%)">
                            <InputNumber min={10} max={90} style={{ width: '100%' }} />
                          </Form.Item>
                        </Col>
                        <Col span={12}>
                          <Form.Item name="shaping_borrow_max" label="Max Borrow (%)">
                            <InputNumber min={5} max={50} style={{ width: '100%' }} />
                          </Form.Item>
                        </Col>
                      </Row>
                    </Card>
                  </>
                ),
              },
            ]}
          />
        </Form>
      </Modal>

      <Modal
        title={`Token Fill Curve Preview - ${previewRule?.key || ''}`}
        open={previewOpen}
        onCancel={() => setPreviewOpen(false)}
        footer={null}
        width={800}
      >
        <p style={{ color: '#8c8c8c', marginBottom: 16 }}>
          Simulated token bucket level over 60 seconds with periodic burst consumption.
          Capacity: {previewRule?.capacity}, Fill Rate: {previewRule?.rate}/s
        </p>
        <ResponsiveContainer width="100%" height={300}>
          <LineChart data={previewData}>
            <CartesianGrid strokeDasharray="3 3" />
            <XAxis dataKey="time" label={{ value: 'Time (s)', position: 'insideBottom', offset: -5 }} />
            <YAxis label={{ value: 'Tokens', angle: -90, position: 'insideLeft' }} />
            <Tooltip />
            <Line type="monotone" dataKey="tokens" stroke="#1677ff" strokeWidth={2} name="Token Level" dot={false} />
            <Line type="monotone" dataKey="burst" stroke="#ff4d4f" strokeDasharray="5 5" name="Capacity" dot={false} />
          </LineChart>
        </ResponsiveContainer>
      </Modal>

      <Modal
        title={`Adaptive Adjustment History - ${selectedRuleKey}`}
        open={adaptiveHistoryModalOpen}
        onCancel={() => setAdaptiveHistoryModalOpen(false)}
        footer={null}
        width={800}
      >
        {adaptiveHistory.length > 0 ? (
          <>
            <Row gutter={16} style={{ marginBottom: 24 }}>
              <Col span={24}>
                <Card title="Quota Multiplier Trend" size="small">
                  <ResponsiveContainer width="100%" height={200}>
                    <AreaChart data={adaptiveHistory}>
                      <CartesianGrid strokeDasharray="3 3" />
                      <XAxis 
                        dataKey="timestamp" 
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
                      <Area 
                        type="monotone" 
                        dataKey="after_multiplier" 
                        stroke="#1890ff" 
                        fill="#91caff" 
                        name="After Quota"
                      />
                    </AreaChart>
                  </ResponsiveContainer>
                </Card>
              </Col>
            </Row>
            <Card title="Adjustment Timeline" size="small">
              <Timeline>
                {adaptiveHistory.slice(0, 50).map((entry, idx) => (
                  <Timeline.Item
                    key={idx}
                    color={entry.direction === 'increase' ? 'green' : entry.direction === 'decrease' ? 'red' : 'blue'}
                  >
                    <Space direction="vertical" size={0}>
                      <Space>
                        <Tag color={entry.direction === 'increase' ? 'green' : entry.direction === 'decrease' ? 'red' : 'blue'}>
                          {entry.direction.toUpperCase()}
                        </Tag>
                        <span style={{ fontWeight: 'bold' }}>
                          {(entry.before_multiplier * 100).toFixed(1)}% → {(entry.after_multiplier * 100).toFixed(1)}%
                        </span>
                      </Space>
                      <span style={{ fontSize: '12px', color: '#8c8c8c' }}>
                        {dayjs(entry.timestamp).format('YYYY-MM-DD HH:mm:ss')}
                      </span>
                      <span style={{ fontSize: '12px' }}>{entry.reason}</span>
                    </Space>
                  </Timeline.Item>
                ))}
              </Timeline>
            </Card>
          </>
        ) : (
          <div style={{ textAlign: 'center', padding: '40px 0', color: '#8c8c8c' }}>
            No adaptive adjustment history yet
          </div>
        )}
      </Modal>

      <Modal
        title={`Token Borrow Flow - Namespace`}
        open={borrowFlowModalOpen}
        onCancel={() => setBorrowFlowModalOpen(false)}
        footer={null}
        width={800}
      >
        {borrowFlow.length > 0 ? (
          <>
            <Card title="Token Borrowing Records" size="small">
              <Table
                dataSource={borrowFlow}
                rowKey={(r, i) => `${r.from_rule_key}-${r.to_rule_key}-${i}`}
                pagination={{ pageSize: 10 }}
                columns={[
                  {
                    title: 'From',
                    dataIndex: 'from_rule_key',
                    key: 'from',
                    render: (v: string) => <Tag color="blue">{v.split('/').pop()}</Tag>,
                  },
                  {
                    title: 'To',
                    dataIndex: 'to_rule_key',
                    key: 'to',
                    render: (v: string) => <Tag color="green">{v.split('/').pop()}</Tag>,
                  },
                  {
                    title: 'Amount',
                    dataIndex: 'amount',
                    key: 'amount',
                    render: (v: number) => <span style={{ fontWeight: 'bold' }}>{v}</span>,
                  },
                  {
                    title: 'Repaid',
                    dataIndex: 'repaid',
                    key: 'repaid',
                    render: (v: boolean) => v 
                      ? <Tag color="green">Yes</Tag> 
                      : <Tag color="orange">Pending</Tag>,
                  },
                  {
                    title: 'Time',
                    dataIndex: 'borrowed_at',
                    key: 'time',
                    render: (v: string) => dayjs(v).format('MM-DD HH:mm:ss'),
                  },
                ]}
              />
            </Card>
          </>
        ) : (
          <div style={{ textAlign: 'center', padding: '40px 0', color: '#8c8c8c' }}>
            No token borrowing records in this namespace
          </div>
        )}
      </Modal>
    </div>
  );
};

export default RateLimitRules;
