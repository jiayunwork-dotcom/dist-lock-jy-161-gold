import React, { useState, useEffect, useMemo } from 'react';
import {
  Row,
  Col,
  Card,
  Table,
  Input,
  Select,
  Tag,
  Button,
  Tree,
  Space,
  Tooltip,
  Progress,
} from 'antd';
import {
  LockOutlined,
  SearchOutlined,
  EyeOutlined,
  ClockCircleOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import dayjs from 'dayjs';
import { listLocks } from '../api/client';
import { LockInfo } from '../types';

const { Search } = Input;
const { Option } = Select;
const { DirectoryTree } = Tree;

const LockList: React.FC = () => {
  const navigate = useNavigate();
  const [locks, setLocks] = useState<LockInfo[]>([]);
  const [searchText, setSearchText] = useState('');
  const [sortBy, setSortBy] = useState('name');
  const [selectedNamespace, setSelectedNamespace] = useState<string | undefined>();
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    const fetchData = async () => {
      setLoading(true);
      try {
        const data = await listLocks();
        setLocks(data);
      } catch (e) {
        // Ignore
      } finally {
        setLoading(false);
      }
    };

    fetchData();
    const interval = setInterval(fetchData, 5000);
    return () => clearInterval(interval);
  }, []);

  const filteredLocks = useMemo(() => {
    let result = locks;

    if (selectedNamespace) {
      result = result.filter(l => l.namespace === selectedNamespace);
    }

    if (searchText) {
      const lower = searchText.toLowerCase();
      result = result.filter(l =>
        l.id.toLowerCase().includes(lower) ||
        l.name.toLowerCase().includes(lower) ||
        l.namespace.toLowerCase().includes(lower)
      );
    }

    if (sortBy === 'contention') {
      result = [...result].sort((a, b) => b.wait_length - a.wait_length);
    } else {
      result = [...result].sort((a, b) => a.id.localeCompare(b.id));
    }

    return result;
  }, [locks, selectedNamespace, searchText, sortBy]);

  const namespaces = Array.from(new Set(locks.map(l => l.namespace)));
  const treeData = [
    {
      title: 'All',
      key: 'all',
      children: namespaces.map(ns => ({
        title: ns,
        key: ns,
      })),
    },
  ];

  const columns = [
    {
      title: 'Lock Name',
      dataIndex: 'id',
      key: 'id',
      render: (text: string, record: LockInfo) => (
        <Space>
          <LockOutlined style={{ color: '#1890ff' }} />
          <Tooltip title={text}>
            <span style={{ fontFamily: 'monospace', cursor: 'pointer' }}>
              {text}
            </span>
          </Tooltip>
        </Space>
      ),
    },
    {
      title: 'Namespace',
      dataIndex: 'namespace',
      key: 'namespace',
      render: (text: string) => <Tag>{text}</Tag>,
      filters: namespaces.map(ns => ({ text: ns, value: ns })),
      onFilter: (value: any, record: LockInfo) => record.namespace === value,
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
      filters: [
        { text: 'Mutex', value: 'mutex' },
        { text: 'RWLock', value: 'rwlock' },
        { text: 'Semaphore', value: 'semaphore' },
        { text: 'Barrier', value: 'barrier' },
      ],
      onFilter: (value: any, record: LockInfo) => record.type === value,
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
      filters: [
        { text: 'Held', value: 'held' },
        { text: 'Waiting', value: 'waiting' },
        { text: 'Free', value: 'free' },
      ],
      onFilter: (value: any, record: LockInfo) => record.state === value,
    },
    {
      title: 'Holders',
      dataIndex: 'holders',
      key: 'holders',
      render: (holders: any[], record: LockInfo) => (
        <Space>
          <UserOutlined />
          <span>
            {holders.length}
            {record.type === 'semaphore' && `/${record.capacity}`}
            {record.type === 'rwlock' && (
              <span style={{ color: '#8c8c8c', fontSize: 12, marginLeft: 4 }}>
                ({holders.filter((h: any) => h.mode === 'read').length}R /
                {holders.filter((h: any) => h.mode === 'write').length}W)
              </span>
            )}
          </span>
        </Space>
      ),
      sorter: (a: LockInfo, b: LockInfo) => a.holders.length - b.holders.length,
    },
    {
      title: 'Wait Queue',
      dataIndex: 'wait_length',
      key: 'wait_length',
      render: (len: number) => (
        <Space>
          <ClockCircleOutlined style={{ color: len > 0 ? '#faad14' : '#52c41a' }} />
          <Progress
            percent={Math.min(len * 10, 100)}
            size="small"
            showInfo={false}
            style={{ width: 60 }}
          />
          <span>{len}</span>
        </Space>
      ),
      sorter: (a: LockInfo, b: LockInfo) => a.wait_length - b.wait_length,
    },
    {
      title: 'Created',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (text: string) => dayjs(text).format('MM-DD HH:mm:ss'),
      sorter: (a: LockInfo, b: LockInfo) =>
        dayjs(a.created_at).valueOf() - dayjs(b.created_at).valueOf(),
    },
    {
      title: 'Action',
      key: 'action',
      render: (_: any, record: LockInfo) => (
        <Button
          type="primary"
          size="small"
          icon={<EyeOutlined />}
          onClick={() => navigate(`/locks/${encodeURIComponent(record.namespace)}/${encodeURIComponent(record.name)}`)}>
          Detail
        </Button>
      ),
    },
  ];

  const onTreeSelect = (keys: any[]) => {
    if (keys[0] === 'all' || !keys[0]) {
      setSelectedNamespace(undefined);
    } else {
      setSelectedNamespace(String(keys[0]));
    }
  };

  return (
    <div>
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={6}>
          <Card title="Namespaces" size="small">
            <DirectoryTree
              defaultExpandAll
              selectedKeys={selectedNamespace ? [selectedNamespace] : ['all']}
              treeData={treeData}
              onSelect={onTreeSelect}
            />
          </Card>
        </Col>
        <Col xs={24} lg={18}>
          <Card>
            <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
              <Col xs={24} md={12}>
                <Search
                  placeholder="Search locks..."
                  prefix={<SearchOutlined />}
                  allowClear
                  value={searchText}
                  onChange={(e) => setSearchText(e.target.value)}
                  onSearch={(val) => setSearchText(val)}
                />
              </Col>
              <Col xs={24} md={8}>
                <Select
                  style={{ width: '100%' }}
                  value={sortBy}
                  onChange={setSortBy}
                  placeholder="Sort by">
                  <Option value="name">Sort by Name</Option>
                  <Option value="contention">Sort by Contention</Option>
                </Select>
              </Col>
              <Col xs={24} md={4}>
                <Button
                  type="primary"
                  onClick={() => {
                    setSearchText('');
                    setSelectedNamespace(undefined);
                  }}>
                  Reset
                </Button>
              </Col>
            </Row>

            <Table
              dataSource={filteredLocks}
              columns={columns}
              rowKey="id"
              loading={loading}
              size="middle"
              pagination={{
                pageSize: 20,
                showSizeChanger: true,
                showQuickJumper: true,
                showTotal: (total) => `Total ${total} locks`,
              }}
            />
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default LockList;
