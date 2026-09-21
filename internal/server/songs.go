package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"audio-repair-studio/internal/audio"
	"audio-repair-studio/internal/library"

	"github.com/gin-gonic/gin"
)

// listSongs 列出所有歌曲(按创建时间倒序)
func (s *Server) listSongs(c *gin.Context) {
	songs, err := s.store.ListSongs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"songs": songs})
}

// getSong 查询单曲详情
func (s *Server) getSong(c *gin.Context) {
	song, err := s.store.GetSong(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, song)
}

// updateSong 更新歌曲元信息(PUT /api/songs/:id)
//
// 用于修正 ffprobe 解析错误的标题/歌手/专辑。空字段会覆盖为空,
// 前端应提交完整表单。
func (s *Server) updateSong(c *gin.Context) {
	id := c.Param("id")
	if _, err := s.store.GetSong(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var body struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
		Album  string `json:"album"`
		Year   int    `json:"year"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON"})
		return
	}
	if err := s.store.UpdateMeta(id, body.Title, body.Artist, body.Album, body.Year); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": id})
}

// toggleFavorite 切换收藏状态(POST /api/songs/:id/favorite)
func (s *Server) toggleFavorite(c *gin.Context) {
	id := c.Param("id")
	if _, err := s.store.GetSong(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	fav, err := s.store.ToggleFavorite(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "favorite": fav})
}

// deleteSong 删除歌曲(同时清理原始/修复/封面文件,并中断正在进行的修复)
func (s *Server) deleteSong(c *gin.Context) {
	id := c.Param("id")
	song, err := s.store.GetSong(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	// 若正在修复,先取消 ffmpeg 任务(避免半成品文件残留)
	s.queue.Cancel(id)
	// 删文件(原始/修复/封面)。原始文件可能被 ffmpeg 占用,需重试几次等句柄释放
	for _, rel := range []string{song.OriginalPath, song.RepairedPath, song.CoverPath} {
		if rel == "" {
			continue
		}
		removeWithRetry(filepath.Join(s.cfg.StoragePath, rel), 5, 200*time.Millisecond)
	}
	if err := s.store.DeleteSong(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// deleteSongs 批量删除歌曲(ids 用逗号分隔,如 ?ids=a,b,c)
//
// 每首歌都会取消修复任务并清理文件,失败的歌曲跳过但不终止整体。
func (s *Server) deleteSongs(c *gin.Context) {
	ids := strings.Split(c.Query("ids"), ",")
	var valid []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no ids provided"})
		return
	}
	// 逐个取消任务 + 删文件(批量 DB 删除在最后)
	for _, id := range valid {
		s.queue.Cancel(id)
		song, err := s.store.GetSong(id)
		if err != nil {
			continue // 不存在则跳过
		}
		for _, rel := range []string{song.OriginalPath, song.RepairedPath, song.CoverPath} {
			if rel == "" {
				continue
			}
			removeWithRetry(filepath.Join(s.cfg.StoragePath, rel), 5, 200*time.Millisecond)
		}
	}
	n, err := s.store.DeleteSongs(valid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": n})
}

// removeWithRetry 删除文件,失败时按间隔重试(应对 ffmpeg 占用句柄的竞态)
func removeWithRetry(path string, attempts int, interval time.Duration) {
	for i := 0; i < attempts; i++ {
		if err := os.Remove(path); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(interval)
	}
}

// retryRepair 重新触发修复(仅允许 failed / repaired 状态重试)
//
// 重试前会清理旧的修复版文件,避免残留;pending/repairing 状态下
// 任务已在队列中,重复重试无意义,返回 409。
// 可选接收 repair_params(JSON 字符串)更新修复参数,不传则沿用原参数。
func (s *Server) retryRepair(c *gin.Context) {
	id := c.Param("id")
	song, err := s.store.GetSong(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if song.Status == library.StatusPending || song.Status == library.StatusRepairing {
		c.JSON(http.StatusConflict, gin.H{"error": "song is already queued or repairing"})
		return
	}
	// 可选:更新修复参数
	newParams := strings.TrimSpace(c.PostForm("repair_params"))
	if newParams != "" {
		// 校验 JSON 合法性
		var p audio.RepairParams
		if err := json.Unmarshal([]byte(newParams), &p); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repair_params JSON"})
			return
		}
	} else {
		newParams = song.RepairParams // 沿用原参数
	}

	// 关键顺序:先尝试入队,成功后再做破坏性操作(删旧修复版/改状态)。
	// 否则队列满时会出现:旧修复版已删、状态已变 pending 却没入队,
	// 已修复的歌就此"丢失"修复版,只能再手动重试。
	if !s.queue.Submit(id) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "repair queue full, retry later"})
		return
	}

	// 入队成功:清理旧修复版文件(若存在),避免新旧格式文件并存残留
	if song.RepairedPath != "" {
		_ = os.Remove(filepath.Join(s.cfg.StoragePath, song.RepairedPath))
	}
	// 重置状态并按需更新参数
	_ = s.store.UpdateStatus(id, library.StatusPending, "", "")
	if newParams != song.RepairParams {
		_ = s.store.UpdateRepairParams(id, newParams)
	}
	c.JSON(http.StatusOK, gin.H{"requeued": id})
}
