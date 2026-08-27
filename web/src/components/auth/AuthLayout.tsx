import React from 'react';
import { Typography } from 'antd';
import logo from '../../assets/modelgate.png';

const { Title, Paragraph } = Typography;

interface AuthLayoutProps {
  children: React.ReactNode;
  title: string;
  description?: React.ReactNode;
  extraContent?: React.ReactNode;
}

export const AuthLayout: React.FC<AuthLayoutProps> = ({ children, title, description, extraContent }) => {
  return (
    <div style={{
      minHeight: '100vh',
      display: 'flex',
      background: '#f8fafc',
    }}>
      {/* 左侧品牌展示 */}
      <div style={{
        flex: 1,
        background: 'linear-gradient(135deg, #0f0c29 0%, #302b63 50%, #24243e 100%)',
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        alignItems: 'center',
        padding: '40px',
        position: 'relative',
        overflow: 'hidden',
      }}>
        {/* 装饰性光圈 */}
        <div style={{
          position: 'absolute',
          top: '-20%',
          left: '-10%',
          width: '400px',
          height: '400px',
          background: 'radial-gradient(circle, rgba(99, 102, 241, 0.15) 0%, transparent 70%)',
          borderRadius: '50%',
        }} />
        <div style={{
          position: 'absolute',
          bottom: '-15%',
          right: '-5%',
          width: '350px',
          height: '350px',
          background: 'radial-gradient(circle, rgba(139, 92, 246, 0.12) 0%, transparent 70%)',
          borderRadius: '50%',
        }} />

        <div style={{ position: 'relative', zIndex: 1, textAlign: 'center', maxWidth: '400px' }}>
          <img
            src={logo}
            alt="Modelgate"
            style={{
              width: '180px',
              height: '60px',
              marginBottom: '32px',
              filter: 'drop-shadow(0 4px 20px rgba(139, 92, 246, 0.3))',
            }}
          />
          <Title level={2} style={{ color: '#fff', marginBottom: '16px', fontWeight: 600 }}>
            {title}
          </Title>
          {description && (
            <Paragraph style={{
              color: 'rgba(255,255,255,0.5)',
              fontSize: '15px',
              lineHeight: '1.8',
              marginTop: '24px',
            }}>
              {description}
            </Paragraph>
          )}

          {extraContent}
        </div>
      </div>

      {/* 右侧表单内容 */}
      <div style={{
        flex: 1,
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        padding: '0 8%',
        background: '#f5f7fa',
        position: 'relative',
      }}>
        {children}
      </div>
    </div>
  );
};

export default AuthLayout;
