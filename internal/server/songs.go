package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
// 时光机功能:不删旧修复版,而是创建新版本(v2, v3...),保留历史供 A/B 对比。
// pending/repairing 状态下任务已在队列中,重复重试无意义,返回 409。
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

	// 解析参数以确定输出扩展名(用于构造版本文件路径)
	params := audio.DefaultParams()
	if newParams != "" {
		_ = json.Unmarshal([]byte(newParams), &params)
	}
	_ = params // 扩展名由 queue.go 内部构造,此处仅校验参数

	// 创建新版本记录(事务内计算 nextVersion,并发安全)
	version, err := s.store.CreateRepairVersion(id, newParams, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 关键顺序:先尝试入队,成功后再更新状态。
	// 入队失败时回滚版本记录。
	if !s.queue.Submit(id, version) {
		_, _ = s.store.DeleteRepairVersion(id, version)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "repair queue full, retry later"})
		return
	}

	// 入队成功:重置状态,更新参数(不删旧修复文件,保留历史版本)
	_ = s.store.UpdateStatus(id, library.StatusPending, "", "")
	if newParams != song.RepairParams {
		_ = s.store.UpdateRepairParams(id, newParams)
	}
	c.JSON(http.StatusOK, gin.H{"requeued": id, "version": version})
}

// listVersions 列出指定歌曲的所有修复版本(时光机功能)
//
// GET /api/songs/:id/versions
func (s *Server) listVersions(c *gin.Context) {
	id := c.Param("id")
	if _, err := s.store.GetSong(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	versions, err := s.store.ListRepairVersions(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

// setCurrentVersion 设置歌曲当前选中的修复版本(切换播放/下载的版本)
//
// PUT /api/songs/:id/versions/current  body: {"version": 2}
func (s *Server) setCurrentVersion(c *gin.Context) {
	id := c.Param("id")
	song, err := s.store.GetSong(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var body struct {
		Version int `json:"version"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON"})
		return
	}
	// 检查目标版本是否存在且状态为 done
	rv, err := s.store.GetRepairVersion(id, body.Version)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
		return
	}
	if rv.Status != "done" {
		c.JSON(http.StatusConflict, gin.H{"error": "version is not done (status: " + rv.Status + ")"})
		return
	}
	if err := s.store.SetCurrentVersion(id, body.Version); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 更新 songs.repaired_path 指向当前版本的路径(供兼容旧逻辑)
	_ = s.store.UpdateStatus(id, song.Status, rv.Path, song.ErrorMsg)
	c.JSON(http.StatusOK, gin.H{"song_id": id, "current_version": body.Version})
}

// deleteVersion 删除指定修复版本(不能删当前版本)
//
// DELETE /api/songs/:id/versions/:ver
func (s *Server) deleteVersion(c *gin.Context) {
	id := c.Param("id")
	song, err := s.store.GetSong(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	verStr := c.Param("ver")
	ver, err := strconv.Atoi(verStr)
	if err != nil || ver < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid version number"})
		return
	}
	// 不能删除当前版本
	if song.CurrentVersion == ver {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot delete current version"})
		return
	}
	// 删除版本记录,拿到文件路径后清理磁盘
	path, err := s.store.DeleteRepairVersion(id, ver)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
		return
	}
	if path != "" {
		removeWithRetry(filepath.Join(s.cfg.StoragePath, path), 3, 100*time.Millisecond)
	}
	c.JSON(http.StatusOK, gin.H{"deleted": ver})
}

// resplit 重新切割整盘源歌曲(POST /api/songs/:id/resplit)
//
// 适用场景:整盘导入后切割点不准,前端展示探测结果,用户手动调整后调此接口重切。
// body 可选 {"points": [12.34, ...]} 不传则用源歌曲 DB 里的 split_points.points。
//
// 行为:异步调 splitCompilation 生成新切片,每片入库入修复队列。
// 注意:不删除旧切片(若 resplit 多次),用户需在前端手动删除旧切片。
// 后续可加 parent_id 字段实现"重切自动删旧切片"。
func (s *Server) resplit(c *gin.Context) {
	id := c.Param("id")
	song, err := s.store.GetSong(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if song.SplitPoints == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "song is not a compilation source (no split_points)"})
		return
	}

	// 解析现有 split_points(包含 spans + points)
	var result audio.SilenceDetectResult
	if err := json.Unmarshal([]byte(song.SplitPoints), &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid split_points JSON"})
		return
	}

	// 可选:body 里的 points 覆盖(用户手动调整后的切割点)
	var body struct {
		Points []float64 `json:"points"`
	}
	if err := c.ShouldBindJSON(&body); err == nil && len(body.Points) > 0 {
		result.Points = body.Points
	}
	if len(result.Points) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no split points"})
		return
	}

	points := result.Points
	mediaType := song.MediaType
	ext := filepath.Ext(song.OriginalPath)
	sourceAbs := filepath.Join(s.cfg.StoragePath, song.OriginalPath)
	sourceName := song.OriginalFilename

	// 异步切割(避免长请求阻塞)
	go func() {
		if err := s.splitCompilation(id, sourceAbs, ext, sourceName, mediaType, points); err != nil {
			log.Printf("[resplit] %s: %v", id, err)
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{"resplit": id, "points": len(points)})
}
