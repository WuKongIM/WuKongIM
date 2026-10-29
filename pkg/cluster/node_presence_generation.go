package cluster

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// presenceReadView is bounded by configured HashSlotCount and published atomically.
// It is local committed knowledge, not instantaneous global revocation proof.
type presenceReadView struct {
	revision    uint64
	epoch       uint64
	owners      controller.PresenceOwnerSet
	ready       bool
	generations []controller.PresenceGeneration
}

// presenceRetiredView keeps a bounded immutable per-HashSlot high water. Only
// actually observed committed predecessors may advance it, never arbitrary CAS input.
type presenceRetiredView struct{ through []uint64 }

type presenceController interface {
	EnsurePresenceOwnerSet(context.Context) (controller.PresenceOwnerSet, error)
	ClaimPresenceGeneration(context.Context, controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error)
	GetPresenceGeneration(context.Context, uint16) (controller.PresenceGenerationSnapshot, error)
}

// EnablePresenceProtocol is called by composition only after all full owner and
// authority handlers are wired. An unwired Node advertises no business support.
func (n *Node) EnablePresenceProtocol(version uint32) error {
	if n == nil || version != controller.PresenceProtocolVersion {
		return controller.ErrPresenceGenerationNotReady
	}
	n.presenceProtocol.Store(version)
	return nil
}

// PresenceBootID is the equality-only owner identity shared with app wiring.
func (n *Node) PresenceBootID() uint64 {
	if n == nil {
		return 0
	}
	return n.presenceBoot
}

// PresenceCapabilities distinguishes schema support from actual business wiring.
func (n *Node) PresenceCapabilities() controller.PresenceCapabilities {
	if n == nil {
		return controller.PresenceCapabilities{}
	}
	return controller.PresenceCapabilities{NodeID: n.cfg.NodeID, BootID: n.presenceBoot, ControllerStateVersion: 1, ControllerCommandVersion: 1, PresenceProtocolVersion: n.presenceProtocol.Load()}
}

// EnsurePresenceOwnerSet activates the exact ingress roster through Controller consensus.
func (n *Node) EnsurePresenceOwnerSet(ctx context.Context) (controller.PresenceOwnerSet, error) {
	if err := n.ensureForeground(); err != nil {
		return controller.PresenceOwnerSet{}, err
	}
	leader, local, err := n.presenceController()
	if err != nil {
		return controller.PresenceOwnerSet{}, err
	}
	var owners controller.PresenceOwnerSet
	if local != nil {
		owners, err = local.EnsurePresenceOwnerSet(ctx)
	} else {
		owners, err = control.NewPresenceControlClient(n.transportClient).Ensure(ctx, leader)
	}
	if err == nil {
		n.refreshPresenceRead(ctx)
	}
	return owners, err
}

// ClaimPresenceGeneration surrounds the rare committed CAS with actual local
// Slot checks. Controller desired placement alone is never execution authority.
func (n *Node) ClaimPresenceGeneration(ctx context.Context, req controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
	if err := ctxErr(ctx); err != nil {
		return controller.PresenceGeneration{}, err
	}
	if n == nil || req.ServingBoot != n.presenceBoot || n.presenceProtocol.Load() != controller.PresenceProtocolVersion {
		return controller.PresenceGeneration{}, controller.ErrPresenceGenerationNotReady
	}
	if err := n.checkPresenceFence(req.Fence, true); err != nil {
		return controller.PresenceGeneration{}, err
	}
	leader, local, err := n.presenceController()
	if err != nil {
		return controller.PresenceGeneration{}, err
	}
	previous, err := n.presenceClaimPredecessor(req)
	if err != nil {
		return controller.PresenceGeneration{}, err
	}
	// Close this observed predecessor before any Controller submission. Even an
	// unknown/failed result cannot reopen it; other Hash Slots retain their gates.
	n.retirePresenceGeneration(req.Fence.HashSlot, previous)
	var result controller.PresenceGeneration
	if local != nil {
		result, err = local.ClaimPresenceGeneration(ctx, req)
	} else {
		result, err = control.NewPresenceControlClient(n.transportClient).Claim(ctx, leader, req)
	}
	if err != nil {
		return controller.PresenceGeneration{}, err
	}
	if result.Fence != req.Fence || result.OwnerSetEpoch != req.OwnerSetEpoch || result.ServingBoot != req.ServingBoot || result.PreviousGeneration != req.ExpectedGeneration || result.Generation <= req.ExpectedGeneration || result.AppliedRaftIndex != result.Generation {
		return controller.PresenceGeneration{}, controller.ErrPresenceGenerationConflict
	}
	if err := n.checkPresenceFence(req.Fence, true); err != nil {
		return controller.PresenceGeneration{}, err
	}
	n.refreshPresenceRead(ctx)
	return result, nil
}

// GetPresenceGeneration obtains G and M from one committed Controller cut.
func (n *Node) GetPresenceGeneration(ctx context.Context, hashSlot uint16) (controller.PresenceGenerationSnapshot, error) {
	if err := n.ensureForeground(); err != nil {
		return controller.PresenceGenerationSnapshot{}, err
	}
	leader, local, err := n.presenceController()
	if err != nil {
		return controller.PresenceGenerationSnapshot{}, err
	}
	var cut controller.PresenceGenerationSnapshot
	if local != nil {
		cut, err = local.GetPresenceGeneration(ctx, hashSlot)
	} else {
		cut, err = control.NewPresenceControlClient(n.transportClient).Get(ctx, leader, hashSlot)
	}
	if err == nil {
		n.publishPresenceCutAt(hashSlot, cut)
	}
	return cut, err
}

// ValidatePresenceGeneration is the rare owner Begin/UID-suspect check. It works
// on non-data owners; it does not pretend their local Slot is the authority.
func (n *Node) ValidatePresenceGeneration(ctx context.Context, claim controller.PresenceGeneration) error {
	if claim.Generation == 0 {
		return controller.ErrPresenceGenerationNotReady
	}
	if err := n.checkPresenceFence(claim.Fence, false); err != nil {
		return err
	}
	cut, err := n.GetPresenceGeneration(ctx, claim.Fence.HashSlot)
	if err != nil {
		return err
	}
	if cut.Generation != claim || cut.OwnerSet.Epoch != claim.OwnerSetEpoch {
		return controller.ErrPresenceGenerationConflict
	}
	own, serving := false, false
	for _, owner := range cut.OwnerSet.Owners {
		if owner.NodeID == n.cfg.NodeID && owner.CapabilityGeneration == n.presenceBoot {
			own = true
		}
		if owner.NodeID == claim.Fence.LeaderNodeID && owner.CapabilityGeneration == claim.ServingBoot {
			serving = true
		}
	}
	if !own || !serving || n.presenceProtocol.Load() != controller.PresenceProtocolVersion {
		return controller.ErrPresenceGenerationNotReady
	}

	if err := n.checkPresenceFence(claim.Fence, false); err != nil {
		return err
	}
	n.publishPresenceCut(cut)
	return n.CheckPresenceOwnerGeneration(claim)
}

// CheckPresenceOwnerGeneration checks only the applied cut and local owner boot.
// Page/End use it without a Controller probe; Begin uses Validate first.
func (n *Node) CheckPresenceOwnerGeneration(claim controller.PresenceGeneration) error {
	if n == nil || claim.Generation == 0 || n.presenceGenerationRetired(claim.Fence.HashSlot, claim.Generation) || n.presenceProtocol.Load() != controller.PresenceProtocolVersion {
		return controller.ErrPresenceGenerationNotReady
	}
	view := n.presenceRead.Load()
	if view == nil || !view.ready || int(claim.Fence.HashSlot) >= len(view.generations) || view.epoch != claim.OwnerSetEpoch || view.generations[claim.Fence.HashSlot] != claim {
		return controller.ErrPresenceGenerationNotReady
	}
	own := false
	serving := false
	for _, owner := range view.owners.Owners {
		if owner.NodeID == n.cfg.NodeID && owner.CapabilityGeneration == n.presenceBoot {
			own = true
		}
		if owner.NodeID == claim.Fence.LeaderNodeID && owner.CapabilityGeneration == claim.ServingBoot {
			serving = true
		}
	}
	if !own || !serving {
		return controller.ErrPresenceGenerationNotReady
	}
	return n.checkPresenceFence(claim.Fence, false)
}

// CheckPresenceGeneration performs no I/O and allocates no cluster-state clone.
// The serving gate must separately bound asynchronous Controller visibility.
func (n *Node) CheckPresenceGeneration(claim controller.PresenceGeneration) error {
	if n == nil || claim.Generation == 0 || n.presenceGenerationRetired(claim.Fence.HashSlot, claim.Generation) || claim.ServingBoot != n.presenceBoot || n.presenceProtocol.Load() != controller.PresenceProtocolVersion {
		return controller.ErrPresenceGenerationNotReady
	}
	view := n.presenceRead.Load()
	if view == nil || !view.ready || int(claim.Fence.HashSlot) >= len(view.generations) || view.epoch != claim.OwnerSetEpoch || view.generations[claim.Fence.HashSlot] != claim {
		return controller.ErrPresenceGenerationNotReady
	}
	return n.checkPresenceFence(claim.Fence, true)
}

func (n *Node) checkPresenceFence(f controller.PresenceFence, requireLocal bool) error {
	route, err := n.RouteHashSlot(f.HashSlot)
	if err != nil {
		return err
	}
	if route.SlotID != f.SlotID || route.Leader != f.LeaderNodeID || route.LeaderTerm != f.LeaderTerm || route.ConfigEpoch != f.ConfigEpoch {
		return controller.ErrPresenceGenerationNotReady
	}
	if !requireLocal {
		return nil
	}
	if f.LeaderNodeID != n.cfg.NodeID || n.slotStatusRuntime == nil {
		return controller.ErrPresenceGenerationNotReady
	}
	status, err := n.slotStatusRuntime.Status(multiraft.SlotID(f.SlotID))
	if err != nil {
		return err
	}
	if status.NodeID != multiraft.NodeID(n.cfg.NodeID) || status.LeaderID != status.NodeID || status.Term != f.LeaderTerm || status.Role != multiraft.RoleLeader {
		return controller.ErrPresenceGenerationNotReady
	}
	return nil
}

func (n *Node) presenceController() (uint64, presenceController, error) {
	if n == nil || n.control == nil {
		return 0, nil, controller.ErrNotStarted
	}
	leader := n.control.LeaderID()
	if leader == 0 {
		return 0, nil, controller.ErrNotLeader
	}
	if leader != n.cfg.NodeID {
		return leader, nil, nil
	}
	runtime, ok := n.control.(presenceController)
	if !ok {
		return 0, nil, controller.ErrPresenceGenerationNotReady
	}
	return leader, runtime, nil
}

func (n *Node) publishPresenceSnapshot(snapshot control.Snapshot) {
	view := &presenceReadView{revision: snapshot.Revision}
	if snapshot.Presence != nil && snapshot.HashSlots.Count > 0 {
		view.generations = make([]controller.PresenceGeneration, int(snapshot.HashSlots.Count))
		view.epoch = snapshot.Presence.OwnerSet.Epoch
		view.owners = snapshot.Presence.OwnerSet.Clone()
		view.ready = snapshot.PresenceRosterCurrent && !snapshot.Maintenance
		for _, generation := range snapshot.Presence.Generations {
			if int(generation.Fence.HashSlot) < len(view.generations) {
				view.generations[generation.Fence.HashSlot] = generation
			} else {
				view.ready = false
			}
		}
	}
	for {
		old := n.presenceRead.Load()
		if old != nil && old.revision > view.revision {
			return
		}
		if n.presenceRead.CompareAndSwap(old, view) {
			return
		}
	}
}
func (n *Node) refreshPresenceRead(ctx context.Context) {
	st, err := n.LocalState(ctx)
	if err != nil {
		return
	}
	snapshot, err := control.SnapshotFromController(st)
	if err == nil {
		n.publishPresenceSnapshot(snapshot)
	}
}

func (n *Node) probePresenceCapabilities(ctx context.Context, nodeID uint64, addr string) (controller.PresenceCapabilities, error) {
	if nodeID == 0 || addr == "" || len(addr) > 1024 {
		return controller.PresenceCapabilities{}, controller.ErrPresenceGenerationNotReady
	}
	if nodeID == n.cfg.NodeID {
		if addr != n.cfg.ListenAddr {
			return controller.PresenceCapabilities{}, controller.ErrPresenceGenerationNotReady
		}
		return n.PresenceCapabilities(), nil
	}
	if n.discovery != nil && n.transportClient != nil {
		resolved, err := n.discovery.Resolve(transport.NodeID(nodeID))
		if err == nil && resolved == addr {
			proof, err := control.NewPresenceControlClient(n.transportClient).Capabilities(ctx, nodeID)
			if err != nil {
				return controller.PresenceCapabilities{}, errors.Join(controller.ErrPresenceGenerationNotReady, err)
			}
			if proof.NodeID != nodeID {
				return controller.PresenceCapabilities{}, controller.ErrPresenceGenerationNotReady
			}
			return proof, nil
		}
	}
	if !n.presenceProbeMu.TryLock() {
		return controller.PresenceCapabilities{}, controller.ErrPresenceGenerationNotReady
	}
	defer n.presenceProbeMu.Unlock()
	if n.stopping.Load() {
		return controller.PresenceCapabilities{}, ErrStopping
	}
	if n.presenceProbePending != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := n.presenceProbePending.StopContext(stopCtx)
		cancel()
		if err != nil {
			return controller.PresenceCapabilities{}, err
		}
		n.presenceProbePending = nil
	}
	// An unjoined endpoint has no authoritative discovery row. A private client
	// pins its requested address and is joined before returning; no global map mutates.
	discovery := clusternet.NewDiscovery()
	discovery.Update([]clusternet.NodeAddress{{NodeID: nodeID, Addr: addr}})
	client := clusternet.NewTransportClient(clusternet.TransportClientConfig{NodeID: n.cfg.NodeID, Discovery: discovery, PoolSize: 1, DialTimeout: time.Second, QueueSize: 1})
	n.presenceProbePending = client
	proof, probeErr := control.NewPresenceControlClient(client).Capabilities(ctx, nodeID)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stopErr := client.StopContext(stopCtx)
	if stopErr == nil {
		n.presenceProbePending = nil
	}
	if probeErr != nil || stopErr != nil {
		return controller.PresenceCapabilities{}, errors.Join(probeErr, stopErr)
	}
	if proof.NodeID != nodeID {
		return controller.PresenceCapabilities{}, controller.ErrPresenceGenerationNotReady
	}
	return proof, nil
}

// RPC dispatch uses only the hosted Controller leader; forwarding is caller-owned
// so stale leader views cannot form a recursive forwarding loop.
type presenceControlBackend struct{ node *Node }

func (b presenceControlBackend) PresenceCapabilities() controller.PresenceCapabilities {
	return b.node.PresenceCapabilities()
}
func (b presenceControlBackend) local() (presenceController, error) {
	if err := b.node.ensureForeground(); err != nil {
		return nil, err
	}
	leader, local, err := b.node.presenceController()
	if err != nil {
		return nil, err
	}
	if leader != b.node.cfg.NodeID || local == nil {
		return nil, controller.ErrNotLeader
	}
	return local, nil
}
func (b presenceControlBackend) EnsurePresenceOwnerSet(ctx context.Context) (controller.PresenceOwnerSet, error) {
	local, err := b.local()
	if err != nil {
		return controller.PresenceOwnerSet{}, err
	}
	return local.EnsurePresenceOwnerSet(ctx)
}
func (b presenceControlBackend) ClaimPresenceGeneration(ctx context.Context, req controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
	local, err := b.local()
	if err != nil {
		return controller.PresenceGeneration{}, err
	}
	return local.ClaimPresenceGeneration(ctx, req)
}
func (b presenceControlBackend) GetPresenceGeneration(ctx context.Context, slot uint16) (controller.PresenceGenerationSnapshot, error) {
	local, err := b.local()
	if err != nil {
		return controller.PresenceGenerationSnapshot{}, err
	}
	return local.GetPresenceGeneration(ctx, slot)
}

func newPresenceBoot() (uint64, error) {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0, fmt.Errorf("presence boot: %w", err)
	}
	boot := binary.BigEndian.Uint64(value[:])
	if boot == 0 {
		return newPresenceBoot()
	}
	return boot, nil
}

// GetLocalPresenceGeneration reads only the applied local publication. It never
// manufactures a claim for a legacy request or performs a Controller probe.
func (n *Node) GetLocalPresenceGeneration(hashSlot uint16) (controller.PresenceGenerationSnapshot, error) {
	if n == nil || n.presenceProtocol.Load() != controller.PresenceProtocolVersion {
		return controller.PresenceGenerationSnapshot{}, controller.ErrPresenceGenerationNotReady
	}
	view := n.presenceRead.Load()
	if view == nil || !view.ready || int(hashSlot) >= len(view.generations) {
		return controller.PresenceGenerationSnapshot{}, controller.ErrPresenceGenerationNotReady
	}
	claim := view.generations[hashSlot]
	if claim.Generation == 0 || claim.OwnerSetEpoch != view.epoch {
		return controller.PresenceGenerationSnapshot{}, controller.ErrPresenceGenerationNotReady
	}
	if err := n.CheckPresenceOwnerGeneration(claim); err != nil {
		return controller.PresenceGenerationSnapshot{}, err
	}
	return controller.PresenceGenerationSnapshot{Revision: view.revision, Generation: claim, OwnerSet: view.owners.Clone()}, nil
}

// A rare linearizable owner Begin may arrive before the mirror watch. Publish
// that exact cut without accepting any lower-revision local state afterward.
func (n *Node) publishPresenceCut(cut controller.PresenceGenerationSnapshot) {
	if cut.Generation.Generation == 0 {
		return
	}
	n.publishPresenceCutAt(cut.Generation.Fence.HashSlot, cut)
}

// A committed Get can explicitly prove an unclaimed slot (G=0). Its M is still
// usable for the first CAS, but GetLocal/Check never turn zero into completeness.
func (n *Node) publishPresenceCutAt(hashSlot uint16, cut controller.PresenceGenerationSnapshot) {
	if cut.Revision == 0 || cut.OwnerSet.Epoch == 0 || (cut.Generation.Generation != 0 && cut.Generation.Fence.HashSlot != hashSlot) {
		return
	}
	n.mu.RLock()
	count := n.controlSnapshot.HashSlots.Count
	n.mu.RUnlock()
	if count == 0 {
		return
	}
	for {
		old := n.presenceRead.Load()
		if old != nil && old.revision > cut.Revision {
			return
		}
		view := &presenceReadView{revision: cut.Revision, epoch: cut.OwnerSet.Epoch, owners: cut.OwnerSet.Clone(), ready: true, generations: make([]controller.PresenceGeneration, int(count))}
		if old != nil && old.epoch == view.epoch {
			copy(view.generations, old.generations)
		}
		slot := int(hashSlot)
		if slot >= len(view.generations) {
			return
		}
		if view.generations[slot].Generation > cut.Generation.Generation {
			return
		}
		view.generations[slot] = cut.Generation
		if n.presenceRead.CompareAndSwap(old, view) {
			return
		}
	}
}

// Stop retains an incompletely joined direct probe for the same lifecycle retry.
func (n *Node) stopPresenceProbe(ctx context.Context) error {
	if !n.presenceProbeMu.TryLock() {
		return &nodeStopIncompleteError{cause: controller.ErrPresenceGenerationNotReady}
	}
	defer n.presenceProbeMu.Unlock()
	if n.presenceProbePending == nil {
		return nil
	}
	if err := n.presenceProbePending.StopContext(ctx); err != nil {
		return &nodeStopIncompleteError{cause: err}
	}
	n.presenceProbePending = nil
	return nil
}

// presenceClaimPredecessor binds retirement to a committed local cut. A stale
// mirror may refuse a claim; the preceding public Get publishes its fresh cut
// without adding a second probe or changing any hot lookup into a Controller RPC.
func (n *Node) presenceClaimPredecessor(req controller.ClaimPresenceGenerationRequest) (uint64, error) {
	view := n.presenceRead.Load()
	if view == nil || !view.ready || int(req.Fence.HashSlot) >= len(view.generations) || req.OwnerSetEpoch == 0 || view.epoch != req.OwnerSetEpoch {
		return 0, controller.ErrPresenceGenerationNotReady
	}
	serving := false
	for _, owner := range view.owners.Owners {
		if owner.NodeID == n.cfg.NodeID && owner.CapabilityGeneration == req.ServingBoot {
			serving = true
			break
		}
	}
	if !serving {
		return 0, controller.ErrPresenceGenerationNotReady
	}
	current := view.generations[req.Fence.HashSlot]
	if current.Generation != 0 && (current.Fence.HashSlot != req.Fence.HashSlot || current.AppliedRaftIndex != current.Generation || current.PreviousGeneration >= current.Generation) {
		return 0, controller.ErrPresenceGenerationNotReady
	}
	if current.Generation == req.ExpectedGeneration {
		return current.Generation, nil
	}
	// An exact idempotent retry proves its previous G from the committed record;
	// it must not retire the already committed successor returned by that retry.
	if current.Generation > req.ExpectedGeneration && current.PreviousGeneration == req.ExpectedGeneration && current.Fence == req.Fence && current.OwnerSetEpoch == req.OwnerSetEpoch && current.ServingBoot == req.ServingBoot {
		return req.ExpectedGeneration, nil
	}
	return 0, controller.ErrPresenceGenerationConflict
}

func (n *Node) presenceGenerationRetired(hashSlot uint16, generation uint64) bool {
	retired := n.presenceRetired.Load()
	return retired != nil && int(hashSlot) < len(retired.through) && generation <= retired.through[hashSlot]
}

func (n *Node) retirePresenceGeneration(hashSlot uint16, generation uint64) {
	if generation == 0 {
		return
	}
	view := n.presenceRead.Load()
	for {
		old := n.presenceRetired.Load()
		if old != nil && int(hashSlot) < len(old.through) && old.through[hashSlot] >= generation {
			return
		}
		count := max(len(view.generations), int(hashSlot)+1) // Preserve the verified slot if publication concurrently closes.
		if old != nil && len(old.through) > count {
			count = len(old.through)
		}
		next := &presenceRetiredView{through: make([]uint64, count)}
		if old != nil {
			copy(next.through, old.through)
		}
		next.through[hashSlot] = generation
		if n.presenceRetired.CompareAndSwap(old, next) {
			return
		}
	}
}
