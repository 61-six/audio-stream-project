package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"audio-stream-project/api"
	"audio-stream-project/internal/audio"
	audiogrpc "audio-stream-project/internal/grpc"

	"google.golang.org/grpc"
)

const (
	port = ":50051"
)

func main() {
	frameMs := flag.Int("frame-ms", 20, "Frame size in milliseconds")
	maxSessions := flag.Int("max-sessions", 10, "Maximum number of concurrent sessions")
	flag.Parse()

	audio.SetFrameSizeMs(*frameMs)
	log.Printf("Frame size set to %d ms (%d bytes)", *frameMs, audio.FrameSizeBytes)
	log.Printf("Max sessions set to %d", *maxSessions)

	log.Println("Starting audio stream server...")

	listener, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	audioServer := audiogrpc.NewAudioServer("./output", *maxSessions)

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
