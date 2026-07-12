package buffer

import (
	"bytes"
	"log"
	"sync"
)

type StreamBuffer struct {
	buf      *bytes.Buffer // 底层字节缓冲区
	mu       sync.Mutex    // 互斥锁，保护共享资源
	cond     *sync.Cond    // 条件变量，用于线程间通信
	maxSize  int           // 最大缓冲区大小
	isClosed bool          // 缓冲区是否已关闭
}

// NewStreamBuffer 流缓冲区的构造函数
func NewStreamBuffer(maxSize int) *StreamBuffer {
	sb := &StreamBuffer{
		buf:     bytes.NewBuffer(make([]byte, 0, maxSize)),
		maxSize: maxSize,
	}
	sb.cond = sync.NewCond(&sb.mu)
	return sb
}

func (sb *StreamBuffer) Write(data []byte) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if sb.isClosed {
		return ErrBufferClosed
	}

	if len(data) > sb.maxSize {
		return ErrDataTooLarge
	}

	for sb.buf.Len()+len(data) > sb.maxSize {
		sb.cond.Wait()
		if sb.isClosed {
			return ErrBufferClosed
		}
	}

	_, err := sb.buf.Write(data)
	if err != nil {
		return err
	}
	log.Printf("[Buffer] written %d bytes, current length: %d/%d bytes", len(data), sb.buf.Len(), sb.maxSize)
	sb.cond.Signal()

	return nil

}

func (sb *StreamBuffer) Read(size int) ([]byte, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	for sb.buf.Len() < size && !sb.isClosed {
		sb.cond.Wait()
	}

	if sb.buf.Len() == 0 {
		return nil, ErrBufferClosed
	}

	actualSize := size
	if sb.buf.Len() < size {
		actualSize = sb.buf.Len()
	}

	data := make([]byte, actualSize)
	n, _ := sb.buf.Read(data)
	if n < actualSize {
		result := make([]byte, n)
		copy(result, data[:n])
		sb.cond.Signal()
		return result, nil
	}

	sb.cond.Signal()
	return data, nil
}

func (sb *StreamBuffer) ReadAll() ([]byte, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if sb.isClosed && sb.buf.Len() == 0 {
		return nil, ErrBufferClosed
	}

	data := sb.buf.Bytes()
	sb.buf.Reset()
	sb.cond.Signal()
	return data, nil
}

func (sb *StreamBuffer) Close() {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	sb.isClosed = true
	sb.cond.Broadcast()
}

func (sb *StreamBuffer) Len() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Len()
}

func (sb *StreamBuffer) IsClosed() bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.isClosed
}
