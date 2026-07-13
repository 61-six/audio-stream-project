package main

import (
	"context"       //管理上下文，用于控制请求生命周期
	"flag"          //命令行参数解析
	"io"            //I/O操作接口，用于读写文件和处理eof
	"log"           //日志
	"os"            //文件操作系统
	"path/filepath" //路径处理，提取文件名

	"audio-stream-project/api" //项目的gRPC API定义（protobuf生成）

	"google.golang.org/grpc"                      //gRPC框架
	"google.golang.org/grpc/credentials/insecure" //不安全凭证
	"google.golang.org/grpc/metadata"             //gRPC metadata
)

const (
	defaultAddress   = "localhost:50051"
	defaultChunkSize = 32 * 1024
)

func main() {
	address := flag.String("address", defaultAddress, "gRPC server address")
	filePath := flag.String("file", "", "Path to audio file")
	chunkSize := flag.Int("chunk-size", defaultChunkSize, "Chunk size in bytes")
	clientID := flag.String("client-id", "client", "Client identifier")

	flag.Parse()
	if *filePath == "" {
		log.Fatal("Please specify a file with --file flag")
	}

	//------音频文件

	file, err := os.Open(*filePath)
	if err != nil {
		log.Fatalf("Failed to open file: %v", err)
	}

	//函数结束时自动关闭文件，防止资源泄漏
	defer file.Close()

	//gRPC建立连接
	conn, err := grpc.Dial(*address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	//创建客户端和流
	client := api.NewAudioServiceClient(conn)

	ctx := metadata.AppendToOutgoingContext(context.Background(), "client-id", *clientID)
	stream, err := client.Upload(ctx)
	if err != nil {
		log.Fatalf("Failed to create stream: %v", err)
	}

	filename := filepath.Base(*filePath)

	metadata := &api.AudioMetadata{
		Filename: filename,
		ClientId: *clientID,
	}

	//通过流发送元数据
	err = stream.Send(&api.UploadRequest{
		Message: &api.UploadRequest_Metadata{Metadata: metadata},
	})
	if err != nil {
		log.Fatalf("Failed to send metadata: %v", err)
	}

	log.Printf("Sent metadata: %s", filename)

	buf := make([]byte, *chunkSize)
	totalBytes := 0
	chunkCount := 0

	for {
		n, err := file.Read(buf)
		if err != nil && err != io.EOF {
			log.Fatalf("Failed to read file: %v", err)
		}

		if n > 0 {
			chunk := &api.AudioChunk{
				Data:   buf[:n],
				IsLast: false,
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
		}

		if err == io.EOF {
			break
		}
	}

	lastChunk := &api.AudioChunk{
		Data:   []byte{},
		IsLast: true,
	}
	err = stream.Send(&api.UploadRequest{
		Message: &api.UploadRequest_Chunk{Chunk: lastChunk},
	})
	if err != nil {
		log.Fatalf("Failed to send last chunk: %v", err)
	}

	log.Printf("Upload completed: %d chunks, %d bytes", chunkCount, totalBytes)

	//循环接收服务器响应
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Failed to receive response: %v", err)
		}

		switch msg := resp.Message.(type) {
		case *api.StatusResponse_Status: //处理实时状态更新
			status := msg.Status
			log.Printf("Status: session=%s, received=%d bytes, chunks=%d, status=%s",
				status.SessionId, status.ReceivedBytes, status.ChunkCount, status.Status)
		case *api.StatusResponse_Summary: // 处理最终处理摘要
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
