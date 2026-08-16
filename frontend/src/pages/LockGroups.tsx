import React, { useState, useEffect } from 'react';
import {
  Card,
  Button,
  Modal,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  message,
  Tag,
  Typography,
  Row,
  Col,
  List,
  Table,
  Popconfirm,
  Divider,
  Descriptions,
  Alert,
} from 'antd';
import {
  TeamOutlined,
  PlusOutlined,
  DeleteOutlined,
  ReloadOutlined,
  LockOutlined,
  PlayCircleOutlined,
  StopOutlined,
  PlusCircleOutlined,
  MinusCircleOutlined,
} from '@ant-design/icons';
import {
  listGroups,
  getGroup,
  createGroup,
  deleteGroup,
  addLockToGroup,
  removeLockFromGroup,
  listLocks,
  batchAcquire,
  batchRelease,
} from '../api/client';
import { LockGroupInfo, LockInfo } from '../types';
import dayjs from 'dayjs';

const { Title, Text } = Typography;
const { Option } = Select;
const { TextArea } = Input;

const LockGroups: React.FC = () => {
  const [groups, setGroups] = useState<LockGroupInfo[]>([]);
  const [locks, setLocks] = useState<LockInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [selectedGroup, setSelectedGroup] = useState<LockGroupInfo | null>(null);
  const [createModalVisible, setCreateModalVisible] = useState(false);
  const [addLockModalVisible, setAddLockModalVisible] = useState(false);
  const [detailModalVisible, setDetailModalVisible] = useState(false);
  const [batchModalVisible, setBatchModalVisible] = useState(false);
  const [form] = Form.useForm();
  const [addLockForm] = Form.useForm();
  const [batchForm] = Form.useForm();

  const fetchData = async () => {
    setLoading(true);
    try {
      const [groupsData, locksData] = await Promise.all([
        listGroups(),
        listLocks(),
      ]);
      setGroups(groupsData);
      setLocks(locksData);
    } catch (e: any) {
      message.error('Failed to load groups');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 5000);
    return () => clearInterval(interval);
  }, []);

  const handleCreateGroup = async (values: any) => {
    try {
      await createGroup(values.name, values.description, values.timeout * 1000000000);
      message.success('Group created successfully');
      setCreateModalVisible(false);
      form.resetFields();
      fetchData();
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to create group');
    }
  };

  const handleDeleteGroup = async (name: string) => {
    try {
      await deleteGroup(name);
      message.success('Group deleted successfully');
      if (selectedGroup?.name === name) {
        setSelectedGroup(null);
        setDetailModalVisible(false);
      }
      fetchData();
    } catch (e: any) {
      message.error('Failed to delete group');
    }
  };

  const handleAddLock = async (values: any) => {
    if (!selectedGroup) return;
    try {
      await addLockToGroup(selectedGroup.name, values.namespace, values.name);
      message.success('Lock added to group successfully');
      setAddLockModalVisible(false);
      addLockForm.resetFields();
      await loadGroupDetail(selectedGroup.name);
      fetchData();
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to add lock');
    }
  };

  const handleRemoveLock = async (namespace: string, name: string) => {
    if (!selectedGroup) return;
    try {
      await removeLockFromGroup(selectedGroup.name, namespace, name);
      message.success('Lock removed from group successfully');
      await loadGroupDetail(selectedGroup.name);
      fetchData();
    } catch (e: any) {
      message.error('Failed to remove lock');
    }
  };

  const loadGroupDetail = async (name: string) => {
    try {
      const group = await getGroup(name);
      setSelectedGroup(group);
    } catch (e: any) {
      message.error('Failed to load group details');
    }
  };

  const handleViewDetail = async (group: LockGroupInfo) => {
    await loadGroupDetail(group.name);
    setDetailModalVisible(true);
  };

  const handleBatchAcquire = async (values: any) => {
    if (!selectedGroup) return;
    try {
      const result = await batchAcquire(
        selectedGroup.name,
        values.client_id,
        values.lease_time * 1000000000,
        values.mode
      );
      if (result.success) {
        message.success('Batch acquire successful');
        setBatchModalVisible(false);
        batchForm.resetFields();
        const tokens = Object.entries(result.tokens || {})
          .map(([k, v]) => `${k}: ${v}`)
          .join(', ');
        message.info(`Tokens: ${tokens}`);
      } else {
        message.error(`Batch acquire failed: ${result.error}`);
      }
      fetchData();
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Batch acquire failed');
    }
  };

  const handleBatchRelease = async () => {
    if (!selectedGroup) return;
    try {
      const values = await batchForm.validateFields(['client_id']);
      const result = await batchRelease(selectedGroup.name, values.client_id);
      if (result.success) {
        message.success(`Batch release successful, released ${result.released_locks?.length || 0} locks`);
        setBatchModalVisible(false);
        batchForm.resetFields();
      } else {
        message.error(`Batch release failed: ${result.error}`);
      }
      fetchData();
    } catch (e: any) {
      message.error('Batch release failed');
    }
  };

  const getStateColor = (state: string) => {
    switch (state) {
      case 'held':
        return 'green';
      case 'waiting':
        return 'orange';
      default:
        return 'default';
    }
  };

  const lockColumns = [
    {
      title: 'Lock ID',
      dataIndex: 'id',
      key: 'id',
      render: (text: string) => <code style={{ fontSize: 12 }}>{text}</code>,
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
      render: (state: string) => <Tag color={getStateColor(state)}>{state}</Tag>,
    },
    {
      title: 'Holders',
      dataIndex: 'holders',
      key: 'holders',
      render: (holders: string[]) => holders.length,
    },
    {
      title: 'Waiters',
      dataIndex: 'waiters',
      key: 'waiters',
    },
  ];

  return (
    <div>
      <Card
        loading={loading}
        title={
          <Space>
            <TeamOutlined />
            <Title level={4} style={{ margin: 0 }}>
              Lock Groups
            </Title>
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
              onClick={() => setCreateModalVisible(true)}
            >
              Create Group
            </Button>
          </Space>
        }
      >
        {groups.length === 0 ? (
          <div style={{ textAlign: 'center', padding: '60px 0', color: '#8c8c8c' }}>
            <TeamOutlined style={{ fontSize: 48, marginBottom: 16 }} />
            <div>No groups created yet</div>
            <div style={{ fontSize: 12, marginTop: 8 }}>
              Click "Create Group" to organize locks into groups
            </div>
          </div>
        ) : (
          <Row gutter={[16, 16]}>
            {groups.map((group) => (
              <Col xs={24} lg={12} xl={8} key={group.name}>
                <Card
                  size="small"
                  title={
                    <Space>
                      <LockOutlined />
                      <Text strong>{group.name}</Text>
                    </Space>
                  }
                  extra={
                    <Space>
                      <Button
                        type="text"
                        size="small"
                        onClick={() => handleViewDetail(group)}
                      >
                        Details
                      </Button>
                      <Popconfirm
                        title="Delete this group?"
                        description="This will not delete the locks themselves, only the grouping."
                        onConfirm={() => handleDeleteGroup(group.name)}
                        okText="Delete"
                        cancelText="Cancel"
                        okButtonProps={{ danger: true }}
                      >
                        <Button
                          type="text"
                          danger
                          size="small"
                          icon={<DeleteOutlined />}
                        />
                      </Popconfirm>
                    </Space>
                  }
                  style={{ height: '100%' }}
                >
                  {group.description && (
                    <div style={{ color: '#8c8c8c', fontSize: 12, marginBottom: 8 }}>
                      {group.description}
                    </div>
                  )}
                  <Descriptions size="small" column={1} bordered>
                    <Descriptions.Item label="Locks">
                      {group.lock_ids.length} locks
                    </Descriptions.Item>
                    <Descriptions.Item label="Timeout">
                      {group.timeout > 0
                        ? `${Math.floor(group.timeout / 1000000000)}s`
                        : 'Default (30s)'}
                    </Descriptions.Item>
                    <Descriptions.Item label="Created">
                      {dayjs(group.created_at).format('MM-DD HH:mm')}
                    </Descriptions.Item>
                  </Descriptions>

                  <Divider style={{ margin: '12px 0' }} />

                  <div style={{ marginBottom: 8 }}>
                    <Text type="secondary" style={{ fontSize: 12 }}>
                      Locks in group:
                    </Text>
                  </div>
                  <div style={{ maxHeight: 100, overflowY: 'auto' }}>
                    {group.lock_ids.length === 0 ? (
                      <Text type="secondary" style={{ fontSize: 12 }}>
                        No locks in this group
                      </Text>
                    ) : (
                      <Space wrap size={[4, 4]}>
                        {group.lock_ids.map((lockId) => (
                          <Tag key={lockId} style={{ margin: 0 }}>
                            {lockId.split('/').pop()}
                          </Tag>
                        ))}
                      </Space>
                    )}
                  </div>
                </Card>
              </Col>
            ))}
          </Row>
        )}
      </Card>

      <Modal
        title="Create Lock Group"
        open={createModalVisible}
        onCancel={() => setCreateModalVisible(false)}
        footer={null}
        destroyOnClose
        width={500}
      >
        <Form form={form} layout="vertical" onFinish={handleCreateGroup}>
          <Form.Item
            name="name"
            label="Group Name"
            rules={[
              { required: true, message: 'Group name is required' },
              { max: 50, message: 'Name too long' },
            ]}
          >
            <Input placeholder="e.g., database-transaction" />
          </Form.Item>

          <Form.Item name="description" label="Description">
            <TextArea
              rows={3}
              placeholder="Describe the purpose of this lock group"
            />
          </Form.Item>

          <Form.Item
            name="timeout"
            label="Batch Timeout (seconds)"
            rules={[{ required: true, message: 'Timeout is required' }]}
            initialValue={30}
          >
            <InputNumber min={1} max={300} style={{ width: '100%' }} addonAfter="seconds" />
          </Form.Item>

          <Alert
            message="If all locks cannot be acquired within this timeout, the entire batch operation will be rolled back."
            type="info"
            showIcon
            style={{ marginBottom: 16 }}
          />

          <Form.Item style={{ marginBottom: 0 }}>
            <Space style={{ float: 'right' }}>
              <Button onClick={() => setCreateModalVisible(false)}>Cancel</Button>
              <Button type="primary" htmlType="submit">
                Create Group
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={
          <Space>
            <TeamOutlined />
            Group Details: {selectedGroup?.name}
          </Space>
        }
        open={detailModalVisible}
        onCancel={() => setDetailModalVisible(false)}
        width={900}
        footer={
          <Space>
            <Button
              icon={<PlusCircleOutlined />}
              onClick={() => setAddLockModalVisible(true)}
            >
              Add Lock
            </Button>
            <Button
              type="primary"
              icon={<PlayCircleOutlined />}
              onClick={() => {
                setBatchModalVisible(true);
              }}
            >
              Batch Operations
            </Button>
            <Button onClick={() => setDetailModalVisible(false)}>Close</Button>
          </Space>
        }
        destroyOnClose
      >
        {selectedGroup && (
          <>
            <Descriptions size="small" bordered column={3}>
              <Descriptions.Item label="Name">{selectedGroup.name}</Descriptions.Item>
              <Descriptions.Item label="Description">
                {selectedGroup.description || '-'}
              </Descriptions.Item>
              <Descriptions.Item label="Timeout">
                {selectedGroup.timeout > 0
                  ? `${Math.floor(selectedGroup.timeout / 1000000000)}s`
                  : 'Default (30s)'}
              </Descriptions.Item>
              <Descriptions.Item label="Total Locks">
                {selectedGroup.lock_ids.length}
              </Descriptions.Item>
              <Descriptions.Item label="Created">
                {dayjs(selectedGroup.created_at).format('YYYY-MM-DD HH:mm:ss')}
              </Descriptions.Item>
              <Descriptions.Item label="Acquire Order">
                <Tag color="blue">Declared Order</Tag>
              </Descriptions.Item>
            </Descriptions>

            <Divider orientation="left">Locks in Group</Divider>

            <Table
              dataSource={selectedGroup.locks}
              columns={[
                ...lockColumns,
                {
                  title: 'Actions',
                  key: 'actions',
                  render: (_, record: any) => (
                    <Popconfirm
                      title="Remove this lock from the group?"
                      onConfirm={() => {
                        const [ns, name] = record.id.split('/', 2);
                        handleRemoveLock(ns, name);
                      }}
                      okText="Remove"
                      cancelText="Cancel"
                    >
                      <Button
                        type="text"
                        danger
                        size="small"
                        icon={<MinusCircleOutlined />}
                      >
                        Remove
                      </Button>
                    </Popconfirm>
                  ),
                },
              ]}
              rowKey="id"
              size="small"
              pagination={false}
              locale={{ emptyText: 'No locks in this group' }}
            />

            {selectedGroup.locks.length > 0 && (
              <>
                <Divider orientation="left">Acquire Order</Divider>
                <div
                  style={{
                    padding: 16,
                    background: '#fafafa',
                    borderRadius: 8,
                  }}
                >
                  <Space direction="vertical" style={{ width: '100%' }}>
                    {selectedGroup.lock_ids.map((lockId, idx) => (
                      <div key={lockId} style={{ display: 'flex', alignItems: 'center' }}>
                        <Tag color="blue" style={{ marginRight: 12 }}>
                          #{idx + 1}
                        </Tag>
                        <code>{lockId}</code>
                        {idx < selectedGroup.lock_ids.length - 1 && (
                          <div style={{ margin: '0 8px', color: '#8c8c8c' }}>→</div>
                        )}
                      </div>
                    ))}
                  </Space>
                  <div style={{ marginTop: 12, fontSize: 12, color: '#8c8c8c' }}>
                    <StopOutlined /> Locks are acquired in this order. On failure, all previously
                    acquired locks are released in reverse order.
                  </div>
                </div>
              </>
            )}
          </>
        )}
      </Modal>

      <Modal
        title="Add Lock to Group"
        open={addLockModalVisible}
        onCancel={() => setAddLockModalVisible(false)}
        footer={null}
        destroyOnClose
      >
        <Form form={addLockForm} layout="vertical" onFinish={handleAddLock}>
          <Row gutter={8}>
            <Col span={12}>
              <Form.Item
                name="namespace"
                label="Namespace"
                rules={[{ required: true, message: 'Required' }]}
              >
                <Select placeholder="Select namespace">
                  {Array.from(new Set(locks.map((l) => l.namespace))).map((ns) => (
                    <Option key={ns} value={ns}>
                      {ns}
                    </Option>
                  ))}
                </Select>
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="name"
                label="Lock Name"
                rules={[{ required: true, message: 'Required' }]}
              >
                <Select placeholder="Select lock" showSearch>
                  {locks.map((l) => (
                    <Option key={l.id} value={l.name}>
                      {l.name}
                    </Option>
                  ))}
                </Select>
              </Form.Item>
            </Col>
          </Row>

          <Alert
            message="The lock will be added to the end of the acquire order."
            type="info"
            showIcon
            style={{ marginBottom: 16 }}
          />

          <Form.Item style={{ marginBottom: 0 }}>
            <Space style={{ float: 'right' }}>
              <Button onClick={() => setAddLockModalVisible(false)}>Cancel</Button>
              <Button type="primary" htmlType="submit">
                Add Lock
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="Batch Operations"
        open={batchModalVisible}
        onCancel={() => setBatchModalVisible(false)}
        footer={null}
        destroyOnClose
      >
        <Form form={batchForm} layout="vertical">
          <Form.Item
            name="client_id"
            label="Client ID"
            rules={[{ required: true, message: 'Client ID is required' }]}
          >
            <Input placeholder="Unique identifier for the client" />
          </Form.Item>

          <Divider orientation="left">Acquire Options</Divider>

          <Row gutter={8}>
            <Col span={12}>
              <Form.Item
                name="lease_time"
                label="Lease Time (seconds)"
                rules={[{ required: true, message: 'Required' }]}
                initialValue={30}
              >
                <InputNumber min={5} max={300} style={{ width: '100%' }} addonAfter="s" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="mode"
                label="Lock Mode"
                rules={[{ required: true, message: 'Required' }]}
                initialValue="write"
              >
                <Select>
                  <Option value="write">Write</Option>
                  <Option value="read">Read</Option>
                </Select>
              </Form.Item>
            </Col>
          </Row>

          <Alert
            message="Batch acquire will attempt to acquire all locks in the declared order. If any lock fails to acquire within the group timeout, all previously acquired locks will be released."
            type="warning"
            showIcon
            style={{ marginBottom: 16 }}
          />

          <Form.Item style={{ marginBottom: 0 }}>
            <Space style={{ float: 'right' }}>
              <Button onClick={() => setBatchModalVisible(false)}>Cancel</Button>
              <Button onClick={handleBatchRelease} icon={<StopOutlined />} danger>
                Batch Release
              </Button>
              <Button
                type="primary"
                onClick={() => batchForm.submit()}
                icon={<PlayCircleOutlined />}
              >
                Batch Acquire
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default LockGroups;
