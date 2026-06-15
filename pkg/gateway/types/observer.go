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

// SessionErrorObserver receives optional low-cardinality session error observations.
type SessionErrorObserver interface {
	OnSessionError(event SessionErrorEvent)
}

// AsyncSendObserver receives optional observations from the asynchronous SEND dispatch path.
type AsyncSendObserver interface {
	OnAsyncSendQueue(event AsyncSendQueueEvent)
	OnAsyncSendBatch(event AsyncSendBatchEvent)
	OnAsyncSendDispatchWait(event AsyncSendDispatchWaitEvent)
}

// AsyncAuthObserver receives optional observations from the asynchronous CONNECT authentication path.
type AsyncAuthObserver interface {
	OnAsyncAuthQueue(event AsyncAuthQueueEvent)
	OnAsyncAuthAdmission(event AsyncAuthAdmissionEvent)
	OnAsyncAuthWait(event AsyncAuthWaitEvent)
}

// AsyncSendAdmissionObserver receives optional asynchronous SEND enqueue outcomes.
type AsyncSendAdmissionObserver interface {
	OnAsyncSendAdmission(event AsyncSendAdmissionEvent)
}

// TransportPressureObserver receives optional gateway transport pressure observations.
type TransportPressureObserver interface {
	OnTransportPressure(event TransportPressureEvent)
}

type ConnectionEvent struct {
	Listener    string
	Network     string
	Protocol    string
	CloseReason CloseReason
}

type AuthEvent struct {
	ConnectionEvent
	Status string
	// Failure is a bounded failure class for failed authentication attempts.
	Failure  string
	Duration time.Duration
}

// AuthFailureClassifier lets entry adapters expose a bounded auth failure class.
type AuthFailureClassifier interface {
	GatewayAuthFailure() string
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

// SessionErrorEvent reports a non-benign session error without high-cardinality raw error text.
type SessionErrorEvent struct {
	ConnectionEvent
	// Class is a bounded error class, usually derived from CloseReason.
	Class string
}

// AsyncSendQueueEvent reports aggregate asynchronous SEND queue occupancy.
type AsyncSendQueueEvent struct {
	// Depth is the current number of queued SEND frames.
	Depth int
	// Capacity is the total queue capacity across shards.
	Capacity int
}

// AsyncAuthQueueEvent reports aggregate asynchronous auth queue occupancy.
type AsyncAuthQueueEvent struct {
	// Depth is the current number of queued CONNECT auth tasks.
	Depth int
	// Capacity is the total async auth queue capacity.
	Capacity int
	// Workers is the number of async auth workers consuming the queue.
	Workers int
}

// AsyncAuthAdmissionEvent reports one asynchronous auth enqueue outcome.
type AsyncAuthAdmissionEvent struct {
	// Result is a low-cardinality enqueue outcome such as ok or full.
	Result string
}

// AsyncAuthWaitEvent reports how long a CONNECT waited before authentication work began.
type AsyncAuthWaitEvent struct {
	ConnectionEvent
	// Duration is the queue wait time before auth work started.
	Duration time.Duration
}

// AsyncSendAdmissionEvent reports one asynchronous SEND enqueue outcome.
type AsyncSendAdmissionEvent struct {
	// Result is a low-cardinality enqueue outcome such as ok or full.
	Result string
}

// TransportPressureEvent reports aggregate gateway transport queue or byte pressure.
type TransportPressureEvent struct {
	// Name is a low-cardinality transport pressure source name.
	Name string
	// Queue is a low-cardinality queue group name.
	Queue string
	// Depth is the current queue item depth when available.
	Depth int
	// Capacity is the queue item capacity when available.
	Capacity int
	// Bytes is the current byte pressure when available.
	Bytes int64
	// BytesCapacity is the configured byte capacity when available.
	BytesCapacity int64
	// Result is a low-cardinality admission outcome such as ok, full, closed, or too_large.
	Result string
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
