package session

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID            string
	ClientID      string
	Filename      string
	ReceivedBytes int64
	ChunkCount    int32
	Status        string
	StartTime     time.Time
	EndTime       time.Time
	OutputFile    string
	FrameCount    int
	DurationMs    int64
	AvgEnergy     float64
}

type Manager struct {
	sessions sync.Map
	mu       sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) CreateSession(clientID string) *Session {
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