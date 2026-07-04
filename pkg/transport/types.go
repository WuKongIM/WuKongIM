package transport

import "github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"

type NodeID = core.NodeID
type Priority = core.Priority
type FrameKind = core.FrameKind
type Discovery = core.Discovery
type Handler = core.Handler
type Observer = core.Observer
type Event = core.Event
type Stats = core.Stats
type Limits = core.Limits
type ServiceOptions = core.ServiceOptions
type OwnedBuffer = core.OwnedBuffer

const (
	PriorityRaft    = core.PriorityRaft
	PriorityControl = core.PriorityControl
	PriorityRPC     = core.PriorityRPC
	PriorityBulk    = core.PriorityBulk

	FrameKindData        = core.FrameKindData
	FrameKindNotify      = core.FrameKindNotify
	FrameKindRPCRequest  = core.FrameKindRPCRequest
	FrameKindRPCResponse = core.FrameKindRPCResponse
	FrameKindControl     = core.FrameKindControl
)

// NewOwnedBuffer wraps caller-owned bytes with an optional release callback.
func NewOwnedBuffer(data []byte, release func([]byte)) OwnedBuffer {
	return core.NewOwnedBuffer(data, release)
}

// CopyOwnedBuffer copies bytes into a new owned buffer.
func CopyOwnedBuffer(data []byte) OwnedBuffer {
	return core.CopyOwnedBuffer(data)
}
