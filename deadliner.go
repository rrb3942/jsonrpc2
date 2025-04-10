package jsonrpc2

import (
	"io"
	"time"
)

// DeadlineReader represents an [io.Reader] that supports setting a deadline.
type DeadlineReader interface {
	io.ReadCloser
	SetReadDeadline(time.Time) error
}

// DeadlineWrite represents an [io.Write] that supports setting a deadline.
type DeadlineWriter interface {
	io.WriteCloser
	SetWriteDeadline(time.Time) error
}
