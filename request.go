package jsonrpc2

import (
	"unsafe"
)

// Request represents a JSON-RPC 2.0 request object sent from a client to a server.
// It includes the JSON-RPC version ([Request.Jsonrpc]), the method to be invoked ([Request.Method]),
// optional parameters ([Request.Params]), and an ID ([Request.ID]) used to correlate requests and responses.
// If the [Request.ID] is omitted, the request is treated as a [Notification].
//
//nolint:govet // We want the field order to match spec examples for clarity, even if not strictly required.
type Request struct {
	Jsonrpc Version `json:"jsonrpc"`
	Method  string  `json:"method"`
	Params  Params  `json:"params,omitzero,omitempty"`
	ID      ID      `json:"id,omitzero"`
}

// NewRequest creates a new JSON-RPC request object with the specified ID and method.
// The [Request.Params] field is initially empty (zero value).
//
// Example:
//
//	reqInt := jsonrpc2.NewRequest(int64(1), "add")
//	reqStr := jsonrpc2.NewRequest("req-001", "getUser")
func NewRequest[I int64 | string](id I, method string) *Request {
	return &Request{Method: method, ID: NewID(id)}
}

// NewRequestWithParams creates a new JSON-RPC request object with the specified ID ([Request.ID]),
// method ([Request.Method]), and parameters ([Request.Params]).
//
// Example:
//
//	req := jsonrpc2.NewRequestWithParams(int64(2), "add", jsonrpc2.NewParamsArray([]any{1, 2}))
//
//	userParams := map[string]any{"userId": 123}
//	paramsObj := jsonrpc2.NewParamsObject(userParams)
//	reqUser := jsonrpc2.NewRequestWithParams("user-req", "getUser", paramsObj)
func NewRequestWithParams[I int64 | string](id I, method string, p Params) *Request {
	return &Request{Method: method, ID: NewID(id), Params: p}
}

// ResponseWithError constructs a [Response] for the current [*Request] and populates the [Response.Error] field.
// If e is of type [Error] it will be used directly.
// Other values for error will automatically be converted to an [ErrInternalError] with the data field populated with the error string.
func (r *Request) ResponseWithError(e error) *Response {
	return &Response{ID: r.ID, Error: asError(e)}
}

// ResponseWithResult constructs a [Response] for the current [*Request] and populates the [Response.Result] with result.
func (r *Request) ResponseWithResult(result any) *Response {
	return &Response{ID: r.ID, Result: NewResult(result)}
}

// IsNotification returns true if the request object represents a JSON-RPC [Notification].
// A [Notification] is a [Request] without an [Request.ID] field.
//
// Example:
//
//	req := jsonrpc2.NewRequest(int64(1), "regularMethod")
//	fmt.Println(req.IsNotification()) // Output: false
//
//	// Notifications are typically created using NewNotification
//	notif := jsonrpc2.NewNotification("logMessage")
//	fmt.Println(notif.AsRequest().IsNotification()) // Output: true
func (r *Request) IsNotification() bool {
	return r.ID.IsZero()
}

// AsNotification attempts to convert the [Request] into a [*Notification].
//
// It returns a valid [*Notification] pointer if the [Request.ID] is zero (meaning it's
// structurally a notification). Otherwise, it returns nil.
//
// Important: This function performs a type conversion using unsafe.Pointer. It does
// NOT create a copy. The returned [*Notification] points to the same underlying
// memory as the original [*Request]. Modifying the [*Notification] will modify the
// [*Request] and vice-versa. Use with caution.
//
// Example:
//
//	notifReq := jsonrpc2.NewNotification("logEvent", jsonrpc2.NewParamsArray([]any{"system start"}))
//	req := notifReq.AsRequest() // req is now a *Request, but IsNotification() is true
//
//	if n := req.AsNotification(); n != nil {
//		fmt.Printf("Got notification: Method=%s\n", n.Method)
//		// Modify through notification pointer
//		n.Method = "newEvent"
//	}
//
//	fmt.Println(req.Method) // Output: newEvent
//
//	regularReq := jsonrpc2.NewRequest(int64(1), "getData")
//	if n := regularReq.AsNotification(); n == nil {
//		fmt.Println("Regular request is not a notification")
//	}
func (r *Request) AsNotification() *Notification {
	// Check for nil receiver first for safety, although IsNotification handles it.
	if r == nil || !r.IsNotification() {
		return nil
	}

	return (*Notification)(unsafe.Pointer(r))
}

// Used in [Batch] for identifying elements.
func (r *Request) id() ID {
	return r.ID
}
