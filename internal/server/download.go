package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"audio-repair-studio/internal/library"

	"github.com/gin-gonic/gin"
)

// download 下载音频文件(原音 / 修复版,支持按版本下载)
//
// GET /api/songs/:id/download?mode=repaired|original&version=N
//
// 与 /play 的区别:通过 Content-Disposition: attachment 强制浏览器
// 弹出"另存为",而不是在线播放;同样支持 HTTP Range(断点续传)。
func (s *Server) download(c *gin.Context) {
	id := c.Param("id")
	mode := library.Mode(c.DefaultQuery("mode", string(library.ModeRepaired)))

	song, err := s.store.GetSong(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "song not found"})
		return
	}

	// 根据模式选择文件与文件名后缀
	var relPath, suffix string
	switch mode {
	case library.ModeOriginal:
		relPath = song.OriginalPath
		suffix = "原版"
	case library.ModeRepaired:
		// 支持按版本下载:?version=N 指定版本
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
			suffix = fmt.Sprintf("修复版v%d", ver)
		} else if song.RepairedPath != "" {
			relPath = song.RepairedPath
			suffix = "修复版"
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

	// 构造下载文件名:<歌名>-<原版|修复版>.<扩展名>
	base := firstNonEmpty(song.Title, strings.TrimSuffix(song.OriginalFilename, filepath.Ext(song.OriginalFilename)))
	filename := sanitizeFilename(fmt.Sprintf("%s-%s%s", base, suffix, filepath.Ext(relPath)))

	// Content-Disposition 必须在 ServeFile 之前设置(标准库不会覆盖该头)
	// 中文文件名走 RFC 5987 的 filename*;filename 提供 ASCII 兜底
	c.Header("Content-Disposition", buildContentDisposition(filename))

	http.ServeFile(c.Writer, c.Request, abs)
}

// sanitizeFilename 去除/替换各操作系统文件名中的非法字符
func sanitizeFilename(name string) string {
	// Windows 保留字符: \ / : * ? " < > | ,以及控制字符
	replacer := strings.NewReplacer(
		`\`, "_", "/", "_", ":", "_", "*", "_",
		`?`, "_", `"`, "_", "<", "_", ">", "_", "|", "_",
	)
	name = replacer.Replace(name)
	// 去掉控制字符
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	name = strings.TrimSpace(b.String())
	name = strings.Trim(name, ".") // Windows 不允许以点结尾
	if name == "" {
		name = "audio"
	}
	return name
}

// buildContentDisposition 构造 Content-Disposition 头值
//
// 形如: attachment; filename="song-repaired.flac"; filename*=UTF-8''%E6%AD%8C...
// 现代浏览器优先使用 filename*(支持中文),旧浏览器回退到 filename。
func buildContentDisposition(filename string) string {
	// ASCII 兜底:非 ASCII 与引号/反斜杠替换为下划线
	var ascii strings.Builder
	for _, r := range filename {
		switch {
		case r < 0x20 || r == 0x7f:
			continue
		case r > 0x7e:
			ascii.WriteRune('_')
		case r == '"' || r == '\\':
			ascii.WriteRune('_')
		default:
			ascii.WriteRune(r)
		}
	}
	fallback := strings.TrimSpace(ascii.String())
	ext := filepath.Ext(filename)
	if strings.Trim(fallback, "_-. ") == "" || fallback == ext {
		fallback = "audio" + ext
	}

	// filename* 编码:PathEscape 会把中文按 UTF-8 百分号编码;
	// 单引号是 filename* 的分隔符,需额外转义
	encoded := strings.ReplaceAll(url.PathEscape(filename), "'", "%27")

	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, fallback, encoded)
}
