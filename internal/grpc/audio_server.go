package grpc

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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

type AudioServer struct {
	api.UnimplementedAudioServiceServer
	sessionManager *session.Manager
	outputDir      string
}

func NewAudioServer(outputDir string) *AudioServer {
	return &AudioServer{
		sessionManager: session.NewManager(),
		outputDir:      outputDir,
	}
}

func (s *AudioServer) Upload(stream api.AudioService_UploadServer) error {
	var sess *session.Session
	var trans *ffmpeg.Transcoder
	var sb *buffer.StreamBuffer
	var metadata *api.AudioMetadata
	var wg sync.WaitGroup

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
			sess = s.sessionManager.CreateSession("unknown")
			sess.Filename = metadata.Filename
			log.Printf("Session created: %s", sess.ID)

			err = s.sendStatus(stream, sess.ID, "metadata received", int64(0), int32(0))
			if err != nil {
				return err
			}

			outputPath := filepath.Join(s.outputDir, fmt.Sprintf("%s.pcm", sess.ID))

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

			sb = buffer.NewStreamBuffer(1024 * 1024)

			wg.Add(1)
			go s.processBuffer(sess, sb, trans, &wg)

		case *api.UploadRequest_Chunk:
			if sess == nil {
				return status.Errorf(codes.InvalidArgument, "metadata must be sent first")
			}

			chunk := msg.Chunk
			sess.ReceivedBytes += int64(len(chunk.Data))
			sess.ChunkCount++

			log.Printf("Chunk received: session=%s, size=%d, chunk_count=%d", sess.ID, len(chunk.Data), sess.ChunkCount)

			err = s.sendStatus(stream, sess.ID, "chunk received", sess.ReceivedBytes, sess.ChunkCount)
			if err != nil {
				return err
			}

			if sb != nil {
				err = sb.Write(chunk.Data)
				if err != nil {
					log.Printf("Buffer write error: %v", err)
				}
			}

			if chunk.IsLast {
				log.Printf("Last chunk received for session: %s", sess.ID)

				if sb != nil {
					sb.Close()
				}

				wg.Wait()

				if trans != nil {
					err = trans.Close()
					if err != nil {
						log.Printf("Transcoder close error: %v", err)
					}
				}

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
	outputPath := filepath.Join(s.outputDir, fmt.Sprintf("%s.pcm", sess.ID))
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return err
	}

	stats, _ := audio.ProcessFrames(data)

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