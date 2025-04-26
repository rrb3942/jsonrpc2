package jsonrpc2

import (
	"unsafe"
)

// Notification represents a JSON-RPC 2.0 Notification object.
// A Notification is structurally the same as a [Request] but lacks an [ID] field.
// This signifies that the client does not expect a response from the server.
//
// Use the constructor functions [NewNotification] or [NewNotificationWithParams]
// to create instances.
//
// See: https://www.jsonrpc.org/specification#notification
type Notification Request

// NewNotification creates a new Notification with the specified method name.
// The Params field is initially empty.
//
// Example:
//
//	notif := jsonrpc2.NewNotification("logEvent")
//	// Marshals to: {"jsonrpc":"2.0","method":"logEvent"}
func NewNotification(method string) *Notification {
	// Explicitly set Jsonrpc version for clarity, even though it's the zero value default.
	// ID remains zero, signifying a notification.
	return &Notification{Method: method}
}

// NewNotificationWithParams creates a new Notification with the specified method name
// and parameters.
//
// Example:
//
//	notif := jsonrpc2.NewNotificationWithParams("system.log", jsonrpc2.NewParamsArray([]any{"User logged out", []map[string]any{"userId": 123}))
//	// Marshals to: {"jsonrpc":"2.0","method":"system.log","params":["User logged out",{"userId":123}]}
func NewNotificationWithParams(method string, p Params) *Notification {
	return &Notification{Method: method, Params: p}
}

// AsRequest returns the [*Notification] as a [*Request] pointer.
//
// **Warning:** This function uses `unsafe.Pointer` for type conversion. It does
// **not** create a copy. The returned [*Request] pointer refers to the **exact
// same memory location** as the original [*Notification]. Modifying the fields
// through the [*Request] pointer will change the original [*Notification], and
// vice-versa.
//
// Example (demonstrating shared memory):
//
//	notif := jsonrpc2.NewNotification("update")
//	reqPtr := notif.AsRequest()
//
//	fmt.Println(notif.Method) // Output: update
//	fmt.Println(reqPtr.Method) // Output: update
//
//	// Modify via the Request pointer
//	reqPtr.Method = "statusUpdate"
//
//	fmt.Println(notif.Method) // Output: statusUpdate (original notification is changed)
//	fmt.Println(reqPtr.Method) // Output: statusUpdate
func (n *Notification) AsRequest() *Request {
	if n == nil {
		return nil
	}
	// Directly convert the pointer type. This is safe because Notification
	// is an alias for Request, meaning they have the same memory layout.
	return (*Request)(unsafe.Pointer(n))
}

// id returns the ID associated with the notification.
// Since notifications inherently lack an ID, this will always return a zero ID.
// Used internally, primarily for matching responses in batch requests (though notifications won't have responses).
func (n *Notification) id() ID {
	// Accessing n.ID is valid because Notification is type Request.
	// For a valid notification, n.ID will always be its zero value.
	return n.ID
}
