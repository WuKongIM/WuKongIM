package types

import "time"

type Observer interface {
	OnConnectionOpen(event ConnectionEvent)
	OnConnectionClose(event ConnectionEvent)
	OnAuth(event AuthEvent)
	OnFrameIn(event FrameEvent)
	OnFrameOut(event FrameEvent)
	OnFrameHandled(event FrameHandleEvent)
}

// AsyncSendObserver receives optional observations from the asynchronous SEND dispatch path.
type AsyncSendObserver interface {
	OnAsyncSendQueue(event AsyncSendQueueEvent)
	OnAsyncSendBatch(event AsyncSendBatchEvent)
	OnAsyncSendDispatchWait(event AsyncSendDispatchWaitEvent)
}

type ConnectionEvent struct {
	Listener    string
	Network     string
	Protocol    string
	CloseReason CloseReason
}

type AuthEvent struct {
	ConnectionEvent
	Status   string
	Duration time.Duration
}

type FrameEvent struct {
	ConnectionEvent
	FrameType string
	Bytes     int
}

type FrameHandleEvent struct {
	ConnectionEvent
	FrameType string
	Duration  time.Duration
	Err       error
}

// AsyncSendQueueEvent reports aggregate asynchronous SEND queue occupancy.
type AsyncSendQueueEvent struct {
	// Depth is the current number of queued SEND frames.
	Depth int
	// Capacity is the total queue capacity across shards.
	Capacity int
}

// AsyncSendBatchEvent reports one asynchronous SEND batch flush.
type AsyncSendBatchEvent struct {
	// Records is the number of SEND frames in the batch.
	Records int
	// Bytes is the total SEND payload bytes in the batch.
	Bytes int
	// Wait is the age of the oldest SEND frame in the batch.
	Wait time.Duration
}

// AsyncSendDispatchWaitEvent reports the time a SEND frame waited before dispatch.
type AsyncSendDispatchWaitEvent struct {
	ConnectionEvent
	// Duration is the queue wait time before handler dispatch.
	Duration time.Duration
}
