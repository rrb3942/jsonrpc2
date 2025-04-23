package jsonrpc2

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewBatch(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	size := 10
	batch := NewBatch[*Request](size)

	tassert.NotNil(batch, "NewBatch should return a non-nil batch")
	tassert.Len(batch, 0, "NewBatch should return an empty batch")
	tassert.Equal(size, cap(batch), "NewBatch should return a batch with the specified capacity")
}

func TestBatch_Add(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	batch := NewBatch[*Request](0)
	tassert.Len(batch, 0)

	req1 := NewRequest(int64(1), "method1")
	req2 := NewRequest("req-2", "method2")

	batch.Add(req1)
	tassert.Len(batch, 1)
	tassert.Equal(req1, batch[0])

	batch.Add(req2)
	tassert.Len(batch, 2)
	tassert.Equal(req2, batch[1])

	// Add multiple
	req3 := NewRequest(int64(3), "method3")
	req4 := NewRequest("req-4", "method4")
	batch.Add(req3, req4)
	tassert.Len(batch, 4)
	tassert.Equal(req3, batch[2])
	tassert.Equal(req4, batch[3])
}

func TestBatch_Grow(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	batch := NewBatch[*Response](2)
	tassert.Equal(2, cap(batch))

	batch.Grow(5) // Grow by 5, total capacity should be at least len + 5 = 0 + 5 = 5
	tassert.GreaterOrEqual(cap(batch), 5, "Capacity should be at least 5 after growing")

	// Add some elements and grow again
	batch.Add(NewResponseWithResult(int64(1), "res1"))
	batch.Add(NewResponseWithResult("res-2", "res2"))
	tassert.Len(batch, 2)

	batch.Grow(10) // Grow by 10, total capacity should be at least len + 10 = 2 + 10 = 12
	tassert.GreaterOrEqual(cap(batch), 12, "Capacity should be at least 12 after growing again")
}

func TestBatch_Index_Contains_Get(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	id1 := NewID(int64(1))
	id2 := NewID("req-2")
	id3 := NewID(int64(3))
	idNotFound := NewID("not-found")

	req1 := NewRequest(int64(1), "method1")       // ID matches id1
	req2 := NewRequest("req-2", "method2")        // ID matches id2
	req3 := NewNotification("notify").AsRequest() // No ID (zero ID)

	batch := NewBatch[*Request](0)
	batch.Add(req1, req2, req3)

	// Test Index
	tassert.Equal(0, batch.Index(id1), "Index for id1 should be 0")
	tassert.Equal(1, batch.Index(id2), "Index for id2 should be 1")
	tassert.Equal(-1, batch.Index(id3), "Index for id3 (not present) should be -1")
	tassert.Equal(-1, batch.Index(idNotFound), "Index for idNotFound should be -1")
	tassert.Equal(-1, batch.Index(ID{}), "Index for zero ID (notification) should be -1") // Notification has zero ID

	// Test Contains
	tassert.True(batch.Contains(id1), "Contains id1 should be true")
	tassert.True(batch.Contains(id2), "Contains id2 should be true")
	tassert.False(batch.Contains(id3), "Contains id3 (not present) should be false")
	tassert.False(batch.Contains(idNotFound), "Contains idNotFound should be false")
	tassert.False(batch.Contains(ID{}), "Contains zero ID (notification) should be false")

	// Test Get
	foundReq, ok := batch.Get(id1)
	tassert.True(ok, "Get id1 should return true")
	tassert.Equal(req1, foundReq, "Get id1 should return req1")

	foundReq, ok = batch.Get(id2)
	tassert.True(ok, "Get id2 should return true")
	tassert.Equal(req2, foundReq, "Get id2 should return req2")

	foundReq, ok = batch.Get(id3)
	tassert.False(ok, "Get id3 (not present) should return false")
	tassert.Nil(foundReq, "Get id3 (not present) should return nil")

	foundReq, ok = batch.Get(idNotFound)
	tassert.False(ok, "Get idNotFound should return false")
	tassert.Nil(foundReq, "Get idNotFound should return nil")

	_, ok = batch.Get(ID{}) // Get notification should fail
	tassert.False(ok, "Get zero ID (notification) should return false")

	// Test on empty batch
	emptyBatch := NewBatch[*Request](0)
	tassert.Equal(-1, emptyBatch.Index(id1), "Index on empty batch should be -1")
	tassert.False(emptyBatch.Contains(id1), "Contains on empty batch should be false")
	foundReq, ok = emptyBatch.Get(id1)
	tassert.False(ok, "Get on empty batch should return false")
	tassert.Nil(foundReq, "Get on empty batch should return nil")
}

func TestBatch_Delete(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	id1 := NewID(int64(1))
	id2 := NewID("req-2")
	id3 := NewID(int64(3))
	idNotFound := NewID("not-found")

	req1 := NewRequest(int64(1), "method1")
	req2 := NewRequest("req-2", "method2")
	req3 := NewRequest(int64(3), "method3")

	batch := NewBatch[*Request](0)
	batch.Add(req1, req2, req3)
	initialLen := len(batch)
	initialCap := cap(batch)

	// Delete existing item (middle)
	deletedReq, ok := batch.Delete(id2)
	tassert.True(ok, "Delete id2 should return true")
	tassert.Equal(req2, deletedReq, "Delete id2 should return req2")
	tassert.Len(batch, initialLen-1, "Length should decrease by 1 after delete")
	tassert.Equal(initialCap, cap(batch), "Capacity should remain the same after delete")
	tassert.Equal(-1, batch.Index(id2), "Index of deleted item should be -1")
	tassert.Equal(0, batch.Index(id1), "Index of req1 should remain 0")
	tassert.Equal(1, batch.Index(id3), "Index of req3 should become 1") // Shifted left

	// Delete existing item (first)
	deletedReq, ok = batch.Delete(id1)
	tassert.True(ok, "Delete id1 should return true")
	tassert.Equal(req1, deletedReq, "Delete id1 should return req1")
	tassert.Len(batch, initialLen-2, "Length should decrease by 1 again")
	tassert.Equal(initialCap, cap(batch), "Capacity should remain the same")
	tassert.Equal(-1, batch.Index(id1), "Index of deleted item should be -1")
	tassert.Equal(0, batch.Index(id3), "Index of req3 should become 0") // Shifted left

	// Delete last remaining item
	deletedReq, ok = batch.Delete(id3)
	tassert.True(ok, "Delete id3 should return true")
	tassert.Equal(req3, deletedReq, "Delete id3 should return req3")
	tassert.Len(batch, 0, "Length should be 0 after deleting last item")
	tassert.Equal(initialCap, cap(batch), "Capacity should remain the same")
	tassert.Equal(-1, batch.Index(id3), "Index of deleted item should be -1")

	// Try deleting from empty batch
	deletedReq, ok = batch.Delete(id1)
	tassert.False(ok, "Delete from empty batch should return false")
	tassert.Nil(deletedReq, "Delete from empty batch should return nil")
	tassert.Len(batch, 0, "Length should remain 0")

	// Try deleting non-existent item
	batch.Add(req1) // Add one back
	deletedReq, ok = batch.Delete(idNotFound)
	tassert.False(ok, "Delete non-existent item should return false")
	tassert.Nil(deletedReq, "Delete non-existent item should return nil")
	tassert.Len(batch, 1, "Length should not change when deleting non-existent item")
	tassert.Equal(0, batch.Index(id1), "Index of existing item should not change")
}

func TestBatchCorrelate(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	// --- Test Case Setup ---
	req1 := NewRequest(int64(1), "method1")
	req2 := NewRequest("req-2", "method2")
	req3 := NewRequest(int64(3), "method3")       // No matching response
	req4 := NewNotification("notify").AsRequest() // Notification, no ID, no match

	res1 := NewResponseWithResult(int64(1), "result1")
	res2 := NewResponseWithError("req-2", ErrInternalError)
	res4 := NewResponseWithResult(int64(4), "result4") // No matching request
	resNull := NewResponseError(ErrParse)              // Null ID, no match

	requests := NewBatch[*Request](0)
	requests.Add(req1, req2, req3, req4)

	responses := NewBatch[*Response](0)
	responses.Add(res1, res2, res4, resNull)

	// --- Correlation Logic ---
	type correlationResult struct {
		req *Request
		res *Response
	}

	results := make([]correlationResult, 0)

	var mu sync.Mutex // Protect results slice if running subtests in parallel (though BatchCorrelate itself is serial)

	BatchCorrelate(requests, responses, func(req *Request, res *Response) bool {
		mu.Lock()
		results = append(results, correlationResult{req: req, res: res})
		mu.Unlock()

		return true // Continue processing
	})

	// --- Assertions ---
	tassert.Len(results, 6, "Should have 6 correlation results (2 matched + 1 unmatched req + 1 unmatched notification + 1 unmatched parse error + 1 unmatched res)")

	// Check each result - order matters based on BatchCorrelate implementation (requests first, then unmatched responses)
	expectedResults := []correlationResult{
		{req: req1, res: res1},   // Matched req1 <-> res1
		{req: req2, res: res2},   // Matched req2 <-> res2
		{req: req3, res: nil},    // Unmatched req3
		{req: req4, res: nil},    // Unmatched req4 (notification) - correlation uses ID, so it won't match null ID response
		{req: nil, res: res4},    // Unmatched res4
		{req: nil, res: resNull}, // Unmatched resNUll
	}

	// Comparison function
	compareResults := func(expected, actual []correlationResult) bool {
		if len(expected) != len(actual) {
			return false
		}

		for i := range expectedResults {
			tassert.Equal(expected[i], actual[i], "Correlation result mismatch")
		}

		return true // If we got here, they matched
	}

	// Perform the comparison
	if !compareResults(expectedResults, results) {
		// Log detailed results if the custom comparison fails overall assertion
		t.Logf("Expected Results: %+v", expectedResults)
		t.Logf("Actual Results:   %+v", results)
		tassert.Fail("Correlation results do not match expected results")
	}

	// Test early exit
	results = make([]correlationResult, 0) // Reset results

	BatchCorrelate(requests, responses, func(req *Request, res *Response) bool {
		mu.Lock()
		results = append(results, correlationResult{req: req, res: res})
		mu.Unlock()
		// Stop after the first correlation (req1 <-> res1)
		return req == nil && res == nil // Only continue if both are nil (never happens)
	})
	tassert.Len(results, 1, "Should have stopped after the first correlation")
	tassert.Equal(req1, results[0].req)
	tassert.Equal(res1, results[0].res)

	// Test with empty requests
	results = make([]correlationResult, 0)
	emptyRequests := NewBatch[*Request](0)
	BatchCorrelate(emptyRequests, responses, func(req *Request, res *Response) bool {
		mu.Lock()
		results = append(results, correlationResult{req: req, res: res})
		mu.Unlock()

		return true
	})
	// Should only contain the unmatched responses
	tassert.Len(results, 4, "Should contain only unmatched responses when requests are empty")

	for i := range responses {
		tassert.Nil(results[i].req) // All requests should be nil
		tassert.Equal(responses[i], results[i].res)
	}

	// Test with empty responses
	results = make([]correlationResult, 0)
	emptyResponses := NewBatch[*Response](0)
	BatchCorrelate(requests, emptyResponses, func(req *Request, res *Response) bool {
		mu.Lock()
		results = append(results, correlationResult{req: req, res: res})
		mu.Unlock()

		return true
	})

	tassert.Len(results, 4, "Should contain only unmatched requests when responses are empty")

	// Should only contain the unmatched requests
	for i := range requests {
		tassert.Nil(results[i].res) // All requests should be nil
		tassert.Equal(requests[i], results[i].req)
	}

	// Test with both empty
	results = make([]correlationResult, 0)

	BatchCorrelate(emptyRequests, emptyResponses, func(req *Request, res *Response) bool {
		mu.Lock()
		results = append(results, correlationResult{req: req, res: res})
		mu.Unlock()

		return true
	})
	tassert.Len(results, 0, "Should have no results when both batches are empty")
}

func TestBatch_MarshalJSON_Request(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	// Setup batch
	req1 := NewRequest(int64(1), "method1").WithParams(NewParamsArray([]string{"p1"}))
	req2 := NewNotification("notify").WithParams(NewParamsObject(map[string]int{"p2": 2})).AsRequest() // Notification
	req3 := NewRequest("req-3", "method3")                                                            // No params

	batch := NewBatch[*Request](0)
	batch.Add(req1, req2, req3)

	// Expected JSON
	// Note: Notifications have no ID field when marshaled as part of a request batch
	expectedJSON := `[
		{"jsonrpc":"2.0","method":"method1","params":["p1"],"id":1},
		{"jsonrpc":"2.0","method":"notify","params":{"p2":2}},
		{"jsonrpc":"2.0","method":"method3","id":"req-3"}
	]`

	// Marshal
	jsonData, err := Marshal(batch)
	tassert.NoError(err, "Marshaling request batch should not produce an error")
	tassert.JSONEq(expectedJSON, string(jsonData), "Marshaled request batch JSON should match expected")

	// Test empty batch
	emptyBatch := NewBatch[*Request](0)
	jsonData, err = Marshal(emptyBatch)
	tassert.NoError(err, "Marshaling empty request batch should not produce an error")
	tassert.Equal("[]", string(jsonData), "Marshaled empty request batch should be '[]'")

	// Test nil batch (should marshal as null)
	var nilBatch Batch[*Request]
	jsonData, err = Marshal(nilBatch)
	tassert.NoError(err, "Marshaling nil request batch should not produce an error")
	tassert.Equal("null", string(jsonData), "Marshaled nil request batch should be 'null'")
}

func TestBatch_UnmarshalJSON_Request(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	// Input JSON
	inputJSON := `[
		{"jsonrpc":"2.0","method":"method1","params":["p1"],"id":1},
		{"jsonrpc":"2.0","method":"notify","params":{"p2":2}},
		{"jsonrpc":"2.0","method":"method3","id":"req-3"},
		{"jsonrpc":"2.0","method":"invalid"}
	]` // Last one is technically a notification without params

	// Expected Batch
	req1 := NewRequest(int64(1), "method1").WithParams(NewParamsArray([]string{"p1"}))
	req2 := NewNotification("notify").WithParams(NewParamsObject(map[string]int{"p2": 2})).AsRequest()
	req3 := NewRequest("req-3", "method3")
	req4 := NewNotification("invalid").AsRequest() // No ID means notification

	expectedBatch := NewBatch[*Request](0)
	expectedBatch.Add(req1, req2, req3, req4)

	// Unmarshal
	var actualBatch Batch[*Request]
	err := Unmarshal([]byte(inputJSON), &actualBatch)
	tassert.NoError(err, "Unmarshaling valid request batch JSON should not produce an error")

	// Deep comparison is tricky due to unexported fields and potential raw messages.
	// Compare lengths and individual elements based on known fields.
	tassert.Len(actualBatch, len(expectedBatch), "Unmarshaled batch length mismatch")
	if len(actualBatch) == len(expectedBatch) {
		for i := range expectedBatch {
			tassert.Equal(expectedBatch[i].Method, actualBatch[i].Method, "Method mismatch at index %d", i)
			tassert.True(expectedBatch[i].ID.Equal(actualBatch[i].ID), "ID mismatch at index %d", i)
			// Comparing Params requires unmarshaling them, which adds complexity.
			// Check if params are present/absent as a basic check.
			tassert.Equal(expectedBatch[i].Params.IsZero(), actualBatch[i].Params.IsZero(), "Params presence mismatch at index %d", i)
		}
	}

	// Test empty array
	var emptyBatch Batch[*Request]
	err = Unmarshal([]byte("[]"), &emptyBatch)
	tassert.NoError(err, "Unmarshaling '[]' should not produce an error")
	tassert.NotNil(emptyBatch, "Unmarshaled empty batch should not be nil") // Should be an empty slice, not nil
	tassert.Len(emptyBatch, 0, "Unmarshaled empty batch should have length 0")

	// Test null
	var nullBatch Batch[*Request]
	err = Unmarshal([]byte("null"), &nullBatch)
	tassert.NoError(err, "Unmarshaling 'null' should not produce an error")
	tassert.Nil(nullBatch, "Unmarshaled null batch should be nil") // Should be nil slice

	// Test invalid JSON
	var invalidBatch Batch[*Request]
	err = Unmarshal([]byte(`[{"method":"test", "id":1`), &invalidBatch) // Malformed JSON
	tassert.Error(err, "Unmarshaling invalid JSON should produce an error")

	// Test invalid type (object instead of array)
	err = Unmarshal([]byte(`{"method":"test", "id":1}`), &invalidBatch)
	tassert.Error(err, "Unmarshaling JSON object into batch should produce an error")
	tassert.Contains(err.Error(), "cannot unmarshal object into Go value of type jsonrpc2.Batch[*jsonrpc2.Request]", "Error message should indicate type mismatch")
}

func TestBatch_MarshalJSON_Response(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	// Setup batch
	res1 := NewResponseWithResult(int64(1), "result1")
	res2 := NewResponseWithError("req-2", ErrInternalError.WithData("details"))
	res3 := NewResponseError(ErrParse) // Null ID error response

	batch := NewBatch[*Response](0)
	batch.Add(res1, res2, res3)

	// Expected JSON
	expectedJSON := `[
		{"jsonrpc":"2.0","result":"result1","id":1},
		{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error","data":"details"},"id":"req-2"},
		{"jsonrpc":"2.0","error":{"code":-32700,"message":"Parse error"},"id":null}
	]`

	// Marshal
	jsonData, err := Marshal(batch)
	tassert.NoError(err, "Marshaling response batch should not produce an error")
	tassert.JSONEq(expectedJSON, string(jsonData), "Marshaled response batch JSON should match expected")

	// Test empty batch
	emptyBatch := NewBatch[*Response](0)
	jsonData, err = Marshal(emptyBatch)
	tassert.NoError(err, "Marshaling empty response batch should not produce an error")
	tassert.Equal("[]", string(jsonData), "Marshaled empty response batch should be '[]'")

	// Test nil batch (should marshal as null)
	var nilBatch Batch[*Response]
	jsonData, err = Marshal(nilBatch)
	tassert.NoError(err, "Marshaling nil response batch should not produce an error")
	tassert.Equal("null", string(jsonData), "Marshaled nil response batch should be 'null'")
}

func TestBatch_UnmarshalJSON_Response(t *testing.T) {
	t.Parallel()
	tassert := assert.New(t)

	// Input JSON
	inputJSON := `[
		{"jsonrpc":"2.0","result":"result1","id":1},
		{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error","data":"details"},"id":"req-2"},
		{"jsonrpc":"2.0","error":{"code":-32700,"message":"Parse error"},"id":null},
		{"jsonrpc":"2.0","result":null,"id":3}
	]`

	// Expected Batch
	res1 := NewResponseWithResult(int64(1), "result1")
	res2 := NewResponseWithError("req-2", ErrInternalError.WithData("details"))
	res3 := NewResponseError(ErrParse) // Creates null ID
	res4 := NewResponseWithResult(int64(3), nil)

	expectedBatch := NewBatch[*Response](0)
	expectedBatch.Add(res1, res2, res3, res4)

	// Unmarshal
	var actualBatch Batch[*Response]
	err := Unmarshal([]byte(inputJSON), &actualBatch)
	tassert.NoError(err, "Unmarshaling valid response batch JSON should not produce an error")

	// Deep comparison is tricky. Compare lengths and key fields.
	tassert.Len(actualBatch, len(expectedBatch), "Unmarshaled batch length mismatch")
	if len(actualBatch) == len(expectedBatch) {
		for i := range expectedBatch {
			tassert.True(expectedBatch[i].ID.Equal(actualBatch[i].ID), "ID mismatch at index %d", i)
			tassert.Equal(expectedBatch[i].Error.IsZero(), actualBatch[i].Error.IsZero(), "Error presence mismatch at index %d", i)
			if !expectedBatch[i].Error.IsZero() {
				tassert.Equal(expectedBatch[i].Error.err.Code, actualBatch[i].Error.err.Code, "Error code mismatch at index %d", i)
				tassert.Equal(expectedBatch[i].Error.err.Message, actualBatch[i].Error.err.Message, "Error message mismatch at index %d", i)
				// Comparing Result/Error Data requires unmarshaling, skip for simplicity or add if needed
			}
			tassert.Equal(expectedBatch[i].Result.IsZero(), actualBatch[i].Result.IsZero(), "Result presence mismatch at index %d", i)
		}
	}

	// Test empty array
	var emptyBatch Batch[*Response]
	err = Unmarshal([]byte("[]"), &emptyBatch)
	tassert.NoError(err, "Unmarshaling '[]' should not produce an error")
	tassert.NotNil(emptyBatch, "Unmarshaled empty batch should not be nil")
	tassert.Len(emptyBatch, 0, "Unmarshaled empty batch should have length 0")

	// Test null
	var nullBatch Batch[*Response]
	err = Unmarshal([]byte("null"), &nullBatch)
	tassert.NoError(err, "Unmarshaling 'null' should not produce an error")
	tassert.Nil(nullBatch, "Unmarshaled null batch should be nil")

	// Test invalid JSON
	var invalidBatch Batch[*Response]
	err = Unmarshal([]byte(`[{"result":"ok", "id":1`), &invalidBatch) // Malformed JSON
	tassert.Error(err, "Unmarshaling invalid JSON should produce an error")

	// Test invalid type (object instead of array)
	err = Unmarshal([]byte(`{"result":"ok", "id":1}`), &invalidBatch)
	tassert.Error(err, "Unmarshaling JSON object into batch should produce an error")
	tassert.Contains(err.Error(), "cannot unmarshal object into Go value of type jsonrpc2.Batch[*jsonrpc2.Response]", "Error message should indicate type mismatch")
}
