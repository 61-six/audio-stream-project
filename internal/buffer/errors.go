package buffer

import "errors"

var (
	ErrBufferClosed = errors.New("buffer is closed")
	ErrDataTooLarge = errors.New("data size exceeds buffer capacity")
)
