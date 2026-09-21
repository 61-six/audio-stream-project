# ============ 阶段1:Go 构建层 ============
FROM docker.m.daocloud.io/library/golang:1.22-alpine AS builder

WORKDIR /app

# 利用缓存:先拷贝依赖清单
COPY go.mod go.sum* ./
RUN GOPROXY=https://goproxy.cn go mod download 2>/dev/null || true

# 拷贝源码
COPY . .

# 静态编译(纯 Go,无 CGO 依赖,便于跨平台运行)
RUN GOPROXY=https://goproxy.cn CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/bin/server ./cmd/server

# ============ 阶段2:运行层 ============
FROM docker.m.daocloud.io/library/ubuntu:22.04

# 安装 ffmpeg(音质修复核心依赖)+ ca-certificates + curl(健康检查)
RUN sed -i 's/archive.ubuntu.com/mirrors.aliyun.com/g' /etc/apt/sources.list && \
    sed -i 's/security.ubuntu.com/mirrors.aliyun.com/g' /etc/apt/sources.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends ffmpeg ca-certificates curl && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

# 拷贝二进制 + 前端静态资源
COPY --from=builder /app/bin/server ./server
COPY --from=builder /app/web ./web

# 数据卷:原始音频 / 修复音频 / 封面 / 扫描导入目录 / SQLite 数据库
RUN mkdir -p /app/storage/original /app/storage/repaired /app/storage/covers /app/storage/import /app/data
VOLUME ["/app/storage", "/app/data"]

EXPOSE 8080

# 健康检查:HTTP 探活
HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
    CMD curl -sf http://localhost:8080/health || exit 1

CMD ["./server"]
