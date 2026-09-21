package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"audio-repair-studio/internal/library"
)

// Queue 修复任务队列
//
//   - Submit 异步入队,立即返回
//   - 内部 worker pool 并发执行,数量由 config 控制
//   - 进度事件通过 ProgressBus 推送给 WebSocket 订阅者
//   - Cancel 可中断正在执行的修复任务(用于删除歌曲时停止 ffmpeg)
type Queue struct {
	store       *library.Store
	storageRoot string         // 存储根目录,用于拼接绝对路径
	concurrency int
	bus         *ProgressBus
	jobs        chan string     // 待处理 songID
	wg          sync.WaitGroup

	mu       sync.Mutex
	cancels  map[string]context.CancelFunc // songID → 取消函数(仅正在执行的)
}

// NewQueue 创建并启动队列
func NewQueue(store *library.Store, storageRoot string, concurrency int, bus *ProgressBus) *Queue {
	if concurrency < 1 {
		concurrency = 1
	}
	q := &Queue{
		store:       store,
		storageRoot: storageRoot,
		concurrency: concurrency,
		bus:         bus,
		jobs:        make(chan string, 256),
		cancels:     make(map[string]context.CancelFunc),
	}
	for i := 0; i < concurrency; i++ {
		q.wg.Add(1)
		go q.worker()
	}
	return q
}

// Submit 投递 songID 到队列。
// 队列满时返回 false(不阻塞调用方),由调用方决定重试或报错。
func (q *Queue) Submit(songID string) bool {
	select {
	case q.jobs <- songID:
		return true
	default:
		return false
	}
}

// Cancel 取消指定歌曲的修复任务(若正在执行)。
// 若任务尚未开始(还在队列里),Cancel 无法移除,需由 worker 启动时自行判断状态。
func (q *Queue) Cancel(songID string) {
	q.mu.Lock()
	cancel, ok := q.cancels[songID]
	q.mu.Unlock()
	if ok {
		cancel()
	}
}

// Stop 等待所有 worker 退出(用于优雅关闭)
func (q *Queue) Stop() {
	close(q.jobs)
	q.wg.Wait()
}

func (q *Queue) worker() {
	defer q.wg.Done()
	for songID := range q.jobs {
		q.repairOne(songID)
	}
}

func (q *Queue) repairOne(songID string) {
	// 可取消 context:删除歌曲时调用 Cancel 会触发 ffmpeg 退出
	ctx, cancel := context.WithCancel(context.Background())
	q.mu.Lock()
	q.cancels[songID] = cancel
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		delete(q.cancels, songID)
		q.mu.Unlock()
		cancel()
	}()

	// 1) 查询歌曲信息(若已被删除,GetSong 返回错误,直接退出)
	song, err := q.store.GetSong(songID)
	if err != nil {
		log.Printf("[queue] get song %s: %v", songID, err)
		return
	}

	// 2) 标记 repairing
	if err := q.store.UpdateStatus(songID, library.StatusRepairing, "", ""); err != nil {
		log.Printf("[queue] mark repairing %s: %v", songID, err)
		return
	}
	q.bus.Publish(songID, ProgressEvent{
		Status:  string(library.StatusRepairing),
		Stage:   "started",
		Percent: 0,
	})

	// 3) 构造输入输出绝对路径,解析用户自定义修复参数(空则用默认)
	params := DefaultParams()
	if song.RepairParams != "" {
		if err := json.Unmarshal([]byte(song.RepairParams), &params); err != nil {
			log.Printf("[queue] song %s repair_params invalid, using defaults: %v", songID, err)
			params = DefaultParams()
		}
	}
	inAbs := filepath.Join(q.storageRoot, song.OriginalPath)
	// 输出扩展名跟随用户选择的格式(mp3/flac/wav)
	outExt := "." + params.OutputFormat
	if params.OutputFormat == "" {
		outExt = DefaultExt()
	}
	outRel := fmt.Sprintf("repaired/%s%s", songID, outExt)
	outAbs := filepath.Join(q.storageRoot, outRel)

	// 4) 调用修复(进度解析回调)
	err = Repair(ctx, inAbs, outAbs, params, func(line string) {
		percent := parseProgressPercent(line, song.DurationMs)
		q.bus.Publish(songID, ProgressEvent{
			Status:  string(library.StatusRepairing),
			Stage:   "processing",
			Percent: percent,
			Detail:  line,
		})
	})

	if err != nil {
		// 清理半成品修复文件,避免残留
		_ = os.Remove(outAbs)
		// context 被取消(歌曲被删除)时不更新 DB(记录可能已删),只记日志
		if ctx.Err() != nil {
			log.Printf("[queue] repair %s cancelled", songID)
			return
		}
		_ = q.store.UpdateStatus(songID, library.StatusFailed, "", err.Error())
		q.bus.Publish(songID, ProgressEvent{
			Status: string(library.StatusFailed),
			Stage:  "failed",
			Error:  err.Error(),
		})
		log.Printf("[queue] repair %s failed: %v", songID, err)
		return
	}

	// 5) 更新数据库
	if err := q.store.UpdateStatus(songID, library.StatusRepaired, outRel, ""); err != nil {
		log.Printf("[queue] mark repaired %s: %v", songID, err)
		return
	}
	q.bus.Publish(songID, ProgressEvent{
		Status:  string(library.StatusRepaired),
		Stage:   "completed",
		Percent: 100,
	})
}
