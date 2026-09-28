// Package audio 整盘切割器:把一张专辑/一盘磁带的长音频
// 按曲间静音自动切成独立曲目。
//
// 工作流:
//  1. DetectSilence 用 ffmpeg silencedetect 扫描整盘,得到静音段
//  2. SplitPoints 把静音段折算成切割点(每段中点)
//  3. SplitAtPoints 用 ffmpeg segment muxer 按时间点切片成独立文件
//
// 设计取舍:
//   - 切割点取静音段中点而非起点/终点,避免切到尾音/前奏;
//   - 用 -c copy 流复制,无损且快(WAV/FLAC/MP3 都支持帧对齐切割);
//   - 切割结果(SilenceDetectResult)序列化为 JSON 存 DB,前端可手动微调
//     后调 /api/songs/:id/resplit 重新切割,源文件不删(可重切)。
package audio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"audio-repair-studio/internal/ffmpeg"
)

// SilenceSpan 静音段(秒),由 silencedetect 输出解析得到
type SilenceSpan struct {
	Start float64 `json:"start"` // 静音开始时间(秒)
	End   float64 `json:"end"`   // 静音结束时间(秒)
}

// SilenceDetectResult 切割探测结果(序列化为 JSON 存 DB)
//
// 前端可读取 Spans/Points 展示,允许用户拖动切割点后调 resplit。
type SilenceDetectResult struct {
	Spans  []SilenceSpan `json:"spans"`  // 静音段列表
	Points []float64     `json:"points"` // 切割点(秒,每段静音中点)
}

// DetectSilence 扫描音频中的曲间静音段
//
// 参数:
//   - noise: 噪声阈值 dB(常见 -50,介质底噪高于此值才算"非静音")
//   - minDuration: 最小静音时长(秒,常见 1.5,曲间至少静音这么久才算切割点)
//
// 解析 ffmpeg silencedetect 输出的 stderr 行:
//
//	silence_start: 12.345
//	silence_end: 14.567 | silence_duration: 2.222
func DetectSilence(ctx context.Context, input string, noise int, minDuration float64) (*SilenceDetectResult, error) {
	args := []string{
		"-nostats", "-i", input,
		"-af", fmt.Sprintf("silencedetect=noise=%ddB:d=%.2f", noise, minDuration),
		"-f", "null", "-",
	}
	t := ffmpeg.NewTranscoder(args...).WithContext(ctx)
	res := &SilenceDetectResult{}
	reStart := regexp.MustCompile(`silence_start:\s*([\d.]+)`)
	reEnd := regexp.MustCompile(`silence_end:\s*([\d.]+)\s*\|`)
	onLine := func(line string) {
		if m := reStart.FindStringSubmatch(line); m != nil {
			// 新静音段开始:压入一条只有 Start 的记录,等 End 行填充
			v, _ := strconv.ParseFloat(m[1], 64)
			res.Spans = append(res.Spans, SilenceSpan{Start: v})
			return
		}
		if m := reEnd.FindStringSubmatch(line); m != nil {
			// 静音段结束:补全最近一条记录的 End
			v, _ := strconv.ParseFloat(m[1], 64)
			if n := len(res.Spans); n > 0 && res.Spans[n-1].End == 0 {
				res.Spans[n-1].End = v
			}
		}
	}
	if err := t.Run(onLine); err != nil {
		return nil, fmt.Errorf("silencedetect: %w", err)
	}
	res.Points = SplitPoints(res.Spans)
	return res, nil
}

// SplitPoints 静音段折算成切割点(取每段中点,秒)
//
// 取中点而非起点/终点,避免切到上一首尾音或下一首前奏;
// 末尾静音(整盘结尾的留白)不算切割点。
func SplitPoints(spans []SilenceSpan) []float64 {
	pts := make([]float64, 0, len(spans))
	for _, s := range spans {
		if s.Start >= 0 && s.End > s.Start {
			pts = append(pts, (s.Start+s.End)/2)
		}
	}
	return pts
}

// SplitAtPoints 按时间点切割音频,输出 N+1 段独立文件
//
// 参数:
//   - outPattern: ffmpeg segment muxer 的输出模板,如 "/path/split_%03d.wav"
//     %03d 会被替换成 000/001/...
//   - points: 切割点列表(秒),N 个点产生 N+1 段
//
// 返回生成的文件绝对路径列表(按段顺序)。
// 用 -c copy 流复制:无损且快,WAV/FLAC 帧对齐;MP3 也可(按帧切割)。
func SplitAtPoints(ctx context.Context, input, outPattern string, points []float64) ([]string, error) {
	if len(points) == 0 {
		return nil, fmt.Errorf("no split points")
	}
	times := make([]string, len(points))
	for i, p := range points {
		times[i] = strconv.FormatFloat(p, 'f', 3, 64)
	}
	args := []string{
		"-y", "-i", input,
		"-f", "segment",
		"-segment_times", strings.Join(times, ","),
		"-c", "copy",
		outPattern,
	}
	t := ffmpeg.NewTranscoder(args...).WithContext(ctx)
	if err := t.Run(nil); err != nil {
		return nil, fmt.Errorf("split: %w", err)
	}
	// segment 输出按 000/001/... 命名,扫描输出目录收集实际文件
	return listSplitFiles(outPattern, len(points)+1)
}

// listSplitFiles 按 segment 序号遍历收集切割产物
//
// outPattern 含 %03d 占位符,按 0..expected-1 生成文件名,
// 返回存在的文件路径列表(顺序即段顺序)。
func listSplitFiles(outPattern string, expected int) ([]string, error) {
	dir := filepath.Dir(outPattern)
	base := filepath.Base(outPattern)
	var files []string
	for i := 0; i < expected; i++ {
		name := fmt.Sprintf(base, i)
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no split files generated at %s", outPattern)
	}
	return files, nil
}
