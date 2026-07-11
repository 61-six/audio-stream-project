package ffmpeg

import (
	"bufio"   // 缓冲读取，用于读取 stderr
	"context" // 上下文管理，控制进程生命周期
	"io"      // I/O 接口
	"os"      // 文件操作
	"os/exec" // 执行外部命令
	"sync"    // 同步原语
)

type Transcoder struct {
	cmd        *exec.Cmd          // FFmpeg 命令实例 管理外部命令
	stdin      io.WriteCloser     // 标准输入管道（写入音频数据）向 FFmpeg 写入原始音频
	stdout     io.ReadCloser      // 标准输出管道（读取转码后数据）从 FFmpeg 读取转码后音频
	stderr     *bufio.Scanner     // 标准错误扫描器（读取日志）读取 FFmpeg 日志信息
	ctx        context.Context    // 上下文 控制命令生命周期
	cancel     context.CancelFunc // 取消函数 主动终止命令
	wg         sync.WaitGroup     // 等待 goroutine 完成
	isRunning  bool               // 运行状态标志 标记转码器是否运行
	outputPath string             // 输出文件路径 保存转码后文件的位置
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
	}
}

// readStdout - 读取转码数据并保存
func (t *Transcoder) readStdout() {
	defer t.wg.Done()
	// 创建输出文件
	file, err := os.Create(t.outputPath)
	if err != nil {
		return
	}
	defer file.Close()
	// 缓冲区 32KB
	buf := make([]byte, 32*1024)
	for {
		n, err := t.stdout.Read(buf)
		if n > 0 {
			file.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
}

// Write 方法 - 写入音频数据
func (t *Transcoder) Write(data []byte) (int, error) {
	if !t.isRunning {
		return 0, ErrNotRunning
	}
	return t.stdin.Write(data)
}

func (t *Transcoder) Close() error {
	if !t.isRunning {
		return nil
	}

	t.isRunning = false

	t.stdin.Close() // 1. 关闭输入，FFmpeg 会收到 EOF

	t.cmd.Wait() // 2. 等待 FFmpeg 进程结束

	t.wg.Wait() // 3. 等待 goroutine 完成

	t.cancel() // 4. 取消上下文

	return nil
}

func (t *Transcoder) IsRunning() bool {
	return t.isRunning
}
