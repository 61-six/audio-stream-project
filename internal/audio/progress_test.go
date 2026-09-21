package audio

import "testing"

// TestParseTimeToMs 测试 ffmpeg 时间戳 "HH:MM:SS.ss" → 毫秒
func TestParseTimeToMs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int64
	}{
		{"正常", "00:01:23.45", 83000},                 // 83s(小数秒截断取整)
		{"带小时", "01:02:03.00", 3723000},            // 3600+120+3 = 3723s
		{"零", "00:00:00.00", 0},
		{"段数不对", "1:2:3:4", 0},
		{"非数字", "abc", 0},
		{"空串", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseTimeToMs(tc.in); got != tc.want {
				t.Errorf("parseTimeToMs(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseProgressPercent 测试从 ffmpeg stderr 行解析修复进度百分比
func TestParseProgressPercent(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		durationMs int64
		want       int
	}{
		{"50%", "frame=100 fps=10 time=00:00:30.00 bitrate=128k", 60000, 50},
		{"无 time=", "frame=100 fps=10", 60000, 0},
		{"总时长为0", "time=00:00:30.00", 0, 0},
		{"超过时长封顶99", "time=00:01:30.00", 60000, 99},
		{"time 在行尾", "frame=100 time=00:00:06.00", 60000, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseProgressPercent(tc.line, tc.durationMs); got != tc.want {
				t.Errorf("parseProgressPercent(%q, %d) = %d, want %d",
					tc.line, tc.durationMs, got, tc.want)
			}
		})
	}
}
