package jsonrpc2

import (
	"slices"
)

// idable is used to allow easier access to the ID field inside a Batch.
type idable interface {
	id() ID
}

// Batchable represents types that can be used in a batch.
type Batchable interface {
	*Request | *Notification | *Response
	idable // Embeds the unexported id() method for internal use.
}

// Batch represents a collection of JSON-RPC 2.0 messages ([*Request], [*Notification], or [*Response])
// intended to be processed together as defined by the specification.
// It provides methods for managing and querying the collection based on message IDs.
//
// See: https://www.jsonrpc.org/specification#batch
type Batch[B Batchable] []B

// NewBatch creates a new, empty [Batch] with an initial capacity specified by `size`.
// Use this constructor to efficiently preallocate space if the approximate number
// of items is known.
//
// Example:
//
//	// Create a batch for requests, expecting around 10 requests.
//	requestBatch := jsonrpc2.NewBatch[*jsonrpc2.Request](10)
func NewBatch[B Batchable](size int) Batch[B] {
	return make(Batch[B], 0, size)
}

// BatchCorrelate matches items between a request batch and a response batch based on their IDs.
// For each request, it attempts to find a corresponding response with the same ID.
//
// The `correlated` function is called for each potential match:
//   - If a request has a matching response, `correlated(request, response)` is called.
//   - If a request has no matching response, `correlated(request, nil)` is called.
//   - If a response has no matching request, `correlated(nil, response)` is called.
//
// The `correlated` function should return `true` to continue processing the batches,
// or `false` to stop correlation immediately.
//
// Note: This function assumes IDs within each batch are unique. If duplicate IDs exist,
// the behavior is based on the first match found by the `Get` method. It does not
// handle one-to-many correlations.
//
// Example:
//
//	reqBatch := jsonrpc2.NewBatch[*jsonrpc2.Request](0)
//	reqBatch.Add(jsonrpc2.NewRequest(int64(1), "add")      // ID 1
//	reqBatch.Add(jsonrpc2.NewRequest(int64(2), "notify")         // ID 2 (no response expected)
//	reqBatch.Add(jsonrpc2.NewNotification("log").AsRequest()) // No ID
//
//	resBatch := jsonrpc2.NewBatch[*jsonrpc2.Response](0)
//	resBatch.Add(jsonrpc2.NewResponseWithResult(int64(1), 3))           // ID 1
//	resBatch.Add(jsonrpc2.NewResponseWithError(int64(3), jsonrpc2.ErrMethodNotFound)) // ID 3 (unmatched)
//
//	jsonrpc2.BatchCorrelate(reqBatch, resBatch, func(req *jsonrpc2.Request, res *jsonrpc2.Response) bool {
//	    if req != nil && res != nil {
//	        fmt.Printf("Matched Request ID %v with Response ID %v\n", req.ID.Value(), res.ID.Value())
//	    } else if req != nil {
//	        fmt.Printf("Unmatched Request ID %v\n", req.ID.Value())
//	    } else if res != nil {
//	        fmt.Printf("Unmatched Response ID %v\n", res.ID.Value())
//	    }
//	    return true // Continue processing
//	})
//	// Output (order may vary slightly for unmatched responses):
//	// Matched Request ID 1 with Response ID 1
//	// Unmatched Request ID 2
//	// Unmatched Request ID <nil>  (For the notification)
//	// Unmatched Response ID 3
func BatchCorrelate(requests Batch[*Request], responses Batch[*Response], correlated func(req *Request, res *Response) (cont bool)) {
	processedResponses := make(map[ID]bool) // Track responses matched to requests

	// Iterate through requests to find their corresponding responses.
	for _, req := range requests {
		// Get handlers Zero ids and returns immediately
		res, found := responses.Get(req.id())
		if found {
			processedResponses[res.id()] = true // Mark response as processed
		}

		if !correlated(req, res) { // Call with req and (res or nil)
			return
		}
	}

	// Iterate through responses to find any that were not matched to a request.
	for _, res := range responses {
		// Skip responses that were already matched during the request iteration.
		if !processedResponses[res.id()] {
			// This response did not correspond to any request in the request batch.
			if !correlated(nil, res) { // Call with nil and res
				return
			}
		}
	}
}

// Add appends one or more items (`v`) to the Batch `b`.
//
// Example:
//
//	batch := jsonrpc2.NewBatch[*jsonrpc2.Request](0)
//	req1 := jsonrpc2.NewRequest(int64(1), "method1")
//	req2 := jsonrpc2.NewRequest("abc", "method2")
//	batch.Add(req1, req2)
//	fmt.Println(len(batch)) // Output: 2
func (b *Batch[B]) Add(v ...B) {
	*b = append(*b, v...)
}

// Grow increases the batch's capacity, if necessary, to guarantee space for
// another `n` elements without further reallocation. See [slices.Grow] for details.
//
// Example:
//
//	batch := jsonrpc2.NewBatch[*jsonrpc2.Response](2)
//	fmt.Println(cap(batch)) // Output: 2
//	batch.Grow(5)
//	fmt.Println(cap(batch)) // Output: >= 5
func (b *Batch[B]) Grow(n int) {
	*b = slices.Grow(*b, n)
}

// Contains checks if the batch includes an element with the specified [ID].
// It returns `true` if a match is found, `false` otherwise.
// Zero-value IDs are not searchable and will always return `false`.
//
// Example:
//
//	batch := jsonrpc2.NewBatch[*jsonrpc2.Request](0)
//	batch.Add(jsonrpc2.NewRequest(int64(1), "method")
//	idToFind := jsonrpc2.NewID(int64(1))
//	fmt.Println(batch.Contains(idToFind)) // Output: true
//	fmt.Println(batch.Contains(jsonrpc2.NewID(int64(2)))) // Output: false
func (b *Batch[B]) Contains(id ID) bool {
	// Index returns -1 if not found or if id is zero/null.
	return b.Index(id) >= 0
}

// Index returns the index of the first element in the batch that matches the given [ID].
// If no element matches, or if the provided `id` is a zero-value ID, it returns -1.
//
// Example:
//
//	batch := jsonrpc2.NewBatch[*jsonrpc2.Request](0)
//	req1 := jsonrpc2.NewRequest(int64(1), "method")
//	req2 := jsonrpc2.NewRequest("abc", "method")
//	batch.Add(req1, req2)
//	fmt.Println(batch.Index(jsonrpc2.NewID(int64(1)))) // Output: 0
//	fmt.Println(batch.Index(jsonrpc2.NewID("abc")))   // Output: 1
//	fmt.Println(batch.Index(jsonrpc2.NewID(int64(2)))) // Output: -1
func (b *Batch[B]) Index(id ID) int {
	// Zero IDs are not considered valid for matching specific requests/responses.
	if !id.IsZero() {
		for i, v := range *b {
			if id.Equal(v.id()) {
				return i
			}
		}
	}

	return -1
}

// Get retrieves the first element from the batch that matches the given [ID].
// It returns the element and `true` if found, or nil and `false` if not found, or if
// the `id` is zero.
//
// Example:
//
//	batch := jsonrpc2.NewBatch[*jsonrpc2.Request](0)
//	req1 := jsonrpc2.NewRequest("method", 1, nil)
//	batch.Add(req1)
//	foundReq, ok := batch.Get(jsonrpc2.NewID(int64(1)))
//	fmt.Println(ok, foundReq.Method) // Output: true method
//	_, ok = batch.Get(jsonrpc2.NewID(int64(2)))
//	fmt.Println(ok) // Output: false
func (b *Batch[B]) Get(id ID) (B, bool) {
	i := b.Index(id)
	if i < 0 {
		return nil, false
	}

	return (*b)[i], true
}

// Delete removes the first element found in the batch that matches the given [ID].
// It returns the deleted element and `true` if an element was deleted,
// or nil and `false` if no matching element was found,
// or if the `id` is zero.
// The remaining elements are shifted to fill the gap, maintaining order.
//
// Example:
//
//	batch := jsonrpc2.NewBatch[*jsonrpc2.Request](0)
//	req1 := jsonrpc2.NewRequest(int64(1), "method")
//	req2 := jsonrpc2.NewRequest(int64(2), "method")
//	batch.Add(req1, req2)
//	fmt.Println(len(batch)) // Output: 2
//	deleted, ok := batch.Delete(jsonrpc2.NewID(int64(1)))
//	fmt.Println(ok, deleted.ID.Value()) // Output: true 1
//	fmt.Println(len(batch)) // Output: 1
//	fmt.Println(batch[0].ID.Value()) // Output: 2
func (b *Batch[B]) Delete(id ID) (B, bool) {
	i := b.Index(id)
	if i < 0 {
		return nil, false
	}

	deleted := (*b)[i]

	*b = slices.Delete(*b, i, i+1)

	return deleted, true
}

// Reset clears all values from the batch and resets its length
// to 0 while maintaining its capacity. This allows for efficient
// reuse of batches.
func (b *Batch[B]) Reset() {
	// Clear all elements
	clear((*b)[:cap(*b)])

	// Reset length
	*b = (*b)[:0]
}
