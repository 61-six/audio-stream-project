package session

import (
	"testing"
)

func TestManager_CreateAndGet(t *testing.T) {
	manager := NewManager(10)

	session := manager.CreateSession("test-client")
	if session == nil {
		t.Error("Session is nil")
	}

	sessionID := session.ID

	retrieved, ok := manager.GetSession(sessionID)
	if !ok {
		t.Error("Expected to find session")
	}

	if retrieved.ID != sessionID {
		t.Errorf("Expected session ID %s, got %s", sessionID, retrieved.ID)
	}
}

func TestManager_Remove(t *testing.T) {
	manager := NewManager(10)

	session := manager.CreateSession("test-client")
	sessionID := session.ID

	manager.RemoveSession(sessionID)

	_, ok := manager.GetSession(sessionID)
	if ok {
		t.Error("Expected session to be removed")
	}
}

func TestManager_NotFound(t *testing.T) {
	manager := NewManager(10)

	_, ok := manager.GetSession("nonexistent")
	if ok {
		t.Error("Expected session not found")
	}
}

func TestManager_SessionCount(t *testing.T) {
	manager := NewManager(10)

	manager.CreateSession("client1")
	manager.CreateSession("client2")

	count := manager.SessionCount()
	if count != 2 {
		t.Errorf("Expected 2 sessions, got %d", count)
	}
}
