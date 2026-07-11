# 实时音频流处理服务

基于 Go 语言实现的实时音频流处理服务，支持 gRPC 流式上传、音频转码、分帧处理和 Docker 部署。

## 项目结构

```
audio-stream-project/
├── cmd/
│   ├── server/          # gRPC 服务端入口
│   └── client/          # gRPC 客户端入口
├── api/
│   ├── audio.proto      # gRPC 接口定义
│   └── audio.pb.go      # 自动生成的 gRPC 代码
├── internal/
│   ├── buffer/          # 流式 Buffer 实现
│   ├── session/         # 会话管理
│   ├── ffmpeg/          # FFmpeg 转码器
│   ├── audio/           # 音频分帧处理
│   └── grpc/            # gRPC 服务实现
├── testdata/            # 测试音频文件
├── output/              # 转码输出目录
├── Dockerfile
├── docker-compose.yml
├── Makefile
└── go.mod
```

## 功能特性

1. **gRPC 流式音频上传**：双向流式传输，支持元信息和分片数据
2. **流式 Buffer**：支持并发安全的写入和读取，带阻塞策略
3. **FFmpeg 转码**：将任意音频转换为 16kHz mono s16le PCM
4. **音频分帧与统计**：20ms 分帧，计算能量统计
5. **Docker 部署**：一键启动服务端和客户端

## 快速开始

### 环境要求

- Go 1.22+
- Docker / Docker Compose
- FFmpeg（本地开发时）

### 启动服务端

```bash
# 方式一：直接运行
go run ./cmd/server

# 方式二：使用 Docker
docker compose up --build
```

### 客户端上传音频

```bash
# 方式一：本地运行
go run ./cmd/client --file ./testdata/sample.wav

# 方式二：使用 Docker
docker compose run client --file ./testdata/sample.wav
```

### 查看输出

转码后的音频文件保存在 `output/{session_id}.pcm`

## 核心实现说明

### gRPC 流式接口设计

`audio.proto` 定义了双向流式接口 `Upload`：

- **请求消息**：使用 `oneof` 结构，支持发送 `AudioMetadata` 或 `AudioChunk`
- **响应消息**：使用 `oneof` 结构，返回 `ProcessingStatus` 或 `ProcessingSummary`
- **交互流程**：客户端先发送元信息，再分块发送音频数据，最后发送 `is_last=true` 标记结束

### Buffer 实现

`internal/buffer/stream_buffer.go`：

- 使用 `bytes.Buffer` 作为底层存储
- 使用 `sync.Mutex` 和 `sync.Cond` 实现并发安全
- 支持最大缓存限制（默认 1MB）
- **Buffer 满时策略**：阻塞等待，直到有空间可用
- 支持关闭操作，唤醒所有等待的 goroutine

### FFmpeg 进程管理

`internal/ffmpeg/transcoder.go`：

- 使用 `exec.CommandContext` 启动 FFmpeg 进程
- 通过 `stdin` 输入原始音频流
- 通过 `stdout` 读取转码后的 PCM 数据
- 使用 `context.WithCancel` 实现进程优雅退出
- 会话结束时调用 `Close()` 释放资源

### 音频分帧处理

`internal/audio/processor.go`：

- **帧大小**：20ms @ 16kHz = 640 bytes（16-bit mono）
- **能量计算**：计算每帧的 RMS 能量
- **统计信息**：总帧数、时长、平均/最大/最小能量

### 并发与异常处理

- 使用 `sync.Map` 管理多个 session
- goroutine 使用 `defer recover()` 防止 panic 传播
- gRPC 错误码规范：`InvalidArgument`, `Internal`
- 连接断开时服务端不会崩溃

### 多 Session 隔离

- 每个连接独立创建 session
- session 使用 UUID 作为唯一标识
- 输出文件以 session_id 命名，避免冲突

## 当前不足

1. **配置硬编码**：端口、Buffer 大小等参数未支持配置文件
2. **缺少优雅退出**：FFmpeg 进程可能需要更长时间清理
3. **无 VAD 检测**：未实现静音检测功能
4. **单元测试覆盖**：Buffer 和 Processor 缺少单元测试
5. **无监控指标**：未集成 Prometheus 监控

## 测试

```bash
# 运行测试
go test ./...

# 代码检查
go vet ./...
go fmt ./...
```

## 部署

```bash
# 构建 Docker 镜像
docker compose build

# 启动服务
docker compose up -d

# 运行客户端测试
docker compose run client --file ./testdata/sample.wav

# 查看日志
docker compose logs -f
```

## License

MIT
