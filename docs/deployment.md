# 部署建议

## 单机部署

```bash
# 1. 复制两个文件到服务器
scp modelgate config.yaml user@server:/opt/modelgate/

# 2. 使用 systemd 管理服务
sudo systemctl enable --now modelgate
```

## 使用 Nginx 反向代理

```nginx
server {
    listen 80;
    server_name llm.company.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        
        # WebSocket 支持（用于流式响应）
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        
        # 超时设置
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

## 多实例高可用部署

```
                    ┌─────────────┐
                    │   Nginx     │
                    │  (负载均衡)  │
                    └──────┬──────┘
                           │
           ┌───────────────┼───────────────┐
           │               │               │
      ┌────┴────┐     ┌────┴────┐     ┌────┴────┐
      │Modelgate │     │Modelgate │     │Modelgate │
      │Instance1│     │Instance2│     │Instance3│
      └────┬────┘     └────┬────┘     └────┬────┘
           │               │               │
           └───────────────┼───────────────┘
                           │
                    ┌──────┴──────┐
                    │  SQLite     │
                    │  (共享存储)  │
                    └─────────────┘
```

**注意**：多实例部署需要使用共享存储（如 NFS）存放 SQLite 数据库文件。
