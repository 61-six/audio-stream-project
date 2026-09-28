// Package audio 音频处理核心:修复镜链 + 任务队列 + 进度推送
package audio

import (
	"context"
	"fmt"
	"strings"

	"audio-repair-studio/internal/ffmpeg"
)

// 介质类型常量:用于按介质预设覆盖修复参数(黑胶/磁带/开盘带)
//
// 介质预设聚焦老录音数字化场景:不同介质的噪声形态与修复重点不同,
// 用户在前端选介质后,服务端 applyMediaPreset 会按介质覆盖相关字段,
// 用户仍可单独调 OutputFormat(输出格式不在预设覆盖范围内)。
const (
	MediaVinyl    = "vinyl"    // 黑胶:爆音/划痕为主,中等底噪
	MediaCassette = "cassette" // 磁带:嘶声为主,高降噪
	MediaReel     = "reel"     // 开盘带:音质较好,轻降噪,不动响度
)

// RepairParams 修复参数(目前采用标准镜链,后续可扩展)
type RepairParams struct {
	EnableDenoise    bool    `json:"enable_denoise"`    // 启用降噪
	DenoiseStrength  float64 `json:"denoise_strength"`  // 降噪强度 0.0-1.0,默认 0.3
	EnableDeclick   bool    `json:"enable_declick"`    // 去点击声/爆音
	EnableLoudnorm   bool    `json:"enable_loudnorm"`   // 响度归一(EBU R128)
	TargetLoudness   float64 `json:"target_loudness"`   // 目标响度 LUFS,默认 -16
	EnableResample   bool    `json:"enable_resample"`   // 重采样到目标采样率
	TargetSampleRate int     `json:"target_sample_rate"` // 目标采样率,默认 48000
	OutputFormat     string  `json:"output_format"`     // 输出格式:wav/flac/mp3,默认 flac
	MediaType        string  `json:"media_type"`        // 介质类型:vinyl/cassette/reel,空=默认标准链
}

// DefaultParams 标准镜链参数(适合大多数流行音乐)
func DefaultParams() RepairParams {
	return RepairParams{
		EnableDenoise:    true,
		DenoiseStrength:  0.3,
		EnableDeclick:    true,
		EnableLoudnorm:   true,
		TargetLoudness:   -16,
		EnableResample:   true,
		TargetSampleRate: 48000,
		OutputFormat:     "flac",
	}
}

// applyMediaPreset 按介质类型覆盖修复参数(在 Repair 入口调用)。
//
// 覆盖范围:降噪强度/去爆音/响度归一/重采样。
// 不覆盖:OutputFormat(让用户保留输出格式选择权)。
// 介质为空或未知时返回 false,调用方按用户参数原样使用。
//
// 设计取舍:不做"介质预设完全覆盖用户参数"是因为用户可能想微调,
// 只覆盖与介质强相关的字段更灵活;但去爆音这类介质强相关开关直接覆盖,
// 避免用户选"黑胶"却忘了开 declick 导致划痕没去掉。
func applyMediaPreset(p *RepairParams) bool {
	switch p.MediaType {
	case MediaVinyl:
		// 黑胶:底噪中等,划痕/爆音是主要问题,必须开 declick
		p.EnableDenoise = true
		p.DenoiseStrength = 0.4
		p.EnableDeclick = true
		p.EnableLoudnorm = true
		p.TargetLoudness = -16
		p.EnableResample = true
		p.TargetSampleRate = 48000
		return true
	case MediaCassette:
		// 磁带:嘶声重,降噪强度提到 0.6;
		// 磁带爆音少见,关掉 declick 避免误伤人声瞬态
		p.EnableDenoise = true
		p.DenoiseStrength = 0.6
		p.EnableDeclick = false
		p.EnableLoudnorm = true
		p.TargetLoudness = -16
		p.EnableResample = true
		p.TargetSampleRate = 48000
		return true
	case MediaReel:
		// 开盘带:音质本身较好,轻降噪即可,不动响度(保留原始动态)
		p.EnableDenoise = true
		p.DenoiseStrength = 0.2
		p.EnableDeclick = false
		p.EnableLoudnorm = false
		p.EnableResample = true
		p.TargetSampleRate = 48000
		return true
	}
	return false
}

// Repair 执行修复(输入文件 → 输出文件)
// onProgress 用于上报 stderr 进度行(队列订阅器解析后转发到 WebSocket)
func Repair(ctx context.Context, input, output string, p RepairParams, onProgress func(line string)) error {
	// 介质预设:若用户指定了介质类型(vinyl/cassette/reel),
	// 覆盖与介质强相关的字段(降噪强度/去爆音/响度归一等)
	applyMediaPreset(&p)
	filters := buildFilterChain(p)
	args := buildArgs(input, output, filters, p)

	t := ffmpeg.NewTranscoder(args...).WithContext(ctx)
	if err := t.Run(onProgress); err != nil {
		stderr := t.Stderr()
		tail := ""
		if len(stderr) > 0 {
			// 取最后 5 行做错误上下文
			start := len(stderr) - 5
			if start < 0 {
				start = 0
			}
			tail = strings.Join(stderr[start:], "\n")
		}
		return fmt.Errorf("ffmpeg repair failed: %w\nstderr tail:\n%s", err, tail)
	}
	return nil
}

// buildFilterChain 组装 -af 滤镜链字符串
// 顺序:降噪 → 去点击 → 响度归一 → 动态响度平衡(平滑音量)
func buildFilterChain(p RepairParams) string {
	var parts []string
	if p.EnableDenoise {
		// afftdn:频域降噪;nr 是降噪比例(0-1),0.3 适合日常音乐
		strength := clamp(p.DenoiseStrength, 0, 1)
		parts = append(parts, fmt.Sprintf("afftdn=nr=%.2f", strength))
	}
	if p.EnableDeclick {
		// adeclick:去除点击声/爆音(磁带/老唱片/麦克风接触不良)
		parts = append(parts, "adeclick")
	}
	if p.EnableLoudnorm {
		// loudnorm:EBU R128 响度归一;I=目标响度,LRA=响度范围,TP=真峰值上限
		target := p.TargetLoudness
		if target == 0 {
			target = -16
		}
		parts = append(parts, fmt.Sprintf("loudnorm=I=%.1f:LRA=11:TP=-1.5", target))
	}
	// dynaudnorm:动态响度平衡,平滑音量起伏(放在最后)
	parts = append(parts, "dynaudnorm=p=0.9")
	return strings.Join(parts, ",")
}

// buildArgs 组装完整 ffmpeg 命令行(不含可执行名)
func buildArgs(input, output, filters string, p RepairParams) []string {
	args := []string{"-y", "-i", input}
	if filters != "" {
		args = append(args, "-af", filters)
	}
	if p.EnableResample && p.TargetSampleRate > 0 {
		args = append(args, "-ar", fmt.Sprintf("%d", p.TargetSampleRate))
	}
	// 输出格式与编码
	format := p.OutputFormat
	if format == "" {
		format = "flac"
	}
	switch format {
	case "flac":
		args = append(args, "-c:a", "flac", "-compression_level", "5")
	case "wav":
		args = append(args, "-c:a", "pcm_s16le")
	case "mp3":
		args = append(args, "-c:a", "libmp3lame", "-b:a", "320k")
	default:
		args = append(args, "-c:a", "flac")
	}
	args = append(args, output)
	return args
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// FormatExt 输出格式对应扩展名
func FormatExt(format string) string {
	switch format {
	case "mp3":
		return ".mp3"
	case "wav":
		return ".wav"
	default:
		return ".flac"
	}
}

// DefaultExt 默认修复产物扩展名
func DefaultExt() string { return FormatExt("flac") }
