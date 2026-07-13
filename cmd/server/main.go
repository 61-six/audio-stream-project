package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"audio-stream-project/api"
	audiogrpc "audio-stream-project/internal/grpc"

	"google.golang.org/grpc"
)

const (
	port = ":50051"
)

func main() {

	log.Println("Starting audio stream server...")

	outputDir := os.TempDir()
	log.Printf("Output directory: %s", outputDir)

	listener, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	audioServer := audiogrpc.NewAudioServer(outputDir)

	s := grpc.NewServer()
	api.RegisterAudioServiceServer(s, audioServer)

	log.Printf("Server listening on %s", port)

	go func() {
		err := s.Serve(listener)
		if err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down server...")
	s.GracefulStop()
	log.Println("Server stopped")
}