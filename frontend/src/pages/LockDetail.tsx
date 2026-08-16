import React, { useState, useEffect } from 'react';
import {
  Row,
  Col,
  Card,
  Descriptions,
  Table,
  Tag,
  Button,
  Modal,
  InputNumber,
  Space,
  message,
  Popconfirm,
  Tabs,
  Typography,
  Tooltip,
} from 'antd';
import {
  LockOutlined,
  DeleteOutlined,
  EditOutlined,
  ReloadOutlined,
  StopOutlined,
  ClockCircleOutlined,
  UserOutlined,
  HistoryOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useParams, useNavigate } from 'react-router-dom';
import dayjs from 'dayjs';
import { getLock, forceReleaseLock, adjustLease, clearQueue, adjustCapacity } from '../api/client';
import { LockInfo } from '../types';

const { Title } = Typography;

const LockDetail: React.FC = () => {
  const { namespace, name } = useParams<{ namespace: string; name: string }>();
  const navigate = useNavigate();
  const [lock, setLock] = useState<LockInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [leaseModalVisible, setLeaseModalVisible] = useState(false);
  const [capacityModalVisible, setCapacityModalVisible] = useState(false);
  const [newLease, setNewLease] = useState(30);
  const [newCapacity, setNewCapacity] = useState(1);

  const fetchData = async () => {
    if (!namespace || !name) return;
    setLoading(true);
    try {
      const data = await getLock(decodeURIComponent(namespace), decodeURIComponent(name));
      setLock(data);
    } catch (e: any) {
      message.error('Failed to load lock details');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 3000);
    return () => clearInterval(interval);
  }, [namespace, name]);

  const handleForceRelease = async () => {
    if (!lock) return;
    try {
      await forceReleaseLock(lock.namespace, lock.name);
      message.success('Lock force released successfully');
      fetchData();
    } catch (e: any) {
      message.error('Failed to force release lock');
    }
  };

  const handleAdjustLease = async () => {
    if (!lock) return;
    try {
      await adjustLease(lock.namespace, lock.name, newLease * 1000000000);
      message.success('Lease time adjusted successfully');
      setLeaseModalVisible(false);
      fetchData();
    } catch (e: any) {
      message.error('Failed to adjust lease time');
    }
  };

  const handleAdjustCapacity = async () => {
    if (!lock) return;
    try {
      await adjustCapacity(lock.namespace, lock.name, newCapacity);
      message.success('Capacity adjusted successfully');
      setCapacityModalVisible(false);
      fetchData();
    } catch (e: any) {
      message.error('Failed to adjust capacity');
    }
  };

  const handleClearQueue = async () => {
    if (!lock) return;
    try {
      await clearQueue(lock.namespace, lock.name);
      message.success('Wait queue cleared successfully');
      fetchData();
    } catch (e: any) {
      message.error('Failed to clear wait queue');
    }
  };

  const typeColors: Record<string, string> = {
    mutex: 'blue',
    rwlock: 'green',
    semaphore: 'orange',
    barrier: 'purple',
  };

  const stateColors: Record<string, string> = {
    held: 'green',
    waiting: 'orange',
    free: 'default',
  };

  const holderColumns = [
    {
      title: 'Client ID',
      dataIndex: 'client_id',
      key: 'client_id',
      render: (text: string) => <code>{text}</code>,
    },
    {
      title: 'Token',
      dataIndex: 'token',
      key: 'token',
      render: (token: number) => <Tag color="cyan">{token}</Tag>,
    },
    {
      title: 'Mode',
      dataIndex: 'mode',
      key: 'mode',
      render: (mode: string) => (
        <Tag color={mode === 'write' ? 'red' : 'green'}>{mode}</Tag>
      ),
    },
    {
      title: 'Acquired At',
      dataIndex: 'acquired_at',
      key: 'acquired_at',
      render: (text: string) => dayjs(text).format('MM-DD HH:mm:ss'),
    },
    {
      title: 'Lease Expiry',
      dataIndex: 'lease_expiry',
      key: 'lease_expiry',
      render: (text: string) => {
        const expiry = dayjs(text);
        const remaining = expiry.diff(dayjs(), 'second');
        return (
          <Space>
            {expiry.format('MM-DD HH:mm:ss')}
            <Tag color={remaining < 10 ? 'red' : remaining < 30 ? 'orange' : 'green'}>
              {remaining}s remaining
            </Tag>
          </Space>
        );
      },
    },
  ];

  const waitQueueColumns = [
    {
      title: 'Client ID',
      dataIndex: 'client_id',
      key: 'client_id',
      render: (text: string) => <code>{text}</code>,
    },
    {
      title: 'Mode',
      dataIndex: 'mode',
      key: 'mode',
      render: (mode: string) => (
        <Tag color={mode === 'write' ? 'red' : 'green'}>{mode}</Tag>
      ),
    },
    {
      title: 'Priority',
      dataIndex: 'priority',
      key: 'priority',
      render: (p: number) => (p === 0 ? 'Default' : p),
    },
    {
      title: 'Requested At',
      dataIndex: 'requested_at',
      key: 'requested_at',
      render: (text: string) => dayjs(text).format('MM-DD HH:mm:ss'),
    },
    {
      title: 'Wait Time',
      dataIndex: 'wait_time',
      key: 'wait_time',
      render: (ms: number) => `${Math.floor(ms / 1000000000)}s`,
    },
  ];

  const historyColumns = [
    {
      title: 'Time',
      dataIndex: 'timestamp',
      key: 'timestamp',
      render: (text: string) => dayjs(text).format('MM-DD HH:mm:ss.SSS'),
    },
    {
      title: 'Event',
      dataIndex: 'event',
      key: 'event',
      render: (event: string, record: any) => {
        const colors: Record<string, string> = {
          acquired: 'green',
          released: 'red',
          expired: 'orange',
          upgraded: 'blue',
          barrier_released: 'purple',
        };
        return (
          <Space>
            {record.cascade_released && (
              <Tooltip title={`Cascade released (parent: ${record.cascade_parent})`}>
                <ThunderboltOutlined style={{ color: '#ff4d4f' }} />
              </Tooltip>
            )}
            <Tag color={colors[event] || 'default'}>{event}</Tag>
            {record.cascade_released && (
              <Tag color="red" style={{ marginLeft: 4 }}>
                CASCADE
              </Tag>
            )}
          </Space>
        );
      },
    },
    {
      title: 'Client ID',
      dataIndex: 'client_id',
      key: 'client_id',
      render: (text: string) => <code>{text}</code>,
    },
    {
      title: 'Mode',
      dataIndex: 'mode',
      key: 'mode',
      render: (mode: string) => mode && <Tag color={mode === 'write' ? 'red' : 'green'}>{mode}</Tag>,
    },
    {
      title: 'Token',
      dataIndex: 'token',
      key: 'token',
      render: (token: number) => token && <Tag color="cyan">{token}</Tag>,
    },
    {
      title: 'Details',
      dataIndex: 'cascade_parent',
      key: 'cascade_parent',
      render: (parent: string, record: any) => {
        if (record.cascade_released && parent) {
          return (
            <span style={{ color: '#8c8c8c', fontSize: 12 }}>
              Parent: <code>{parent}</code>
            </span>
          );
        }
        return null;
      },
    },
  ];

  if (!lock) {
    return <Card loading={true} />;
  }

  const tabItems = [
    {
      key: 'holders',
      label: (
        <span>
          <UserOutlined />
          Holders ({lock.holders.length})
        </span>
      ),
      children: (
        <Table
          dataSource={lock.holders}
          columns={holderColumns}
          rowKey="token"
          size="middle"
          pagination={false}
          locale={{ emptyText: 'No holders' }}
        />
      ),
    },
    {
      key: 'waiters',
      label: (
        <span>
          <ClockCircleOutlined />
          Wait Queue ({lock.wait_queue.length})
        </span>
      ),
      children: (
        <Table
          dataSource={lock.wait_queue}
          columns={waitQueueColumns}
          rowKey="client_id"
          size="middle"
          pagination={false}
          locale={{ emptyText: 'Wait queue is empty' }}
        />
      ),
    },
    {
      key: 'history',
      label: (
        <span>
          <HistoryOutlined />
          History ({lock.history?.length || 0})
        </span>
      ),
      children: (
        <Table
          dataSource={lock.history || []}
          columns={historyColumns}
          rowKey="timestamp"
          size="middle"
          pagination={{ pageSize: 20 }}
          locale={{ emptyText: 'No history' }}
        />
      ),
    },
  ];

  return (
    <div>
      <Card
        loading={loading}
        title={
          <Space>
            <LockOutlined />
            <Title level={4} style={{ margin: 0 }}>
              {lock.id}
            </Title>
            <Tag color={typeColors[lock.type]}>{lock.type}</Tag>
            <Tag color={stateColors[lock.state]}>{lock.state}</Tag>
          </Space>
        }
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} onClick={fetchData}>
              Refresh
            </Button>
            <Button onClick={() => navigate('/locks')}>Back</Button>
          </Space>
        }>
        <Descriptions bordered size="small" column={3}>
          <Descriptions.Item label="Namespace">{lock.namespace}</Descriptions.Item>
          <Descriptions.Item label="Name">{lock.name}</Descriptions.Item>
          <Descriptions.Item label="Type">
            <Tag color={typeColors[lock.type]}>{lock.type}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="State">
            <Tag color={stateColors[lock.state]}>{lock.state}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="Queue Mode">{lock.queue_mode}</Descriptions.Item>
          <Descriptions.Item label="Lease Time">{lock.lease_time / 1000000000}s</Descriptions.Item>
          {lock.type === 'semaphore' && (
            <>
              <Descriptions.Item label="Capacity">{lock.capacity}</Descriptions.Item>
              <Descriptions.Item label="Holders">{lock.holders.length} / {lock.capacity}</Descriptions.Item>
            </>
          )}
          {lock.type !== 'semaphore' && (
            <Descriptions.Item label="Holders">{lock.holders.length}</Descriptions.Item>
          )}
          <Descriptions.Item label="Wait Queue">
            {lock.wait_queue.length} clients waiting
          </Descriptions.Item>
          <Descriptions.Item label="Max Token">
            <Tag color="cyan">{lock.max_token}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="Created At">
            {dayjs(lock.created_at).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
        </Descriptions>

        <Card
          size="small"
          title="Admin Operations"
          style={{ marginTop: 16 }}
          type="inner">
          <Space wrap>
            <Button
              type="primary"
              icon={<EditOutlined />}
              onClick={() => {
                setNewLease(Math.floor(lock.lease_time / 1000000000));
                setLeaseModalVisible(true);
              }}>
              Adjust Lease
            </Button>
            {lock.type === 'semaphore' && (
              <Button
                type="primary"
                icon={<EditOutlined />}
                onClick={() => {
                  setNewCapacity(lock.capacity);
                  setCapacityModalVisible(true);
                }}>
                Adjust Capacity
              </Button>
            )}
            <Popconfirm
              title="Clear wait queue?"
              description="All waiting clients will be notified and removed from the queue."
              onConfirm={handleClearQueue}
              okText="Yes"
              cancelText="No">
              <Button icon={<StopOutlined />} danger={lock.wait_queue.length > 0}>
                Clear Queue
              </Button>
            </Popconfirm>
            <Popconfirm
              title="Force release lock?"
              description="This will forcibly release the lock and invalidate all tokens. This operation cannot be undone."
              onConfirm={handleForceRelease}
              okText="Force Release"
              cancelText="Cancel"
              okButtonProps={{ danger: true }}>
              <Button icon={<DeleteOutlined />} danger={lock.holders.length > 0}>
                Force Release
              </Button>
            </Popconfirm>
          </Space>
        </Card>
      </Card>

      <Card style={{ marginTop: 16 }}>
        <Tabs items={tabItems} defaultActiveKey="holders" />
      </Card>

      <Modal
        title="Adjust Lease Time"
        open={leaseModalVisible}
        onOk={handleAdjustLease}
        onCancel={() => setLeaseModalVisible(false)}
        okText="Adjust"
        destroyOnClose>
        <div style={{ marginBottom: 16 }}>
          Current lease time: {Math.floor(lock.lease_time / 1000000000)}s
        </div>
        <div>
          <span style={{ marginRight: 8 }}>New lease time:</span>
          <InputNumber
            min={5}
            max={300}
            value={newLease}
            onChange={(v) => setNewLease(v ?? 30)}
            addonAfter="seconds"
          />
        </div>
      </Modal>

      <Modal
        title="Adjust Semaphore Capacity"
        open={capacityModalVisible}
        onOk={handleAdjustCapacity}
        onCancel={() => setCapacityModalVisible(false)}
        okText="Adjust"
        destroyOnClose>
        <div style={{ marginBottom: 16 }}>
          Current capacity: {lock.capacity}
        </div>
        <div>
          <span style={{ marginRight: 8 }}>New capacity:</span>
          <InputNumber
            min={1}
            max={1000}
            value={newCapacity}
            onChange={(v) => setNewCapacity(v ?? 1)}
          />
        </div>
        <div style={{ marginTop: 8, color: '#8c8c8c', fontSize: 12 }}>
          Increasing capacity will immediately wake waiting clients.
          Decreasing capacity does not affect current holders.
        </div>
      </Modal>
    </div>
  );
};

export default LockDetail;
