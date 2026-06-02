package delivery

import "errors"

const defaultManagerAsyncWorkers = 1

// ErrManagerQueueFull reports that committed delivery work could not enter the bounded manager queue.
var ErrManagerQueueFull = errors.New("internalv2/runtime/delivery: manager queue full")

// ErrManagerClosed reports that the manager is not accepting async work.
var ErrManagerClosed = errors.New("internalv2/runtime/delivery: manager closed")

type managerState uint8

const (
	managerStateClosed managerState = iota
	managerStateOpen
	managerStateClosing
)

type managerCommand struct {
	envelope Envelope
}
