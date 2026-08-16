import React, { useState, useEffect } from 'react';
import { Layout, Menu, Badge, ConfigProvider, theme } from 'antd';
import {
  DashboardOutlined,
  LockOutlined,
  ClusterOutlined,
  AlertOutlined,
  HistoryOutlined,
  SettingOutlined,
  ForkOutlined,
  TeamOutlined,
  SafetyOutlined,
  BarChartOutlined,
  FileTextOutlined,
} from '@ant-design/icons';
import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router-dom';
import Dashboard from './pages/Dashboard';
import LockList from './pages/LockList';
import LockDetail from './pages/LockDetail';
import Cluster from './pages/Cluster';
import Alerts from './pages/Alerts';
import AdminOperations from './pages/AdminOperations';
import DependencyGraph from './pages/DependencyGraph';
import LockGroups from './pages/LockGroups';
import RateLimitRules from './pages/RateLimitRules';
import RateLimitMonitor from './pages/RateLimitMonitor';
import RateLimitEvents from './pages/RateLimitEvents';
import { listAlerts } from './api/client';
import { Alert } from './types';

const { Header, Sider, Content } = Layout;

const AppContent: React.FC = () => {
  const location = useLocation();
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [collapsed, setCollapsed] = useState(false);

  useEffect(() => {
    const fetchAlerts = async () => {
      try {
        const data = await listAlerts();
        setAlerts(data);
      } catch (e) {
        // Ignore
      }
    };
    fetchAlerts();
    const interval = setInterval(fetchAlerts, 10000);
    return () => clearInterval(interval);
  }, []);

  const menuItems = [
    {
      key: '/',
      icon: <DashboardOutlined />,
      label: <Link to="/">Dashboard</Link>,
    },
    {
      key: '/locks',
      icon: <LockOutlined />,
      label: <Link to="/locks">Locks</Link>,
    },
    {
      key: '/dependencies',
      icon: <ForkOutlined />,
      label: <Link to="/dependencies">Dependency Graph</Link>,
    },
    {
      key: '/groups',
      icon: <TeamOutlined />,
      label: <Link to="/groups">Lock Groups</Link>,
    },
    {
      key: '/cluster',
      icon: <ClusterOutlined />,
      label: <Link to="/cluster">Cluster</Link>,
    },
    {
      key: '/alerts',
      icon: <Badge count={alerts.length} size="small"><AlertOutlined /></Badge>,
      label: <Link to="/alerts">Alerts</Link>,
    },
    {
      key: '/operations',
      icon: <HistoryOutlined />,
      label: <Link to="/operations">Operations</Link>,
    },
    {
      key: '/ratelimit-rules',
      icon: <SafetyOutlined />,
      label: <Link to="/ratelimit-rules">Rate Limit</Link>,
    },
    {
      key: '/ratelimit-monitor',
      icon: <BarChartOutlined />,
      label: <Link to="/ratelimit-monitor">Rate Monitor</Link>,
    },
    {
      key: '/ratelimit-events',
      icon: <FileTextOutlined />,
      label: <Link to="/ratelimit-events">Rate Events</Link>,
    },
  ];

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider collapsible collapsed={collapsed} onCollapse={setCollapsed}
        theme="light"
        style={{
          boxShadow: '2px 0 8px rgba(0,0,0,0.05)',
        }}>
        <div style={{
          height: 64,
          margin: 0,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          fontSize: collapsed ? 18 : 20,
          fontWeight: 'bold',
          color: '#1677ff',
          background: 'linear-gradient(135deg, #e6f4ff 0%, #bae0ff 100%)',
        }}>
          {collapsed ? 'DL' : 'DistLock'}
        </div>
        <Menu
          mode="inline"
          selectedKeys={[location.pathname]}
          items={menuItems}
          style={{ borderRight: 'none' }}
        />
      </Sider>
      <Layout>
        <Header style={{
          background: '#fff',
          padding: '0 24px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          boxShadow: '0 2px 8px rgba(0,0,0,0.05)',
        }}>
          <h2 style={{ margin: 0, color: '#262626' }}>
            {location.pathname === '/' && 'Dashboard Overview'}
            {location.pathname === '/locks' && 'Lock Management'}
            {location.pathname.startsWith('/locks/') && 'Lock Detail'}
            {location.pathname === '/dependencies' && 'Lock Dependency Graph'}
            {location.pathname === '/groups' && 'Lock Groups'}
            {location.pathname === '/cluster' && 'Cluster Status'}
            {location.pathname === '/alerts' && 'Alert Center'}
            {location.pathname === '/operations' && 'Admin Operations'}
            {location.pathname === '/ratelimit-rules' && 'Rate Limit Rules'}
            {location.pathname === '/ratelimit-monitor' && 'Rate Limit Monitor'}
            {location.pathname === '/ratelimit-events' && 'Rate Limit Events'}
          </h2>
          <div style={{ color: '#8c8c8c', fontSize: 14 }}>
            <SettingOutlined style={{ marginRight: 8 }} />
            Distributed Lock Management
          </div>
        </Header>
        <Content style={{ padding: '24px', background: '#f5f5f5' }}>
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/locks" element={<LockList />} />
            <Route path="/locks/:namespace/:name" element={<LockDetail />} />
            <Route path="/dependencies" element={<DependencyGraph />} />
            <Route path="/groups" element={<LockGroups />} />
            <Route path="/cluster" element={<Cluster />} />
            <Route path="/alerts" element={<Alerts />} />
            <Route path="/operations" element={<AdminOperations />} />
            <Route path="/ratelimit-rules" element={<RateLimitRules />} />
            <Route path="/ratelimit-monitor" element={<RateLimitMonitor />} />
            <Route path="/ratelimit-events" element={<RateLimitEvents />} />
          </Routes>
        </Content>
      </Layout>
    </Layout>
  );
};

const App: React.FC = () => {
  return (
    <ConfigProvider
      theme={{
        algorithm: theme.defaultAlgorithm,
        token: {
          colorPrimary: '#1677ff',
          borderRadius: 8,
        },
      }}>
      <BrowserRouter>
        <AppContent />
      </BrowserRouter>
    </ConfigProvider>
  );
};

export default App;
