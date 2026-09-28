package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"audio-repair-studio/internal/library"

	"github.com/gin-gonic/gin"
)

// play 流式播放(支持 HTTP Range,浏览器 <audio> 原生支持拖动进度条)
//
// GET /play/:id?mode=original|repaired&version=N
// mode=repaired 时不传 version 则播放 current_version 指向的版本
func (s *Server) play(c *gin.Context) {
	id := c.Param("id")
	mode := library.Mode(c.DefaultQuery("mode", string(library.ModeRepaired)))

	song, err := s.store.GetSong(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "song not found"})
		return
	}

	// 根据模式选择文件路径
	var relPath string
	switch mode {
	case library.ModeOriginal:
		relPath = song.OriginalPath
	case library.ModeRepaired:
		// 支持按版本播放:?version=N 指定版本,不传则用 current_version
		if vStr := c.Query("version"); vStr != "" {
			ver, err := strconv.Atoi(vStr)
			if err != nil || ver < 1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid version"})
				return
			}
			rv, err := s.store.GetRepairVersion(id, ver)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
				return
			}
			relPath = rv.Path
		} else if song.RepairedPath != "" {
			relPath = song.RepairedPath // current_version 对应的路径(由 queue/切换时写入)
		} else {
			c.JSON(http.StatusNotFound, gin.H{"error": "repaired version not available yet"})
			return
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mode"})
		return
	}

	abs := filepath.Join(s.cfg.StoragePath, relPath)
	if _, err := os.Stat(abs); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file missing on disk"})
		return
	}

	// http.ServeFile 内部已实现 Range 协议 + Content-Type 自动识别
	http.ServeFile(c.Writer, c.Request, abs)
}
