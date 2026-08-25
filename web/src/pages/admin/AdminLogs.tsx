import React, { useEffect, useMemo, useState } from 'react';
import { Button, Input, message, Space } from 'antd';
import { SyncOutlined, DownloadOutlined } from '@ant-design/icons';
import api from '../../api';
import AccessLogsTable from '../../components/AccessLogsTable';

const formatLocalDate = (date: Date) => {
  const year = date.getFullYear();
  const month = `${date.getMonth() + 1}`.padStart(2, '0');
  const day = `${date.getDate()}`.padStart(2, '0');
  return `${year}-${month}-${day}`;
};

const downloadBlob = (blob: Blob, filename: string) => {
  const url = window.URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.setAttribute('download', filename);
  document.body.appendChild(link);
  link.click();
  link.parentNode?.removeChild(link);
  window.URL.revokeObjectURL(url);
};

const AdminLogs: React.FC = () => {
  const [accessLogs, setAccessLogs] = useState<any[]>([]);
  const [users, setUsers] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [exportingLogs, setExportingLogs] = useState(false);
  const [exportingUsage, setExportingUsage] = useState(false);
  const [startDate, setStartDate] = useState(() => {
    const date = new Date();
    date.setDate(date.getDate() - 6);
    return formatLocalDate(date);
  });
  const [endDate, setEndDate] = useState(() => formatLocalDate(new Date()));

  const fetchData = async () => {
    setLoading(true);
    try {
      const [logsRes, usersRes] = await Promise.all([
        api.get('/api/v1/admin/access-logs?detailed=true&limit=50'),
        api.get('/api/v1/admin/users?page_size=1000'),
      ]);
      setAccessLogs(logsRes.data.data || []);
      setUsers(usersRes.data.data || []);
    } catch (err) {
      console.error('Failed to fetch admin access logs:', err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, []);

  const userMap = useMemo(() => {
    const map: Record<string, string> = {};
    users.forEach((u) => {
      map[u.id] = u.name || u.email || u.id;
    });
    return map;
  }, [users]);

  const validateDateRange = () => {
    if (!startDate || !endDate) {
      message.warning('请选择开始和结束日期');
      return false;
    }
    if (startDate > endDate) {
      message.warning('结束日期不能早于开始日期');
      return false;
    }
    return true;
  };

  const handleExportLogs = async () => {
    setExportingLogs(true);
    try {
      const response = await api.get('/api/v1/admin/access-logs/export', {
        responseType: 'blob',
      });
      const dateStr = new Date().toISOString().slice(0, 10).replace(/-/g, '');
      downloadBlob(new Blob([response.data]), `token-logs-${dateStr}.zip`);
    } catch (err) {
      console.error('Export failed:', err);
      message.error('导出失败，请稍后重试');
    } finally {
      setExportingLogs(false);
    }
  };

  const handleExportTokenUsage = async () => {
    if (!validateDateRange()) return;
    setExportingUsage(true);
    try {
      const response = await api.get('/api/v1/admin/token-usage/export', {
        params: {
          start_date: startDate,
          end_date: endDate,
        },
        responseType: 'blob',
      });
      downloadBlob(new Blob([response.data]), `token-usage-${startDate}-to-${endDate}.csv`);
    } catch (err) {
      console.error('Token usage export failed:', err);
      message.error('导出 Token 使用失败，请稍后重试');
    } finally {
      setExportingUsage(false);
    }
  };

  return (
    <>
      <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', gap: 12, flexWrap: 'wrap' }}>
        <Space wrap>
          <Input
            type="date"
            value={startDate}
            onChange={(e) => setStartDate(e.target.value)}
            style={{ width: 160 }}
          />
          <Input
            type="date"
            value={endDate}
            onChange={(e) => setEndDate(e.target.value)}
            style={{ width: 160 }}
          />
          <Button
            icon={<DownloadOutlined />}
            onClick={handleExportTokenUsage}
            loading={exportingUsage}
            type="primary"
          >
            导出 Token 使用
          </Button>
        </Space>
        <Space wrap>
          <Button
            icon={<DownloadOutlined />}
            onClick={handleExportLogs}
            loading={exportingLogs}
          >
            导出访问明细（最近记录）
          </Button>
          <Button icon={<SyncOutlined />} onClick={fetchData} loading={loading}>
            刷新
          </Button>
        </Space>
      </div>
      <AccessLogsTable
        logs={accessLogs}
        loading={loading}
        isAdmin={true}
        userMap={userMap}
        scroll={{ y: 600 }}
      />
    </>
  );
};

export default AdminLogs;
