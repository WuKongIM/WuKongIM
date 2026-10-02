package mqttsession

import (
	"context"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
)

// RetiredBoot is immutable proof minted only by a terminal, fully drained
// registry. Its zero value proves nothing. It contains no client identity.
type RetiredBoot struct {
	nodeID, lastConnectionID uint64
	bootID                   string
}

// Identity returns the boot and inclusive issued-ID bound for durable recording.
func (p RetiredBoot) Identity() (nodeID uint64, bootID string, lastConnectionID uint64) {
	return p.nodeID, p.bootID, p.lastConnectionID
}

// Retirement cannot be minted from fencing, an empty running registry, elapsed
// leases or unresolved effects. Terminal admission prevents future reservations.
func (m *Owners) Retirement() (RetiredBoot, error) {
	if m == nil {
		return RetiredBoot{}, ErrOwnerInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.stopped || len(m.entries) != 0 {
		return RetiredBoot{}, ErrOwnerUnknown
	}
	return RetiredBoot{nodeID: m.opts.NodeID, bootID: m.opts.BootID, lastConnectionID: m.issued}, nil
}

// RetiredOwners proves quiescence from explicit immutable retirement facts.
// Missing facts, unrecognised formats and uncertainty must fail closed.
type RetiredOwners interface {
	Quiesce(context.Context, contract.Owner) error
}

// Isolation combines the current registry with older proved boot retirements
// for this node only. It never falls back after a current-boot registry failure.
type Isolation struct {
	Owners  *Owners
	Retired RetiredOwners
}

func (i Isolation) Quiesce(ctx context.Context, o contract.Owner) error {
	if i.Owners == nil || ctx == nil || o.Validate() != nil {
		return ErrOwnerInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.NodeID != i.Owners.opts.NodeID {
		return ErrOwnerUnknown
	}
	if o.BootID == i.Owners.opts.BootID {
		return i.Owners.Quiesce(ctx, o)
	}
	if i.Retired == nil {
		return ErrOwnerUnknown
	}
	if err := i.Retired.Quiesce(ctx, o); err != nil {
		return err
	}
	return ctx.Err()
}
