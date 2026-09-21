// Package ffmpeg 封装 FFmpeg / FFprobe 进程调用
package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// AudioInfo 解析得到的音频元信息
type AudioInfo struct {
	Format     string // 容器格式(mp3/flac/wav/...)
	Codec      string // 音频编码(mp3/aac/flac/pcm_s16le/...)
	Bitrate    int    // 比特率(kbps,展示用)
	DurationMs int64  // 时长(毫秒)
	SampleRate int    // 采样率
	Channels   int    // 声道数
	Title      string // 标签:标题
	Artist     string // 标签:艺术家
	Album      string // 标签:专辑
	Year       int    // 标签:年份
	HasCover   bool   // 是否包含内嵌封面(视频流 / attached_pic)
}

// probeOutput ffprobe JSON 输出结构(仅取需要的字段)
type probeOutput struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		SampleRate string `json:"sample_rate"`
		Channels   int    `json:"channels"`
		BitRate    string `json:"bit_rate"`
		Duration   string `json:"duration"`
	} `json:"streams"`
	Format struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		BitRate    string            `json:"bit_rate"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
}

// Probe 调用 ffprobe 解析音频文件元信息
func Probe(ctx context.Context, input string) (*AudioInfo, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		input,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}
	var po probeOutput
	if err := json.Unmarshal(out, &po); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	info := &AudioInfo{Format: po.Format.FormatName}
	// 遍历所有流:第一个音频流取主信息;视频流即内嵌封面(MP3/FLAC 的 attached_pic)
	for _, s := range po.Streams {
		switch s.CodecType {
		case "audio":
			if info.Codec != "" {
				continue // 已取过第一个音频流
			}
			info.Codec = s.CodecName
			info.SampleRate, _ = strconv.Atoi(s.SampleRate)
			info.Channels = s.Channels
			if s.BitRate != "" {
				info.Bitrate, _ = strconv.Atoi(s.BitRate)
			}
			if s.Duration != "" {
				info.DurationMs = parseDurationMs(s.Duration)
			}
		case "video":
			// 音频文件里的视频流几乎都是内嵌封面
			info.HasCover = true
		}
	}
	// format 级别兜底
	if info.DurationMs == 0 && po.Format.Duration != "" {
		info.DurationMs = parseDurationMs(po.Format.Duration)
	}
	if info.Bitrate == 0 && po.Format.BitRate != "" {
		info.Bitrate, _ = strconv.Atoi(po.Format.BitRate)
	}
	// 转换为 kbps 展示
	info.Bitrate = info.Bitrate / 1000

	// 标签(键名大小写不敏感)
	for k, v := range po.Format.Tags {
		switch strings.ToLower(k) {
		case "title":
			info.Title = v
		case "artist":
			info.Artist = v
		case "album":
			info.Album = v
		case "date", "year":
			info.Year, _ = strconv.Atoi(v)
		}
	}
	return info, nil
}

// parseDurationMs 把 "123.456" 秒转成毫秒整数
func parseDurationMs(s string) int64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f * 1000)
}
