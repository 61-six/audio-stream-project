package server

import (
	"net/http"
	"os"
	"path/filepath"

	"audio-repair-studio/internal/library"

	"github.com/gin-gonic/gin"
)

// play 流式播放(支持 HTTP Range,浏览器 <audio> 原生支持拖动进度条)
//
// GET /play/:id?mode=original|repaired
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
		if song.RepairedPath == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "repaired version not available yet"})
			return
		}
		relPath = song.RepairedPath
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
