package audio

import (
	"encoding/binary"
	"math"
)

const (
	SampleRate     = 16000
	Channels       = 1
	BytesPerSample = 2
)

var (
	FrameSizeMs    = 20
	FrameSizeBytes = (SampleRate * FrameSizeMs / 1000) * Channels * BytesPerSample
)

func SetFrameSizeMs(ms int) {
	FrameSizeMs = ms
	FrameSizeBytes = (SampleRate * FrameSizeMs / 1000) * Channels * BytesPerSample
}

// 帧统计结构体
type FrameStatistics struct {
	FrameCount  int
	DurationMs  int64
	TotalEnergy float64
	AvgEnergy   float64
	MaxEnergy   float64
	MinEnergy   float64
}

// 处理音频帧
func ProcessFrames(data []byte) (*FrameStatistics, []float64) {
	stats := &FrameStatistics{
		MinEnergy: math.MaxFloat64, // 初始化为最大值，便于后续比较
	}

	var energies []float64
	frameCount := len(data) / FrameSizeBytes

	for i := 0; i < frameCount; i++ {
		start := i * FrameSizeBytes
		end := start + FrameSizeBytes
		if end > len(data) {
			end = len(data)
		}

		frame := data[start:end]
		energy := calculateEnergy(frame)

		energies = append(energies, energy)
		stats.FrameCount++
		stats.TotalEnergy += energy

		if energy > stats.MaxEnergy {
			stats.MaxEnergy = energy
		}
		if energy < stats.MinEnergy {
			stats.MinEnergy = energy
		}
	}

	// 计算平均值和时长
	if stats.FrameCount > 0 {
		stats.AvgEnergy = stats.TotalEnergy / float64(stats.FrameCount)
	}

	stats.DurationMs = int64(stats.FrameCount) * int64(FrameSizeMs)

	return stats, energies
}

// 能量计算函数
func calculateEnergy(frame []byte) float64 {
	var energy float64
	sampleCount := len(frame) / BytesPerSample

	for i := 0; i < sampleCount; i++ {
		start := i * BytesPerSample
		sample := int16(binary.LittleEndian.Uint16(frame[start : start+2]))
		energy += float64(sample) * float64(sample)
	}

	if sampleCount > 0 {
		energy = math.Sqrt(energy / float64(sampleCount))
	}

	return energy
}

func GetOutputFormat() string {
	return "pcm_s16le, 16000Hz, mono"
}
