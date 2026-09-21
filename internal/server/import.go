package server

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"audio-repair-studio/internal/audio"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// importing 防止重复触发扫描导入(单机单用户,原子标志足够)
var importing atomic.Bool

// importScan 扫描导入目录并批量入库
//
// POST /api/import
//
// 使用方式:用户把歌曲文件(可含子目录)放到服务器的导入目录(IMPORT_DIR,
// 默认 storage/import),点网页上的「扫描导入」按钮触发。
//
// 流程:递归扫描音频文件 → 按文件名查重(已入库跳过) → 复制到
// storage/original → ffprobe/封面/入库/入修复队列(复用 probeAndInsert)。
// 导入在后台执行,进度通过 WebSocket 推送(stage=import_*),完成后前端刷新列表。
// 采用复制而非移动:导入目录中的原文件保持不动,可安全重复扫描。
func (s *Server) importScan(c *gin.Context) {
	dir := s.cfg.ImportDir()

	// 1) 递归收集音频文件
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			log.Printf("[import] walk %s: %v", path, walkErr)
			return nil // 单个条目出错不终止整体扫描
		}
		if d.IsDir() {
			return nil
		}
		if allowedExt[strings.ToLower(filepath.Ext(path))] {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "scan failed", "detail": err.Error()})
		return
	}

	// 2) 空目录:返回目录路径,前端提示用户把歌放到哪里
	if len(files) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"dir":     dir,
			"found":   0,
			"message": "import directory has no audio files",
		})
		return
	}

	// 3) 已有导入任务在跑?
	if !importing.CompareAndSwap(false, true) {
		c.JSON(http.StatusConflict, gin.H{"error": "import already running"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"dir":     dir,
		"found":   len(files),
		"message": "import started",
	})

	// 4) 后台逐个导入,进度走 WebSocket(songID 为空,前端按 stage 识别)
	go func() {
		defer importing.Store(false)

		var imported, skipped, failed int
		s.bus.Publish("", audio.ProgressEvent{Stage: "import_start", Percent: 0})

		for i, src := range files {
			name := filepath.Base(src)

			// 按 文件名+文件大小 去重,避免同名不同歌被误跳过
			exists, err := s.store.ExistsByNameAndSize(name, fileSize(src))
			if err != nil {
				log.Printf("[import] dedup check %s: %v", name, err)
			}
			if exists {
				skipped++
			} else if err := s.importOne(src, name, func(filePct int) {
				// 文件内复制进度 → 折算到整体进度(0-100),前端浮层平滑走动
				s.bus.Publish("", audio.ProgressEvent{
					Stage:   "import_copy",
					Percent: (i*100 + filePct) / len(files),
					Detail:  name,
				})
			}); err != nil {
				failed++
				log.Printf("[import] %s failed: %v", name, err)
			} else {
				imported++
			}

			s.bus.Publish("", audio.ProgressEvent{
				Stage:   "import_progress",
				Percent: (i + 1) * 100 / len(files),
				Detail:  name,
			})
		}

		s.bus.Publish("", audio.ProgressEvent{
			Stage:   "import_done",
			Percent: 100,
			Detail:  fmt.Sprintf("导入 %d 首,跳过 %d 首,失败 %d 首", imported, skipped, failed),
		})
		log.Printf("[import] finished: imported=%d skipped=%d failed=%d", imported, skipped, failed)
	}()
}

// importOne 复制单个文件到存储区并走统一入库流程
//
// onCopy 可选回调:复制过程中按文件内百分比(0-100)回调,
// 用于推送字节级进度(大文件复制时浮层不再卡住不动)。
func (s *Server) importOne(srcAbs, displayName string, onCopy func(filePct int)) error {
	ext := strings.ToLower(filepath.Ext(displayName))
	id := uuid.NewString()
	origRel := fmt.Sprintf("original/%s%s", id, ext)
	origAbs := filepath.Join(s.cfg.StoragePath, origRel)

	if err := copyFile(srcAbs, origAbs, func(copied, total int64) {
		if onCopy != nil && total > 0 {
			onCopy(int(copied * 100 / total))
		}
	}); err != nil {
		// 复制中途失败(磁盘满/IO 错误)时目标文件是半成品,必须清理,
		// 否则 storage/original 会残留垃圾文件
		_ = os.Remove(origAbs)
		return fmt.Errorf("copy %s: %w", displayName, err)
	}

	if _, err := s.probeAndInsert(id, origRel, origAbs, displayName, ""); err != nil {
		_ = os.Remove(origAbs) // probeAndInsert 内部也会清理,双保险
		return fmt.Errorf("ingest %s: %w", displayName, err)
	}
	return nil
}

// copyFile 本地文件复制(用于目录导入,不移动源文件)
//
// onProgress 可选:每跨过一个整数百分比回调一次 copied/total;
// 采用 1MB 缓冲分段读写,而不是 io.Copy 一把梭,以支持大文件进度上报。
func copyFile(src, dst string, onProgress func(copied, total int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	fi, err := in.Stat()
	if err != nil {
		return err
	}
	total := fi.Size()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, 1<<20) // 1MB
	var copied int64
	lastPct := -1
	for {
		nr, readErr := in.Read(buf)
		if nr > 0 {
			nw, writeErr := out.Write(buf[0:nr])
			if nw < nr || writeErr != nil {
				if writeErr != nil {
					return writeErr
				}
				return io.ErrShortWrite
			}
			copied += int64(nw)
			// 整数百分比变化时才回调,避免大量 WS 消息
			if onProgress != nil && total > 0 {
				if pct := int(copied * 100 / total); pct > lastPct {
					onProgress(copied, total)
					lastPct = pct
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return out.Sync()
}
