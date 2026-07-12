FROM docker.m.daocloud.io/library/golang:1.22-alpine AS builder

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories

RUN apk add --no-cache git gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN GOPROXY=https://goproxy.cn go mod download

COPY . .

RUN GOPROXY=https://goproxy.cn CGO_ENABLED=0 go build -o server ./cmd/server
RUN GOPROXY=https://goproxy.cn CGO_ENABLED=0 go build -o client ./cmd/client

FROM docker.m.daocloud.io/library/ubuntu:22.04

RUN sed -i 's/archive.ubuntu.com/mirrors.aliyun.com/g' /etc/apt/sources.list && \
    sed -i 's/security.ubuntu.com/mirrors.aliyun.com/g' /etc/apt/sources.list && \
    apt-get update && apt-get install -y --no-install-recommends ffmpeg netcat-openbsd && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app/server .
COPY --from=builder /app/client .
COPY --from=builder /app/testdata ./testdata
RUN mkdir -p ./output

EXPOSE 50051

CMD ["./server"]
