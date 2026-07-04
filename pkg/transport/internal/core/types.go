package core

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// NodeID identifies a cluster node in transport routing.
type NodeID uint64

// Priority selects the scheduling lane for an outbound frame.
type Priority uint8

const (
	PriorityRaft Priority = iota + 1
	PriorityControl
	PriorityRPC
	PriorityBulk
)

// Valid reports whether the priority is one of the transport lanes.
func (p Priority) Valid() bool {
	return p >= PriorityRaft && p <= PriorityBulk
}

// Validate returns ErrInvalidPriority when the priority is outside the public contract.
func (p Priority) Validate() error {
	if p.Valid() {
		return nil
	}
	return fmt.Errorf("%w: %d", ErrInvalidPriority, p)
}

// FrameKind identifies the logical behavior of a wire frame.
type FrameKind uint8

const (
	FrameKindData FrameKind = iota + 1
	FrameKindNotify
	FrameKindRPCRequest
	FrameKindRPCResponse
	FrameKindControl
)

// Valid reports whether the frame kind is known to this transport version.
func (k FrameKind) Valid() bool {
	return k >= FrameKindData && k <= FrameKindControl
}

// Discovery resolves node IDs to dialable network addresses.
type Discovery interface {
	Resolve(nodeID NodeID) (addr string, err error)
}

// Handler processes a service payload and returns an optional response payload.
type Handler func(ctx context.Context, payload []byte) ([]byte, error)

// Observer receives transport events from non-hot-path observation drains.
type Observer interface {
	ObserveTransport(event Event)
}

// Event describes a transport lifecycle, scheduling, or service observation.
type Event struct {
	// Name identifies the observed transport event.
	Name string
	// NodeID is the peer or local node associated with the event.
	NodeID NodeID
	// SourceID identifies the connection-local source for aggregation; it is not exported as a metric label.
	SourceID uint64
	// Priority is the scheduling lane associated with the event.
	Priority Priority
	// ServiceID is the service associated with the event, when applicable.
	ServiceID uint16
	// ServiceAlias is the operator-facing service name registered with the service.
	ServiceAlias string
	// Kind is the wire frame kind associated with byte traffic events.
	Kind FrameKind
	// Result classifies the event outcome.
	Result string
	// Items is the queued item count or current count associated with the event.
	Items int
	// Capacity is the queued item capacity associated with the event.
	Capacity int
	// Bytes is the payload or queue byte count associated with the event.
	Bytes int
	// BytesCapacity is the queued byte capacity associated with the event.
	BytesCapacity int64
	// Inflight is the currently running handler or pending RPC count associated with the event.
	Inflight int
	// PoolRunning is the current direct ants/v2 pool running count, when available.
	PoolRunning int
	// PoolCapacity is the configured direct ants/v2 pool capacity, when available.
	PoolCapacity int
	// PoolWaiting is the current direct ants/v2 pool waiting count, when available.
	PoolWaiting int
	// Duration is the elapsed time associated with the event.
	Duration time.Duration
}

// Stats is a point-in-time snapshot of transport counters.
type Stats struct {
	// Peers is the number of known peer nodes.
	Peers int
	// Connections is the number of active transport connections.
	Connections int
	// QueuedItems is the total number of queued outbound or service items.
	QueuedItems int64
	// QueuedBytes is the total number of queued payload bytes.
	QueuedBytes int64
	// PendingRPC is the number of RPC calls awaiting responses.
	PendingRPC int64
}

// Limits bounds frame sizes, connection queues, batching, and I/O timeouts.
type Limits struct {
	// MaxFrameBodyBytes is the maximum body size accepted for a single frame; it must be positive.
	MaxFrameBodyBytes int
	// MaxQueuedBytesPerConn is the per-connection queued byte budget; it must be positive.
	MaxQueuedBytesPerConn int64
	// MaxQueuedItemsPerConn is the per-connection queued item budget; it must be positive.
	MaxQueuedItemsPerConn int
	// MaxBatchBytes is the maximum bytes written in one batch; it must be positive and no larger than MaxFrameBodyBytes.
	MaxBatchBytes int
	// MaxBatchFrames is the maximum frame count written in one batch; it must be positive.
	MaxBatchFrames int
	// DialFailureCooldown is the peer reconnect backoff after a dial failure; zero disables cooldown.
	DialFailureCooldown time.Duration
	// WriteTimeout bounds individual connection writes; zero disables the write deadline.
	WriteTimeout time.Duration
	// ReadIdleTimeout bounds idle reads; zero disables idle read timeout enforcement.
	ReadIdleTimeout time.Duration
}

// Validate rejects impossible transport limits before runtime components start.
func (l Limits) Validate() error {
	if l.MaxFrameBodyBytes <= 0 {
		return fmt.Errorf("%w: MaxFrameBodyBytes must be positive", ErrInvalidConfig)
	}
	if l.MaxQueuedBytesPerConn <= 0 {
		return fmt.Errorf("%w: MaxQueuedBytesPerConn must be positive", ErrInvalidConfig)
	}
	if l.MaxQueuedItemsPerConn <= 0 {
		return fmt.Errorf("%w: MaxQueuedItemsPerConn must be positive", ErrInvalidConfig)
	}
	if l.MaxBatchBytes <= 0 || l.MaxBatchBytes > l.MaxFrameBodyBytes {
		return fmt.Errorf("%w: MaxBatchBytes must be in 1..MaxFrameBodyBytes", ErrInvalidConfig)
	}
	if l.MaxBatchFrames <= 0 {
		return fmt.Errorf("%w: MaxBatchFrames must be positive", ErrInvalidConfig)
	}
	if l.DialFailureCooldown < 0 || l.WriteTimeout < 0 || l.ReadIdleTimeout < 0 {
		return fmt.Errorf("%w: timeouts must be non-negative", ErrInvalidConfig)
	}
	return nil
}

// ServiceOptions configures a registered service worker pool and queue.
type ServiceOptions struct {
	// Alias is the operator-facing name for this service in observations.
	Alias string
	// Concurrency is the number of workers for this service; it must be positive.
	Concurrency int
	// QueueSize is the maximum queued item count for this service; it must be positive.
	QueueSize int
	// MaxQueueBytes is the maximum queued payload bytes for this service; it must be positive.
	MaxQueueBytes int64
	// Timeout bounds handler execution for this service; zero disables per-request handler timeout.
	Timeout time.Duration
	// MaxPayload bounds accepted payload bytes for this service; zero means use the transport frame limit.
	MaxPayload int
}

// Validate rejects service settings that would create unbounded or unusable work queues.
func (o ServiceOptions) Validate() error {
	if o.Concurrency <= 0 {
		return fmt.Errorf("%w: service concurrency must be positive", ErrInvalidConfig)
	}
	if o.QueueSize <= 0 {
		return fmt.Errorf("%w: service queue size must be positive", ErrInvalidConfig)
	}
	if o.MaxQueueBytes <= 0 {
		return fmt.Errorf("%w: service max queue bytes must be positive", ErrInvalidConfig)
	}
	if o.Timeout < 0 {
		return fmt.Errorf("%w: service timeout must be non-negative", ErrInvalidConfig)
	}
	if o.MaxPayload < 0 {
		return fmt.Errorf("%w: service max payload must be non-negative", ErrInvalidConfig)
	}
	return nil
}

// RemoteError is a structured error returned by a remote service handler.
type RemoteError struct {
	// Code is a stable machine-readable remote error code.
	Code string
	// Message is the human-readable remote error detail.
	Message string
}

func (e RemoteError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

var (
	ErrStopped         = errors.New("transport: stopped")
	ErrTimeout         = errors.New("transport: timeout")
	ErrCanceled        = errors.New("transport: canceled")
	ErrNodeNotFound    = errors.New("transport: node not found")
	ErrQueueFull       = errors.New("transport: queue full")
	ErrMsgTooLarge     = errors.New("transport: message too large")
	ErrInvalidFrame    = errors.New("transport: invalid frame")
	ErrInvalidPriority = errors.New("transport: invalid priority")
	ErrDialFailed      = errors.New("transport: dial failed")
	ErrBusy            = errors.New("transport: busy")
	ErrInvalidConfig   = errors.New("transport: invalid config")
)

type ownedState struct {
	data     []byte
	release  func([]byte)
	released atomic.Bool
}

// OwnedBuffer carries payload bytes plus explicit release ownership.
type OwnedBuffer struct {
	state *ownedState
	data  []byte
}

// NewOwnedBuffer wraps caller-owned bytes with an optional release callback.
func NewOwnedBuffer(data []byte, release func([]byte)) OwnedBuffer {
	if release == nil {
		return OwnedBuffer{data: data}
	}
	return OwnedBuffer{state: &ownedState{data: data, release: release}}
}

// CopyOwnedBuffer copies bytes into a new owned buffer.
func CopyOwnedBuffer(data []byte) OwnedBuffer {
	copied := append([]byte(nil), data...)
	return NewOwnedBuffer(copied, nil)
}

// Bytes returns the current payload bytes.
func (b OwnedBuffer) Bytes() []byte {
	if b.state == nil {
		return b.data
	}
	if b.state.released.Load() {
		return nil
	}
	return b.state.data
}

// Len returns the payload length.
func (b OwnedBuffer) Len() int {
	return len(b.Bytes())
}

// Release releases the payload at most once.
func (b *OwnedBuffer) Release() {
	if b.state == nil {
		b.data = nil
		return
	}
	if !b.state.released.CompareAndSwap(false, true) {
		b.state = nil
		b.data = nil
		return
	}
	data := b.state.data
	release := b.state.release
	b.state.data = nil
	b.state = nil
	b.data = nil
	if release != nil {
		release(data)
	}
}
