import React, { useState } from 'react';
import { Table, Tag, Button, Modal, Descriptions, Tooltip as AntTooltip, Card } from 'antd';
import { EyeOutlined } from '@ant-design/icons';

interface AccessLogsTableProps {
  logs: any[];
  loading: boolean;
  isAdmin?: boolean;
  userMap?: Record<string, string>;
  scroll?: { y: number };
}

const AccessLogsTable: React.FC<AccessLogsTableProps> = ({
  logs,
  loading,
  isAdmin = false,
  userMap = {},
  scroll = { y: 400 },
}) => {
  const [detailModalVisible, setDetailModalVisible] = useState(false);
  const [selectedLog, setSelectedLog] = useState<any>(null);

  // 工具函数
  const formatBytes = (bytes: number): string => {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  };

  const formatDuration = (ms: number): string => {
    if (ms == null) return '-';
    if (ms < 1000) return `${ms}ms`;
    if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
    const minutes = Math.floor(ms / 60000);
    const seconds = ((ms % 60000) / 1000).toFixed(1);
    return seconds === '0.0' ? `${minutes}m` : `${minutes}m ${seconds}s`;
  };

  const getStatusColor = (status: number): string => {
    if (status >= 200 && status < 300) return 'success';
    if (status >= 400) return 'error';
    return 'warning';
  };

  const formatJSON = (jsonString: string): string => {
    if (!jsonString) return '';
    try {
      const parsed = JSON.parse(jsonString);
      return JSON.stringify(parsed, null, 2);
    } catch {
      return jsonString;
    }
  };

  const escapeHtml = (unsafe: string): string => {
    return unsafe
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
  };

  const baseColumns = [
    {
      title: '时间',
      dataIndex: 'timestamp',
      key: 'timestamp',
      width: 160,
      render: (timestamp: string) => {
        const date = new Date(timestamp);
        return date.toLocaleString('zh-CN');
      },
    },
    // User column inserted here conditionally later
    {
      title: '来源/客户端',
      dataIndex: 'user_agent',
      key: 'user_agent',
      width: 130,
      ellipsis: true,
      render: (ua: string) => (
        <AntTooltip title={ua}>
          <span>{ua || '-'}</span>
        </AntTooltip>
      ),
    },
    {
      title: '协议',
      dataIndex: 'path',
      key: 'protocol',
      width: 100,
      render: (path: string) => {
        if (!path) return '-';
        if (path.includes('/v1/messages')) return <Tag color="purple">Anthropic</Tag>;
        return <Tag color="blue">OpenAI</Tag>;
      },
    },
    {
      title: '模型',
      dataIndex: 'model_name',
      key: 'model_name',
      width: 100,
      render: (model: string) => model ? <Tag color="default">{model}</Tag> : '-',
    },
    {
      title: '流量(字节)',
      key: 'traffic',
      width: 140,
      render: (_: any, record: any) => (
        <span>
          <span style={{ color: '#52c41a' }}>↑{formatBytes(record.request_bytes || 0)}</span>
          <span style={{ margin: '0 4px' }}>/</span>
          <span style={{ color: '#1890ff' }}>↓{formatBytes(record.response_bytes || 0)}</span>
        </span>
      ),
    },
    {
      title: 'Tokens',
      key: 'tokens',
      width: 140,
      render: (_: any, record: any) => (
        <span>
          <span style={{ color: '#fa8c16' }}>↑{record.input_tokens || 0}</span>
          <span style={{ margin: '0 4px' }}>/</span>
          <span style={{ color: '#722ed1' }}>↓{record.output_tokens || 0}</span>
        </span>
      ),
    },
    {
      title: 'IP地址',
      dataIndex: 'client_ip',
      key: 'client_ip',
      width: 130,
    },
    {
      title: '状态',
      dataIndex: 'status_code',
      key: 'status_code',
      width: 90,
      render: (status: number) => (
        <Tag color={getStatusColor(status)}>{status}</Tag>
      ),
    },
    {
      title: '耗时',
      dataIndex: 'duration_ms',
      key: 'duration_ms',
      width: 80,
      render: (ms: number) => formatDuration(ms),
    },
    {
      title: '操作',
      key: 'action',
      width: 90,
      render: (_: any, record: any) => (
        <Button
          type="link"
          size="small"
          icon={<EyeOutlined />}
          onClick={() => {
            setSelectedLog(record);
            setDetailModalVisible(true);
          }}
        >
          详情
        </Button>
      ),
    },
  ];

  const columns = isAdmin ? [
    baseColumns[0],
    {
      title: '用户',
      dataIndex: 'user_id',
      key: 'user_id',
      width: 120,
      render: (userId: string) => {
        const name = userMap[userId] || userId;
        return (
          <AntTooltip title={userId}>
            <span>{name}</span>
          </AntTooltip>
        );
      },
    },
    ...baseColumns.slice(1)
  ] : baseColumns;

  return (
    <>
      <Table
        dataSource={logs}
        columns={columns}
        rowKey={(record: any) => `${record.timestamp}-${record.path}-${record.user_id || record.client_ip}`}
        loading={loading}
        pagination={{
          defaultPageSize: 50,
          pageSizeOptions: ['20', '50', '100', '200'],
          showSizeChanger: true,
          showTotal: (total) => `共 ${total} 条`,
          position: ['bottomRight'],
        }}
        scroll={scroll}
        size="small"
      />

      {/* 详情弹窗 */}
      <Modal
        title="请求/响应详情"
        open={detailModalVisible}
        onCancel={() => setDetailModalVisible(false)}
        width={900}
        footer={null}
      >
        {selectedLog && (
          <div style={{ maxHeight: '70vh', overflow: 'auto' }}>
            <Card title="基础信息" size="small" style={{ marginBottom: 16 }}>
              <Descriptions column={2} size="small">
                {isAdmin && <Descriptions.Item label="User ID">{selectedLog.user_id}</Descriptions.Item>}
                {isAdmin && <Descriptions.Item label="User Name">{userMap[selectedLog.user_id] || selectedLog.user_id}</Descriptions.Item>}
                <Descriptions.Item label="Method">{selectedLog.method}</Descriptions.Item>
                <Descriptions.Item label="Path">{selectedLog.path}</Descriptions.Item>
                <Descriptions.Item label="Model">{selectedLog.model_name || '-'}</Descriptions.Item>
                <Descriptions.Item label="Client IP">{selectedLog.client_ip}</Descriptions.Item>
                <Descriptions.Item label="Status Code">
                  <Tag color={getStatusColor(selectedLog.status_code)}>
                    {selectedLog.status_code}
                  </Tag>
                </Descriptions.Item>
              </Descriptions>
            </Card>

            <Card title="请求信息" size="small" style={{ marginBottom: 16 }}>
              <Descriptions column={1} size="small">
                <Descriptions.Item label="User Agent" style={{ wordBreak: 'break-all' }}>
                  {selectedLog.user_agent}
                </Descriptions.Item>
                <Descriptions.Item label="Tokens" style={{ wordBreak: 'break-all' }}>
                  Input: {selectedLog.input_tokens || 0} / Output: {selectedLog.output_tokens || 0}
                </Descriptions.Item>
                <Descriptions.Item label="Headers">
                  <pre style={{
                    maxHeight: 150,
                    overflow: 'auto',
                    background: '#f5f5f5',
                    padding: 8,
                    borderRadius: 4,
                    fontSize: 12
                  }}>
                    {JSON.stringify(selectedLog.request_headers, null, 2)}
                  </pre>
                </Descriptions.Item>
                <Descriptions.Item label="Request Body">
                  <pre style={{
                    maxHeight: 300,
                    overflow: 'auto',
                    background: '#f5f5f5',
                    padding: 8,
                    borderRadius: 4,
                    fontSize: 12
                  }}>
                    {formatJSON(selectedLog.request_body)}
                  </pre>
                </Descriptions.Item>
              </Descriptions>
            </Card>

            <Card title="响应信息" size="small">
              <Descriptions column={1} size="small">
                <Descriptions.Item label="Response Bytes">
                  {formatBytes(selectedLog.response_bytes || 0)}
                </Descriptions.Item>
                <Descriptions.Item label="Headers">
                  <pre style={{
                    maxHeight: 150,
                    overflow: 'auto',
                    background: '#f5f5f5',
                    padding: 8,
                    borderRadius: 4,
                    fontSize: 12
                  }}>
                    {JSON.stringify(selectedLog.response_headers, null, 2)}
                  </pre>
                </Descriptions.Item>
                <Descriptions.Item label="Response Body">
                  <pre style={{
                    maxHeight: 400,
                    overflow: 'auto',
                    background: '#f5f5f5',
                    padding: 8,
                    borderRadius: 4,
                    fontSize: 12
                  }}>
                    {escapeHtml(formatJSON(selectedLog.response_body))}
                  </pre>
                </Descriptions.Item>
              </Descriptions>
            </Card>
          </div>
        )}
      </Modal>
    </>
  );
};

export default AccessLogsTable;
