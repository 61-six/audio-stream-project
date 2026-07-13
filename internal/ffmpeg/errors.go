package ffmpeg

import "errors"

var (
	ErrTranscoderNotStarted = errors.New("transcoder not started")
	ErrTranscoderClosed     = errors.New("transcoder closed")
	ErrInvalidOutputPath    = errors.New("invalid output path")
)