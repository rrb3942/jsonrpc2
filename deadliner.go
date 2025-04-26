package jsonrpc2

import (
	"io"
	"time"
)

// DeadlineReader represents an [io.ReaderCloser] that supports setting a deadline.
type DeadlineReader interface {
	io.ReadCloser
	SetReadDeadline(time.Time) error
}

// DeadlineWrite represents an [io.WriteCloser] that supports setting a deadline.
type DeadlineWriter interface {
	io.WriteCloser
	SetWriteDeadline(time.Time) error
}
