package jsonrpc2

import (
	"context"
	"errors"
)

var (
	errUnreachable = errors.New("bad type for BatchBuilder. should be unreachable")
)

// BatchBuildable represents the support types for [BatchBuilder].
type BatchBuildable interface {
	*Request | *Notification
	idable
}

// BatchBuilder may be reused by calling Reset() to clear the underlying [Batch].
type BatchBuilder[B BatchBuildable] struct {
	parent *Client
	Batch[B]
}

// parent [*Client].
func (b *BatchBuilder[B]) Add(method string, params any) error {
	reqParams, err := makeParamsFromAny(params)
	if err != nil {
		return err
	}

	switch batch := any(b.Batch).(type) {
	case Batch[*Request]:
		batch.Add(NewRequestWithParams(b.parent.nextID(), method, reqParams))
	case Batch[*Notification]:
		batch.Add(NewNotificationWithParams(method, reqParams))
	}

	// Impossible based on type restrictions
	return errUnreachable
}

// Call sends the batch over the parent [*Client], handling proper dispatch for
// [*Request] and [*Notification] types.
//
// For batch types of [*Notification], Batch[*Response] will always be nil.
func (b *BatchBuilder[B]) Call(ctx context.Context) (Batch[*Response], error) {
	switch batch := any(b.Batch).(type) {
	case Batch[*Request]:
		return b.parent.callBatch(ctx, batch)
	case Batch[*Notification]:
		return nil, b.parent.notifyBatch(ctx, batch)
	}

	// Impossible based on type restrictions
	return nil, errUnreachable
}
