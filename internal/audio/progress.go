package audio

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ProgressEvent 进度事件(发给 WebSocket 订阅者)
type ProgressEvent struct {
	SongID  string    `json:"song_id"`
	Version int       `json:"version,omitempty"` // 修复版本号(时光机功能,0=旧逻辑)
	Status  string    `json:"status"`             // repairing/repaired/failed
	Stage   string    `json:"stage"`              // started/processing/completed/failed
	Percent int       `json:"percent"`            // 0-100
	Detail  string    `json:"detail"`              // ffmpeg stderr 行(可选)
	Error   string    `json:"error"`              // 失败原因
	Sent    time.Time `json:"sent"`
}

// ProgressBus 进度事件总线
//
// 每个 WebSocket 连接订阅所有进度事件(简单实现,后续可按 songID 过滤)。
type ProgressBus struct {
	mu    sync.RWMutex
	chans map[chan ProgressEvent]struct{}
}

// NewProgressBus 创建事件总线
func NewProgressBus() *ProgressBus {
	return &ProgressBus{chans: make(map[chan ProgressEvent]struct{})}
}

// Subscribe 订阅,返回接收 channel;不再订阅时调用 Unsubscribe
func (b *ProgressBus) Subscribe() chan ProgressEvent {
	ch := make(chan ProgressEvent, 64)
	b.mu.Lock()
	b.chans[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe 取消订阅并关闭 channel
func (b *ProgressBus) Unsubscribe(ch chan ProgressEvent) {
	b.mu.Lock()
	if _, ok := b.chans[ch]; ok {
		delete(b.chans, ch)
		close(ch)
	}
	b.mu.Unlock()
}

// Publish 广播进度事件(非阻塞,channel 满了直接丢弃,避免 worker 阻塞)
func (b *ProgressBus) Publish(songID string, ev ProgressEvent) {
	ev.SongID = songID
	ev.Sent = time.Now()
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.chans {
		select {
		case ch <- ev:
		default:
			// 订阅者消费过慢,丢掉这条避免阻塞 worker
		}
	}
}

// parseProgressPercent 从 ffmpeg stderr 行中解析当前进度百分比
//
// ffmpeg 进度行示例:"frame= 1234 fps=30 q=-1.0 size= 1024kB time=00:01:23.45 ..."
func parseProgressPercent(line string, durationMs int64) int {
	if durationMs <= 0 {
		return 0
	}
	idx := strings.Index(line, "time=")
	if idx < 0 {
		return 0
	}
	rest := line[idx+5:]
	end := strings.IndexByte(rest, ' ')
	if end < 0 {
		end = len(rest)
	}
	currentMs := parseTimeToMs(rest[:end])
	if currentMs <= 0 {
		return 0
	}
	percent := int(currentMs * 100 / durationMs)
	if percent < 0 {
		percent = 0
	}
	if percent > 99 {
		percent = 99 // 留 100 给最终 completed
	}
	return percent
}

// parseTimeToMs 把 "00:01:23.45" 转为毫秒
func parseTimeToMs(s string) int64 {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0
	}
	var h, m int64
	var sec float64
	_, _ = fmt.Sscanf(parts[0], "%d", &h)
	_, _ = fmt.Sscanf(parts[1], "%d", &m)
	_, _ = fmt.Sscanf(parts[2], "%f", &sec)
	return (h*3600+m*60+int64(sec)) * 1000
}
