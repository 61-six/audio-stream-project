// Package server HTTP 路由与 handler 实现
package server

import (
	"crypto/subtle"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"audio-repair-studio/internal/audio"
	"audio-repair-studio/internal/config"
	"audio-repair-studio/internal/library"

	"github.com/gin-gonic/gin"
)

// Server HTTP 服务依赖装配
type Server struct {
	cfg     *config.Config
	store   *library.Store
	queue   *audio.Queue
	bus     *audio.ProgressBus
	tickets *ticketStore // WS 一次性 ticket(仅启用 Basic Auth 时需要)
}

// New 创建 Server 并注册所有路由
func New(cfg *config.Config, store *library.Store, queue *audio.Queue, bus *audio.ProgressBus) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	// CORS 中间件:与 WebSocket 的 CheckOrigin 策略保持一致
	// 本地单用户场景允许任意来源,便于前后端分离部署调试
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	// 可选 Basic Auth:配置了 AUTH_USER/AUTH_PASSWORD 才启用
	// /health 放行(容器健康检查不能带密码);OPTIONS 预检已在上面的 CORS 层终止
	if cfg.AuthEnabled() {
		r.Use(basicAuthMiddleware(cfg.AuthUser, cfg.AuthPassword))
	}

	s := &Server{cfg: cfg, store: store, queue: queue, bus: bus, tickets: newTicketStore()}

	// 健康检查
	r.GET("/health", s.health)

	// 业务 API
	api := r.Group("/api")
	{
		api.POST("/songs", s.upload)
		api.GET("/songs", s.listSongs)
		api.GET("/songs/:id", s.getSong)
		api.PUT("/songs/:id", s.updateSong)
		api.DELETE("/songs", s.deleteSongs)
		api.DELETE("/songs/:id", s.deleteSong)
		api.POST("/songs/:id/retry", s.retryRepair)
		api.POST("/songs/:id/favorite", s.toggleFavorite)
		api.GET("/songs/:id/download", s.download)
		api.POST("/import", s.importScan)
		api.GET("/ws-ticket", s.wsTicket)
	}

	// 流式播放(原音 / 修复版)
	// 注意:Gin 的 r.GET 不会自动响应 HEAD(与 net/http 默认 ServeMux 不同),
	// 而 HTML5 <audio> 在 seek 探测 / 预检时会发 HEAD,因此同时注册 GET+HEAD。
	r.Match([]string{"GET", "HEAD"}, "/play/:id", s.play)

	// WebSocket 进度推送
	r.GET("/ws/progress", s.wsProgress)

	// 静态资源(前端 + 封面图)
	r.Static("/static", "web/static")
	r.Static("/covers", s.cfg.CoversDir())
	r.GET("/", func(c *gin.Context) {
		c.File(filepath.Join("web/static", "index.html"))
	})

	return r
}

// health 健康检查:除 HTTP 探活外,验证 DB 可写、ffmpeg 可用、存储目录可写
func (s *Server) health(c *gin.Context) {
	checks := gin.H{}
	healthy := true

	// 1) DB 探活
	if err := s.store.Ping(); err != nil {
		checks["db"] = err.Error()
		healthy = false
	} else {
		checks["db"] = "ok"
	}

	// 2) ffmpeg / ffprobe 可用(两者都是强依赖:转码与元信息探测)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		checks["ffmpeg"] = "not found"
		healthy = false
	} else {
		checks["ffmpeg"] = "ok"
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		checks["ffprobe"] = "not found"
		healthy = false
	} else {
		checks["ffprobe"] = "ok"
	}

	// 3) 存储目录可写(写一个探针文件然后删除)
	probe := filepath.Join(s.cfg.StoragePath, "_health_probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		checks["storage"] = err.Error()
		healthy = false
	} else {
		_ = os.Remove(probe)
		checks["storage"] = "ok"
	}

	status := http.StatusOK
	if !healthy {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{
		"status":  "ok",
		"version": "0.1.0",
		"env":     s.cfg.Env,
		"checks":  checks,
	})
}

// basicAuthMiddleware HTTP Basic 认证中间件
//
// 用户名/密码比较用恒定时间算法,防止计时侧信道;
// 未携带或凭证错误时返回 401 + WWW-Authenticate,浏览器会弹出登录框。
// /health 路径放行:Docker 健康检查无法携带凭证。
func basicAuthMiddleware(user, password string) gin.HandlerFunc {
	userBytes := []byte(user)
	passBytes := []byte(password)
	return func(c *gin.Context) {
		// /health 与 /ws/progress 不走 Basic Auth:
		// /health 供容器探活;WS 无法带 Authorization 头,改用一次性 ticket
		// (wsProgress 内部自行校验 query 中的 ticket)
		if c.Request.URL.Path == "/health" || c.Request.URL.Path == "/ws/progress" {
			c.Next()
			return
		}
		u, p, ok := c.Request.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(u), userBytes) == 1
		passOK := subtle.ConstantTimeCompare([]byte(p), passBytes) == 1
		if !ok || !userOK || !passOK {
			c.Header("WWW-Authenticate", `Basic realm="audio-repair-studio"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
