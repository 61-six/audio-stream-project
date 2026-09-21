package ffmpeg

import (
	"context"
	"fmt"
	"strings"
)

// ExtractCover 从音频文件中抽取内嵌封面(MP3/FLAC/M4A 的 attached_pic 视频流),
// 统一转码为 JPEG 写入 output 路径。
//
// 调用前应先通过 Probe 确认 info.HasCover == true;
// 文件没有内嵌封面时 ffmpeg 会返回错误(无视频流),调用方按"无封面"降级处理即可。
func ExtractCover(ctx context.Context, input, output string) error {
	args := []string{
		"-y",
		"-i", input,
		"-an",          // 丢弃音频流
		"-vframes", "1", // 只取第一帧(封面就是一张静态图)
		"-c:v", "mjpeg", // 无论内嵌是 mjpeg 还是 png,统一输出 JPEG
		output,
	}

	t := NewTranscoder(args...).WithContext(ctx)
	if err := t.Run(nil); err != nil {
		stderr := t.Stderr()
		tail := ""
		if len(stderr) > 0 {
			start := len(stderr) - 3
			if start < 0 {
				start = 0
			}
			tail = strings.Join(stderr[start:], "\n")
		}
		return fmt.Errorf("ffmpeg extract cover failed: %w\nstderr tail:\n%s", err, tail)
	}
	return nil
}
