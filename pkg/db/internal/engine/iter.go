package engine

import "github.com/cockroachdb/pebble/v2"

// Iter wraps a Pebble iterator and returns copied keys and values.
type Iter struct {
	iter *pebble.Iterator
}

// First positions the iterator at the first key in bounds.
func (it *Iter) First() bool {
	return it != nil && it.iter != nil && it.iter.First()
}

// SeekGE positions the iterator at the first key greater than or equal to key.
func (it *Iter) SeekGE(key []byte) bool {
	return it != nil && it.iter != nil && it.iter.SeekGE(key)
}

// Next advances the iterator.
func (it *Iter) Next() bool {
	return it != nil && it.iter != nil && it.iter.Next()
}

// Key returns a copy of the current key.
func (it *Iter) Key() []byte {
	if it == nil || it.iter == nil {
		return nil
	}
	return append([]byte(nil), it.iter.Key()...)
}

// Value returns a copy of the current value.
func (it *Iter) Value() ([]byte, error) {
	if it == nil || it.iter == nil {
		return nil, nil
	}
	value, err := it.iter.ValueAndErr()
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), value...), nil
}

// Error returns any accumulated iterator error.
func (it *Iter) Error() error {
	if it == nil || it.iter == nil {
		return nil
	}
	return it.iter.Error()
}

// Close releases iterator resources.
func (it *Iter) Close() error {
	if it == nil || it.iter == nil {
		return nil
	}
	iter := it.iter
	it.iter = nil
	return iter.Close()
}
