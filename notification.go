package jsonrpc2

import (
	"unsafe"
)

type Notification Request

func NewNotification(method string) *Notification {
	return &Notification{Method: method, ID: NewNullID()}
}

func NewNotificationWithParams(method string, p Params) *Notification {
	return &Notification{Method: method, ID: NewNullID(), Params: p}
}

// AsRequest returns the [Notification] as a [*Request].
//
// This is NOT a copy, but a pointer of type [*Request] that points to the same
// memory as the notification.
//
// The primary use of this method is for including [Notifications] in a batch call of [][*Request],
// however the caller must ensure at least one member of the batch is NOT a notification or the call may
// block forever. This pattern should be avoided by clients.
func (n *Notification) AsRequest() *Request {
	if n == nil {
		return nil
	}

	return (*Request)(unsafe.Pointer(n))
}
