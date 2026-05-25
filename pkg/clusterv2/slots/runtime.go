package slots

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// Adapter wraps a concrete Multi-Raft runtime for clusterv2 modules.
type Adapter struct {
	runtime *multiraft.Runtime
}

// NewAdapter wraps runtime.
func NewAdapter(runtime *multiraft.Runtime) *Adapter { return &Adapter{runtime: runtime} }

// OpenSlot opens a local Slot.
func (a *Adapter) OpenSlot(ctx context.Context, opts multiraft.SlotOptions) error {
	return a.runtime.OpenSlot(ctx, opts)
}

// BootstrapSlot bootstraps a local Slot.
func (a *Adapter) BootstrapSlot(ctx context.Context, req multiraft.BootstrapSlotRequest) error {
	return a.runtime.BootstrapSlot(ctx, req)
}

// Propose submits data to a local Slot and waits for commit.
func (a *Adapter) Propose(ctx context.Context, slotID multiraft.SlotID, payload []byte) (multiraft.Future, error) {
	return a.runtime.Propose(ctx, slotID, payload)
}

// Status returns local Slot status.
func (a *Adapter) Status(slotID multiraft.SlotID) (multiraft.Status, error) {
	return a.runtime.Status(slotID)
}

// Step delivers a remote Raft message.
func (a *Adapter) Step(ctx context.Context, env multiraft.Envelope) error {
	return a.runtime.Step(ctx, env)
}

// CloseSlot closes a local Slot.
func (a *Adapter) CloseSlot(ctx context.Context, slotID multiraft.SlotID) error {
	return a.runtime.CloseSlot(ctx, slotID)
}

var _ Runtime = (*Adapter)(nil)
