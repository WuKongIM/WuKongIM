package propose

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
)

var (
	// ErrInvalidRequest indicates that a propose request is missing routing information or command bytes.
	ErrInvalidRequest = errors.New("clusterv2/propose: invalid request")
	// ErrInvalidPayload indicates that a propose payload cannot be decoded.
	ErrInvalidPayload = errors.New("clusterv2/propose: invalid payload")
	// ErrNotLeader indicates that the target node is not the local Slot leader.
	ErrNotLeader = errors.New("clusterv2/propose: not leader")
)

// Request submits one Slot metadata command through a route snapshot.
type Request struct {
	// Key is used to compute routing when Target does not carry a hash slot.
	Key string
	// Command is the encoded Slot state machine command.
	Command []byte
	// Target optionally carries explicit routing information.
	Target Target
}

// Target identifies an explicit hash slot and optional physical Slot target.
type Target struct {
	// HashSlot is valid when HasHashSlot is true.
	HashSlot uint16
	// HasHashSlot distinguishes an explicit hash slot 0 from an omitted hash slot.
	HasHashSlot bool
	// SlotID is valid when HasSlotID is true.
	SlotID uint32
	// HasSlotID indicates SlotID was explicitly provided.
	HasSlotID bool
}

// Router resolves propose requests to Slot leaders.
type Router interface {
	// RouteKey routes key to a physical Slot.
	RouteKey(string) (routing.Route, error)
	// RouteHashSlot routes hashSlot to a physical Slot.
	RouteHashSlot(uint16) (routing.Route, error)
	// RouteSlot validates and routes an explicit Slot/hash-slot pair.
	RouteSlot(uint32, uint16) (routing.Route, error)
}

// SlotRuntime proposes payloads to local Slot leaders.
type SlotRuntime interface {
	// IsLocalLeader reports whether this node is leader for slotID.
	IsLocalLeader(slotID uint32) bool
	// Propose submits payload to the local Slot runtime.
	Propose(context.Context, uint32, []byte) error
}

// ForwardClient forwards proposals to remote Slot leaders.
type ForwardClient interface {
	// ForwardPropose forwards req to nodeID.
	ForwardPropose(context.Context, uint64, ForwardRequest) error
}

// ForwardRequest is the wire-level Slot proposal request.
type ForwardRequest struct {
	// SlotID is the target physical Slot.
	SlotID uint32
	// HashSlot is the logical hash slot carried in Payload.
	HashSlot uint16
	// Payload is the encoded Slot proposal payload.
	Payload []byte
}
