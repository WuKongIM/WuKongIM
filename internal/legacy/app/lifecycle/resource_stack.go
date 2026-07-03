package lifecycle

import (
	"errors"
	"fmt"
	"sync"
)

// ResourceStack closes resources in reverse construction order unless ownership is released.
type ResourceStack struct {
	mu       sync.Mutex
	closers  []resourceCloser
	closed   bool
	released bool
}

type resourceCloser struct {
	name    string
	closeFn func() error
}

// Push registers a resource closer after the resource has been constructed.
func (s *ResourceStack) Push(name string, closeFn func() error) {
	if closeFn == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.released {
		return
	}
	s.closers = append(s.closers, resourceCloser{name: name, closeFn: closeFn})
}

// Close closes owned resources in reverse construction order.
func (s *ResourceStack) Close() error {
	s.mu.Lock()
	if s.closed || s.released {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	closers := s.closers
	s.closers = nil
	s.mu.Unlock()

	var err error
	for i := len(closers) - 1; i >= 0; i-- {
		closer := closers[i]
		if closeErr := closer.closeFn(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("%s: %w", closer.name, closeErr))
		}
	}
	return err
}

// Release transfers resource ownership to the successfully built App.
func (s *ResourceStack) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.released = true
	s.closers = nil
}
