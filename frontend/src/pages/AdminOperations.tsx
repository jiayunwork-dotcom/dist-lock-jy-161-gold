import React, { useState, useEffect } from 'react';
import {
  Row,
  Col,
  Card,
  Table,
  Tag,
  Button,
  Space,
  message,
  Typography,
  Descriptions,
  Timeline,
  Statistic,
} from 'antd';
import {
  HistoryOutlined,
  UserOutlined,
  ReloadOutlined,
  DeleteOutlined,
  UnlockOutlined,
  ClockCircleOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { listAdminOperations } from '../api/client';
import { AdminOperation } from '../types';

const { Title, Text } = Typography;

const AdminOperations: React.FC = () => {
  const [operations, setOperations] = useState<AdminOperation[]>([]);
  const [loading, setLoading] = useState(false);

  const fetchData = async () => {
    setLoading(true);
    try {
      const data = await listAdminOperations();
      setOperations(data);
    } catch (e: any) {
      // Ignore
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, []);

  const actionIcons: Record<string, React.ReactNode> = {
    force_release: <UnlockOutlined style={{ color: '#faad14' }} />,
    adjust_lease: <ClockCircleOutlined style={{ color: '#1890ff' }} />,
    clear_queue: <DeleteOutlined style={{ color: '#ff4d4f' }} />,
    adjust_capacity: <ClockCircleOutlined style={{ color: '#52c41a' }} />,
  };

  const actionColors: Record<string, string> = {
    force_release: 'orange',
    adjust_lease: 'blue',
    clear_queue: 'red',
    adjust_capacity: 'green',
  };

  const actionNames: Record<string, string> = {
    force_release: 'Force Release',
    adjust_lease: 'Adjust Lease',
    clear_queue: 'Clear Queue',
    adjust_capacity: 'Adjust Capacity',
  };

  const columns = [
    {
      title: 'Time',
      dataIndex: 'timestamp',
      key: 'timestamp',
      render: (text: string) => (
        <Space>
          <Text type="secondary" style={{ fontFamily: 'monospace', fontSize: 12 }}>
            {dayjs(text).format('MM-DD HH:mm:ss')}
          </Text>
        </Space>
      ),
    },
    {
      title: 'Action',
      dataIndex: 'Action',
      key: 'action',
      render: (action: string) => (
        <Space>
          {actionIcons[action]}
          <Tag color={actionColors[action] || 'default'}>
            {actionNames[action] || action}
          </Tag>
        </Space>
      ),
    },
    {
      title: 'Lock',
      dataIndex: 'LockID',
      key: 'lock_id',
      render: (text: string) => <code style={{ fontSize: 12 }}>{text}</code>,
    },
    {
      title: 'Details',
      key: 'details',
      render: (_: any, record: AdminOperation) => {
        return <Text type="secondary">{record.details || '-'}</Text>;
      },
    },
    {
      title: 'Admin',
      dataIndex: 'operator',
      key: 'admin',
      render: (text: string) => (
        <Space>
          <UserOutlined />
          <span>{text}</span>
        </Space>
      ),
    },
  ];

  const stats = [
    {
      title: 'Total Operations',
      value: operations.length,
      color: '#1890ff',
    },
    {
      title: 'Force Releases',
      value: operations.filter(o => o.action === 'force_release').length,
      color: '#faad14',
    },
    {
      title: 'Lease Adjustments',
      value: operations.filter(o => o.action === 'adjust_lease').length,
      color: '#52c41a',
    },
    {
      title: 'Queue Clears',
      value: operations.filter(o => o.action === 'clear_queue').length,
      color: '#ff4d4f',
    },
  ];

  const recentOperations = operations.slice(0, 10);

  return (
    <div>
      <Row gutter={[16, 16]}>
        {stats.map((stat, idx) => (
          <Col xs={24} sm={12} lg={6} key={idx}>
            <Card>
              <Statistic
                title={stat.title}
                value={stat.value}
                valueStyle={{ color: stat.color }}
              />
            </Card>
          </Col>
        ))}
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} lg={16}>
          <Card
            title={
              <Space>
                <HistoryOutlined />
                Operation Log
              </Space>
            }
            extra={
              <Button icon={<ReloadOutlined />} onClick={fetchData}>
                Refresh
              </Button>
            }>
            <Table
              dataSource={operations}
              columns={columns}
              rowKey="ID"
              loading={loading}
              size="middle"
              pagination={{
                pageSize: 20,
                showSizeChanger: true,
                showQuickJumper: true,
              }}
            />
          </Card>
        </Col>

        <Col xs={24} lg={8}>
          <Card
            title={
              <Space>
                <HistoryOutlined />
                Recent Activity
              </Space>
            }>
            {recentOperations.length === 0 ? (
              <div style={{ textAlign: 'center', padding: '24px 0', color: '#8c8c8c' }}>
                <HistoryOutlined style={{ fontSize: 32, marginBottom: 8 }} />
                <div style={{ fontSize: 12 }}>No operations recorded yet</div>
              </div>
            ) : (
              <Timeline
                mode="left"
                items={recentOperations.map(op => ({
                  color: actionColors[op.action] || 'blue',
                  dot: actionIcons[op.action],
                  label: (
                    <Text type="secondary" style={{ fontSize: 11 }}>
                      {dayjs(op.timestamp).format('HH:mm:ss')}
                    </Text>
                  ),
                  children: (
                    <div>
                      <Tag color={actionColors[op.action] || 'default'}>
                        {actionNames[op.action] || op.action}
                      </Tag>
                      <div style={{ fontSize: 12, marginTop: 4 }}>
                        <code>{op.lock_id}</code>
                      </div>
                    </div>
                  ),
                }))}
              />
            )}
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default AdminOperations;
