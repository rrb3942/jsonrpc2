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
	idable
}

// Batch is a slice of a type that can be used as a batch in calls and their responses.
type Batch[B Batchable] []B

// NewBatch returns a new batch with an initial capacity of size.
func NewBatch[B Batchable](size int) Batch[B] {
	return make(Batch[B], 0, size)
}

// BatchCorrelate compares ids in members of a Batch[*Request] and a Batch[*Response], calling the correlated function for each matched pair.
//
// Unmatched members will call correlated without an acommpaning member, so correlated must check if either [*Request] or [*Response] is nil.
//
// The correlated function must return true to continue processing, or may return false to break iteration early.
//
// BatchCorrelate does not handle cases of duplicate or reused ids, it expects every request/response to have a unique id.
func BatchCorrelate(requests Batch[*Request], responses Batch[*Response], correlated func(*Request, *Response) (cont bool)) {
	// Go through requests first
	for _, req := range requests {
		res, _ := responses.Get(req.id())
		if !correlated(req, res) {
			return
		}
	}

	// Now check for any responses that did not match to a request
	for _, res := range responses {
		_, ok := requests.Get(res.id())

		if !ok {
			if !correlated(nil, res) {
				return
			}
		}
	}
}

// Add appends one or more items to a Batch.
func (b *Batch[B]) Add(v ...B) {
	*b = append(*b, v...)
}

// Grows the batches capacity to guarantee space for another n elements.
//
// See [slices.Grow].
func (b *Batch[B]) Grow(n int) {
	*b = slices.Grow(*b, n)
}

// Contains returns true if an element of Batch shares the same ID as id.
func (b *Batch[B]) Contains(id ID) bool {
	i := b.Index(id)

	return i >= 0
}

// Index returns the index of the first element sharing id.
//
// If no elements match, returns -1.
func (b *Batch[B]) Index(id ID) int {
	if !id.IsZero() {
		for i, v := range *b {
			if id.Equal(v.id()) {
				return i
			}
		}
	}

	return -1
}

// It returns the element and a boolean indicate if it was found.
func (b *Batch[B]) Get(id ID) (B, bool) {
	i := b.Index(id)

	if i < 0 {
		return nil, false
	}

	return (*b)[i], true
}

// Delete deletes the first element sharing id in the batch, shifting all other elements to fill any gaps.
//
// It returns the deleted element and boolean of true if it deleted anything.
func (b *Batch[B]) Delete(id ID) (B, bool) {
	i := b.Index(id)

	if i < 0 {
		return nil, false
	}

	deleted := (*b)[i]

	oldlen := len(*b)
	*b = append((*b)[:i], (*b)[i+1:]...)

	clear((*b)[len(*b):oldlen])

	return deleted, true
}
