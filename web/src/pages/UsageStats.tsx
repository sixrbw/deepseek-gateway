import React, { useEffect, useState } from 'react';
import { Card, Descriptions, Tag, Statistic, Row, Col, Progress, DatePicker, Button, Space, message } from 'antd';
import { DownloadOutlined } from '@ant-design/icons';
import type { Dayjs } from 'dayjs';
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts';
import api from '../api';
import AccessLogsTable from '../components/AccessLogsTable';

const { RangePicker } = DatePicker;

const UsageStats: React.FC = () => {
  const [quota, setQuota] = useState<any>({});
  const [_usageRecords, setUsageRecords] = useState<any[]>([]);
  const [weeklyData, setWeeklyData] = useState<any[]>([]);
  const [accessLogs, setAccessLogs] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);

  // View filter state
  const [viewRange, setViewRange] = useState<[Dayjs | null, Dayjs | null] | null>(null);

  // Export state
  const [exportRange, setExportRange] = useState<[Dayjs | null, Dayjs | null] | null>(null);
  const [exporting, setExporting] = useState(false);

  useEffect(() => {
    fetchData();
  }, []);

  const fetchData = async (range?: [Dayjs | null, Dayjs | null] | null) => {
    const activeRange = range !== undefined ? range : viewRange;
    let logsUrl = '/api/v1/user/access-logs?detailed=true&limit=1000';
    if (activeRange && activeRange[0] && activeRange[1]) {
      logsUrl += `&start_date=${activeRange[0].format('YYYY-MM-DD')}&end_date=${activeRange[1].format('YYYY-MM-DD')}`;
    }
    try {
      const [quotaRes, usageRes, logsRes] = await Promise.all([
        api.get('/api/v1/user/quota'),
        api.get('/api/v1/user/usage'),
        api.get(logsUrl),
      ]);

      setQuota(quotaRes.data.data || {});
      const records = usageRes.data.data || [];
      setUsageRecords(records);

      const chartData = records.map((record: any) => {
        const date = new Date(record.date);
        const weekdays = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];
        return {
          date: weekdays[date.getDay()],
          requests: record.requests,
        };
      }).reverse();
      setWeeklyData(chartData);

      setAccessLogs(logsRes.data.data || []);
    } catch (err) {
      console.error('Failed to fetch usage data:', err);
    } finally {
      setLoading(false);
    }
  };

  const handleExport = async () => {
    if (!exportRange || !exportRange[0] || !exportRange[1]) {
      message.warning('请先选择导出的日期范围');
      return;
    }

    const startDate = exportRange[0].format('YYYY-MM-DD');
    const endDate = exportRange[1].format('YYYY-MM-DD');

    setExporting(true);
    try {
      const resp = await api.get('/api/v1/admin/access-logs/export-by-dates', {
        params: { start_date: startDate, end_date: endDate },
        responseType: 'blob',
      });
      const blob = new Blob([resp.data], { type: 'text/csv;charset=utf-8;' });
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = `access-logs-${startDate}-${endDate}.csv`;
      document.body.appendChild(link);
      link.click();
      document.body.removeChild(link);
      URL.revokeObjectURL(url);
      message.success('成功导出所选日期范围访问日志');
    } catch {
      message.error('导出失败，请检查权限或网络后重试');
    } finally {
      setExporting(false);
    }
  };

  const requestUsagePercent = quota.daily_requests_limit
    ? Math.round((quota.daily_requests_used / quota.daily_requests_limit) * 100)
    : 0;

  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>使用统计</h2>

      {/* 配额概览 */}
      <Row gutter={16} style={{ marginBottom: 24 }}>
        <Col span={12}>
          <Card>
            <Statistic
              title="今日请求次数"
              value={quota.daily_requests_used || 0}
              suffix={`/ ${quota.daily_requests_limit || 0}`}
            />
            <Progress
              percent={requestUsagePercent}
              status={requestUsagePercent > 90 ? 'exception' : 'active'}
              style={{ marginTop: 8 }}
            />
          </Card>
        </Col>
        <Col span={12}>
          <Card>
            <Statistic
              title="可用模型数"
              value={quota.models_allowed?.length || 0}
              suffix="个"
            />
            <div style={{ marginTop: 8 }}>
              {quota.models_allowed?.map((model: string) => (
                <Tag key={model} style={{ margin: '0 4px 4px 0' }}>
                  {model}
                </Tag>
              ))}
            </div>
          </Card>
        </Col>
      </Row>

      {/* 配额详情 */}
      <Card title="配额详情" style={{ marginBottom: 24 }}>
        <Descriptions bordered column={2}>
          <Descriptions.Item label="速率限制">
            {quota.rate_limit} 请求/{quota.rate_window || 60}秒
          </Descriptions.Item>
          <Descriptions.Item label="每日请求限额">
            {quota.daily_requests_limit?.toLocaleString() || '无限制'}
          </Descriptions.Item>
          <Descriptions.Item label="重置时间">
            {quota.reset_time || '每日 00:00'}
          </Descriptions.Item>
        </Descriptions>
      </Card>

      {/* 最近访问记录 */}
      <Card title="最近访问记录" style={{ marginBottom: 24 }}>
        <div style={{ marginBottom: 12 }}>
          <Space>
            <span>日期范围：</span>
            <RangePicker
              value={viewRange}
              onChange={(val) => {
                const newRange = val as [Dayjs | null, Dayjs | null] | null;
                setViewRange(newRange);
                fetchData(newRange);
              }}
              format="YYYY-MM-DD"
              allowClear
            />
            <Button onClick={() => { setViewRange(null); fetchData(null); }} size="small">
              显示全部
            </Button>
          </Space>
        </div>
        <AccessLogsTable
          logs={accessLogs}
          loading={loading}
          isAdmin={false}
          scroll={{ y: 400 }}
        />
      </Card>

      {/* 日志导出（仅管理员可用） */}
      <Card title="导出访问日志" style={{ marginBottom: 24 }}>
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          <div>
            <span style={{ marginRight: 8 }}>选择日期范围：</span>
            <RangePicker
              onChange={(val) => setExportRange(val as [Dayjs | null, Dayjs | null] | null)}
              format="YYYY-MM-DD"
              allowClear
            />
          </div>
          <div style={{ color: '#999', fontSize: 12 }}>
            将导出所选日期范围内的全部访问日志（含请求/响应详情）。仅管理员可导出。
          </div>
          <Button
            type="primary"
            icon={<DownloadOutlined />}
            loading={exporting}
            onClick={handleExport}
            disabled={!exportRange || !exportRange[0] || !exportRange[1]}
          >
            导出 CSV
          </Button>
        </Space>
      </Card>

      {/* 使用趋势图 */}
      <Card title="最近7天使用趋势" style={{ marginBottom: 24 }}>
        <ResponsiveContainer width="100%" height={300}>
          <BarChart data={weeklyData}>
            <CartesianGrid strokeDasharray="3 3" />
            <XAxis dataKey="date" />
            <YAxis />
            <Tooltip />
            <Bar dataKey="requests" name="请求数" fill="#1890ff" />
          </BarChart>
        </ResponsiveContainer>
      </Card>
    </div>
  );
};

export default UsageStats;
