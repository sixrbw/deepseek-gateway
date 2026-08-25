import React, { useEffect, useState } from 'react';
import { Table, Button, Tag, message, Modal, Form, Input, Space, Popconfirm, Tooltip, Switch, Drawer, InputNumber, Select } from 'antd';
import { PlusOutlined, EditOutlined, DeleteOutlined, SettingOutlined, ReloadOutlined, CheckCircleOutlined, CloseCircleOutlined, ArrowLeftOutlined } from '@ant-design/icons';
import api from '../../api';
import type { Model, Backend, ModelFormValues, BackendFormValues } from './types';


const ModelTab: React.FC = () => {
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(false);

  // Model modal states
  const [modelModalVisible, setModelModalVisible] = useState(false);
  const [modelModalTitle, setModelModalTitle] = useState('创建模型');
  const [editingModel, setEditingModel] = useState<Model | null>(null);
  const [modelForm] = Form.useForm();

  // Import gateway states
  const [importModalVisible, setImportModalVisible] = useState(false);
  const [importLoading, setImportLoading] = useState(false);
  const [importForm] = Form.useForm();

  // Backend drawer states
  const [backendDrawerVisible, setBackendDrawerVisible] = useState(false);
  const [selectedModelId, setSelectedModelId] = useState<string | null>(null);
  const [selectedModelName, setSelectedModelName] = useState<string>('');
  const [modelBackends, setModelBackends] = useState<Backend[]>([]);
  const [backendsLoading, setBackendsLoading] = useState(false);
  const [selectedBackendIds, setSelectedBackendIds] = useState<string[]>([]);
  const [sourceFilter, setSourceFilter] = useState<string | undefined>(undefined);

  // Backend modal states
  const [backendModalVisible, setBackendModalVisible] = useState(false);
  const [backendModalTitle, setBackendModalTitle] = useState('创建后端');
  const [editingBackend, setEditingBackend] = useState<Backend | null>(null);
  const [backendForm] = Form.useForm();

  const [messageApi, contextHolder] = message.useMessage();

  const fetchData = async () => {
    setLoading(true);
    try {
      const res = await api.get('/api/v1/admin/models');
      const modelsData = res.data.data || [];
      const modelsWithBackendCount = await Promise.all(
        modelsData.map(async (model: Model) => {
          try {
            const backendsRes = await api.get(`/api/v1/admin/models/${encodeURIComponent(model.id)}/backends`);
            const backendsList = backendsRes.data.data || [];
            return { ...model, backend_count: backendsList.length };
          } catch {
            return { ...model, backend_count: 0 };
          }
        })
      );
      setModels(modelsWithBackendCount);
    } catch {
      messageApi.error('获取数据失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, []);

  // Model management functions
  const handleImportGatewaySubmit = async (values: { prefix: string; base_url: string; api_key?: string; source_platform: string; source_group?: string }) => {
    setImportLoading(true);
    try {
      const res = await api.post('/api/v1/admin/models/import', values);
      messageApi.success(res.data.message || '导入成功');
      setImportModalVisible(false);
      importForm.resetFields();
      fetchData();
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '导入失败');
    } finally {
      setImportLoading(false);
    }
  };

  const handleCreateModel = () => {
    setEditingModel(null);
    setModelModalTitle('创建模型');
    modelForm.resetFields();
    modelForm.setFieldsValue({ enabled: true });
    setModelModalVisible(true);
  };

  const handleEditModel = (model: Model) => {
    setEditingModel(model);
    setModelModalTitle('编辑模型');
    modelForm.setFieldsValue({
      id: model.id,
      name: model.name,
      description: model.description,
      enabled: model.enabled,
      context_window: model.context_window,
      model_params: model.model_params ? JSON.stringify(model.model_params, null, 2) : '',
    });
    setModelModalVisible(true);
  };

  const handleDeleteModel = async (model: Model) => {
    try {
      await api.delete(`/api/v1/admin/models/${encodeURIComponent(model.id)}`);
      messageApi.success('模型删除成功');
      fetchData();
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '删除失败');
    }
  };

  const handleToggleModelEnabled = async (model: Model) => {
    try {
      await api.put(`/api/v1/admin/models/${encodeURIComponent(model.id)}`, {
        name: model.name,
        description: model.description,
        enabled: !model.enabled,
      });
      messageApi.success(model.enabled ? '模型已禁用' : '模型已启用');
      fetchData();
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '操作失败');
    }
  };

  const handleModelSubmit = async (values: ModelFormValues) => {
    try {
      let modelParams: any = {};
      if (values.model_params && values.model_params.trim() !== '') {
        try {
          modelParams = JSON.parse(values.model_params);
        } catch {
          messageApi.error('模型参数 JSON 格式错误');
          return;
        }
      }

      if (editingModel) {
        await api.put(`/api/v1/admin/models/${encodeURIComponent(editingModel.id)}`, {
          name: values.name,
          description: values.description,
          enabled: values.enabled,
          context_window: values.context_window || 0,
          model_params: modelParams,
        });
        messageApi.success('模型更新成功');
      } else {
        await api.post('/api/v1/admin/models', {
          id: values.id,
          name: values.name,
          description: values.description,
          enabled: values.enabled,
          context_window: values.context_window || 0,
          model_params: modelParams,
        });
        messageApi.success('模型创建成功');

        if (values.base_url) {
          try {
            await api.post(`/api/v1/admin/models/${encodeURIComponent(values.id)}/backends`, {
              id: `${values.id}-1`,
              base_url: values.base_url,
              api_key: values.api_key || '',
              weight: 1,
              enabled: true,
            });
          } catch {
            messageApi.warning('模型创建成功，但默认后端实例创建失败');
          }
        }
      }
      setModelModalVisible(false);
      fetchData();
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '操作失败');
    }
  };

  // Backend management functions
  const handleManageBackends = async (modelId: string, modelName: string) => {
    setSelectedModelId(modelId);
    setSelectedModelName(modelName);
    setSelectedBackendIds([]);
    setSourceFilter(undefined);
    setBackendDrawerVisible(true);
    await fetchModelBackends(modelId);
  };

  const fetchModelBackends = async (modelId: string) => {
    setBackendsLoading(true);
    try {
      const res = await api.get(`/api/v1/admin/models/${encodeURIComponent(modelId)}/backends`);
      setModelBackends(res.data.data || []);
      setSelectedBackendIds([]);
    } catch {
      messageApi.error('获取后端列表失败');
    } finally {
      setBackendsLoading(false);
    }
  };

  const handleCloseBackendDrawer = () => {
    setBackendDrawerVisible(false);
    setSelectedModelId(null);
    setSelectedModelName('');
    setModelBackends([]);
    setSelectedBackendIds([]);
    setSourceFilter(undefined);
  };

  const generateBackendId = () => {
    const safeModelId = selectedModelId || '';
    const existingIds = new Set(modelBackends.map(b => b.id));
    let seq = 1;
    while (existingIds.has(`${safeModelId}-${seq}`)) {
      seq++;
    }
    return `${safeModelId}-${seq}`;
  };

  const handleCreateBackend = () => {
    setEditingBackend(null);
    setBackendModalTitle('创建后端');
    backendForm.resetFields();
    backendForm.setFieldsValue({ id: generateBackendId(), weight: 1, enabled: true, max_concurrency: 0 });
    setBackendModalVisible(true);
  };

  const handleEditBackend = (backend: Backend) => {
    setEditingBackend(backend);
    setBackendModalTitle('编辑后端');
    backendForm.setFieldsValue({
      id: backend.id,
      base_url: backend.base_url,
      model_name: backend.model_name,
      source_platform: backend.source_platform,
      source_group: backend.source_group,
      weight: backend.weight,
      enabled: backend.enabled,
      max_concurrency: backend.max_concurrency ?? 0,
    });
    setBackendModalVisible(true);
  };

  const handleDeleteBackend = async (backend: Backend) => {
    if (!selectedModelId) return;
    try {
      await api.delete(`/api/v1/admin/models/${encodeURIComponent(selectedModelId)}/backends/${encodeURIComponent(backend.id)}`);
      messageApi.success('删除成功');
      fetchModelBackends(selectedModelId);
      fetchData();
    } catch {
      messageApi.error('删除失败');
    }
  };

  const handleBackendSubmit = async (values: BackendFormValues) => {
    if (!selectedModelId) return;
    try {
      if (editingBackend) {
        await api.put(`/api/v1/admin/models/${encodeURIComponent(selectedModelId)}/backends/${encodeURIComponent(values.id)}`, values);
        messageApi.success('更新成功');
      } else {
        await api.post(`/api/v1/admin/models/${encodeURIComponent(selectedModelId)}/backends`, values);
        messageApi.success('创建成功');
      }
      setBackendModalVisible(false);
      fetchModelBackends(selectedModelId);
      fetchData();
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '操作失败');
    }
  };

  const handleToggleBackendEnabled = async (backend: Backend) => {
    if (!selectedModelId) return;
    try {
      await api.put(`/api/v1/admin/models/${encodeURIComponent(selectedModelId)}/backends/${encodeURIComponent(backend.id)}`, {
        ...backend,
        enabled: !backend.enabled,
      });
      messageApi.success(backend.enabled ? '后端已禁用' : '后端已启用');
      fetchModelBackends(selectedModelId);
      fetchData();
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '操作失败');
    }
  };

  const handleBatchDeleteBackends = async () => {
    if (!selectedModelId || selectedBackendIds.length === 0) return;
    try {
      const response = await api.post(`/api/v1/admin/models/${encodeURIComponent(selectedModelId)}/backends/batch-delete`, {
        backend_ids: selectedBackendIds,
      });
      messageApi.success(response.data.message || '批量删除成功');
      setSelectedBackendIds([]);
      fetchModelBackends(selectedModelId);
      fetchData();
    } catch (err: unknown) {
      const error = err as { response?: { data?: { error?: string } } };
      messageApi.error(error.response?.data?.error || '批量删除失败');
    }
  };

  const importedBackends = modelBackends.filter((backend) => Boolean(backend.source_platform));
  const sourceOptions = Array.from(
    new Set(
      importedBackends.map((backend) => backend.source_group || backend.source_platform).filter(Boolean)
    )
  ).map((value) => ({ label: value as string, value: value as string }));
  const filteredModelBackends = sourceFilter
    ? modelBackends.filter((backend) => (backend.source_group || backend.source_platform) === sourceFilter)
    : modelBackends;

  const modelColumns = [
    { title: 'ID', dataIndex: 'id', ellipsis: true },
    { title: '名称', dataIndex: 'name' },
    { title: '描述', dataIndex: 'description', ellipsis: true },
    {
      title: '上下文长度',
      dataIndex: 'context_window',
      render: (v: number) => v ? <Tag color="purple">{v}</Tag> : <Tag>无限制</Tag>,
    },
    {
      title: '后端数量',
      dataIndex: 'backend_count',
      render: (count: number) => <Tag color="blue">{count || 0}</Tag>,
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      render: (enabled: boolean, record: Model) => (
        <Tag
          color={enabled ? 'green' : 'default'}
          style={{ cursor: 'pointer' }}
          onClick={() => handleToggleModelEnabled(record)}
        >
          {enabled ? '启用' : '禁用'}
        </Tag>
      ),
    },
    {
      title: '操作',
      render: (_: unknown, record: Model) => (
        <Space>
          <Tooltip title="编辑">
            <Button
              type="link"
              size="small"
              icon={<EditOutlined />}
              onClick={() => handleEditModel(record)}
            />
          </Tooltip>
          <Tooltip title="管理后端">
            <Button
              type="link"
              size="small"
              icon={<SettingOutlined />}
              onClick={() => handleManageBackends(record.id, record.name)}
            />
          </Tooltip>
          <Popconfirm
            title="确认删除"
            description={`删除模型 "${record.name}" 将同时删除其所有后端实例，确定要继续吗？`}
            onConfirm={() => handleDeleteModel(record)}
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

  const backendColumns = [
    { title: 'ID', dataIndex: 'id', key: 'id', ellipsis: true, width: 150 },
    {
      title: 'BaseURL',
      dataIndex: 'base_url',
      key: 'base_url',
      ellipsis: true,
      width: 350,
    },
    {
      title: '后端模型名',
      dataIndex: 'model_name',
      key: 'model_name',
      ellipsis: true,
      width: 150,
      render: (name: string) => name || '-',
    },
    {
      title: '来源平台',
      dataIndex: 'source_platform',
      key: 'source_platform',
      width: 140,
      render: (sourcePlatform?: string) => sourcePlatform ? <Tag color="geekblue">{sourcePlatform}</Tag> : '-',
    },
    {
      title: '来源分组',
      dataIndex: 'source_group',
      key: 'source_group',
      width: 140,
      render: (sourceGroup: string | undefined, record: Backend) => (sourceGroup || record.source_platform)
        ? <Tag color="cyan">{sourceGroup || record.source_platform}</Tag>
        : '-',
    },
    {
      title: '权重',
      dataIndex: 'weight',
      key: 'weight',
      width: 80,
      align: 'center' as const,
      render: (weight: number) => <Tag color="blue">{weight}</Tag>,
    },
    {
      title: '最大并发',
      dataIndex: 'max_concurrency',
      key: 'max_concurrency',
      width: 100,
      align: 'center' as const,
      render: (max: number) => max > 0 ? <Tag color="orange">{max}</Tag> : <Tag color="default">无限制</Tag>,
    },
    {
      title: '健康状态',
      dataIndex: 'healthy',
      key: 'healthy',
      width: 120,
      align: 'center' as const,
      render: (healthy: boolean, record: Backend) => {
        if (!record.enabled) return <Tag>已禁用</Tag>;
        return healthy ? (
          <Tag color="green" icon={<CheckCircleOutlined />}>健康</Tag>
        ) : (
          <Tag color="red" icon={<CloseCircleOutlined />}>不健康</Tag>
        );
      },
    },
    {
      title: '启用状态',
      dataIndex: 'enabled',
      key: 'enabled',
      width: 100,
      align: 'center' as const,
      render: (enabled: boolean, record: Backend) => (
        <Switch
          checked={enabled}
          onChange={() => handleToggleBackendEnabled(record)}
          size="small"
        />
      ),
    },
    {
      title: '操作',
      key: 'action',
      width: 100,
      align: 'center' as const,
      render: (_: unknown, record: Backend) => (
        <Space>
          <Tooltip title="编辑">
            <Button
              type="link"
              size="small"
              icon={<EditOutlined />}
              onClick={() => handleEditBackend(record)}
            />
          </Tooltip>
          <Popconfirm
            title="确认删除"
            description={`删除后端 "${record.name || record.id}"，确定要继续吗？`}
            onConfirm={() => handleDeleteBackend(record)}
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
        <Space>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={handleCreateModel}
          >
            创建模型
          </Button>
          <Button
            icon={<PlusOutlined />}
            onClick={() => {
              importForm.resetFields();
              setImportModalVisible(true);
            }}
          >
            从网关导入
          </Button>
        </Space>
      </div>
      <Table
        dataSource={models}
        columns={modelColumns}
        rowKey="id"
        loading={loading}
      />

      {/* Backend Management Drawer */}
      <Drawer
        title={
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <Button
              type="text"
              icon={<ArrowLeftOutlined />}
              onClick={handleCloseBackendDrawer}
              style={{ padding: '4px 8px', marginLeft: -8 }}
            />
            <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              <div style={{ fontSize: 17, fontWeight: 600, lineHeight: 1.4 }}>后端管理</div>
              <div style={{ color: '#8c8c8c', fontSize: 12, lineHeight: 1.4 }}>
                模型: {selectedModelName || selectedModelId}
              </div>
            </div>
          </div>
        }
        placement="right"
        width={1200}
        onClose={handleCloseBackendDrawer}
        open={backendDrawerVisible}
        styles={{ body: { padding: 24, paddingTop: 16 } }}
        closeIcon={null}
      >
        <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between' }}>
          <div>
            <p style={{ color: '#666', margin: 0 }}>
              管理模型 "{selectedModelName || selectedModelId}" 的后端实例，支持负载均衡配置
            </p>
          </div>
          <Space>
            <Select
              allowClear
              placeholder="按来源分组筛选"
              style={{ width: 220 }}
              value={sourceFilter}
              onChange={(value) => {
                setSourceFilter(value);
                setSelectedBackendIds([]);
              }}
              options={sourceOptions}
            />
            <Popconfirm
              title="确认批量删除"
              description={`将删除已选中的 ${selectedBackendIds.length} 个已导入后端实例，确定继续吗？`}
              onConfirm={handleBatchDeleteBackends}
              okText="删除"
              cancelText="取消"
              okButtonProps={{ danger: true }}
              disabled={selectedBackendIds.length === 0}
            >
              <Button danger disabled={selectedBackendIds.length === 0}>
                批量删除已导入项
              </Button>
            </Popconfirm>
            <Button
              icon={<ReloadOutlined />}
              onClick={() => selectedModelId && fetchModelBackends(selectedModelId)}
            >
              刷新
            </Button>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={handleCreateBackend}
            >
              创建后端
            </Button>
          </Space>
        </div>
        <Table
          dataSource={filteredModelBackends}
          columns={backendColumns}
          rowKey="id"
          loading={backendsLoading}
          rowSelection={{
            selectedRowKeys: selectedBackendIds,
            onChange: (keys) => setSelectedBackendIds(keys as string[]),
            getCheckboxProps: (record: Backend) => ({
              disabled: !record.source_platform,
            }),
          }}
        />
      </Drawer>

      {/* Backend Create/Edit Modal */}
      <Modal
        title={backendModalTitle}
        open={backendModalVisible}
        onCancel={() => setBackendModalVisible(false)}
        onOk={() => backendForm.submit()}
        okText={editingBackend ? '保存' : '创建'}
        width={600}
        destroyOnClose
      >
        <Form
          form={backendForm}
          onFinish={handleBackendSubmit}
          layout="horizontal"
          labelCol={{ span: 6 }}
          wrapperCol={{ span: 18 }}
          style={{ marginTop: 24 }}
        >
          <Form.Item
            name="id"
            label="后端ID"
            rules={[
              { required: true, message: '请输入后端ID' },
              { max: 128, message: '后端ID长度不能超过128个字符' },
              {
                pattern: /^[a-zA-Z0-9][a-zA-Z0-9._\-]*$/,
                message: '仅允许字母、数字、点(.)、连字符(-)和下划线(_)，且必须以字母或数字开头',
              },
            ]}
            extra="唯一标识，如：backend-1, aws-us-east-1"
          >
            <Input disabled={!!editingBackend} placeholder="如：backend-1" />
          </Form.Item>

          <Form.Item
            name="base_url"
            label="BaseURL"
            rules={[
              { required: true, message: '请输入BaseURL' },
              { type: 'url', message: '请输入有效的URL' },
            ]}
            extra="后端服务地址，如：http://localhost:8080"
          >
            <Input placeholder="如：http://localhost:8080" />
          </Form.Item>

          <Form.Item
            name="model_name"
            label="ModelName"
            extra="后端实际的模型名称（可选），不填则使用模型ID"
          >
            <Input placeholder="如：gpt-4-turbo" />
          </Form.Item>

          <Form.Item
            name="source_platform"
            label="来源平台"
            extra="用于标记该后端来自哪个平台（可选）"
          >
            <Input placeholder="如：openai, azure, google" />
          </Form.Item>

          <Form.Item
            name="source_group"
            label="来源分组"
            extra="用于同平台导入后端归组展示（可选）"
          >
            <Input placeholder="如：openai-prod" />
          </Form.Item>

          <Form.Item
            name="api_key"
            label="API Key"
            extra={editingBackend ? "留空表示不修改（安全考虑，不显示当前值）" : "后端服务的API密钥（可选）"}
          >
            <Input.Password placeholder="API Key（可选）" />
          </Form.Item>

          <Form.Item
            name="weight"
            label="权重"
            rules={[{ required: true, message: '请输入权重' }]}
            extra="负载均衡权重，数值越大分配越多请求（默认1）"
          >
            <InputNumber min={1} max={100} style={{ width: '100%' }} />
          </Form.Item>

          <Form.Item
            name="max_concurrency"
            label="最大并发"
            extra="该后端最大并发请求数，0 表示不限制（根据后端服务器承载能力设置）"
          >
            <InputNumber min={0} max={10000} style={{ width: '100%' }} placeholder="如：5（0=不限制）" />
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

      {/* Model Create/Edit Modal */}
      <Modal
        title={modelModalTitle}
        open={modelModalVisible}
        onCancel={() => setModelModalVisible(false)}
        onOk={() => modelForm.submit()}
        okText={editingModel ? '保存' : '创建'}
        width={600}
      >
        <Form
          form={modelForm}
          onFinish={handleModelSubmit}
          layout="horizontal"
          labelCol={{ span: 6 }}
          wrapperCol={{ span: 18 }}
          style={{ marginTop: 24 }}
        >
          <Form.Item
            name="id"
            label="模型ID"
            rules={[
              { required: true, message: '请输入模型ID' },
              { max: 128, message: '模型ID长度不能超过128个字符' },
              {
                pattern: /^[a-zA-Z0-9][a-zA-Z0-9._\-]*$/,
                message: '仅允许字母、数字、点(.)、连字符(-)和下划线(_)，且必须以字母或数字开头',
              },
            ]}
            extra="唯一标识，如：gpt-4, claude-3-opus"
          >
            <Input disabled={!!editingModel} placeholder="如：gpt-4" />
          </Form.Item>

          <Form.Item
            name="name"
            label="显示名称"
            rules={[{ required: true, message: '请输入显示名称' }]}
          >
            <Input placeholder="如：GPT-4" />
          </Form.Item>

          <Form.Item
            name="description"
            label="描述"
          >
            <Input.TextArea rows={3} placeholder="模型描述信息（可选）" />
          </Form.Item>

          <Form.Item
            name="enabled"
            label="启用状态"
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>

          <Form.Item
            name="context_window"
            label="上下文长度"
            extra="模型支持的最大上下文长度，留空或者设置为0表示不限制"
          >
            <InputNumber min={0} style={{ width: '100%' }} placeholder="如：128000" />
          </Form.Item>

          <Form.Item
            name="model_params"
            label="模型参数"
            extra='JSON 格式，如：{"enable_thinking": false}'
          >
            <Input.TextArea
              rows={4}
              placeholder={`{
  "enable_thinking": false,
  "max_tokens": 4096
}`}
            />
          </Form.Item>

          {!editingModel && (
            <>
              <Form.Item style={{ marginBottom: 8 }}>
                <div style={{ borderTop: '1px solid #f0f0f0', paddingTop: 16, color: '#8c8c8c', fontSize: 13 }}>
                  以下为可选项，填写后将同时创建一个后端实例
                </div>
              </Form.Item>
              <Form.Item
                name="base_url"
                label="BaseURL"
                rules={[
                  { type: 'url', message: '请输入有效的URL' },
                ]}
                extra="后端服务地址，如：https://api.openai.com"
              >
                <Input placeholder="如：https://api.openai.com" />
              </Form.Item>
              <Form.Item
                name="api_key"
                label="API Key"
                extra="后端服务的API密钥（可选）"
              >
                <Input.Password placeholder="API Key（可选）" />
              </Form.Item>
            </>
          )}
        </Form>
      </Modal>

      {/* Import Gateway Modal */}
      <Modal
        title="从网关批量导入模型"
        open={importModalVisible}
        onCancel={() => setImportModalVisible(false)}
        onOk={() => importForm.submit()}
        confirmLoading={importLoading}
        okText="导入"
        width={600}
      >
        <Form
          form={importForm}
          onFinish={handleImportGatewaySubmit}
          layout="vertical"
          style={{ marginTop: 24 }}
        >
          <Form.Item
            name="prefix"
            label="前缀 (Prefix)"
            rules={[
              { required: true, message: '请输入前缀' },
              { pattern: /^[a-zA-Z0-9]+$/, message: '前缀只能包含英文字母和数字' }
            ]}
            extra="用于生成后端ID（格式：前缀-模型名-序号），例如：google"
          >
            <Input placeholder="输入服务提供商前缀，如 google, azure, openai" />
          </Form.Item>

          <Form.Item
            name="source_platform"
            label="来源平台"
            rules={[
              { required: true, message: '请输入来源平台' },
            ]}
            extra="用于标记导入来源，例如：OpenAI、Azure、Google"
          >
            <Input placeholder="例如 openai" />
          </Form.Item>

          <Form.Item
            name="source_group"
            label="来源分组"
            extra="可选；留空时默认与来源平台相同"
          >
            <Input placeholder="例如 openai-prod" />
          </Form.Item>

          <Form.Item
            name="base_url"
            label="BaseURL"
            rules={[
              { required: true, message: '请输入上游网关 BaseURL' },
              { type: 'url', message: '请输入有效的 URL' }
            ]}
            extra="上游网关服务地址，系统会调用 {BaseURL}/v1/models"
          >
            <Input placeholder="例如 https://api.openai.com" />
          </Form.Item>

          <Form.Item
            name="api_key"
            label="API Key"
            extra="访问上游网关所需的 API Key（可选）"
          >
            <Input.Password placeholder="API Key" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default ModelTab;
