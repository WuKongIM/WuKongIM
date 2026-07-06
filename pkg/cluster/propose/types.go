package propose

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

var (
	// ErrInvalidRequest indicates that a propose request is missing routing information or command bytes.
	ErrInvalidRequest = errors.New("cluster/propose: invalid request")
	// ErrInvalidPayload indicates that a propose payload cannot be decoded.
	ErrInvalidPayload = errors.New("cluster/propose: invalid payload")
	// ErrNotLeader indicates that the target node is not the local Slot leader.
	ErrNotLeader = errors.New("cluster/propose: not leader")
	// ErrProposalBackpressure indicates that Slot proposal admission is saturated.
	ErrProposalBackpressure = errors.New("cluster/propose: proposal backpressure")
	// ErrBackgroundProposalThrottled indicates that background proposal admission is throttled.
	ErrBackgroundProposalThrottled = errors.New("cluster/propose: background proposal throttled")
)

// ProposalClass identifies the admission priority of a Slot metadata proposal.
type ProposalClass uint8

const (
	// ProposalClassForeground is the default class for user-facing metadata writes.
	ProposalClassForeground ProposalClass = iota
	// ProposalClassBackground is used for retryable projection writes.
	ProposalClassBackground
)

type proposalClassContextKey struct{}

// WithProposalClass marks proposals derived from ctx with class.
func WithProposalClass(ctx context.Context, class ProposalClass) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, proposalClassContextKey{}, normalizeProposalClass(class))
}

// ProposalClassFromContext returns the proposal class carried by ctx.
func ProposalClassFromContext(ctx context.Context) ProposalClass {
	if ctx == nil {
		return ProposalClassForeground
	}
	class, _ := ctx.Value(proposalClassContextKey{}).(ProposalClass)
	return normalizeProposalClass(class)
}

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

// ResultSlotRuntime proposes payloads to local Slot leaders and returns apply bytes.
type ResultSlotRuntime interface {
	// IsLocalLeader reports whether this node is leader for slotID.
	IsLocalLeader(slotID uint32) bool
	// ProposeResult submits payload and returns the Slot FSM apply result bytes.
	ProposeResult(context.Context, uint32, []byte) ([]byte, error)
}

// ForwardClient forwards proposals to remote Slot leaders.
type ForwardClient interface {
	// ForwardPropose forwards req to nodeID.
	ForwardPropose(context.Context, uint64, ForwardRequest) error
}

// ResultForwardClient forwards proposals and returns remote apply bytes.
type ResultForwardClient interface {
	// ForwardProposeResult forwards req to nodeID and returns the Slot FSM apply result bytes.
	ForwardProposeResult(context.Context, uint64, ForwardRequest) ([]byte, error)
}

// ForwardRequest is the wire-level Slot proposal request.
type ForwardRequest struct {
	// SlotID is the target physical Slot.
	SlotID uint32
	// HashSlot is the logical hash slot carried in Payload.
	HashSlot uint16
	// Class is the proposal admission class for the target Slot runtime.
	Class ProposalClass
	// WantResult requests the remote handler to return Slot FSM apply bytes.
	WantResult bool
	// Payload is the encoded Slot proposal payload.
	Payload []byte
}

func normalizeProposalClass(class ProposalClass) ProposalClass {
	switch class {
	case ProposalClassBackground:
		return ProposalClassBackground
	default:
		return ProposalClassForeground
	}
}
