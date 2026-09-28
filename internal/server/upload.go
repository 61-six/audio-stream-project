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

	"audio-repair-studio/internal/audio"
	"audio-repair-studio/internal/ffmpeg"
	"audio-repair-studio/internal/library"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// allowedExt 允许上传的音频扩展名(白名单)
var allowedExt = map[string]bool{
	".mp3": true, ".flac": true, ".wav": true, ".m4a": true,
	".aac": true, ".ogg": true, ".wma": true,
}

// maxUploadBytes 单文件上传上限 500MB(音频文件合理上限,防止撑爆磁盘)
const maxUploadBytes = 500 << 20 // 500 * 1024 * 1024

// upload 处理文件上传
//
// 流程:接收 → ffprobe 解析元信息 → 落盘 → 入库 → 入修复队列
func (s *Server) upload(c *gin.Context) {
	// 限制请求体大小,超限返回 413,防止大文件撑爆磁盘
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		// MaxBytesReader 超限时返回错误,统一提示
		if strings.Contains(err.Error(), "request body too large") {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file too large, max 500MB"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file field 'file'"})
		return
	}
	defer file.Close()

	// 可选:修复参数(JSON 字符串),不传则用默认参数
	repairParamsJSON := strings.TrimSpace(c.PostForm("repair_params"))

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedExt[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported format"})
		return
	}

	id := uuid.NewString()
	origRel := fmt.Sprintf("original/%s%s", id, ext)
	origAbs := filepath.Join(s.cfg.StoragePath, origRel)

	// 落盘
	dst, err := os.Create(origAbs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "save failed",
			"path":   origAbs,
			"detail": err.Error(),
		})
		return
	}
	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		_ = os.Remove(origAbs)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "write failed",
			"path":   origAbs,
			"detail": err.Error(),
		})
		return
	}
	dst.Close()

	// 探测元信息 → 抽封面 → 入库 → 入修复队列(与目录导入共用)
	song, err := s.probeAndInsert(id, origRel, origAbs, header.Filename, repairParamsJSON, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "save to db failed",
			"detail": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":      id,
		"status":  string(library.StatusPending),
		"message": "uploaded, repair queued",
		"song":    song,
	})
}

// probeAndInsert 对一个已落盘到 storage/original 的音频文件执行:
// ffprobe 解析元信息 → 抽取内嵌封面 → 写入 SQLite → 投递修复队列。
//
// 网页上传与「扫描目录导入」两条链路共用此逻辑,保证入库行为一致。
// repairParamsJSON 为可选修复参数(JSON),空串或非法时用默认参数。
// submitQueue 控制是否入修复队列:整盘源不入队(只修切片,不重复修整盘)。
// 失败时负责清理已落盘的原始文件/封面;成功后返回歌曲实体。
func (s *Server) probeAndInsert(id, origRel, origAbs, displayName, repairParamsJSON string, submitQueue bool) (*library.Song, error) {
	ctx := context.Background()
	ext := strings.ToLower(filepath.Ext(displayName))

	// 校验修复参数:非法则用空串(队列端会降级为默认参数)
	// 同时解析出介质类型存到独立列(media_type),便于前端展示与按介质筛选
	mediaType := ""
	if repairParamsJSON != "" {
		var p audio.RepairParams
		if err := json.Unmarshal([]byte(repairParamsJSON), &p); err != nil {
			log.Printf("[ingest] invalid repair_params for %s: %v, using defaults", displayName, err)
			repairParamsJSON = ""
		} else {
			mediaType = p.MediaType
		}
	}

	// ffprobe 解析元信息(失败不致命,降级为空值)
	info, err := ffmpeg.Probe(ctx, origAbs)
	if err != nil {
		log.Printf("[ingest] ffprobe %s failed: %v", displayName, err)
		info = &ffmpeg.AudioInfo{Format: strings.TrimPrefix(ext, ".")}
	}

	// 抽取内嵌封面(失败不致命:无封面或 ffmpeg 报错都降级为无封面,前端用占位图兜底)
	coverRel := ""
	if info.HasCover {
		coverRel = fmt.Sprintf("covers/%s.jpg", id)
		if err := ffmpeg.ExtractCover(ctx, origAbs, filepath.Join(s.cfg.StoragePath, coverRel)); err != nil {
			log.Printf("[ingest] extract cover %s failed: %v", id, err)
			coverRel = ""
		}
	}

	// 入库
	song := &library.Song{
		ID:               id,
		Title:            firstNonEmpty(info.Title, strings.TrimSuffix(displayName, ext)),
		Artist:           info.Artist,
		Album:            info.Album,
		Year:             info.Year,
		OriginalFilename: displayName,
		OriginalPath:     origRel,
		OriginalSize:     fileSize(origAbs),
		CoverPath:        coverRel,
		DurationMs:       info.DurationMs,
		SampleRate:       info.SampleRate,
		Channels:         info.Channels,
		Bitrate:          info.Bitrate,
		Codec:            info.Codec,
		RepairParams:     repairParamsJSON,
		MediaType:        mediaType,
		Status:           library.StatusPending,
	}
	if err := s.store.AddSong(song); err != nil {
		_ = os.Remove(origAbs)
		if coverRel != "" {
			_ = os.Remove(filepath.Join(s.cfg.StoragePath, coverRel))
		}
		return nil, err
	}

	// 入修复队列(队列满时记录日志,歌曲保持 pending 可手动重试)
	// 整盘源跳过入队:整盘源只作切割源,后续切片各自入队修复
	if submitQueue {
		// 创建初始版本记录(v1)
		version, verr := s.store.CreateRepairVersion(id, song.RepairParams, "")
		if verr != nil {
			log.Printf("[ingest] create repair version for %s: %v", id, verr)
		}
		if !s.queue.Submit(id, version) {
			log.Printf("[ingest] repair queue full, song %s stays pending", id)
		}
	}
	return song, nil
}

// firstNonEmpty 返回第一个非空字符串
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// fileSize 返回文件大小(字节),失败返回 0
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
