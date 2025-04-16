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
	for i, v := range *b {
		if id.Equal(v.id()) {
			return i
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
