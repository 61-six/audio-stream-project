package grpc

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"audio-stream-project/api"
	"audio-stream-project/internal/audio"
	"audio-stream-project/internal/buffer"
	"audio-stream-project/internal/ffmpeg"
	"audio-stream-project/internal/session"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AudioServer 实现 gRPC 服务接口，管理音频上传和处理
type AudioServer struct {
	api.UnimplementedAudioServiceServer                  // 嵌入未实现的服务，保证向前兼容
	sessionManager                      *session.Manager // 会话管理器
	outputDir                           string           // 输出目录
}

func NewAudioServer(outputDir string) *AudioServer {
	return &AudioServer{
		sessionManager: session.NewManager(),
		outputDir:      outputDir,
	}
}

func (s *AudioServer) Upload(stream api.AudioService_UploadServer) error {
	var sess *session.Session       // 会话信息
	var trans *ffmpeg.Transcoder    // FFmpeg 转码器
	var sb *buffer.StreamBuffer     // 流缓冲区
	var metadata *api.AudioMetadata // 元数据
	var wg sync.WaitGroup           // 等待组

	log.Println("New connection received")

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			log.Println("Client closed connection")
			return nil
		}
		if err != nil {
			log.Printf("Error receiving message: %v", err)
			return status.Errorf(codes.Internal, "failed to receive: %v", err)
		}

		switch msg := req.Message.(type) {
		case *api.UploadRequest_Metadata:
			metadata = msg.Metadata
			log.Printf("Received metadata: filename=%s", metadata.Filename)
			// 1. 创建会话
			sess = s.sessionManager.CreateSession("unknown")
			sess.Filename = metadata.Filename
			log.Printf("Session created: %s", sess.ID)
			// 2. 发送状态更新
			err = s.sendStatus(stream, sess.ID, "metadata received", int64(0), int32(0))
			if err != nil {
				return err
			}
			// 3. 启动 FFmpeg
			outputPath := fmt.Sprintf("%s/%s.pcm", s.outputDir, sess.ID)

			trans = ffmpeg.NewTranscoder(outputPath)
			err = trans.Start()
			if err != nil {
				log.Printf("Failed to start ffmpeg: %v", err)
				return status.Errorf(codes.Internal, "failed to start ffmpeg: %v", err)
			}
			log.Println("FFmpeg started")

			err = s.sendStatus(stream, sess.ID, "ffmpeg started", int64(0), int32(0))
			if err != nil {
				return err
			}
			// 4. 创建缓冲区
			sb = buffer.NewStreamBuffer(1024 * 1024)
			// 5. 启动处理协程
			wg.Add(1)
			go s.processBuffer(sess, sb, trans, &wg)

			// 处理音频数据块
		case *api.UploadRequest_Chunk:
			if sess == nil {
				return status.Errorf(codes.InvalidArgument, "metadata must be sent first")
			}

			chunk := msg.Chunk
			sess.ReceivedBytes += int64(len(chunk.Data))
			sess.ChunkCount++

			log.Printf("Chunk received: session=%s, size=%d, chunk_count=%d", sess.ID, len(chunk.Data), sess.ChunkCount)
			// 1. 发送状态更新
			err = s.sendStatus(stream, sess.ID, "chunk received", sess.ReceivedBytes, sess.ChunkCount)
			if err != nil {
				return err
			}
			// 2. 写入缓冲区
			if sb != nil {
				err = sb.Write(chunk.Data)
				if err != nil {
					log.Printf("Buffer write error: %v", err)
				}
			}
			// 处理最后一块数据
			if chunk.IsLast {
				log.Printf("Last chunk received for session: %s", sess.ID)
				// 1. 关闭缓冲区
				if sb != nil {
					sb.Close()
				}
				// 2. 等待处理完成
				wg.Wait()
				// 3. 关闭转码器
				if trans != nil {
					err = trans.Close()
					if err != nil {
						log.Printf("Transcoder close error: %v", err)
					}
				}
				// 4. 处理输出文件
				sess.Status = "processing"
				err = s.sendStatus(stream, sess.ID, "transcode finished", sess.ReceivedBytes, sess.ChunkCount)
				if err != nil {
					return err
				}

				err = s.processOutput(sess)
				if err != nil {
					log.Printf("Output processing error: %v", err)
					return status.Errorf(codes.Internal, "processing failed: %v", err)
				}
				// 5. 发送处理摘要
				sess.Status = "completed"
				sess.EndTime = sess.StartTime.Add(time.Duration(sess.DurationMs) * time.Millisecond)

				summary := &api.ProcessingSummary{
					SessionId:     sess.ID,
					ReceivedBytes: sess.ReceivedBytes,
					ChunkCount:    sess.ChunkCount,
					OutputFormat:  audio.GetOutputFormat(),
					FrameSize:     int32(audio.FrameSizeBytes),
					FrameCount:    int32(sess.FrameCount),
					DurationMs:    sess.DurationMs,
					AvgEnergy:     sess.AvgEnergy,
					OutputFile:    sess.OutputFile,
				}

				err = stream.Send(&api.StatusResponse{
					Message: &api.StatusResponse_Summary{Summary: summary},
				})
				if err != nil {
					return err
				}

				log.Printf("Session completed: %s", sess.ID)
				log.Println(sess.String())

				return nil
			}
		}
	}
}

func (s *AudioServer) processBuffer(sess *session.Session, sb *buffer.StreamBuffer, trans *ffmpeg.Transcoder, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		if r := recover(); r != nil {
			log.Printf("processBuffer panic: %v", r)
		}
	}()

	log.Printf("processBuffer started for session: %s", sess.ID)

	for {
		// 从缓冲区读取数据 (每次 32KB)
		data, err := sb.Read(32 * 1024)
		if err != nil {
			if err == buffer.ErrBufferClosed {
				log.Printf("processBuffer buffer closed for session: %s", sess.ID)
				break
			}
			log.Printf("Buffer read error: %v", err)
			break
		}

		log.Printf("processBuffer read %d bytes for session: %s", len(data), sess.ID)
		// 写入 FFmpeg 转码器
		if trans != nil && trans.IsRunning() {
			n, err := trans.Write(data)
			log.Printf("processBuffer wrote %d bytes to transcoder for session: %s", n, sess.ID)
			if err != nil {
				log.Printf("Transcoder write error: %v", err)
				break
			}
		} else if trans != nil && !trans.IsRunning() {
			log.Printf("processBuffer transcoder not running for session: %s", sess.ID)
			break
		}
	}

	log.Printf("processBuffer finished for session: %s", sess.ID)
}

func (s *AudioServer) processOutput(sess *session.Session) error {
	outputPath := fmt.Sprintf("%s/%s.pcm", s.outputDir, sess.ID)
	// 1. 读取转码后的 PCM 文件
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return err
	}
	// 2. 分析音频帧
	stats, _ := audio.ProcessFrames(data)
	// 3. 更新会话信息
	sess.FrameCount = stats.FrameCount
	sess.DurationMs = stats.DurationMs
	sess.AvgEnergy = stats.AvgEnergy
	sess.OutputFile = fmt.Sprintf("%s.pcm", sess.ID)

	log.Printf("Frame statistics: frame_count=%d, duration_ms=%d, avg_energy=%.2f",
		stats.FrameCount, stats.DurationMs, stats.AvgEnergy)

	return nil
}

func (s *AudioServer) sendStatus(stream api.AudioService_UploadServer, sessionID string, statusStr string, receivedBytes int64, chunkCount int32) error {
	return stream.Send(&api.StatusResponse{
		Message: &api.StatusResponse_Status{
			Status: &api.ProcessingStatus{
				SessionId:     sessionID,
				ReceivedBytes: receivedBytes,
				ChunkCount:    chunkCount,
				Status:        statusStr,
			},
		},
	})
}
