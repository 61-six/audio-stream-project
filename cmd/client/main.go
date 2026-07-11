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
)

const (
	defaultAddress   = "localhost:50051" //默认服务器地址和端口
	defaultChunkSize = 32 * 1024         //默认块大小：32kb 32KB是平衡性能和网络传输的常见值
)

func main() {

	//----命令行参数解析 用于判断文件参数是否提供
	address := flag.String("address", defaultAddress, "gRPC server address")
	filePath := flag.String("file", "", "Path to audio file")
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

	//创建音频服务的gRPC客户端
	stream, err := client.Upload(context.Background())
	if err != nil {
		log.Fatalf("Failed to create stream: %v", err)
	}

	filename := filepath.Base(*filePath) //提取完整路径

	//创建元数据对象，包含文件名
	metadata := &api.AudioMetadata{
		Filename: filename,
	}

	//通过流发送元数据
	err = stream.Send(&api.UploadRequest{
		Message: &api.UploadRequest_Metadata{Metadata: metadata},
	})
	if err != nil {
		log.Fatalf("Failed to send metadata: %v", err)
	}

	log.Printf("Sent metadata: %s", filename)

	//发送
	buf := make([]byte, defaultChunkSize)
	totalBytes := 0
	chunkCount := 0

	//读取和发送文件数据

	for {
		//读取文件 n为实际读取字节数
		n, err := file.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Failed to read file: %v", err)
		}

		//判断最后一块数据  注 如果读取的字节数小于缓冲区大小，说明是最后一块
		isLast := false
		if n < defaultChunkSize {
			isLast = true
		}

		//创建音频对象 用isLast标记是否为最后一块
		chunk := &api.AudioChunk{
			Data:   buf[:n],
			IsLast: isLast,
		}

		//通过流发送数据块 将chunk包装为UploadRequest消息
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
