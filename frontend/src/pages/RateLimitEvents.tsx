import React, { useState, useEffect, useCallback } from 'react';
import { Card, Table, Tag, Input, DatePicker, Space, Button, Statistic, Row, Col } from 'antd';
import { SearchOutlined, ReloadOutlined } from '@ant-design/icons';
import { listRateLimitEvents } from '../api/client';
import { RateLimitEvent, RateLimitAlgorithm } from '../types';
import dayjs from 'dayjs';

const reasonLabels: Record<string, string> = {
  token_bucket_exhausted: 'Token Bucket Exhausted',
  sliding_window_exhausted: 'Sliding Window Limit',
  leaky_bucket_full: 'Leaky Bucket Full',
};

const reasonColors: Record<string, string> = {
  token_bucket_exhausted: 'red',
  sliding_window_exhausted: 'volcano',
  leaky_bucket_full: 'orange',
};

const algorithmColors: Record<RateLimitAlgorithm, string> = {
  token_bucket: 'blue',
  sliding_window: 'green',
  leaky_bucket: 'orange',
};

const RateLimitEvents: React.FC = () => {
  const [events, setEvents] = useState<RateLimitEvent[]>([]);
  const [loading, setLoading] = useState(false);
  const [filterRuleKey, setFilterRuleKey] = useState('');
  const [filterClientId, setFilterClientId] = useState('');
  const [filterDateRange, setFilterDateRange] = useState<[dayjs.Dayjs | null, dayjs.Dayjs | null] | null>(null);

  const fetchEvents = useCallback(async () => {
    setLoading(true);
    try {
      const params: any = {};
      if (filterRuleKey) params.rule_key = filterRuleKey;
      if (filterClientId) params.client_id = filterClientId;
      if (filterDateRange && filterDateRange[0] && filterDateRange[1]) {
        params.start = filterDateRange[0].toISOString();
        params.end = filterDateRange[1].toISOString();
      }
      const data = await listRateLimitEvents(params);
      setEvents(data || []);
    } catch (e) {
      // ignore
    }
    setLoading(false);
  }, [filterRuleKey, filterClientId, filterDateRange]);

  useEffect(() => {
    fetchEvents();
  }, [fetchEvents]);

  const reasonCounts = events.reduce((acc, e) => {
    acc[e.reason] = (acc[e.reason] || 0) + 1;
    return acc;
  }, {} as Record<string, number>);

  const algorithmCounts = events.reduce((acc, e) => {
    acc[e.algorithm] = (acc[e.algorithm] || 0) + 1;
    return acc;
  }, {} as Record<string, number>);

  const columns = [
    {
      title: 'Timestamp',
      dataIndex: 'timestamp',
      key: 'timestamp',
      render: (ts: string) => dayjs(ts).format('YYYY-MM-DD HH:mm:ss.SSS'),
      sorter: (a: RateLimitEvent, b: RateLimitEvent) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime(),
      defaultSortOrder: 'descend' as const,
    },
    {
      title: 'Client ID',
      dataIndex: 'client_id',
      key: 'client_id',
      render: (id: string) => <Tag>{id}</Tag>,
    },
    {
      title: 'Rule Key',
      dataIndex: 'rule_key',
      key: 'rule_key',
      render: (key: string) => <Tag color="purple">{key}</Tag>,
    },
    {
      title: 'Request Key',
      dataIndex: 'request_key',
      key: 'request_key',
      render: (key: string) => <Tag color="geekblue">{key}</Tag>,
    },
    {
      title: 'Algorithm',
      dataIndex: 'algorithm',
      key: 'algorithm',
      render: (algo: RateLimitAlgorithm) => (
        <Tag color={algorithmColors[algo]}>{algo.replace('_', ' ')}</Tag>
      ),
    },
    {
      title: 'Reason',
      dataIndex: 'reason',
      key: 'reason',
      render: (reason: string) => (
        <Tag color={reasonColors[reason] || 'default'}>
          {reasonLabels[reason] || reason}
        </Tag>
      ),
    },
  ];

  return (
    <div>
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}>
          <Card size="small">
            <Statistic title="Total Rejections" value={events.length} valueStyle={{ color: '#ff4d4f' }} />
          </Card>
        </Col>
        <Col span={6}>
          <Card size="small">
            <Statistic
              title="Token Bucket Rejections"
              value={algorithmCounts.token_bucket || 0}
              valueStyle={{ color: '#1677ff' }}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card size="small">
            <Statistic
              title="Sliding Window Rejections"
              value={algorithmCounts.sliding_window || 0}
              valueStyle={{ color: '#52c41a' }}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card size="small">
            <Statistic
              title="Leaky Bucket Rejections"
              value={algorithmCounts.leaky_bucket || 0}
              valueStyle={{ color: '#fa8c16' }}
            />
          </Card>
        </Col>
      </Row>

      <Card
        title="Rejection Events"
        extra={
          <Space>
            <Input
              placeholder="Filter by rule key"
              prefix={<SearchOutlined />}
              value={filterRuleKey}
              onChange={e => setFilterRuleKey(e.target.value)}
              style={{ width: 200 }}
              allowClear
            />
            <Input
              placeholder="Filter by client ID"
              prefix={<SearchOutlined />}
              value={filterClientId}
              onChange={e => setFilterClientId(e.target.value)}
              style={{ width: 180 }}
              allowClear
            />
            <DatePicker.RangePicker
              showTime
              onChange={dates => setFilterDateRange(dates as any)}
              style={{ width: 360 }}
            />
            <Button icon={<ReloadOutlined />} onClick={fetchEvents}>
              Refresh
            </Button>
          </Space>
        }
      >
        <Table
          dataSource={events}
          columns={columns}
          rowKey={(r) => `${r.timestamp}-${r.client_id}-${r.rule_key}`}
          loading={loading}
          pagination={{ pageSize: 20, showSizeChanger: true, showTotal: (total) => `Total ${total} events` }}
          size="small"
        />
      </Card>
    </div>
  );
};

export default RateLimitEvents;
