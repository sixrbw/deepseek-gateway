import React, { useEffect, useState } from 'react';
import { Table, Button, Tag, message, Modal, Form, Input, Space, Popconfirm, Tooltip, Switch, Select } from 'antd';
import { PlusOutlined, EditOutlined, DeleteOutlined } from '@ant-design/icons';
import api from '../../api';
import type { User, UserFormValues, Policy } from './types';


const UserTab: React.FC = () => {
  const [users, setUsers] = useState<User[]>([]);
  const [policies, setPolicies] = useState<Policy[]>([]);
  const [loading, setLoading] = useState(false);

  // Pagination and sorting states
  const [userPagination, setUserPagination] = useState({ current: 1, pageSize: 20, total: 0 });
  const [userSort, setUserSort] = useState({ field: 'created_at', order: 'desc' });

  // Modal states
  const [userModalVisible, setUserModalVisible] = useState(false);
  const [userModalTitle, setUserModalTitle] = useState('创建用户');
  const [editingUser, setEditingUser] = useState<User | null>(null);
  const [userForm] = Form.useForm();

  const [messageApi, contextHolder] = message.useMessage();

  const fetchPolicies = async () => {
    try {
      const res = await api.get('/api/v1/admin/policies');
      setPolicies(res.data.data || []);
    } catch {
      messageApi.error('获取策略列表失败');
    }
  };

  const fetchUsers = async (
    page = userPagination.current,
    pageSize = userPagination.pageSize,
    sortBy = userSort.field,
    sortOrder = userSort.order
  ) => {
    setLoading(true);
    try {
      const res = await api.get('/api/v1/admin/users', {
        params: { page, page_size: pageSize, sort_by: sortBy, sort_order: sortOrder }
      });
      setUsers(res.data.data || []);
      setUserPagination(prev => ({ ...prev, current: res.data.page, total: res.data.total }));
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '获取用户列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchUsers();
    fetchPolicies();
  }, []);

  const handleCreateUser = () => {
    setEditingUser(null);
    setUserModalTitle('创建用户');
    userForm.resetFields();
    userForm.setFieldsValue({ enabled: true, role: 'user', quota_policies: ['default'] });
    setUserModalVisible(true);
  };

  const handleEditUser = (user: User) => {
    setEditingUser(user);
    setUserModalTitle('编辑用户');
    userForm.setFieldsValue({
      id: user.id,
      email: user.email,
      name: user.name,
      role: user.role,
      department: user.department,
      quota_policies: user.quota_policies?.length ? user.quota_policies : (user.quota_policy ? [user.quota_policy] : ['default']),
      enabled: user.enabled,
    });
    setUserModalVisible(true);
  };

  const handleDeleteUser = async (user: User) => {
    try {
      await api.delete(`/api/v1/admin/users/${user.id}`);
      messageApi.success('用户删除成功');
      fetchUsers();
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '删除失败');
    }
  };

  const handleToggleUserEnabled = async (user: User) => {
    try {
      await api.put(`/api/v1/admin/users/${user.id}`, {
        ...user,
        enabled: !user.enabled,
      });
      messageApi.success(user.enabled ? '用户已禁用' : '用户已启用');
      fetchUsers();
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '操作失败');
    }
  };

  const handleUserSubmit = async (values: UserFormValues) => {
    try {
      if (editingUser) {
        await api.put(`/api/v1/admin/users/${editingUser.id}`, {
          name: values.name,
          role: values.role,
          department: values.department,
          quota_policy: values.quota_policies[0] || 'default',
          quota_policies: values.quota_policies,
          enabled: values.enabled,
        });
        messageApi.success('用户更新成功');
      } else {
        await api.post('/api/v1/admin/users', {
          id: values.id,
          email: values.email,
          password: values.password,
          name: values.name,
          role: values.role,
          department: values.department,
          quota_policy: values.quota_policies[0] || 'default',
          quota_policies: values.quota_policies,
          enabled: values.enabled,
        });
        messageApi.success('用户创建成功');
      }
      setUserModalVisible(false);
      fetchUsers();
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '操作失败');
    }
  };

  const userColumns = [
    { title: '邮箱', dataIndex: 'email', sorter: true },
    { title: '姓名', dataIndex: 'name' },
    { title: '角色', dataIndex: 'role', render: (role: string) => <Tag color={role === 'admin' ? 'red' : 'default'}>{role}</Tag> },
    { title: '部门', dataIndex: 'department' },
    {
      title: '配额策略',
      dataIndex: 'quota_policies',
      sorter: true,
      render: (_: string[], record: User) => {
        const policies = record.quota_policies?.length ? record.quota_policies : (record.quota_policy ? [record.quota_policy] : []);
        return policies.length ? policies.map(p => <Tag key={p} color="blue">{p}</Tag>) : '-';
      },
    },
    {
      title: '启用状态',
      dataIndex: 'enabled',
      sorter: true,
      render: (enabled: boolean, record: User) => (
        <Space size="small">
          <Switch
            checked={enabled}
            onChange={() => handleToggleUserEnabled(record)}
            size="small"
          />
          {!enabled && !record.last_login_at && (
            <Tag color="orange">待审核</Tag>
          )}
        </Space>
      ),
    },
    {
      title: '最后登录',
      dataIndex: 'last_login_at',
      sorter: true,
      render: (time: string) => time ? new Date(time).toLocaleString() : '-',
    },
    {
      title: '操作',
      render: (_: unknown, record: User) => (
        <Space>
          <Tooltip title="编辑">
            <Button
              type="link"
              size="small"
              icon={<EditOutlined />}
              onClick={() => handleEditUser(record)}
            />
          </Tooltip>
          <Popconfirm
            title="确认删除"
            description={`删除用户 "${record.name || record.email}"，确定要继续吗？`}
            onConfirm={() => handleDeleteUser(record)}
            okText="删除"
            cancelText="取消"
            okButtonProps={{ danger: true }}
          >
            <Tooltip title="删除">
              <Button type="link" danger size="small" icon={<DeleteOutlined />} />
            </Tooltip>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      {contextHolder}
      <div style={{ marginBottom: 16 }}>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={handleCreateUser}
        >
          创建用户
        </Button>
      </div>
      <Table
        dataSource={users}
        columns={userColumns}
        rowKey="id"
        loading={loading}
        pagination={{
          current: userPagination.current,
          pageSize: userPagination.pageSize,
          total: userPagination.total,
          showSizeChanger: true,
          pageSizeOptions: ['20', '30', '50'],
          showTotal: (total) => `共 ${total} 个用户`,
        }}
        onChange={(pagination, _filters, sorter) => {
          const s = Array.isArray(sorter) ? sorter[0] : sorter;
          const newPage = pagination.current || 1;
          const newPageSize = pagination.pageSize || 20;
          const newSortField = (s?.field as string) || 'created_at';
          const newSortOrder = s?.order === 'ascend' ? 'asc' : 'desc';
          setUserPagination(prev => ({ ...prev, current: newPage, pageSize: newPageSize }));
          setUserSort({ field: newSortField, order: newSortOrder });
          fetchUsers(newPage, newPageSize, newSortField, newSortOrder);
        }}
      />

      {/* User Create/Edit Modal */}
      <Modal
        title={userModalTitle}
        open={userModalVisible}
        onCancel={() => setUserModalVisible(false)}
        onOk={() => userForm.submit()}
        okText={editingUser ? '保存' : '创建'}
        width={600}
        destroyOnClose
      >
        <Form
          form={userForm}
          onFinish={handleUserSubmit}
          layout="horizontal"
          labelCol={{ span: 6 }}
          wrapperCol={{ span: 18 }}
          style={{ marginTop: 24 }}
        >
          <Form.Item
            name="id"
            label="用户ID"
            rules={[{ required: !editingUser, message: '请输入用户ID' }]}
            extra="唯一标识，如：zhangsan, lisi"
            hidden={!!editingUser}
          >
            <Input disabled={!!editingUser} placeholder="如：zhangsan" />
          </Form.Item>

          <Form.Item
            name="email"
            label="邮箱"
            rules={[
              { required: true, message: '请输入邮箱' },
              { type: 'email', message: '请输入有效的邮箱地址' },
            ]}
          >
            <Input disabled={!!editingUser} placeholder="如：user@example.com" />
          </Form.Item>

          {!editingUser && (
            <Form.Item
              name="password"
              label="密码"
              rules={[{ required: !editingUser, message: '请输入密码' }]}
              extra="初始密码，创建后建议用户修改"
            >
              <Input.Password placeholder="请输入初始密码" />
            </Form.Item>
          )}

          <Form.Item
            name="name"
            label="姓名"
            rules={[{ required: true, message: '请输入姓名' }]}
          >
            <Input placeholder="如：张三" />
          </Form.Item>

          <Form.Item
            name="role"
            label="角色"
            rules={[{ required: true, message: '请选择角色' }]}
          >
            <Select placeholder="请选择角色">
              <Select.Option value="admin">管理员</Select.Option>
              <Select.Option value="manager">经理</Select.Option>
              <Select.Option value="user">普通用户</Select.Option>
            </Select>
          </Form.Item>

          <Form.Item
            name="department"
            label="部门"
          >
            <Input placeholder="如：技术部（可选）" />
          </Form.Item>

          <Form.Item
            name="quota_policies"
            label="配额策略"
            rules={[{ required: true, message: '请选择至少一个配额策略' }]}
            extra="每个模型只能属于一个策略，含通配符(*)的策略不能与其他策略同时绑定"
          >
            <Select mode="multiple" placeholder="请选择配额策略">
              {policies.map(policy => (
                <Select.Option key={policy.name} value={policy.name}>{policy.name}</Select.Option>
              ))}
            </Select>
          </Form.Item>

          <Form.Item
            name="enabled"
            label="启用状态"
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default UserTab;
