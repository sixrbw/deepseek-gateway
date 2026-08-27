import React, { useState, useEffect } from 'react';
import { Form, Input, Button, Typography, Divider, message } from 'antd';
import { UserOutlined, LockOutlined } from '@ant-design/icons';
import { useNavigate, Link } from 'react-router-dom';
import api from '../api';

import AuthLayout from '../components/auth/AuthLayout';

const { Title, Text } = Typography;

const Login: React.FC = () => {
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();
  const [messageApi, contextHolder] = message.useMessage();
  const [ssoConfig, setSsoConfig] = useState<{ enabled: boolean; auth_url?: string; provider?: string }>({ enabled: false });
  const [registrationEnabled, setRegistrationEnabled] = useState(false);
  const [versionInfo, setVersionInfo] = useState<{ version?: string; build_time?: string; commit?: string }>({});

  useEffect(() => {
    if (localStorage.getItem('token')) {
      navigate('/chat');
    }
    // Check SSO and registration config
    api.get('/api/v1/auth/sso/config').then(res => {
      if (res.data.data && res.data.data.enabled) {
        setSsoConfig(res.data.data);
      }
    }).catch(() => {
      // Ignored
    });
    api.get('/api/v1/config/frontend').then(res => {
      setRegistrationEnabled(res.data.data?.registration_enabled || false);
      setVersionInfo({
        version: res.data.data?.version,
        build_time: res.data.data?.build_time,
        commit: res.data.data?.commit,
      });
    }).catch(() => { });
  }, [navigate]);

  const onFinish = async (values: { email: string; password: string }) => {
    setLoading(true);
    try {
      const res = await api.post('/api/v1/auth/login', values);
      localStorage.setItem('token', res.data.data.token);
      localStorage.setItem('user', JSON.stringify(res.data.data.user));
      messageApi.success('登录成功');
      navigate('/chat');
    } catch (err: any) {
      const status = err.response?.status;
      const errMsg = err.response?.data?.error;
      if (status === 403 && (errMsg === 'account disabled' || errMsg === 'user disabled')) {
        navigate('/register?pending=true');
      } else {
        messageApi.error(errMsg || '登录失败');
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <AuthLayout 
      title="Modelgate"
    >
      {contextHolder}
      <div style={{ maxWidth: '420px', width: '100%', margin: '0 auto' }}>
        <div style={{ marginBottom: '40px' }}>
          <Title level={2} style={{ color: '#1f2937', marginBottom: '8px', fontWeight: 600 }}>
            登录
          </Title>
        </div>

        {ssoConfig.enabled && (
          <div style={{ marginBottom: '24px' }}>
            <Button
              type="primary"
              size="large"
              block
              onClick={() => { window.location.href = ssoConfig.auth_url || '/api/v1/auth/sso/login'; }}
              style={{
                height: '48px',
                background: '#4f46e5',
                boxShadow: '0 4px 14px 0 rgba(79, 70, 229, 0.39)',
                fontSize: '16px',
                fontWeight: 500,
              }}
            >
              使用 {ssoConfig.provider || 'SSO'} 登录
            </Button>
            <Divider style={{ margin: '24px 0', color: '#9ca3af', fontSize: '13px' }} plain>
              或使用本地账号
            </Divider>
          </div>
        )}

        <Form
          name="login"
          onFinish={onFinish}
          layout="vertical"
          size="large"
          requiredMark={false}
        >
          <Form.Item
            name="email"
            rules={[
              { required: true, message: '请输入邮箱' },
              { type: 'email', message: '请输入有效的邮箱地址' }
            ]}
          >
            <Input
              prefix={<UserOutlined style={{ color: '#9ca3af' }} />}
              placeholder="name@company.com"
              style={{
                height: '48px',
                background: '#f9fafb',
                borderColor: '#e5e7eb',
              }}
            />
          </Form.Item>

          <Form.Item
            name="password"
            rules={[{ required: true, message: '请输入密码' }]}
          >
            <Input.Password
              prefix={<LockOutlined style={{ color: '#9ca3af' }} />}
              placeholder="密码"
              style={{
                height: '48px',
                background: '#f9fafb',
                borderColor: '#e5e7eb',
              }}
            />
          </Form.Item>

          <Form.Item style={{ marginTop: '32px', marginBottom: '16px' }}>
            <Button
              type="primary"
              htmlType="submit"
              block
              loading={loading}
              style={{
                height: '48px',
                background: ssoConfig.enabled ? '#fff' : 'linear-gradient(135deg, #667eea 0%, #764ba2 100%)',
                color: ssoConfig.enabled ? '#1f2937' : '#fff',
                borderColor: ssoConfig.enabled ? '#d1d5db' : 'transparent',
                boxShadow: ssoConfig.enabled ? '0 1px 2px 0 rgba(0, 0, 0, 0.05)' : '0 4px 15px rgba(102, 126, 234, 0.35)',
                fontSize: '16px',
                fontWeight: 500,
                border: ssoConfig.enabled ? undefined : 'none',
              }}
            >
              登 录
            </Button>
          </Form.Item>

          {registrationEnabled && (
            <div style={{ textAlign: 'center' }}>
              <Text style={{ color: '#6b7280' }}>
                还没有账号？ <Link to="/register" style={{ color: '#4f46e5', fontWeight: 500 }}>立即申请</Link>
              </Text>
            </div>
          )}
        </Form>

        {/* 底部版权 */}
        <div style={{
          position: 'absolute',
          bottom: '24px',
          left: 0,
          color: '#bfbfbf',
          fontSize: '12px',
          textAlign: 'center',
          width: '100%',
        }}>
          <div>© {new Date().getFullYear()} Modelgate</div>
          <div style={{ marginTop: '4px', opacity: 0.8, fontSize: '11px' }}>
            版本: {versionInfo?.version || 'N/A'} ({versionInfo?.commit?.substring(0, 7) || 'N/A'}) | 编译时间: {versionInfo?.build_time || 'N/A'}
          </div>
        </div>
      </div>
    </AuthLayout>
  );
};

export default Login;
