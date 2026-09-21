package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ticketTTL WebSocket 一次性 ticket 的有效期
//
// ticket 由前端在建立 WS 连接前通过 /api/ws-ticket(走 Basic Auth)获取,
// 有效期只需覆盖"取 ticket → 发起 WS 握手"这几秒,30 秒足够。
const ticketTTL = 30 * time.Second

// ticketStore WebSocket 握手用的一次性 ticket 存储(内存)
//
// 存在原因:浏览器 WebSocket API 无法自定义 Authorization 头,
// Basic Auth 启用后 WS 无法像 fetch 那样自动携带凭证。
// 改用短期一次性 ticket:凭证校验在 HTTPS 的普通接口完成,
// ticket 只用于随后一次 WS 握手,用后即废,避免密码出现在 URL 中。
type ticketStore struct {
	mu      sync.Mutex
	tickets map[string]time.Time // ticket → 过期时间
}

func newTicketStore() *ticketStore {
	return &ticketStore{tickets: make(map[string]time.Time)}
}

// Issue 生成一个新的一次性 ticket(32 字节随机数的 hex 编码)
func (t *ticketStore) Issue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	ticket := hex.EncodeToString(b)

	t.mu.Lock()
	t.tickets[ticket] = time.Now().Add(ticketTTL)
	t.cleanupLocked()
	t.mu.Unlock()
	return ticket, nil
}

// Consume 校验并消费 ticket:存在且未过期 → 删除并返回 true(一次性)
func (t *ticketStore) Consume(ticket string) bool {
	if ticket == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	exp, ok := t.tickets[ticket]
	if !ok {
		return false
	}
	delete(t.tickets, ticket) // 无论是否过期都删除,杜绝重放
	return time.Now().Before(exp)
}

// cleanupLocked 顺带清除过期 ticket(调用方持锁)
func (t *ticketStore) cleanupLocked() {
	now := time.Now()
	for k, exp := range t.tickets {
		if now.After(exp) {
			delete(t.tickets, k)
		}
	}
}

// wsTicket 签发 WebSocket 握手用的一次性 ticket
//
// GET /api/ws-ticket
//
// 本接口走正常的 Basic Auth 中间件(fetch 可自动携带浏览器已缓存的凭证)。
// 未启用认证时 required=false、ticket 为空,前端无需拼接 ticket;
// 启用认证时返回一次性 ticket,前端须在 30 秒内用它完成 WS 握手。
func (s *Server) wsTicket(c *gin.Context) {
	if !s.cfg.AuthEnabled() {
		c.JSON(http.StatusOK, gin.H{"required": false, "ticket": ""})
		return
	}
	ticket, err := s.tickets.Issue()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "issue ticket failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"required": true, "ticket": ticket})
}
