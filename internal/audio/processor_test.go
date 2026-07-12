package audio

import (
	"testing"
)

func TestFrameSizeBytes(t *testing.T) {
	if FrameSizeBytes != 640 {
		t.Errorf("Expected frame size 640, got %d", FrameSizeBytes)
	}
}

func TestProcessFrames(t *testing.T) {
	data := make([]byte, 10*640)
	for i := range data {
		data[i] = 128
	}

	stats, energies := ProcessFrames(data)

	if stats.FrameCount != 10 {
		t.Errorf("Expected 10 frames, got %d", stats.FrameCount)
	}

	if len(energies) != 10 {
		t.Errorf("Expected 10 energy values, got %d", len(energies))
	}
}

func TestCalculateEnergy(t *testing.T) {
	data := make([]byte, 4)
	data[0] = 100
	data[1] = 0
	data[2] = 100
	data[3] = 0

	energy := calculateEnergy(data)
	if energy == 0 {
		t.Error("Expected energy > 0")
	}
}

func TestGetOutputFormat(t *testing.T) {
	format := GetOutputFormat()
	if format != "pcm_s16le, 16000Hz, mono" {
		t.Errorf("Expected pcm_s16le, 16000Hz, mono, got %s", format)
	}
}
