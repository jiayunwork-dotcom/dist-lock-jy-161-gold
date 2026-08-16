import React, { useState, useEffect } from 'react';
import {
  Row,
  Col,
  Card,
  Statistic,
  Table,
  Progress,
  Tag,
  Tooltip,
} from 'antd';
import {
  LockOutlined,
  ClockCircleOutlined,
  UserOutlined,
  AlertOutlined,
  LineChartOutlined,
} from '@ant-design/icons';
import {
  LineChart,
  Line,
  AreaChart,
  Area,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip as ReTooltip,
  ResponsiveContainer,
} from 'recharts';
import dayjs from 'dayjs';
import { listLocks, getClusterStatus } from '../api/client';
import { LockInfo, ClusterStatus, MetricPoint, TopLock } from '../types';

const Dashboard: React.FC = () => {
  const [locks, setLocks] = useState<LockInfo[]>([]);
  const [cluster, setCluster] = useState<ClusterStatus | null>(null);
  const [holdTimeData, setHoldTimeData] = useState<MetricPoint[]>([]);
  const [deadlockData, setDeadlockData] = useState<MetricPoint[]>([]);

  useEffect(() => {
    const fetchData = async () => {
      try {
        const [locksData, clusterData] = await Promise.all([
          listLocks(),
          getClusterStatus(),
        ]);
        setLocks(locksData);
        setCluster(clusterData);
      } catch (e) {
        // Ignore
      }
    };

    fetchData();
    const interval = setInterval(fetchData, 5000);
    return () => clearInterval(interval);
  }, []);

  useEffect(() => {
    const generateData = () => {
      const now = dayjs();
      const holdData: MetricPoint[] = [];
      const deadData: MetricPoint[] = [];

      for (let i = 23; i >= 0; i--) {
        const time = now.subtract(i, 'hour');
        holdData.push({
          time: time.format('HH:00'),
          value: Math.floor(Math.random() * 30) + 10,
        });
        deadData.push({
          time: time.format('HH:00'),
          value: Math.floor(Math.random() * 3),
        });
      }

      setHoldTimeData(holdData);
      setDeadlockData(deadData);
    };
    generateData();
  }, []);

  const activeLocks = locks.filter(l => l.state === 'held').length;
  const waitingLocks = locks.filter(l => l.state === 'waiting').length;
  const totalWaiters = locks.reduce((acc, l) => acc + l.wait_length, 0);

  const topLocks: TopLock[] = [...locks]
    .sort((a, b) => b.wait_length - a.wait_length)
    .slice(0, 10)
    .map(l => ({ name: l.id, contention: l.wait_length }));

  const nodeStates = cluster?.servers.map(s => ({
    name: s.id,
    isLeader: s.id === cluster?.node_id && cluster?.state === 'Leader',
    state: s.id === cluster?.node_id ? cluster?.state : 'Follower',
  })) || [];

  const stats = [
    {
      title: 'Active Locks',
      value: activeLocks,
      icon: <LockOutlined style={{ color: '#52c41a', fontSize: 24 }} />,
      color: '#52c41a',
    },
    {
      title: 'Waiting Locks',
      value: waitingLocks,
      icon: <ClockCircleOutlined style={{ color: '#faad14', fontSize: 24 }} />,
      color: '#faad14',
    },
    {
      title: 'Total Waiters',
      value: totalWaiters,
      icon: <UserOutlined style={{ color: '#1890ff', fontSize: 24 }} />,
      color: '#1890ff',
    },
    {
      title: 'Cluster Nodes',
      value: nodeStates.length,
      icon: <AlertOutlined style={{ color: '#722ed1', fontSize: 24 }} />,
      color: '#722ed1',
    },
  ];

  const lockColumns = [
    {
      title: 'Lock Name',
      dataIndex: 'id',
      key: 'id',
      render: (text: string) => (
        <Tooltip title={text}>
          <span style={{ fontFamily: 'monospace' }}>{text}</span>
        </Tooltip>
      ),
    },
    {
      title: 'Type',
      dataIndex: 'type',
      key: 'type',
      render: (type: string) => {
        const colors: Record<string, string> = {
          mutex: 'blue',
          rwlock: 'green',
          semaphore: 'orange',
          barrier: 'purple',
        };
        return <Tag color={colors[type]}>{type}</Tag>;
      },
    },
    {
      title: 'State',
      dataIndex: 'state',
      key: 'state',
      render: (state: string) => {
        const colors: Record<string, string> = {
          held: 'green',
          waiting: 'orange',
          free: 'default',
        };
        return <Tag color={colors[state]}>{state}</Tag>;
      },
    },
    {
      title: 'Holders',
      dataIndex: 'holders',
      key: 'holders',
      render: (holders: any[]) => holders.length,
    },
    {
      title: 'Wait Queue',
      dataIndex: 'wait_length',
      key: 'wait_length',
      render: (len: number) => (
        <Progress
          percent={Math.min(len * 10, 100)}
          size="small"
          showInfo={false}
          format={() => len}
        />
      ),
    },
  ];

  return (
    <div>
      <Row gutter={[16, 16]}>
        {stats.map((stat, idx) => (
          <Col xs={24} sm={12} lg={6} key={idx}>
            <Card>
              <Statistic
                title={stat.title}
                value={stat.value}
                prefix={stat.icon}
                valueStyle={{ color: stat.color }}
              />
            </Card>
          </Col>
        ))}
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} lg={12}>
          <Card
            title={
              <span>
                <LineChartOutlined style={{ marginRight: 8 }} />
                Lock Hold Time Trend (24h)
              </span>
            }>
            <div style={{ height: 300 }}>
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={holdTimeData}>
                  <defs>
                    <linearGradient id="colorHold" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#1890ff" stopOpacity={0.8} />
                      <stop offset="95%" stopColor="#1890ff" stopOpacity={0.1} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" />
                  <YAxis />
                  <ReTooltip />
                  <Area
                    type="monotone"
                    dataKey="value"
                    stroke="#1890ff"
                    fillOpacity={1}
                    fill="url(#colorHold)"
                    name="Avg Hold Time (s)"
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </Card>
        </Col>

        <Col xs={24} lg={12}>
          <Card
            title={
              <span>
                <AlertOutlined style={{ marginRight: 8 }} />
                Deadlock Events (24h)
              </span>
            }>
            <div style={{ height: 300 }}>
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={deadlockData}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" />
                  <YAxis allowDecimals={false} />
                  <ReTooltip />
                  <Line
                    type="monotone"
                    dataKey="value"
                    stroke="#ff4d4f"
                    strokeWidth={2}
                    dot={{ fill: '#ff4d4f' }}
                    name="Deadlocks"
                  />
                </LineChart>
              </ResponsiveContainer>
            </div>
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} lg={12}>
          <Card
            title={
              <span>
                <LockOutlined style={{ marginRight: 8 }} />
                Contention Top 10
              </span>
            }>
            <div style={{ height: 300 }}>
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={topLocks} layout="vertical">
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis type="number" />
                  <YAxis
                    dataKey="name"
                    type="category"
                    width={200}
                    tick={{ fontSize: 12, fontFamily: 'monospace' }}
                  />
                  <ReTooltip />
                  <Bar dataKey="contention" fill="#1890ff" name="Waiters" />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </Card>
        </Col>

        <Col xs={24} lg={12}>
          <Card
            title={
              <span>
                <AlertOutlined style={{ marginRight: 8 }} />
                Cluster Nodes
              </span>
            }>
            <div style={{ padding: '16px 0' }}>
              {nodeStates.map((node, idx) => (
                <div
                  key={idx}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    padding: '12px 16px',
                    marginBottom: 8,
                    background: node.isLeader ? '#e6f4ff' : '#fafafa',
                    borderRadius: 8,
                    borderLeft: `4px solid ${node.isLeader ? '#1890ff' : '#d9d9d9'}`,
                  }}>
                  <div
                    style={{
                      width: 12,
                      height: 12,
                      borderRadius: '50%',
                      background: node.state === 'Leader' ? '#52c41a' : '#1890ff',
                      marginRight: 12,
                    }}
                  />
                  <div style={{ flex: 1 }}>
                    <div style={{ fontWeight: node.isLeader ? 'bold' : 'normal' }}>
                      {node.name}
                      {node.isLeader && (
                        <Tag color="green" style={{ marginLeft: 8 }}>LEADER</Tag>
                      )}
                    </div>
                    <div style={{ color: '#8c8c8c', fontSize: 12 }}>
                      State: {node.state}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24}>
          <Card
            title={
              <span>
                <LockOutlined style={{ marginRight: 8 }} />
                Active Locks
              </span>
            }>
            <Table
              dataSource={locks.filter(l => l.state !== 'free')}
              columns={lockColumns}
              rowKey="id"
              size="middle"
              pagination={{ pageSize: 10 }}
            />
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default Dashboard;
