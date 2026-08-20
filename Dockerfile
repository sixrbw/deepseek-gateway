# 前端构建阶段
FROM node:22-alpine AS web-builder

WORKDIR /app/web
# 复制前端配置文件和源码
COPY web/package*.json ./
RUN npm install
COPY web/ ./
RUN npm run build

# 后端构建阶段
FROM golang:1.25-alpine AS go-builder

WORKDIR /app

# 安装依赖
RUN apk add --no-cache git

# 复制依赖文件
COPY go.mod go.sum ./
RUN go mod download

# 复制后端源代码
COPY . .

# 将前端构建产物复制到内嵌目录
COPY --from=web-builder /app/web/dist ./internal/infra/static/dist

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o modelgate ./cmd/server
# 生产镜像
FROM alpine:latest

WORKDIR /app

# 安装 ca-certificates 用于 HTTPS
RUN apk --no-cache add ca-certificates

# 复制二进制文件和配置文件
COPY --from=go-builder /app/modelgate .
COPY config.yaml.example ./config.yaml

# 暴露端口
EXPOSE 8080

# 运行
CMD ["./modelgate"]
