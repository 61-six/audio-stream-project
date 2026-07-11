package main

import (
	"log"
	"net" //网络操作，用于监听tcp连接
	"os"
	"os/signal"
	"syscall" // 系统调用常量

	"audio-stream-project/api"
	audiogrpc "audio-stream-project/internal/grpc" //避免与包名冲突

	"google.golang.org/grpc"
)

const (
	port = ":50051"
)

func main() {
	log.Println("Starting audio stream server...")

	// 创建 TCP 监听器
	listener, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	//创建音频服务实例
	audioServer := audiogrpc.NewAudioServer("./output")

	//创建 gRPC 服务器 默认配置
	s := grpc.NewServer()
	//注册服务
	api.RegisterAudioServiceServer(s, audioServer)

	log.Printf("Server listening on %s", port)

	//启动服务器
	go func() {
		err := s.Serve(listener)
		if err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	//关闭配置
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down server...")
	s.GracefulStop()
	log.Println("Server stopped")
}
