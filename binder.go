package jsonrpc2

import (
	"context"
)

// Binder is used to configure a [*RPCServer] before it has started.
// It is called whenever a new [*RPCServer] is created.
//
// The cancel function may be used to stop the current [*RPCServer].
type Binder interface {
	// Called on new connections or new http requests
	Bind(context.Context, *RPCServer, context.CancelCauseFunc)
}

// NewFuncBinder returns a [Binder] runs the given function on bind.
//
//nolint:ireturn //Helper function
func NewFuncBinder(binder func(context.Context, *RPCServer, context.CancelCauseFunc)) Binder {
	return &funcBinder{funcBind: binder}
}

// funcBinder is used to wrap a function into a [Binder].
type funcBinder struct {
	funcBind func(context.Context, *RPCServer, context.CancelCauseFunc)
}

// Handle implements [Binder].
func (fh *funcBinder) Bind(ctx context.Context, rpc *RPCServer, stop context.CancelCauseFunc) {
	fh.funcBind(ctx, rpc, stop)
}
