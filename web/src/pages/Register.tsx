import React, { useState } from 'react';
import { Form, Input, Button, Typography, message, Result } from 'antd';
import { UserOutlined, LockOutlined, MailOutlined } from '@ant-design/icons';
import { useNavigate, Link, useSearchParams } from 'react-router-dom';
import api from '../api';
import AuthLayout from '../components/auth/AuthLayout';

const { Title, Text } = Typography;

const Register: React.FC = () => {
  const [searchParams] = useSearchParams();
  const [loading, setLoading] = useState(false);
  const [registered, setRegistered] = useState(searchParams.get('pending') === 'true');
  const navigate = useNavigate();
  const [messageApi, contextHolder] = message.useMessage();

  const onFinish = async (values: any) => {
    setLoading(true);
    try {
      await api.post('/api/v1/auth/register', {
        email: values.email,
        password: values.password,
        name: values.name,
      });
      setRegistered(true);
    } catch (err: any) {
      const errMsg = err.response?.data?.error;
      if (errMsg === 'email already exists') {
        messageApi.error('该邮箱已被注册');
      } else {
        messageApi.error(errMsg || '注册失败');
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <AuthLayout
      title="Modelgate"
      description="注册账号后需管理员审核"
    >
      {contextHolder}
      
      {/* 注册成功提示页 */}
      {registered ? (
        <div style={{ maxWidth: '420px', width: '100%', margin: '0 auto' }}>
          <Result
            status="success"
            title="申请提交成功"
            subTitle="您的注册申请已提交，请等待管理员审核。审核通过后即可使用此账号登录。"
            extra={[
              <Button type="primary" key="login" onClick={() => navigate('/login')} size="large">
                返回登录页
              </Button>
            ]}
          />
        </div>
      ) : (
        <div style={{ maxWidth: '420px', width: '100%', margin: '0 auto' }}>
          <div style={{ marginBottom: '40px' }}>
            <Title level={2} style={{ color: '#1f2937', marginBottom: '8px', fontWeight: 600 }}>
              申请账号
            </Title>
            <Text style={{ color: '#6b7280', fontSize: '15px' }}>
              请填写真实有效的信息以便管理员审核
            </Text>
          </div>

          <Form
            name="register"
            onFinish={onFinish}
            layout="vertical"
            size="large"
            requiredMark={false}
          >
            <Form.Item
              name="name"
              rules={[{ required: true, message: '请输入姓名/昵称' }]}
            >
              <Input
                prefix={<UserOutlined style={{ color: '#9ca3af' }} />}
                placeholder="姓名 / 昵称"
                style={{ height: '48px', background: '#f9fafb', borderColor: '#e5e7eb' }}
              />
            </Form.Item>

            <Form.Item
              name="email"
              rules={[
                { required: true, message: '请输入邮箱' },
                { type: 'email', message: '请输入有效的邮箱地址' }
              ]}
            >
              <Input
                prefix={<MailOutlined style={{ color: '#9ca3af' }} />}
                placeholder="企业邮箱 (用于登录和接收通知)"
                style={{ height: '48px', background: '#f9fafb', borderColor: '#e5e7eb' }}
              />
            </Form.Item>

            <Form.Item
              name="password"
              rules={[
                { required: true, message: '请输入密码' },
                { min: 6, message: '密码长度至少6位' }
              ]}
            >
              <Input.Password
                prefix={<LockOutlined style={{ color: '#9ca3af' }} />}
                placeholder="设置密码 (至少6位)"
                style={{ height: '48px', background: '#f9fafb', borderColor: '#e5e7eb' }}
              />
            </Form.Item>

            <Form.Item
              name="confirmPassword"
              dependencies={['password']}
              rules={[
                { required: true, message: '请确认密码' },
                ({ getFieldValue }) => ({
                  validator(_, value) {
                    if (!value || getFieldValue('password') === value) {
                      return Promise.resolve();
                    }
                    return Promise.reject(new Error('两次输入的密码不一致'));
                  },
                }),
              ]}
            >
              <Input.Password
                prefix={<LockOutlined style={{ color: '#9ca3af' }} />}
                placeholder="确认密码"
                style={{ height: '48px', background: '#f9fafb', borderColor: '#e5e7eb' }}
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
                  background: '#4f46e5',
                  boxShadow: '0 4px 14px 0 rgba(79, 70, 229, 0.39)',
                  fontSize: '16px',
                  fontWeight: 500,
                }}
              >
                提交申请
              </Button>
            </Form.Item>

            <div style={{ textAlign: 'center' }}>
              <Text style={{ color: '#6b7280' }}>
                已有账号？ <Link to="/login" style={{ color: '#4f46e5', fontWeight: 500 }}>直接登录</Link>
              </Text>
            </div>
          </Form>
        </div>
      )}
    </AuthLayout>
  );
};

export default Register;
