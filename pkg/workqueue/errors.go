package workqueue

import "errors"

var (
	// ErrInvalidConfig reports invalid workqueue construction options.
	ErrInvalidConfig = errors.New("workqueue: invalid config")
	// ErrClosed reports that admission has already been closed.
	ErrClosed = errors.New("workqueue: closed")
	// ErrFull reports that a bounded queue has no admission capacity.
	ErrFull = errors.New("workqueue: full")
)
