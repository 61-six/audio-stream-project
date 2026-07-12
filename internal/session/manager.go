package session

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID            string    // 会话唯一标识
	ClientID      string    // 客户端标识
	Filename      string    // 原始文件名
	ReceivedBytes int64     // 已接收字节数
	ChunkCount    int32     // 已接收数据块数
	Status        string    // 会话状态
	StartTime     time.Time // 开始时间
	EndTime       time.Time // 结束时间
	OutputFile    string    // 输出文件名
	FrameCount    int       // 音频帧数
	DurationMs    int64     // 音频时长（毫秒）
	AvgEnergy     float64   // 平均能量
}

type Manager struct {
	sessions    sync.Map
	mu          sync.RWMutex
	maxSessions int
}

func NewManager(maxSessions int) *Manager {
	return &Manager{
		maxSessions: maxSessions,
	}
}

func (m *Manager) CreateSession(clientID string) *Session {
	m.mu.Lock()
	count := m.SessionCount()
	if m.maxSessions > 0 && count >= m.maxSessions {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	session := &Session{
		ID:        uuid.New().String(),
		ClientID:  clientID,
		Status:    "created",
		StartTime: time.Now(),
	}

	m.sessions.Store(session.ID, session)
	return session
}

func (m *Manager) GetSession(sessionID string) (*Session, bool) {
	value, ok := m.sessions.Load(sessionID)
	if !ok {
		return nil, false
	}
	return value.(*Session), true
}

func (m *Manager) UpdateSession(sessionID string, update func(*Session)) {
	if session, ok := m.GetSession(sessionID); ok {
		update(session)
	}
}

func (m *Manager) RemoveSession(sessionID string) {
	m.sessions.Delete(sessionID)
}

func (m *Manager) GetAllSessions() []*Session {
	var sessions []*Session
	m.sessions.Range(func(key, value interface{}) bool {
		sessions = append(sessions, value.(*Session))
		return true
	})
	return sessions
}

func (m *Manager) SessionCount() int {
	count := 0
	m.sessions.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

func (s *Session) String() string {
	return fmt.Sprintf(
		"session_id: %s\nreceived_bytes: %d\nchunk_count: %d\noutput_format: pcm_s16le, 16000Hz, mono\nframe_size: %d bytes\nframe_count: %d\nduration_ms: %d\navg_energy: %.2f",
		s.ID,
		s.ReceivedBytes,
		s.ChunkCount,
		640,
		s.FrameCount,
		s.DurationMs,
		s.AvgEnergy,
	)
}
