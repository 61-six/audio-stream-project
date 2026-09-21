package server

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// upgrader WebSocket 升级器(允许跨域,本地开发友好)
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsConns 当前 WebSocket 连接数(用于限制上限,防止被刷连接耗尽资源)
var wsConns atomic.Int64

const maxWsConns = 50 // 单用户场景足够,公网部署可调大

// wsProgress WebSocket 推送修复进度
//
// 客户端建立连接后,会持续收到所有歌曲的 ProgressEvent。
// 后端在每次 ffmpeg 输出 stderr 行时通过 ProgressBus 广播。
func (s *Server) wsProgress(c *gin.Context) {
	// Basic Auth 启用时:浏览器 WS 无法带 Authorization 头,
	// 改用一次性 ticket(前端先经 /api/ws-ticket 获取),校验失败拒绝握手
	if s.cfg.AuthEnabled() {
		ticket := c.Query("ticket")
		if !s.tickets.Consume(ticket) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired ws ticket"})
			return
		}
	}

	// 连接数超限则拒绝
	if wsConns.Add(1) > maxWsConns {
		wsConns.Add(-1)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many websocket connections"})
		return
	}
	defer wsConns.Add(-1)

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)

	// 心跳(避免代理超时断开)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// 后台读取客户端消息(主要处理 close 帧,避免连接泄漏)
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
