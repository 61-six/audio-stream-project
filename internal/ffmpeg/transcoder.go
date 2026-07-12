package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
)

type Transcoder struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	stderr     *bufio.Scanner
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	isRunning  bool
	outputPath string
	mu         sync.Mutex
	runErr     error
	stderrBuf  []string
}

// NewTranscoder 创建可取消的上下文 初始化转码器实例 指定输出文件路径
func NewTranscoder(outputPath string) *Transcoder {
	ctx, cancel := context.WithCancel(context.Background())
	return &Transcoder{
		ctx:        ctx,
		cancel:     cancel,
		outputPath: outputPath,
	}
}

// start（）启动ffmpeg
func (t *Transcoder) Start() error {
	args := []string{
		"-i", "pipe:0", // 从标准输入读取
		"-f", "s16le", // 输出格式：16位小端 PCM
		"-acodec", "pcm_s16le", // 音频编码：PCM 16位
		"-ac", "1", // 声道数：单声道
		"-ar", "16000", // 采样率：16kHz
		"pipe:1", // 输出到标准输出
	}

	t.cmd = exec.CommandContext(t.ctx, "ffmpeg", args...)

	var err error
	// 创建标准输入管道（向 FFmpeg 写入数据）
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return err
	}
	// 创建标准输出管道（从 FFmpeg 读取数据）
	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// 创建标准错误管道（读取 FFmpeg 日志）
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return err
	}
	t.stderr = bufio.NewScanner(stderr)
	//启动 FFmpeg 进程
	if err := t.cmd.Start(); err != nil {
		return err
	}

	t.isRunning = true

	t.wg.Add(2)
	go t.readStderr() //读取日志（避免管道阻塞）
	go t.readStdout() //读取转码后的音频数据

	return nil
}

func (t *Transcoder) readStderr() {
	defer t.wg.Done()
	for t.stderr.Scan() {
		t.mu.Lock()
		t.stderrBuf = append(t.stderrBuf, t.stderr.Text())
		t.mu.Unlock()
	}
	if err := t.stderr.Err(); err != nil {
		t.mu.Lock()
		t.runErr = errors.Join(t.runErr, err)
		t.mu.Unlock()
	}
}

func (t *Transcoder) readStdout() {
	defer t.wg.Done()
	file, err := os.Create(t.outputPath)
	if err != nil {
		t.mu.Lock()
		t.runErr = errors.Join(t.runErr, err)
		t.mu.Unlock()
		return
	}
	defer file.Close()

	buf := make([]byte, 32*1024)
	for {
		n, err := t.stdout.Read(buf)
		if n > 0 {
			if _, writeErr := file.Write(buf[:n]); writeErr != nil {
				t.mu.Lock()
				t.runErr = errors.Join(t.runErr, writeErr)
				t.mu.Unlock()
			}
		}
		if err != nil {
			break
		}
	}
}

func (t *Transcoder) Write(data []byte) (int, error) {
	t.mu.Lock()
	isRunning := t.isRunning
	t.mu.Unlock()
	if !isRunning {
		return 0, ErrNotRunning
	}
	return t.stdin.Write(data)
}

func (t *Transcoder) Close() error {
	t.mu.Lock()
	if !t.isRunning {
		t.mu.Unlock()
		return t.runErr
	}
	t.isRunning = false
	t.mu.Unlock()

	t.stdin.Close()

	if err := t.cmd.Wait(); err != nil {
		t.mu.Lock()
		t.runErr = errors.Join(t.runErr, err)
		t.mu.Unlock()
	}

	t.wg.Wait()

	t.cancel()

	t.mu.Lock()
	err := t.runErr
	t.mu.Unlock()
	return err
}

func (t *Transcoder) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.isRunning
}

func (t *Transcoder) GetStderr() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string{}, t.stderrBuf...)
}
