import React, { useState, useEffect } from 'react';
import {
  Row,
  Col,
  Card,
  Table,
  Tag,
  Button,
  Space,
  Modal,
  Form,
  InputNumber,
  Switch,
  message,
  Typography,
  List,
  Alert as AntAlert,
} from 'antd';
import {
  AlertOutlined,
  SettingOutlined,
  BellOutlined,
  WarningOutlined,
  CheckCircleOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { listAlerts, configureAlerts, listLocks } from '../api/client';
import { Alert, AlertRule, LockInfo } from '../types';

const { Title, Text } = Typography;

const Alerts: React.FC = () => {
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [locks, setLocks] = useState<LockInfo[]>([]);
  const [configModalVisible, setConfigModalVisible] = useState(false);
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);

  const fetchData = async () => {
    setLoading(true);
    try {
      const [alertsData, locksData] = await Promise.all([
        listAlerts(),
        listLocks(),
      ]);
      setAlerts(alertsData);
      setLocks(locksData);
    } catch (e: any) {
      // Ignore
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 10000);
    return () => clearInterval(interval);
  }, []);

  useEffect(() => {
    const defaultRules: AlertRule[] = [
      {
        id: 'lock_hold_timeout',
        name: 'Lock Hold Time Exceeded',
        condition: 'lock_hold_time',
        threshold: 300,
        duration: 60,
        enabled: true,
      },
      {
        id: 'wait_queue_length',
        name: 'Wait Queue Too Long',
        condition: 'wait_queue_length',
        threshold: 10,
        duration: 30,
        enabled: true,
      },
      {
        id: 'deadlock_frequency',
        name: 'Deadlock Frequency High',
        condition: 'deadlock_frequency',
        threshold: 5,
        duration: 300,
        enabled: true,
      },
      {
        id: 'node_offline',
        name: 'Node Offline',
        condition: 'node_offline',
        threshold: 1,
        duration: 10,
        enabled: true,
      },
    ];
    setRules(defaultRules);
  }, []);

  const handleSaveRules = async () => {
    try {
      await configureAlerts(rules);
      message.success('Alert rules saved successfully');
      setConfigModalVisible(false);
    } catch (e: any) {
      message.error('Failed to save alert rules');
    }
  };

  const severityColors: Record<string, string> = {
    critical: 'red',
    warning: 'orange',
    info: 'blue',
  };

  const columns = [
    {
      title: 'Severity',
      dataIndex: 'Severity',
      key: 'severity',
      render: (severity: string) => (
        <Tag color={severityColors[severity] || 'default'}>
          {severity === 'critical' && <WarningOutlined style={{ marginRight: 4 }} />}
          {severity.toUpperCase()}
        </Tag>
      ),
    },
    {
      title: 'Name',
      dataIndex: 'Name',
      key: 'name',
    },
    {
      title: 'Message',
      dataIndex: 'Message',
      key: 'message',
    },
    {
      title: 'Value',
      dataIndex: 'Value',
      key: 'value',
      render: (value: number, record: Alert) => (
        <Space>
          <span style={{ color: '#faad14', fontWeight: 'bold' }}>{value}</span>
          <span style={{ color: '#8c8c8c' }}>/ {record.Threshold}</span>
        </Space>
      ),
    },
    {
      title: 'Timestamp',
      dataIndex: 'Timestamp',
      key: 'timestamp',
      render: (text: string) => dayjs(text).format('MM-DD HH:mm:ss'),
    },
  ];

  const longLocks = locks
    .filter(l => {
      if (l.holders.length === 0) return false;
      const holder = l.holders[0];
      const holdTime = dayjs().diff(dayjs(holder.acquired_at), 'second');
      return holdTime > 60;
    })
    .sort((a, b) => {
      const holdA = dayjs().diff(dayjs(a.holders[0]?.acquired_at || 0), 'second');
      const holdB = dayjs().diff(dayjs(b.holders[0]?.acquired_at || 0), 'second');
      return holdB - holdA;
    })
    .slice(0, 5);

  const highContention = locks
    .filter(l => l.wait_length > 0)
    .sort((a, b) => b.wait_length - a.wait_length)
    .slice(0, 5);

  return (
    <div>
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={16}>
          <Card
            title={
              <Space>
                <AlertOutlined />
                Active Alerts
              </Space>
            }
            extra={
              <Button
                type="primary"
                icon={<SettingOutlined />}
                onClick={() => {
                  form.setFieldsValue({ rules });
                  setConfigModalVisible(true);
                }}>
                Configure Rules
              </Button>
            }>
            {alerts.length === 0 ? (
              <div style={{ textAlign: 'center', padding: '48px 0', color: '#8c8c8c' }}>
                <CheckCircleOutlined style={{ fontSize: 48, color: '#52c41a', marginBottom: 16 }} />
                <div>No active alerts</div>
                <div style={{ fontSize: 12, marginTop: 8 }}>All systems operating normally</div>
              </div>
            ) : (
              <Table
                dataSource={alerts}
                columns={columns}
                rowKey="ID"
                loading={loading}
                size="middle"
                pagination={false}
              />
            )}
          </Card>
        </Col>

        <Col xs={24} lg={8}>
          <Card
            title={
              <Space>
                <BellOutlined />
                System Warnings
              </Space>
            }>
            {longLocks.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <AntAlert
                  type="warning"
                  showIcon
                  message={`${longLocks.length} locks held for over 60 seconds`}
                  style={{ marginBottom: 8 }}
                />
                <List
                  size="small"
                  dataSource={longLocks}
                  renderItem={(item) => {
                    const holdTime = dayjs().diff(dayjs(item.holders[0]?.acquired_at || 0), 'second');
                    return (
                      <List.Item>
                        <Space direction="vertical" style={{ width: '100%' }}>
                          <code style={{ fontSize: 12 }}>{item.id}</code>
                          <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                            <Text type="secondary" style={{ fontSize: 12 }}>
                              Holder: {item.holders[0]?.client_id}
                            </Text>
                            <Tag color="orange" style={{ margin: 0 }}>
                              {holdTime}s
                            </Tag>
                          </div>
                        </Space>
                      </List.Item>
                    );
                  }}
                />
              </div>
            )}

            {highContention.length > 0 && (
              <div>
                <AntAlert
                  type="info"
                  showIcon
                  message={`${highContention.length} locks with waiting clients`}
                  style={{ marginBottom: 8 }}
                />
                <List
                  size="small"
                  dataSource={highContention}
                  renderItem={(item) => (
                    <List.Item>
                      <Space direction="vertical" style={{ width: '100%' }}>
                        <code style={{ fontSize: 12 }}>{item.id}</code>
                        <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                          <Text type="secondary" style={{ fontSize: 12 }}>
                            {item.type}
                          </Text>
                          <Tag color="blue" style={{ margin: 0 }}>
                            {item.wait_length} waiting
                          </Tag>
                        </div>
                      </Space>
                    </List.Item>
                  )}
                />
              </div>
            )}

            {longLocks.length === 0 && highContention.length === 0 && (
              <div style={{ textAlign: 'center', padding: '24px 0', color: '#8c8c8c' }}>
                <CheckCircleOutlined style={{ fontSize: 32, color: '#52c41a', marginBottom: 8 }} />
                <div style={{ fontSize: 12 }}>No warnings detected</div>
              </div>
            )}
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24}>
          <Card
            title={
              <Space>
                <SettingOutlined />
                Configured Rules
              </Space>
            }>
            <List
              dataSource={rules}
              renderItem={(rule) => (
                <List.Item
                  actions={[
                    <Switch
                      checked={rule.enabled}
                      onChange={(checked) => {
                        setRules(rules.map(r =>
                          r.id === rule.id ? { ...r, enabled: checked } : r
                        ));
                      }}
                    />,
                  ]}>
                  <List.Item.Meta
                    title={
                      <Space>
                        {rule.name}
                        {rule.enabled ? (
                          <Tag color="green">Enabled</Tag>
                        ) : (
                          <Tag color="default">Disabled</Tag>
                        )}
                      </Space>
                    }
                    description={
                      <Space>
                        <Text type="secondary">Condition: {rule.condition}</Text>
                        <Text type="secondary">Threshold: {rule.threshold}</Text>
                        <Text type="secondary">Duration: {rule.duration}s</Text>
                      </Space>
                    }
                  />
                </List.Item>
              )}
            />
          </Card>
        </Col>
      </Row>

      <Modal
        title="Configure Alert Rules"
        open={configModalVisible}
        onOk={handleSaveRules}
        onCancel={() => setConfigModalVisible(false)}
        okText="Save"
        width={800}
        destroyOnClose>
        <Form form={form} layout="vertical">
          <Form.List name="rules">
            {() => (
              <Space direction="vertical" style={{ width: '100%' }}>
                {rules.map((rule, index) => (
                  <Card key={rule.id} size="small" title={rule.name}>
                    <Row gutter={[16, 16]}>
                      <Col xs={24} sm={8}>
                        <Form.Item label="Threshold" style={{ marginBottom: 0 }}>
                          <InputNumber
                            min={0}
                            value={rule.threshold}
                            onChange={(value) => {
                              const newRules = [...rules];
                              newRules[index].threshold = value as number;
                              setRules(newRules);
                            }}
                            style={{ width: '100%' }}
                          />
                        </Form.Item>
                      </Col>
                      <Col xs={24} sm={8}>
                        <Form.Item label="Duration (seconds)" style={{ marginBottom: 0 }}>
                          <InputNumber
                            min={1}
                            value={rule.duration}
                            onChange={(value) => {
                              const newRules = [...rules];
                              newRules[index].duration = value as number;
                              setRules(newRules);
                            }}
                            style={{ width: '100%' }}
                          />
                        </Form.Item>
                      </Col>
                      <Col xs={24} sm={8}>
                        <Form.Item label="Enabled" style={{ marginBottom: 0 }}>
                          <Switch
                            checked={rule.enabled}
                            onChange={(checked) => {
                              const newRules = [...rules];
                              newRules[index].enabled = checked;
                              setRules(newRules);
                            }}
                          />
                        </Form.Item>
                      </Col>
                    </Row>
                  </Card>
                ))}
              </Space>
            )}
          </Form.List>
        </Form>
      </Modal>
    </div>
  );
};

export default Alerts;
