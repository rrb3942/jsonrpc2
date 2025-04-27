package jsonrpc2

import (
	"context"
	"errors"
)

var (
	// errUnreachable indicates an internal logic error where type constraints should prevent execution.
	errUnreachable = errors.New("jsonrpc2: bad type for BatchBuilder, should be unreachable")
)

// BatchBuildable defines the types supported within a [BatchBuilder]: either [*Request] or [*Notification].
type BatchBuildable interface {
	*Request | *Notification // The batch can hold either requests or notifications, but not both.
	idable                   // Constraint for internal operations.
}

// BatchBuilder simplifies the creation of JSON-RPC 2.0 batches ([*Request] or [*Notification]).
// It handles ID generation for requests and parameter preparation.
//
// **Important:** Obtain a BatchBuilder only through [Client.NewBatchRequest] or [Client.NewBatchNotify].
// Do not create instances directly.
//
// The builder embeds a [Batch] of the specified type ([*Request] or [*Notification]).
// This underlying batch can be accessed directly (e.g., `builder.Batch`) if needed,
// for example, to correlate requests with responses using [BatchCorrelate].
//
// A BatchBuilder can be reused after calling [BatchBuilder.Call] and then calling
// its `Reset()` method (e.g., `builder.Reset()`).
//
// Example (Request Batch):
//
//	// Assume 'client' is an initialized *jsonrpc2.Client
//	reqBuilder := client.NewBatchRequest()
//	err := reqBuilder.Add("add", []int{1, 2}) // Adds a request with auto-generated ID
//	if err != nil { /* handle error */ }
//	err = reqBuilder.Add("subtract", map[string]int{"minuend": 10, "subtrahend": 5})
//	if err != nil { /* handle error */ }
//
//	// Send the batch
//	resBatch, err := reqBuilder.Call(context.Background())
//	if err != nil { /* handle error */ }
//
//	// Correlate requests and responses (accessing the embedded Batch)
//	jsonrpc2.BatchCorrelate(reqBuilder.Batch, resBatch, func(req *jsonrpc2.Request, res *jsonrpc2.Response) bool {
//	    if req != nil && res != nil && res.Error.IsZero() {
//	        fmt.Printf("Request %v (%s) succeeded.\n", req.ID.Value(), req.Method)
//	    } else if req != nil && res != nil && !res.Error.IsZero() {
//	        fmt.Printf("Request %v (%s) failed: %s\n", req.ID.Value(), req.Method, res.Error.Message)
//	    } else if req != nil {
//	        fmt.Printf("Request %v (%s) received no response.\n", req.ID.Value(), req.Method)
//	    }
//	    return true // Continue correlation
//	})
//
//	// Reuse the builder
//	reqBuilder.Reset()
//
// Example (Notification Batch):
//
//	notifyBuilder := client.NewBatchNotify()
//	err = notifyBuilder.Add("log", "System started")
//	if err != nil { /* handle error */ }
//	err = notifyBuilder.Add("ping", nil)
//	if err != nil { /* handle error */ }
//
//	// Send the notifications (no responses expected)
//	_, err = notifyBuilder.Call(context.Background()) // Response batch is always nil for notifications
//	if err != nil { /* handle error */ }
//
//	// Reuse the builder
//	notifyBuilder.Reset()
type BatchBuilder[B BatchBuildable] struct {
	parent   *Client // The client used for sending the batch and generating IDs.
	Batch[B]         // The embedded batch being built.
}

// Add creates a new JSON-RPC message ([*Request] or [*Notification]) with the given
// method and parameters, and adds it to the batch.
//
// For request batches ([Client.NewBatchRequest]), it automatically assigns a unique ID.
// For notification batches ([Client.NewBatchNotify]), no ID is assigned.
//
// The `params` argument is automatically prepared for transmission.
// Returns an error if parameter preparation fails (e.g., due to marshalling issues).
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
	default:
		return errUnreachable
	}

	return nil // Successfully added
}

// Call sends the batch using the associated [Client].
//
// The behavior depends on the type of batch builder:
//   - Request Batch ([Client.NewBatchRequest]): Sends the requests and attempts to receive
//     corresponding responses. Returns the [Batch[*Response]] and any transport/processing error.
//   - Notification Batch ([Client.NewBatchNotify]): Sends the notifications. The returned
//     [Batch[*Response]] will always be `nil`. Returns any transport/processing error.
//
// After calling Call, the builder still contains the sent batch items.
// Use its `Reset()` method to clear it for reuse.
func (b *BatchBuilder[B]) Call(ctx context.Context) (Batch[*Response], error) {
	// This switch dispatches to the appropriate Client method based on the batch type.
	switch batch := any(b.Batch).(type) {
	case Batch[*Request]:
		// Send requests and expect responses.
		return b.parent.callBatch(ctx, batch) // Returns (responseBatch, error)
	case Batch[*Notification]:
		// Send notifications; no responses are expected or processed by the client.
		err := b.parent.notifyBatch(ctx, batch) // Returns error only
		return nil, err                         // Response batch is always nil for notifications
	default:
		// This case should be impossible due to the BatchBuildable constraint.
		return nil, errUnreachable
	}
}
