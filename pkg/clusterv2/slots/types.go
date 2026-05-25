package slots

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// Assignment is the local Slot convergence view consumed by Manager.
type Assignment struct {
	// SlotID is the physical Slot ID.
	SlotID uint32
	// DesiredPeers are node IDs that should host this Slot.
	DesiredPeers []uint64
	// PreferredLeader is the desired bootstrap owner when set.
	PreferredLeader uint64
	// HashSlots are logical hash slots currently owned by this physical Slot.
	HashSlots []uint16
	// Bootstrapped reports whether control/observation has evidence that the group exists.
	Bootstrapped bool
}

// Status is a compact Slot runtime observation used by routing.
type Status struct {
	// SlotID is the physical Slot ID.
	SlotID uint32
	// Leader is the best-known Slot leader node ID.
	Leader uint64
	// Peers are observed current voter node IDs.
	Peers []uint64
}

// StorageFactory creates local Raft storage for one Slot.
type StorageFactory func(slotID uint32) (multiraft.Storage, error)

// StateMachineFactory creates a local Slot state machine for one Slot.
type StateMachineFactory func(slotID uint32, hashSlots []uint16) (multiraft.StateMachine, error)

// Runtime is the subset of Multi-Raft operations used by clusterv2 Slot management.
type Runtime interface {
	// OpenSlot opens an existing local Slot runtime.
	OpenSlot(context.Context, multiraft.SlotOptions) error
	// BootstrapSlot bootstraps a new local Slot runtime.
	BootstrapSlot(context.Context, multiraft.BootstrapSlotRequest) error
	// Propose submits data to a local Slot runtime.
	Propose(context.Context, multiraft.SlotID, []byte) (multiraft.Future, error)
	// Status returns the latest local Slot status.
	Status(multiraft.SlotID) (multiraft.Status, error)
	// Step delivers a remote Raft message to a local Slot runtime.
	Step(context.Context, multiraft.Envelope) error
	// CloseSlot closes a local Slot runtime.
	CloseSlot(context.Context, multiraft.SlotID) error
}

// Config wires a Manager.
type Config struct {
	// LocalNode is this node's stable cluster identity.
	LocalNode uint64
	// Runtime is the local Multi-Raft runtime adapter.
	Runtime Runtime
	// Storage creates Slot Raft storage.
	Storage StorageFactory
	// StateMachine creates Slot state machines.
	StateMachine StateMachineFactory
}
