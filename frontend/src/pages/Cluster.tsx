import React, { useState, useEffect } from 'react';
import {
  Row,
  Col,
  Card,
  Table,
  Tag,
  Button,
  Progress,
  Statistic,
  Space,
  Modal,
  Input,
  message,
  Popconfirm,
  Descriptions,
} from 'antd';
import {
  ClusterOutlined,
  PlusOutlined,
  DeleteOutlined,
  ReloadOutlined,
  CrownOutlined,
  TeamOutlined,
  DatabaseOutlined,
} from '@ant-design/icons';
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from 'recharts';
import dayjs from 'dayjs';
import { getClusterStatus, addPeer, removePeer } from '../api/client';
import { ClusterStatus, MetricPoint } from '../types';

const Cluster: React.FC = () => {
  const [cluster, setCluster] = useState<ClusterStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [addModalVisible, setAddModalVisible] = useState(false);
  const [newNodeId, setNewNodeId] = useState('');
  const [newNodeAddr, setNewNodeAddr] = useState('');
  const [replicationData, setReplicationData] = useState<MetricPoint[]>([]);

  const fetchData = async () => {
    setLoading(true);
    try {
      const data = await getClusterStatus();
      setCluster(data);
    } catch (e: any) {
      message.error('Failed to load cluster status');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 2000);
    return () => clearInterval(interval);
  }, []);

  useEffect(() => {
    const generateData = () => {
      const now = dayjs();
      const data: MetricPoint[] = [];
      for (let i = 29; i >= 0; i--) {
        data.push({
          time: now.subtract(i, 'second').format('HH:mm:ss'),
          value: Math.floor(Math.random() * 50) + 1,
        });
      }
      setReplicationData(data);
    };
    generateData();
    const interval = setInterval(generateData, 1000);
    return () => clearInterval(interval);
  }, []);

  const handleAddPeer = async () => {
    if (!newNodeId || !newNodeAddr) {
      message.error('Please fill in all fields');
      return;
    }
    try {
      await addPeer(newNodeId, newNodeAddr);
      message.success('Peer added successfully');
      setAddModalVisible(false);
      setNewNodeId('');
      setNewNodeAddr('');
      fetchData();
    } catch (e: any) {
      message.error('Failed to add peer');
    }
  };

  const handleRemovePeer = async (nodeId: string) => {
    try {
      await removePeer(nodeId);
      message.success('Peer removed successfully');
      fetchData();
    } catch (e: any) {
      message.error('Failed to remove peer');
    }
  };

  const nodeColumns = [
    {
      title: 'Node ID',
      dataIndex: 'id',
      key: 'id',
      render: (text: string) => <code>{text}</code>,
    },
    {
      title: 'Address',
      dataIndex: 'address',
      key: 'address',
      render: (text: string) => <code>{text}</code>,
    },
    {
      title: 'Status',
      key: 'status',
      render: (_: any, record: any) => {
        const isLeader = !!(cluster && record.id === cluster.node_id && cluster.state === 'Leader');
        const state = isLeader ? 'Leader' : 'Follower';
        const color = isLeader ? 'green' : 'blue';
        return (
          <Space>
            {isLeader && <CrownOutlined style={{ color: '#faad14' }} />}
            <Tag color={color}>{state}</Tag>
          </Space>
        );
      },
    },
    {
      title: 'Action',
      key: 'action',
      render: (_: any, record: any) => {
        const isLeader = !!(cluster && record.id === cluster.node_id && cluster.state === 'Leader');
        return (
          <Popconfirm
            title="Remove this peer?"
            disabled={isLeader}
            onConfirm={() => handleRemovePeer(record.id)}
            okText="Yes"
            cancelText="No">
            <Button
              type="text"
              danger
              icon={<DeleteOutlined />}
              disabled={isLeader}
              size="small">
              Remove
            </Button>
          </Popconfirm>
        );
      },
    },
  ];

  const stats = [
    {
      title: 'Total Nodes',
      value: cluster?.servers.length || 0,
      icon: <TeamOutlined style={{ color: '#1890ff', fontSize: 24 }} />,
      color: '#1890ff',
    },
    {
      title: 'Last Index',
      value: cluster?.last_index || 0,
      icon: <DatabaseOutlined style={{ color: '#52c41a', fontSize: 24 }} />,
      color: '#52c41a',
    },
    {
      title: 'Applied Index',
      value: cluster?.applied_index || 0,
      icon: <DatabaseOutlined style={{ color: '#722ed1', fontSize: 24 }} />,
      color: '#722ed1',
    },
    {
      title: 'Replication Lag',
      value: cluster ? (cluster.last_index - cluster.applied_index) : 0,
      icon: <ClusterOutlined style={{ color: '#fa8c16', fontSize: 24 }} />,
      color: '#fa8c16',
    },
  ];

  const replicationProgress = cluster
    ? Math.min(100, (cluster.applied_index / Math.max(cluster.last_index, 1)) * 100)
    : 100;

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
              <Space>
                <ClusterOutlined />
                Raft Log Replication
              </Space>
            }>
            <div style={{ marginBottom: 16 }}>
              <div style={{ marginBottom: 8 }}>
                <Space>
                  <span>Sync Progress:</span>
                  <span style={{ fontFamily: 'monospace' }}>
                    {cluster?.applied_index} / {cluster?.last_index}
                  </span>
                </Space>
              </div>
              <Progress
                percent={replicationProgress}
                status={replicationProgress < 100 ? 'active' : 'success'}
                strokeColor={{
                  '0%': '#108ee9',
                  '100%': '#87d068',
                }}
              />
            </div>
            <div style={{ height: 250 }}>
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={replicationData}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" />
                  <YAxis />
                  <Tooltip />
                  <Line
                    type="monotone"
                    dataKey="value"
                    stroke="#1890ff"
                    strokeWidth={2}
                    dot={false}
                    name="Latency (ms)"
                  />
                </LineChart>
              </ResponsiveContainer>
            </div>
          </Card>
        </Col>

        <Col xs={24} lg={12}>
          <Card
            title={
              <Space>
                <CrownOutlined />
                Current Status
              </Space>
            }>
            {cluster && (
              <Descriptions bordered size="small" column={1}>
                <Descriptions.Item label="Node ID">
                  <code>{cluster.node_id}</code>
                </Descriptions.Item>
                <Descriptions.Item label="State">
                  <Tag color={cluster.state === 'Leader' ? 'green' : 'blue'}>
                    {cluster.state}
                  </Tag>
                  {cluster.state === 'Leader' && (
                    <CrownOutlined style={{ color: '#faad14', marginLeft: 8 }} />
                  )}
                </Descriptions.Item>
                <Descriptions.Item label="Leader">
                  <code>{cluster.leader || 'None'}</code>
                </Descriptions.Item>
                <Descriptions.Item label="Last Log Index">
                  {cluster.last_index}
                </Descriptions.Item>
                <Descriptions.Item label="Applied Index">
                  {cluster.applied_index}
                </Descriptions.Item>
                <Descriptions.Item label="Commit Lag">
                  <Tag color={cluster.last_index - cluster.applied_index > 10 ? 'orange' : 'green'}>
                    {cluster.last_index - cluster.applied_index} entries
                  </Tag>
                </Descriptions.Item>
              </Descriptions>
            )}
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24}>
          <Card
            title={
              <Space>
                <ClusterOutlined />
                Cluster Nodes
              </Space>
            }
            extra={
              <Space>
                <Button icon={<ReloadOutlined />} onClick={fetchData}>
                  Refresh
                </Button>
                <Button
                  type="primary"
                  icon={<PlusOutlined />}
                  onClick={() => setAddModalVisible(true)}>
                  Add Peer
                </Button>
              </Space>
            }>
            <Table
              dataSource={cluster?.servers || []}
              columns={nodeColumns}
              rowKey="id"
              loading={loading}
              size="middle"
              pagination={false}
            />
          </Card>
        </Col>
      </Row>

      <Modal
        title="Add Peer"
        open={addModalVisible}
        onOk={handleAddPeer}
        onCancel={() => setAddModalVisible(false)}
        okText="Add"
        destroyOnClose>
        <Space direction="vertical" style={{ width: '100%' }}>
          <div>
            <div style={{ marginBottom: 8 }}>Node ID:</div>
            <Input
              placeholder="e.g., node4"
              value={newNodeId}
              onChange={(e) => setNewNodeId(e.target.value)}
            />
          </div>
          <div>
            <div style={{ marginBottom: 8 }}>Raft Address:</div>
            <Input
              placeholder="e.g., node4:7000"
              value={newNodeAddr}
              onChange={(e) => setNewNodeAddr(e.target.value)}
            />
          </div>
        </Space>
      </Modal>
    </div>
  );
};

export default Cluster;
