import React, { useEffect, useState, useMemo } from 'react';
import { Button, message, DatePicker, Space } from 'antd';
import { SyncOutlined, DownloadOutlined } from '@ant-design/icons';
import type { Dayjs } from 'dayjs';
import api from '../../api';
import AccessLogsTable from '../../components/AccessLogsTable';

const { RangePicker } = DatePicker;

const AdminLogs: React.FC = () => {
  const [accessLogs, setAccessLogs] = useState<any[]>([]);
  const [users, setUsers] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [exportRange, setExportRange] = useState<[Dayjs | null, Dayjs | null] | null>(null);

  const fetchData = async () => {
    setLoading(true);
    try {
      const [logsRes, usersRes] = await Promise.all([
        api.get('/api/v1/admin/access-logs?detailed=true&limit=50'),
        api.get('/api/v1/admin/users?page_size=1000') // Fetch a large enough page to get all users roughly
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
    users.forEach(u => {
      map[u.id] = u.name || u.email || u.id;
    });
    return map;
  }, [users]);

  const handleExport = async () => {
    if (!exportRange || !exportRange[0] || !exportRange[1]) {
      message.warning('请先选择导出的日期范围');
      return;
    }
    const startDate = exportRange[0].format('YYYY-MM-DD');
    const endDate = exportRange[1].format('YYYY-MM-DD');
    setExporting(true);
    try {
      const response = await api.get('/api/v1/admin/access-logs/export', {
        params: { start_date: startDate, end_date: endDate },
        responseType: 'blob',
      });
      const url = window.URL.createObjectURL(new Blob([response.data]));
      const link = document.createElement('a');
      link.href = url;
      link.setAttribute('download', `token-logs-${startDate}-${endDate}.zip`);
      document.body.appendChild(link);
      link.click();
      link.parentNode?.removeChild(link);
      window.URL.revokeObjectURL(url);
    } catch (err) {
      console.error('Export failed:', err);
      message.error('导出失败，请稍后重试');
    } finally {
      setExporting(false);
    }
  };

  return (
    <>
      <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', gap: 8 }}>
        <Space size="small">
          <span>导出日期范围：</span>
          <RangePicker
            onChange={(val) => setExportRange(val as [Dayjs | null, Dayjs | null] | null)}
            format="YYYY-MM-DD"
            allowClear
          />
        </Space>
        <Space size="small">
          <Button
            icon={<DownloadOutlined />}
            onClick={handleExport}
            loading={exporting}
            disabled={!exportRange || !exportRange[0] || !exportRange[1]}
          >
            导出（按 Token）
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
