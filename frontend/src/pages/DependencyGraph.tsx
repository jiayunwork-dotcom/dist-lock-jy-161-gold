import React, { useState, useEffect, useRef } from 'react';
import {
  Card,
  Button,
  Modal,
  Form,
  Input,
  Select,
  Space,
  message,
  Tag,
  Typography,
  Row,
  Col,
  List,
  Alert,
} from 'antd';
import {
  ForkOutlined,
  PlusOutlined,
  DeleteOutlined,
  ReloadOutlined,
  InfoCircleOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { getDependencyGraph, registerDependency, removeDependency, listLocks } from '../api/client';
import { DependencyGraphData, LockInfo, GraphNode, GraphEdge } from '../types';

const { Title, Text } = Typography;
const { Option } = Select;

const DependencyGraph: React.FC = () => {
  const [graphData, setGraphData] = useState<DependencyGraphData | null>(null);
  const [locks, setLocks] = useState<LockInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [addModalVisible, setAddModalVisible] = useState(false);
  const [form] = Form.useForm();
  const canvasRef = useRef<HTMLDivElement>(null);
  const [nodes, setNodes] = useState<GraphNode[]>([]);
  const [edges, setEdges] = useState<GraphEdge[]>([]);

  const fetchData = async () => {
    setLoading(true);
    try {
      const [graph, locksData] = await Promise.all([
        getDependencyGraph(),
        listLocks(),
      ]);
      setGraphData(graph);
      setLocks(locksData);
      parseGraphData(graph);
    } catch (e: any) {
      message.error('Failed to load dependency graph');
    } finally {
      setLoading(false);
    }
  };

  const parseGraphData = (data: DependencyGraphData) => {
    const nodeSet = new Set<string>();
    const parsedEdges: GraphEdge[] = [];

    for (const [parent, children] of Object.entries(data.edges)) {
      nodeSet.add(parent);
      for (const child of Object.keys(children)) {
        nodeSet.add(child);
        parsedEdges.push({ source: parent, target: child });
      }
    }

    for (const lockId of Object.keys(data.states)) {
      nodeSet.add(lockId);
    }

    const parsedNodes: GraphNode[] = Array.from(nodeSet).map((id) => ({
      id,
      label: id.split('/').pop() || id,
      state: data.states[id] || 'free',
      clients: data.clients[id] || [],
    }));

    setNodes(parsedNodes);
    setEdges(parsedEdges);
  };

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 5000);
    return () => clearInterval(interval);
  }, []);

  const handleAddDependency = async (values: any) => {
    try {
      await registerDependency(
        values.parent_namespace,
        values.parent_name,
        values.child_namespace,
        values.child_name
      );
      message.success('Dependency registered successfully');
      setAddModalVisible(false);
      form.resetFields();
      fetchData();
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to register dependency');
    }
  };

  const handleRemoveDependency = async (parent: string, child: string) => {
    const [parentNs, parentName] = parent.split('/', 2);
    const [childNs, childName] = child.split('/', 2);
    try {
      await removeDependency(parentNs, parentName, childNs, childName);
      message.success('Dependency removed successfully');
      fetchData();
    } catch (e: any) {
      message.error('Failed to remove dependency');
    }
  };

  const getNodeColor = (state: string) => {
    switch (state) {
      case 'held':
        return '#52c41a';
      case 'waiting':
        return '#faad14';
      default:
        return '#bfbfbf';
    }
  };

  const renderSVGGraph = () => {
    if (nodes.length === 0) {
      return (
        <div style={{ textAlign: 'center', padding: '60px 0', color: '#8c8c8c' }}>
          <InfoCircleOutlined style={{ fontSize: 48, marginBottom: 16 }} />
          <div>No dependencies registered yet</div>
          <div style={{ fontSize: 12, marginTop: 8 }}>
            Click "Add Dependency" to create lock dependencies
          </div>
        </div>
      );
    }

    const minWidth = 900;
    const nodeRadius = 45;
    const levelPadding = 120;
    const nodePadding = 100;

    const levels: Record<string, number> = {};
    const visited = new Set<string>();

    const findRoots = () => {
      const hasParent = new Set<string>();
      for (const edge of edges) {
        hasParent.add(edge.target);
      }
      const roots = nodes.filter((n) => !hasParent.has(n.id)).map((n) => n.id);
      if (roots.length === 0 && nodes.length > 0) {
        const hasChildren = new Set<string>();
        for (const edge of edges) {
          hasChildren.add(edge.source);
        }
        const leafNodes = nodes.filter((n) => !hasChildren.has(n.id)).map((n) => n.id);
        if (leafNodes.length > 0) {
          return [leafNodes[0]];
        }
        return [nodes[0].id];
      }
      return roots;
    };

    const assignLevels = (nodeId: string, level: number) => {
      if (visited.has(nodeId)) return;
      visited.add(nodeId);
      levels[nodeId] = Math.max(levels[nodeId] || 0, level);

      for (const edge of edges) {
        if (edge.source === nodeId) {
          assignLevels(edge.target, level + 1);
        }
      }
    };

    const roots = findRoots();
    roots.forEach((root) => assignLevels(root, 0));

    nodes.forEach((node) => {
      if (!(node.id in levels)) {
        levels[node.id] = 0;
      }
    });

    const maxLevel = Math.max(...Object.values(levels), 0);
    const nodesPerLevel: Record<number, GraphNode[]> = {};
    nodes.forEach((node) => {
      const level = levels[node.id];
      if (!nodesPerLevel[level]) nodesPerLevel[level] = [];
      nodesPerLevel[level].push(node);
    });

    const maxNodesPerLevel = Math.max(...Object.values(nodesPerLevel).map((arr) => arr.length), 1);
    const width = Math.max(minWidth, (maxLevel + 1) * levelPadding + 100);
    const height = Math.max(400, maxNodesPerLevel * nodePadding + 100);

    const nodePositions: Record<string, { x: number; y: number }> = {};
    const levelWidth = width / (maxLevel + 1);

    for (const [level, levelNodes] of Object.entries(nodesPerLevel)) {
      const lvl = parseInt(level);
      const totalVerticalSpace = height - 80;
      const nodeSpacing = totalVerticalSpace / (levelNodes.length + 1);
      
      levelNodes.sort((a, b) => a.id.localeCompare(b.id));
      
      levelNodes.forEach((node, idx) => {
        nodePositions[node.id] = {
          x: 60 + levelWidth * lvl + levelWidth / 2 - 30,
          y: 40 + nodeSpacing * (idx + 1),
        };
      });
    }

    return (
      <svg width="100%" height={height} viewBox={`0 0 ${width} ${height}`}>
        <defs>
          <marker
            id="arrowhead"
            markerWidth="10"
            markerHeight="7"
            refX="9"
            refY="3.5"
            orient="auto"
          >
            <polygon points="0 0, 10 3.5, 0 7" fill="#8c8c8c" />
          </marker>
        </defs>

        {edges.map((edge, idx) => {
          const sourcePos = nodePositions[edge.source];
          const targetPos = nodePositions[edge.target];
          if (!sourcePos || !targetPos) return null;

          const dx = targetPos.x - sourcePos.x;
          const dy = targetPos.y - sourcePos.y;
          const dist = Math.sqrt(dx * dx + dy * dy);
          const startX = sourcePos.x + (dx / dist) * nodeRadius;
          const startY = sourcePos.y + (dy / dist) * nodeRadius;
          const endX = targetPos.x - (dx / dist) * nodeRadius;
          const endY = targetPos.y - (dy / dist) * nodeRadius;

          return (
            <line
              key={idx}
              x1={startX}
              y1={startY}
              x2={endX}
              y2={endY}
              stroke="#8c8c8c"
              strokeWidth="2"
              markerEnd="url(#arrowhead)"
            />
          );
        })}

        {nodes.map((node) => {
          const pos = nodePositions[node.id];
          if (!pos) return null;

          return (
            <g key={node.id}>
              <circle
                cx={pos.x}
                cy={pos.y}
                r={nodeRadius}
                fill={getNodeColor(node.state)}
                stroke="#fff"
                strokeWidth="3"
                style={{ filter: 'drop-shadow(0 2px 4px rgba(0,0,0,0.2))' }}
              />
              <text
                x={pos.x}
                y={pos.y - 5}
                textAnchor="middle"
                fill="#fff"
                fontSize="11"
                fontWeight="bold"
              >
                {node.label.length > 10 ? node.label.slice(0, 10) + '...' : node.label}
              </text>
              <text
                x={pos.x}
                y={pos.y + 10}
                textAnchor="middle"
                fill="#fff"
                fontSize="10"
              >
                {node.clients.length > 0 ? `${node.clients.length} holder` : ''}
              </text>
            </g>
          );
        })}
      </svg>
    );
  };

  const renderDependenciesList = () => {
    if (edges.length === 0) {
      return null;
    }

    return (
      <Card
        size="small"
        title={
          <Space>
            <ForkOutlined />
            Dependencies ({edges.length})
          </Space>
        }
        style={{ marginTop: 16 }}
      >
        <List
          dataSource={edges}
          renderItem={(edge) => (
            <List.Item
              actions={[
                <Button
                  type="text"
                  danger
                  size="small"
                  icon={<DeleteOutlined />}
                  onClick={() => handleRemoveDependency(edge.source, edge.target)}
                >
                  Remove
                </Button>,
              ]}
            >
              <List.Item.Meta
                title={
                  <Space>
                    <code>{edge.source}</code>
                    <Text type="secondary">→</Text>
                    <code>{edge.target}</code>
                  </Space>
                }
              />
            </List.Item>
          )}
        />
      </Card>
    );
  };

  return (
    <div>
      <Card
        loading={loading}
        title={
          <Space>
            <ForkOutlined />
            <Title level={4} style={{ margin: 0 }}>
              Lock Dependency Graph
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
              onClick={() => setAddModalVisible(true)}
            >
              Add Dependency
            </Button>
          </Space>
        }
      >
        <Row gutter={16}>
          <Col xs={24} lg={18}>
            <div
              ref={canvasRef}
              style={{
                minHeight: 400,
                border: '1px solid #f0f0f0',
                borderRadius: 8,
                background: '#fafafa',
                overflow: 'auto',
              }}
            >
              {renderSVGGraph()}
            </div>
          </Col>
          <Col xs={24} lg={6}>
            <Card size="small" title="Legend">
              <Space direction="vertical" style={{ width: '100%' }}>
                <Space>
                  <div
                    style={{
                      width: 16,
                      height: 16,
                      borderRadius: '50%',
                      background: '#52c41a',
                    }}
                  />
                  <span>Held</span>
                </Space>
                <Space>
                  <div
                    style={{
                      width: 16,
                      height: 16,
                      borderRadius: '50%',
                      background: '#faad14',
                    }}
                  />
                  <span>Waiting</span>
                </Space>
                <Space>
                  <div
                    style={{
                      width: 16,
                      height: 16,
                      borderRadius: '50%',
                      background: '#bfbfbf',
                    }}
                  />
                  <span>Free</span>
                </Space>
              </Space>
            </Card>
            <Card size="small" title="Node States" style={{ marginTop: 16 }}>
              <List
                size="small"
                dataSource={nodes.filter((n) => n.state !== 'free')}
                locale={{ emptyText: 'No active locks' }}
                renderItem={(node) => (
                  <List.Item>
                    <Space>
                      <Tag color={getNodeColor(node.state)}>{node.state}</Tag>
                      <code style={{ fontSize: 12 }}>{node.id}</code>
                    </Space>
                    {node.clients.length > 0 && (
                      <div style={{ fontSize: 11, color: '#8c8c8c' }}>
                        Holders: {node.clients.join(', ')}
                      </div>
                    )}
                  </List.Item>
                )}
              />
            </Card>
          </Col>
        </Row>

        {renderDependenciesList()}
      </Card>

      <Modal
        title="Add Dependency"
        open={addModalVisible}
        onCancel={() => setAddModalVisible(false)}
        footer={null}
        destroyOnClose
      >
        <Form form={form} layout="vertical" onFinish={handleAddDependency}>
          <Title level={5}>Parent Lock (must be held first)</Title>
          <Row gutter={8}>
            <Col span={12}>
              <Form.Item
                name="parent_namespace"
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
                name="parent_name"
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

          <div style={{ textAlign: 'center', margin: '16px 0', color: '#8c8c8c' }}>
            ↓ depends on ↓
          </div>

          <Title level={5}>Child Lock (depends on parent)</Title>
          <Row gutter={8}>
            <Col span={12}>
              <Form.Item
                name="child_namespace"
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
                name="child_name"
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
            message="Note: The system will detect cycles and reject invalid dependencies."
            type="info"
            showIcon
            style={{ marginBottom: 16 }}
          />

          <Form.Item style={{ marginBottom: 0 }}>
            <Space style={{ float: 'right' }}>
              <Button onClick={() => setAddModalVisible(false)}>Cancel</Button>
              <Button type="primary" htmlType="submit">
                Add Dependency
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default DependencyGraph;
