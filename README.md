# Audio Stream Project

基于 gRPC 的音频流传输与处理服务。

## 功能特性

- gRPC 流式上传
- FFmpeg 音频转码
- 流式缓冲区管理
- 音频帧分析

## 技术栈

- Go 1.22
- gRPC
- FFmpeg
- Docker

## 快速开始

### 本地运行

```bash
go run ./cmd/server
go run ./cmd/client --file ./testdata/sample.wav
```

### Docker 运行

```bash
docker compose up -d
docker compose run --profile client client --file /app/testdata/sample.wav
```

## 项目结构

```
.
├── api/
│   ├── audio.proto
│   ├── audio.pb.go
│   └── audio_grpc.pb.go
├── cmd/
│   ├── client/
│   └── server/
├── internal/
│   ├── audio/
│   ├── buffer/
│   ├── ffmpeg/
│   ├── grpc/
│   └── session/
├── testdata/
└── output/
```

## API 接口

### Upload (流式)

上传音频文件并获取处理状态和摘要。

## 输出格式

转码后的音频文件为 PCM 格式：
- 采样率: 16000Hz
- 声道: 单声道
- 位深: 16位

输出文件位于 `./output/` 目录，命名格式为 `{session_id}.pcm`。

## 许可证

MIT