package buffer

import "errors"

var (
	ErrBufferClosed = errors.New("buffer closed")
	ErrBufferFull   = errors.New("buffer full")
)