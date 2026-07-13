package ffmpeg

import (
	"bufio"
	"errors"
	"io"
	"log"
	"os/exec"
	"sync"
)

type Transcoder struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   io.ReadCloser
	output   string
	isRunning bool
	mu       sync.Mutex
	runErr   error
}

func NewTranscoder(outputPath string) *Transcoder {
	return &Transcoder{
		output: outputPath,
	}
}

func (t *Transcoder) Start() error {
	t.mu.Lock()
	if t.isRunning {
		t.mu.Unlock()
		return errors.New("transcoder already running")
	}
	t.mu.Unlock()

	t.cmd = exec.Command("ffmpeg",
		"-i", "pipe:0",
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ar", "16000",
		"-ac", "1",
		"-y",
		t.output,
	)

	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return err
	}

	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		return err
	}

	t.stderr, err = t.cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := t.cmd.Start(); err != nil {
		return err
	}

	t.mu.Lock()
	t.isRunning = true
	t.mu.Unlock()

	go t.readStdout()
	go t.readStderr()

	return nil
}

func (t *Transcoder) Write(data []byte) (int, error) {
	t.mu.Lock()
	if !t.isRunning {
		t.mu.Unlock()
		return 0, ErrTranscoderNotStarted
	}
	t.mu.Unlock()

	if t.stdin == nil {
		return 0, ErrTranscoderNotStarted
	}

	return t.stdin.Write(data)
}

func (t *Transcoder) Close() error {
	t.mu.Lock()
	if !t.isRunning {
		t.mu.Unlock()
		return nil
	}
	t.isRunning = false
	t.mu.Unlock()

	if t.stdin != nil {
		t.stdin.Close()
	}

	var errs []error
	if t.cmd != nil {
		if err := t.cmd.Wait(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				if exitErr.ExitCode() != 0 && exitErr.ExitCode() != 255 {
					errs = append(errs, err)
				}
			} else {
				errs = append(errs, err)
			}
		}
	}

	t.mu.Lock()
	if t.runErr != nil {
		errs = append(errs, t.runErr)
	}
	t.mu.Unlock()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (t *Transcoder) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.isRunning
}

func (t *Transcoder) readStdout() {
	scanner := bufio.NewScanner(t.stdout)
	for scanner.Scan() {
		log.Printf("[FFmpeg stdout] %s", scanner.Text())
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.mu.Lock()
		t.runErr = errors.Join(t.runErr, err)
		t.mu.Unlock()
	}
}

func (t *Transcoder) readStderr() {
	scanner := bufio.NewScanner(t.stderr)
	for scanner.Scan() {
		log.Printf("[FFmpeg stderr] %s", scanner.Text())
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.mu.Lock()
		t.runErr = errors.Join(t.runErr, err)
		t.mu.Unlock()
	}
}