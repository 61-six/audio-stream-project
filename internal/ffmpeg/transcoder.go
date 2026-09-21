package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"sync"
)

// Transcoder 文件级 FFmpeg 处理器(输入文件 → 输出文件)
//
// 与原 audio-stream-project 的 stdin/stdout 版本不同,这里直接处理落盘文件,
// 更适合入库后批量修复的场景。
type Transcoder struct {
	args      []string // 完整 ffmpeg 参数(不含可执行文件名)
	ctx       context.Context
	cancel    context.CancelFunc
	cmd       *exec.Cmd
	mu        sync.Mutex
	stderrBuf []string
	runErr    error
	done      chan struct{}
}

// ErrNotRunning 处理器未运行
var ErrNotRunning = errors.New("transcoder not running")

// NewTranscoder 构造处理器,传入完整 ffmpeg 参数(不含可执行名)
// 例如:["-i", "in.mp3", "-af", "afftdn", "out.flac"]
func NewTranscoder(args ...string) *Transcoder {
	ctx, cancel := context.WithCancel(context.Background())
	return &Transcoder{
		args:   args,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

// WithContext 注入外部 context(用于超时/取消控制)
func (t *Transcoder) WithContext(ctx context.Context) *Transcoder {
	ctx, cancel := context.WithCancel(ctx)
	t.ctx = ctx
	t.cancel = cancel
	return t
}

// Run 启动 ffmpeg,阻塞直到完成,返回最终错误
// onProgress 回调收到 stderr 行(调用方可解析 time= 进度信息)
func (t *Transcoder) Run(onProgress func(line string)) error {
	t.cmd = exec.CommandContext(t.ctx, "ffmpeg", t.args...)

	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := t.cmd.Start(); err != nil {
		return err
	}

	// 后台读取 stderr(包含进度信息和错误日志)
	go func() {
		defer close(t.done)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 进度行较长
		for scanner.Scan() {
			line := scanner.Text()
			t.mu.Lock()
			t.stderrBuf = append(t.stderrBuf, line)
			t.mu.Unlock()
			if onProgress != nil {
				onProgress(line)
			}
		}
		if err := scanner.Err(); err != nil {
			t.mu.Lock()
			t.runErr = errors.Join(t.runErr, err)
			t.mu.Unlock()
		}
	}()

	// 等待 ffmpeg 退出
	if err := t.cmd.Wait(); err != nil {
		<-t.done
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.runErr != nil {
			return t.runErr
		}
		return err
	}
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runErr
}

// Cancel 取消处理(会触发 ffmpeg 退出)
func (t *Transcoder) Cancel() {
	t.cancel()
}

// Stderr 返回 stderr 行副本(用于错误诊断)
func (t *Transcoder) Stderr() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.stderrBuf))
	copy(out, t.stderrBuf)
	return out
}
