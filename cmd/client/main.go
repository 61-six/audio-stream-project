package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"

	"audio-stream-project/api"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultAddress   = "localhost:50051"
	defaultChunkSize = 32 * 1024
)

func main() {

	address := flag.String("address", defaultAddress, "gRPC server address")
	filePath := flag.String("file", "", "Path to audio file")

	flag.Parse()
	if *filePath == "" {
		log.Fatal("Please specify a file with --file flag")
	}

	file, err := os.Open(*filePath)
	if err != nil {
		log.Fatalf("Failed to open file: %v", err)
	}

	defer file.Close()

	conn, err := grpc.Dial(*address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := api.NewAudioServiceClient(conn)

	stream, err := client.Upload(context.Background())
	if err != nil {
		log.Fatalf("Failed to create stream: %v", err)
	}

	filename := filepath.Base(*filePath)

	metadata := &api.AudioMetadata{
		Filename: filename,
	}

	err = stream.Send(&api.UploadRequest{
		Message: &api.UploadRequest_Metadata{Metadata: metadata},
	})
	if err != nil {
		log.Fatalf("Failed to send metadata: %v", err)
	}

	log.Printf("Sent metadata: %s", filename)

	buf := make([]byte, defaultChunkSize)
	totalBytes := 0
	chunkCount := 0

	for {
		n, err := file.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Failed to read file: %v", err)
		}

		isLast := false
		if n < defaultChunkSize {
			isLast = true
		}

		chunk := &api.AudioChunk{
			Data:   buf[:n],
			IsLast: isLast,
		}

		err = stream.Send(&api.UploadRequest{
			Message: &api.UploadRequest_Chunk{Chunk: chunk},
		})
		if err != nil {
			log.Fatalf("Failed to send chunk: %v", err)
		}

		totalBytes += n
		chunkCount++

		if chunkCount%10 == 0 {
			log.Printf("Sent %d chunks, %d bytes", chunkCount, totalBytes)
		}

		if isLast {
			break
		}
	}

	log.Printf("Upload completed: %d chunks, %d bytes", chunkCount, totalBytes)

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Failed to receive response: %v", err)
		}

		switch msg := resp.Message.(type) {
		case *api.StatusResponse_Status:
			status := msg.Status
			log.Printf("Status: session=%s, received=%d bytes, chunks=%d, status=%s",
				status.SessionId, status.ReceivedBytes, status.ChunkCount, status.Status)
		case *api.StatusResponse_Summary:
			summary := msg.Summary
			log.Println("\nProcessing Summary:")
			log.Printf("  session_id: %s", summary.SessionId)
			log.Printf("  received_bytes: %d", summary.ReceivedBytes)
			log.Printf("  chunk_count: %d", summary.ChunkCount)
			log.Printf("  output_format: %s", summary.OutputFormat)
			log.Printf("  frame_size: %d bytes", summary.FrameSize)
			log.Printf("  frame_count: %d", summary.FrameCount)
			log.Printf("  duration_ms: %d", summary.DurationMs)
			log.Printf("  avg_energy: %.2f", summary.AvgEnergy)
			log.Printf("  output_file: %s", summary.OutputFile)
		}
	}

	log.Println("Upload and processing completed successfully")
}