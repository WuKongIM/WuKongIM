package cluster

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"
)

// writeProbeProofTTL bounds undetected write-quorum loss even when runtime
// status still reports the same leader. Cache hits never renew this window.
const writeProbeProofTTL = 2 * time.Second

var errWriteProbeRecheck = errors.New("cluster: recheck live state after shared write probe")

// writeProbeCache retains at most one proof and one active proposal batch per node.
// Callers wait on a shared channel using their own contexts; no worker or queue is created.
type writeProbeCache struct {
	mu sync.Mutex
	// generation fences maintenance, restart and observed readiness failures,
	// including an in-flight proposal that completes after invalidation.
	generation uint64
	proof      *writeProbeProof
	flight     *writeProbeFlight
	// now defaults to time.Now; tests can control proof age without elapsed-time sleeps.
	now func() time.Time
}

type writeProbeProof struct {
	plan      writeProbeStatusPlan
	startedAt time.Time
}

type writeProbeFlight struct {
	done chan struct{}
	// err is published before done closes and remains immutable afterward.
	err error
}

func (c *writeProbeCache) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// invalidateWriteProbeProof prevents reuse across a loss of readiness or lifecycle boundary.
func (n *Node) invalidateWriteProbeProof() {
	n.writeProbe.mu.Lock()
	n.writeProbe.generation++
	n.writeProbe.proof = nil
	n.writeProbe.mu.Unlock()
}

// probeWriteReadyWithProof runs only after current local and remote Slot status
// validation. It coalesces the expensive Raft writes, never the live health gates.
func (n *Node) probeWriteReadyWithProof(ctx context.Context, plan writeProbeStatusPlan, generation uint64) error {
	c := &n.writeProbe
	c.mu.Lock()
	if c.generation != generation {
		c.mu.Unlock()
		return ErrRouteNotReady
	}
	now := c.currentTime()
	if proof := c.proof; proof != nil {
		age := now.Sub(proof.startedAt)
		if age >= 0 && age < writeProbeProofTTL && sameWriteProbeAuthority(plan, proof.plan) {
			c.mu.Unlock()
			if err := n.checkWriteProbeCurrent(ctx, plan); err != nil {
				return err
			}
			c.mu.Lock()
			current := c.generation == generation
			c.mu.Unlock()
			if !current {
				return ErrRouteNotReady
			}
			return nil
		}
	}
	if flight := c.flight; flight != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-flight.done:
			// The owning caller's deadline must not cancel another caller's
			// chance to prove readiness within its own remaining budget.
			if flight.err != nil && !errors.Is(flight.err, context.Canceled) && !errors.Is(flight.err, context.DeadlineExceeded) {
				return flight.err
			}
			return errWriteProbeRecheck
		}
	}
	flight := &writeProbeFlight{done: make(chan struct{})}
	c.flight = flight
	c.proof = nil
	c.mu.Unlock()

	err := n.proposeWriteProbe(ctx, plan.slotHashSlots, plan.slotLeaders)
	if err == nil {
		err = n.checkWriteProbeCurrent(ctx, plan)
	}
	c.mu.Lock()
	if err == nil && c.generation != generation {
		err = ErrRouteNotReady
	}
	if err == nil {
		c.proof = &writeProbeProof{plan: plan, startedAt: now}
	}
	flight.err = err
	c.flight = nil
	close(flight.done)
	c.mu.Unlock()
	if err == nil {
		// Reusing a proof must not extend the Channel data-plane lease.
		n.markChannelDataPlaneLeaseVisible()
	}
	return err
}

// checkWriteProbeCurrent fences both fresh commits and cache hits against
// cancellation, lifecycle changes, placement loss and changed route authority.
func (n *Node) checkWriteProbeCurrent(ctx context.Context, plan writeProbeStatusPlan) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := n.ensureForeground(); err != nil {
		return err
	}
	snapshot := n.Snapshot()
	if !snapshot.RoutesReady || !snapshot.SlotsReady || snapshot.HashSlotCount == 0 {
		return ErrRouteNotReady
	}
	if err := n.probeChannelPlacementReady(); err != nil {
		return err
	}
	return n.ensureWriteProbeStatusPlanCurrent(plan)
}

func sameWriteProbeAuthority(a, b writeProbeStatusPlan) bool {
	return a.revision == b.revision &&
		slices.Equal(a.hashToSlot, b.hashToSlot) &&
		slices.Equal(a.localAssignedSlotIDs, b.localAssignedSlotIDs) &&
		maps.Equal(a.slotLeaders, b.slotLeaders) &&
		maps.Equal(a.slotLeaderTerms, b.slotLeaderTerms)
}
