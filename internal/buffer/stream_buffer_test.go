package buffer

import (
	"testing"
	"time"
)

func TestStreamBuffer_WriteRead(t *testing.T) {
	sb := NewStreamBuffer(1024)
	data := []byte("hello world")

	err := sb.Write(data)
	if err != nil {
		t.Errorf("Write error: %v", err)
	}

	result, err := sb.Read(len(data))
	if err != nil {
		t.Errorf("Read error: %v", err)
	}

	if string(result) != string(data) {
		t.Errorf("Expected %s, got %s", data, result)
	}

	sb.Close()
}

func TestStreamBuffer_Close(t *testing.T) {
	sb := NewStreamBuffer(1024)
	sb.Close()

	_, err := sb.Read(1)
	if err != ErrBufferClosed {
		t.Errorf("Expected ErrBufferClosed, got %v", err)
	}
}

func TestStreamBuffer_BlockingRead(t *testing.T) {
	sb := NewStreamBuffer(1024)

	go func() {
		time.Sleep(100 * time.Millisecond)
		sb.Write([]byte("test"))
	}()

	result, err := sb.Read(4)
	if err != nil {
		t.Errorf("Read error: %v", err)
	}

	if string(result) != "test" {
		t.Errorf("Expected test, got %s", result)
	}

	sb.Close()
}
