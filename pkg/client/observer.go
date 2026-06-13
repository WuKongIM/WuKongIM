package client

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Observer receives optional low-cardinality client observations.
type Observer interface {
	OnConnect(ConnectEvent)
	OnSendQueue(SendQueueEvent)
	OnSendBatch(SendBatchEvent)
	OnSendAck(SendAckEvent)
	OnRecv(RecvEvent)
	OnError(ErrorEvent)
}

// ConnectEvent describes one CONNECT attempt.
type ConnectEvent struct {
	// Addr is the target TCP address.
	Addr string
	// UID is the session user id.
	UID string
	// Elapsed is the time spent on the CONNECT attempt.
	Elapsed time.Duration
	// Err is the CONNECT result error, if any.
	Err error
}

// SendQueueEvent describes local SEND queue admission.
type SendQueueEvent struct {
	// Depth is the queue depth after the observed operation.
	Depth int
	// Capacity is the configured queue capacity.
	Capacity int
	// Result is a low-cardinality admission result.
	Result string
}

// SendBatchEvent describes one writer batch flush.
type SendBatchEvent struct {
	// Records is the number of SEND frames in the batch.
	Records int
	// Bytes is the total encoded byte count in the batch.
	Bytes int
	// Elapsed is the time spent collecting and writing the batch.
	Elapsed time.Duration
	// Err is the batch write result error, if any.
	Err error
}

// SendAckEvent describes one matched SENDACK.
type SendAckEvent struct {
	// ReasonCode is the server SENDACK reason.
	ReasonCode frame.ReasonCode
	// Elapsed is the time from enqueue to SENDACK.
	Elapsed time.Duration
	// Err is the mapped SEND result error, if any.
	Err error
}

// RecvEvent describes one inbound RECV frame.
type RecvEvent struct {
	// ChannelType is the incoming message channel type.
	ChannelType uint8
	// Bytes is the inbound payload byte count.
	Bytes int
	// Dropped reports whether the client dropped the frame locally.
	Dropped bool
}

// ErrorEvent describes an internal client error.
type ErrorEvent struct {
	// Op is the low-cardinality operation name.
	Op string
	// Class is the low-cardinality error class.
	Class string
	// Err is the original error.
	Err error
}
