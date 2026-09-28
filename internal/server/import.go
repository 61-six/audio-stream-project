package server

import (
	"context"
	"encoding/json"
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
// POST /api/import?compilation=1&media_type=vinyl
//
// 使用方式:用户把歌曲文件(可含子目录)放到服务器的导入目录(IMPORT_DIR,
// 默认 storage/import),点网页上的「扫描导入」按钮触发。
//
// 普通模式(默认):每个文件独立入库 + 入修复队列。
// 整盘模式(?compilation=1):文件作为整盘源入库(不入修复队列),
//   探测曲间静音 → 切片成独立曲目 → 每片入修复队列;
//   源文件保留(可重新切割),切割元数据存入 split_points 字段。
//   media_type 可选(vinyl/cassette/reel),会作为介质预设传给切片的修复参数。
//
// 流程:递归扫描音频文件 → 按文件名查重(已入库跳过) → 复制到
// storage/original → ffprobe/封面/入库/入修复队列(复用 probeAndInsert)。
// 导入在后台执行,进度通过 WebSocket 推送(stage=import_*),完成后前端刷新列表。
// 采用复制而非移动:导入目录中的原文件保持不动,可安全重复扫描。
func (s *Server) importScan(c *gin.Context) {
	// 整盘模式开关:用户在前端勾选"整盘导入"后传 ?compilation=1
	compilation := c.Query("compilation") == "1"
	// 介质类型:整盘导入时用于切片的修复参数预设(vinyl/cassette/reel)
	mediaType := c.Query("media_type")
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
			}, compilation, mediaType); err != nil {
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
// 参数:
//   - onCopy: 复制过程中按文件内百分比(0-100)回调,推送字节级进度
//   - compilation: 整盘模式(true=作整盘源切割,false=普通入库)
//   - mediaType: 介质类型(整盘模式传给切片修复参数预设)
//
// 普通模式:复制 → 入库 → 入修复队列。
// 整盘模式:复制 → 入库(不入队)→ 切片 → 每片入库入队。
func (s *Server) importOne(srcAbs, displayName string, onCopy func(filePct int), compilation bool, mediaType string) error {
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

	// 构造 repair_params JSON(含介质类型);空介质时不传参数,用默认
	repairParamsJSON := ""
	if mediaType != "" {
		repairParamsJSON = fmt.Sprintf(`{"media_type":%q}`, mediaType)
	}

	// 普通模式:入修复队列;整盘模式:不入队(后续切片各自入队)
	submitQueue := !compilation
	if _, err := s.probeAndInsert(id, origRel, origAbs, displayName, repairParamsJSON, submitQueue); err != nil {
		_ = os.Remove(origAbs) // probeAndInsert 内部也会清理,双保险
		return fmt.Errorf("ingest %s: %w", displayName, err)
	}

	// 整盘模式:探测曲间静音 → 切片入库
	if compilation {
		if err := s.splitCompilation(id, origAbs, ext, displayName, mediaType, nil); err != nil {
			// 切割失败不致命:源文件已入库,用户可在前端手动重切
			log.Printf("[import] split %s failed (source still ingested): %v", displayName, err)
		}
	}
	return nil
}

// splitCompilation 对已入库的整盘源执行曲间静音探测 + 切片入库
//
// 流程:
//  1. 探测曲间静音(噪声 -50dB,最短 1.5s)— 若 points 非空则跳过,用用户调整后的点
//  2. 折算切割点 → 序列化为 JSON 存到源歌曲的 split_points 字段
//  3. ffmpeg segment muxer 按切割点切片到临时目录
//  4. 每片移动到 storage/original/<partUUID>.<ext>,入库并入修复队列
//
// 参数:
//   - points: 用户手动调整后的切割点(秒);nil 时自动探测(首次整盘导入场景)
//
// 失败处理:任一步失败都返回错误,但源歌曲已入库不回滚(用户可手动重切)。
// 切割临时目录在函数返回时清理(切片已移走)。
// 注意:不删除旧切片(若 resplit 多次),用户需在前端手动删除旧切片。
// 后续可加 parent_id 字段实现"重切自动删旧切片"。
func (s *Server) splitCompilation(sourceID, sourceAbs, ext, sourceName, mediaType string, points []float64) error {
	ctx := context.Background()

	// 1) 探测曲间静音(噪声 -50dB,最短 1.5s)— resplit 时跳过用用户调整的点
	var result *audio.SilenceDetectResult
	if len(points) > 0 {
		// resplit:用用户调整后的切割点,不重新探测
		result = &audio.SilenceDetectResult{Points: points}
	} else {
		r, err := audio.DetectSilence(ctx, sourceAbs, -50, 1.5)
		if err != nil {
			return fmt.Errorf("detect silence: %w", err)
		}
		result = r
	}
	if len(result.Points) == 0 {
		// 无切割点:整盘可能就一首,源文件已入库即可
		log.Printf("[split] %s no silence points, source ingested as-is", sourceName)
		return nil
	}

	// 2) 切割元数据存 DB(前端可读取展示/手动微调后调 resplit)
	if jsonBytes, err := json.Marshal(result); err == nil {
		if err := s.store.UpdateSplitPoints(sourceID, string(jsonBytes)); err != nil {
			log.Printf("[split] save split_points for %s: %v", sourceID, err)
		}
	}

	// 3) 切片到临时目录(避免与 final 路径冲突)
	splitDir := filepath.Join(s.cfg.StoragePath, "original", "_split_"+sourceID)
	if err := os.MkdirAll(splitDir, 0o755); err != nil {
		return fmt.Errorf("mkdir split dir: %w", err)
	}
	defer os.RemoveAll(splitDir) // 切片已移走,清理临时目录

	// segment muxer 的输出模板:%03d 会被替换成 000/001/...
	pattern := filepath.Join(splitDir, fmt.Sprintf("part_%%03d%s", ext))
	files, err := audio.SplitAtPoints(ctx, sourceAbs, pattern, result.Points)
	if err != nil {
		return fmt.Errorf("split: %w", err)
	}

	// 4) 每片移动到正式位置 → 入库 → 入修复队列
	for i, f := range files {
		partID := uuid.NewString()
		partRel := fmt.Sprintf("original/%s%s", partID, ext)
		partAbs := filepath.Join(s.cfg.StoragePath, partRel)
		if err := os.Rename(f, partAbs); err != nil {
			// 移动失败:跳过这片,继续后续(部分切片仍可用)
			log.Printf("[split] move part %d (%s): %v", i+1, f, err)
			continue
		}
		// 切片显示名:"<源文件名> - 第 N 首"
		baseName := strings.TrimSuffix(sourceName, ext)
		partName := fmt.Sprintf("%s - 第 %d 首%s", baseName, i+1, ext)
		// 切片继承源文件的介质类型(用预设修复参数)
		repairParamsJSON := ""
		if mediaType != "" {
			repairParamsJSON = fmt.Sprintf(`{"media_type":%q}`, mediaType)
		}
		if _, err := s.probeAndInsert(partID, partRel, partAbs, partName, repairParamsJSON, true); err != nil {
			// 入库失败:该片不入库,文件残留 storage/original 由用户手动清理
			log.Printf("[split] ingest part %d: %v", i+1, err)
			_ = os.Remove(partAbs)
			continue
		}
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
