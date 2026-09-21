package server

import (
	"strings"
	"testing"
)

// TestSanitizeFilename 测试跨系统非法字符清洗
func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"Windows非法字符", `a/b:c*d?.mp3`, "a_b_c_d_.mp3"},
		{"首尾空格裁剪", "  name.mp3  ", "name.mp3"},
		{"空串兜底", "", "audio"},
		{"纯点兜底", "....", "audio"},
		{"控制字符移除", "a\x00b.mp3", "ab.mp3"},
		{"点结尾裁剪", "song.mp3.", "song.mp3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeFilename(tc.in); got != tc.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBuildContentDisposition 测试 RFC 5987 下载头构造
func TestBuildContentDisposition(t *testing.T) {
	t.Run("ASCII文件名", func(t *testing.T) {
		got := buildContentDisposition("song.mp3")
		want := `attachment; filename="song.mp3"; filename*=UTF-8''song.mp3`
		if got != want {
			t.Errorf("got:\n  %s\nwant:\n  %s", got, want)
		}
	})

	t.Run("中文文件名走百分号编码", func(t *testing.T) {
		got := buildContentDisposition("歌曲.flac")
		// ASCII 兜底部分:中文替换为下划线
		if !strings.Contains(got, `filename="__.flac"`) {
			t.Errorf("缺少 ASCII 兜底 filename, got: %s", got)
		}
		// filename* 部分:"歌" 的 UTF-8 编码为 E6AD8C
		if !strings.Contains(got, "filename*=UTF-8''%E6%AD%8C") {
			t.Errorf("缺少 RFC5987 编码的 filename*, got: %s", got)
		}
	})

	t.Run("引号被替换", func(t *testing.T) {
		got := buildContentDisposition(`a"b\c.mp3`)
		if !strings.Contains(got, `filename="a_b_c.mp3"`) {
			t.Errorf("引号/反斜杠应替换为下划线, got: %s", got)
		}
	})
}
