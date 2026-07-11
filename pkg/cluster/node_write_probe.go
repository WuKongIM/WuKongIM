package cluster

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const maxWriteProbePhysicalSlots = 4

// ProbeWriteReady verifies that Slot metadata writes and new Channel placement can proceed.
func (n *Node) ProbeWriteReady(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := n.ensureForeground(); err != nil {
		return err
	}
	snapshot := n.Snapshot()
	if !snapshot.RoutesReady || !snapshot.SlotsReady || snapshot.HashSlotCount == 0 {
		return fmt.Errorf("%w: routes=%t slots=%t hashSlotCount=%d", ErrRouteNotReady, snapshot.RoutesReady, snapshot.SlotsReady, snapshot.HashSlotCount)
	}
	if err := n.probeChannelPlacementReady(); err != nil {
		if refreshErr := n.refreshControlSnapshot(ctx); refreshErr != nil {
			return fmt.Errorf("%w: refresh control snapshot: %v", err, refreshErr)
		}
		snapshot = n.Snapshot()
		if !snapshot.RoutesReady || !snapshot.SlotsReady || snapshot.HashSlotCount == 0 {
			return fmt.Errorf("%w: routes=%t slots=%t hashSlotCount=%d", ErrRouteNotReady, snapshot.RoutesReady, snapshot.SlotsReady, snapshot.HashSlotCount)
		}
		if err := n.probeChannelPlacementReady(); err != nil {
			return err
		}
	}
	validateStatuses := n.slotStatusRuntime != nil
	var statusPlan writeProbeStatusPlan
	var slotHashSlots map[uint32]uint16
	var slotLeaders map[uint32]uint64
	if validateStatuses {
		var err error
		statusPlan, err = n.captureWriteProbeStatusPlan(snapshot)
		if err != nil {
			return err
		}
		slotHashSlots = statusPlan.slotHashSlots
		slotLeaders = statusPlan.slotLeaders
		if err := n.validateWriteProbeSlotStatuses(ctx, statusPlan); err != nil {
			return err
		}
		if err := n.ensureWriteProbeStatusPlanCurrent(statusPlan); err != nil {
			return err
		}
	} else {
		var err error
		slotHashSlots, slotLeaders, err = n.collectWriteProbeRoutes(snapshot.HashSlotCount)
		if err != nil {
			return err
		}
	}

	slotIDs := selectWriteProbeSlotIDs(slotHashSlots, slotLeaders, n.cfg.NodeID)

	command := metafsm.EncodeNoopCommand()
	for _, slotID := range slotIDs {
		hashSlot := slotHashSlots[slotID]
		err := n.Propose(ctx, ProposeRequest{
			Command: command,
			Target: ProposeTarget{
				HashSlot:    hashSlot,
				HasHashSlot: true,
				SlotID:      slotID,
				HasSlotID:   true,
			},
		})
		if err != nil {
			return fmt.Errorf("write probe slot=%d hash_slot=%d: %w", slotID, hashSlot, err)
		}
	}
	if validateStatuses {
		if err := n.ensureWriteProbeStatusPlanCurrent(statusPlan); err != nil {
			return err
		}
	}
	n.markChannelDataPlaneLeaseVisible()
	return nil
}

// writeProbeStatusPlan freezes the route and local-assignment evidence used by one readiness probe.
type writeProbeStatusPlan struct {
	// revision is the control revision shared by the Node snapshot and route table.
	revision uint64
	// hashToSlot preserves the complete logical-to-physical route mapping for the final fence.
	hashToSlot []uint32
	// slotHashSlots selects one representative logical hash slot per physical Slot.
	slotHashSlots map[uint32]uint16
	// slotLeaders records the expected live leader for every routed physical Slot.
	slotLeaders map[uint32]uint64
	// slotLeaderTerms records the observed leader term paired with slotLeaders.
	slotLeaderTerms map[uint32]uint64
	// localAssignedSlotIDs includes followers as well as locally led Slots.
	localAssignedSlotIDs []uint32
	// localStatusRuntime reads node-local Multi-Raft status without an RPC round trip.
	localStatusRuntime slotStatusRuntime
	// remoteStatusCaller batches authoritative status reads by the expected remote leader.
	remoteStatusCaller clusternet.Caller
}

// captureWriteProbeStatusPlan reads one internally consistent control and route view.
func (n *Node) captureWriteProbeStatusPlan(snapshot Snapshot) (writeProbeStatusPlan, error) {
	if n == nil || n.router == nil {
		return writeProbeStatusPlan{}, ErrRouteNotReady
	}
	n.mu.RLock()
	stateRevision := n.snapshot.StateRevision
	controlSnapshot := n.controlSnapshot.Clone()
	statusRuntime := n.slotStatusRuntime
	statusCaller := n.slotStatusCaller
	n.mu.RUnlock()
	if snapshot.StateRevision != stateRevision || controlSnapshot.Revision != stateRevision {
		return writeProbeStatusPlan{}, fmt.Errorf("%w: write probe snapshot revision changed from %d to %d", ErrRouteNotReady, snapshot.StateRevision, stateRevision)
	}
	table := n.router.Table()
	if table == nil || table.Revision != stateRevision || table.HashSlotCount != snapshot.HashSlotCount || len(table.HashToSlot) != int(snapshot.HashSlotCount) {
		return writeProbeStatusPlan{}, fmt.Errorf("%w: write probe route revision=%d state_revision=%d hash_slots=%d", ErrRouteNotReady, routeTableRevision(table), stateRevision, snapshot.HashSlotCount)
	}
	plan := writeProbeStatusPlan{
		revision:           stateRevision,
		hashToSlot:         append([]uint32(nil), table.HashToSlot...),
		slotHashSlots:      make(map[uint32]uint16),
		slotLeaders:        make(map[uint32]uint64),
		slotLeaderTerms:    make(map[uint32]uint64),
		localStatusRuntime: statusRuntime,
		remoteStatusCaller: statusCaller,
	}
	for hashSlot, slotID := range table.HashToSlot {
		if slotID == 0 {
			return writeProbeStatusPlan{}, fmt.Errorf("%w: write probe hash_slot=%d has no physical Slot", ErrRouteNotReady, hashSlot)
		}
		leader := table.SlotLeaders[slotID]
		if leader == 0 {
			return writeProbeStatusPlan{}, fmt.Errorf("write probe route hash_slot=%d slot=%d: %w", hashSlot, slotID, ErrNoSlotLeader)
		}
		if previous, ok := plan.slotLeaders[slotID]; ok && previous != leader {
			return writeProbeStatusPlan{}, fmt.Errorf("%w: write probe slot=%d has inconsistent route leaders %d and %d", ErrRouteNotReady, slotID, previous, leader)
		}
		if _, ok := plan.slotHashSlots[slotID]; !ok {
			plan.slotHashSlots[slotID] = uint16(hashSlot)
			plan.slotLeaders[slotID] = leader
			plan.slotLeaderTerms[slotID] = table.SlotLeaderTerms[slotID]
		}
	}
	for _, slot := range controlSnapshot.Slots {
		if writeProbeContainsNodeID(slot.DesiredPeers, n.cfg.NodeID) {
			plan.localAssignedSlotIDs = append(plan.localAssignedSlotIDs, slot.SlotID)
		}
	}
	sort.Slice(plan.localAssignedSlotIDs, func(i, j int) bool { return plan.localAssignedSlotIDs[i] < plan.localAssignedSlotIDs[j] })
	return plan, nil
}

func routeTableRevision(table *routing.Table) uint64 {
	if table == nil {
		return 0
	}
	return table.Revision
}

func writeProbeContainsNodeID(nodeIDs []uint64, want uint64) bool {
	for _, nodeID := range nodeIDs {
		if nodeID == want {
			return true
		}
	}
	return false
}

// collectWriteProbeRoutes preserves the Propose-only custom override behavior.
func (n *Node) collectWriteProbeRoutes(hashSlotCount uint16) (map[uint32]uint16, map[uint32]uint64, error) {
	slotHashSlots := make(map[uint32]uint16)
	slotLeaders := make(map[uint32]uint64)
	for hashSlot := uint16(0); hashSlot < hashSlotCount; hashSlot++ {
		route, err := n.RouteHashSlot(hashSlot)
		if err != nil {
			return nil, nil, fmt.Errorf("write probe route hash_slot=%d: %w", hashSlot, err)
		}
		if route.Leader == 0 {
			return nil, nil, fmt.Errorf("write probe route hash_slot=%d slot=%d: %w", hashSlot, route.SlotID, ErrNoSlotLeader)
		}
		if leader, ok := slotLeaders[route.SlotID]; ok && leader != route.Leader {
			return nil, nil, fmt.Errorf("%w: write probe slot=%d has inconsistent route leaders %d and %d", ErrRouteNotReady, route.SlotID, leader, route.Leader)
		}
		if _, ok := slotHashSlots[route.SlotID]; !ok {
			slotHashSlots[route.SlotID] = hashSlot
			slotLeaders[route.SlotID] = route.Leader
		}
	}
	return slotHashSlots, slotLeaders, nil
}

// validateWriteProbeSlotStatuses proves every routed physical Slot has a live
// runtime that agrees with the foreground route before bounded noop proposals.
func (n *Node) validateWriteProbeSlotStatuses(ctx context.Context, plan writeProbeStatusPlan) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil || plan.localStatusRuntime == nil {
		return fmt.Errorf("%w: write probe local Slot status runtime unavailable", ErrRouteNotReady)
	}

	localSlots := make(map[uint32]struct{}, len(plan.localAssignedSlotIDs))
	for _, slotID := range plan.localAssignedSlotIDs {
		localSlots[slotID] = struct{}{}
	}
	remoteSlotIDs := make(map[uint64][]uint32)
	for slotID, leader := range plan.slotLeaders {
		switch {
		case leader == 0:
			return fmt.Errorf("%w: write probe slot=%d has no runtime leader", ErrRouteNotReady, slotID)
		case leader == n.cfg.NodeID:
			localSlots[slotID] = struct{}{}
		default:
			remoteSlotIDs[leader] = append(remoteSlotIDs[leader], slotID)
		}
	}
	localSlotIDs := make([]uint32, 0, len(localSlots))
	for slotID := range localSlots {
		localSlotIDs = append(localSlotIDs, slotID)
	}
	sort.Slice(localSlotIDs, func(i, j int) bool { return localSlotIDs[i] < localSlotIDs[j] })
	for _, slotID := range localSlotIDs {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		status, err := plan.localStatusRuntime.Status(multiraft.SlotID(slotID))
		if err != nil {
			if contextError := writeProbeContextError(ctx, err); contextError != nil {
				return contextError
			}
			return writeProbeLocalStatusError(n.cfg.NodeID, slotID, err)
		}
		if uint32(status.SlotID) != slotID {
			return fmt.Errorf("%w: write probe local Slot status requested=%d returned=%d", ErrRouteNotReady, slotID, status.SlotID)
		}
		if status.LeaderID == 0 {
			return fmt.Errorf("%w: write probe local Slot status slot=%d has zero leader", ErrRouteNotReady, slotID)
		}
		if expectedLeader, routed := plan.slotLeaders[slotID]; routed {
			if uint64(status.LeaderID) != expectedLeader {
				return fmt.Errorf("%w: write probe local Slot status slot=%d leader=%d want=%d", ErrRouteNotReady, slotID, status.LeaderID, expectedLeader)
			}
			if expectedTerm := plan.slotLeaderTerms[slotID]; status.Term != expectedTerm {
				return fmt.Errorf("%w: write probe local Slot status slot=%d term=%d want=%d", ErrRouteNotReady, slotID, status.Term, expectedTerm)
			}
		}
	}

	if len(remoteSlotIDs) == 0 {
		return nil
	}
	if plan.remoteStatusCaller == nil {
		return fmt.Errorf("%w: write probe remote Slot status caller unavailable", ErrRouteNotReady)
	}
	remoteLeaders := make([]uint64, 0, len(remoteSlotIDs))
	for leader := range remoteSlotIDs {
		remoteLeaders = append(remoteLeaders, leader)
	}
	sort.Slice(remoteLeaders, func(i, j int) bool { return remoteLeaders[i] < remoteLeaders[j] })
	client := slotStatusClient{caller: plan.remoteStatusCaller}
	for _, leader := range remoteLeaders {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		slotIDs := remoteSlotIDs[leader]
		sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
		statuses, err := client.Statuses(ctx, leader, slotIDs)
		if err != nil {
			if contextError := writeProbeContextError(ctx, err); contextError != nil {
				return contextError
			}
			return fmt.Errorf("%w: write probe remote Slot status leader=%d: %w", ErrRouteNotReady, leader, err)
		}
		if err := validateRemoteWriteProbeStatuses(leader, slotIDs, plan.slotLeaderTerms, statuses); err != nil {
			return err
		}
	}
	return nil
}

func writeProbeLocalStatusError(nodeID uint64, slotID uint32, err error) error {
	if errors.Is(err, multiraft.ErrSlotNotFound) || errors.Is(err, multiraft.ErrSlotClosed) || errors.Is(err, multiraft.ErrRuntimeClosed) {
		return fmt.Errorf("%w: write probe local Slot status node=%d slot=%d: %v: %w", ErrRouteNotReady, nodeID, slotID, err, ErrSlotNotFound)
	}
	return fmt.Errorf("%w: write probe local Slot status node=%d slot=%d: %w", ErrRouteNotReady, nodeID, slotID, err)
}

// ensureWriteProbeStatusPlanCurrent rejects probes whose route authority changed while they ran.
func (n *Node) ensureWriteProbeStatusPlanCurrent(plan writeProbeStatusPlan) error {
	if n == nil || n.router == nil {
		return ErrRouteNotReady
	}
	n.mu.RLock()
	stateRevision := n.snapshot.StateRevision
	controlRevision := n.controlSnapshot.Revision
	n.mu.RUnlock()
	if stateRevision != plan.revision || controlRevision != plan.revision {
		return fmt.Errorf("%w: write probe revision changed from %d to state=%d control=%d", ErrRouteNotReady, plan.revision, stateRevision, controlRevision)
	}
	current := n.router.Table()
	if current == nil || current.Revision != plan.revision || len(current.HashToSlot) != len(plan.hashToSlot) {
		return fmt.Errorf("%w: write probe route table changed during validation", ErrRouteNotReady)
	}
	for i, slotID := range plan.hashToSlot {
		if current.HashToSlot[i] != slotID {
			return fmt.Errorf("%w: write probe hash_slot=%d changed from slot=%d to slot=%d", ErrRouteNotReady, i, slotID, current.HashToSlot[i])
		}
	}
	for slotID, leader := range plan.slotLeaders {
		if current.SlotLeaders[slotID] != leader || current.SlotLeaderTerms[slotID] != plan.slotLeaderTerms[slotID] {
			return fmt.Errorf("%w: write probe slot=%d route authority changed", ErrRouteNotReady, slotID)
		}
	}
	return nil
}

func validateRemoteWriteProbeStatuses(leader uint64, slotIDs []uint32, expectedTerms map[uint32]uint64, statuses []routing.SlotStatus) error {
	expected := make(map[uint32]struct{}, len(slotIDs))
	for _, slotID := range slotIDs {
		expected[slotID] = struct{}{}
	}
	seen := make(map[uint32]struct{}, len(statuses))
	for _, status := range statuses {
		if _, ok := expected[status.SlotID]; !ok {
			return fmt.Errorf("%w: write probe remote Slot status leader=%d returned unexpected slot=%d", ErrRouteNotReady, leader, status.SlotID)
		}
		if _, ok := seen[status.SlotID]; ok {
			return fmt.Errorf("%w: write probe remote Slot status leader=%d returned duplicate slot=%d", ErrRouteNotReady, leader, status.SlotID)
		}
		seen[status.SlotID] = struct{}{}
		if status.Leader == 0 {
			return fmt.Errorf("%w: write probe remote Slot status slot=%d has zero leader", ErrRouteNotReady, status.SlotID)
		}
		if status.Leader != leader {
			return fmt.Errorf("%w: write probe remote Slot status slot=%d leader=%d want=%d", ErrRouteNotReady, status.SlotID, status.Leader, leader)
		}
		if expectedTerm := expectedTerms[status.SlotID]; status.LeaderTerm != expectedTerm {
			return fmt.Errorf("%w: write probe remote Slot status slot=%d term=%d want=%d", ErrRouteNotReady, status.SlotID, status.LeaderTerm, expectedTerm)
		}
	}
	for _, slotID := range slotIDs {
		if _, ok := seen[slotID]; !ok {
			return fmt.Errorf("%w: write probe remote Slot status leader=%d missing slot=%d", ErrRouteNotReady, leader, slotID)
		}
	}
	return nil
}

func writeProbeContextError(ctx context.Context, err error) error {
	if contextError := ctxErr(ctx); contextError != nil {
		return contextError
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func (n *Node) markChannelDataPlaneLeaseVisible() {
	if n == nil || n.channels == nil || n.channelDataPlaneLease == nil {
		return
	}
	n.channelDataPlaneLease.MarkVisible(time.Now())
}

func selectWriteProbeSlotIDs(slotHashSlots map[uint32]uint16, slotLeaders map[uint32]uint64, localNodeID uint64) []uint32 {
	slotIDs := make([]uint32, 0, len(slotHashSlots))
	for slotID := range slotHashSlots {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	if len(slotIDs) <= maxWriteProbePhysicalSlots {
		return slotIDs
	}

	selected := make(map[uint32]struct{}, maxWriteProbePhysicalSlots)
	add := func(slotID uint32) {
		if len(selected) >= maxWriteProbePhysicalSlots {
			return
		}
		selected[slotID] = struct{}{}
	}
	for _, slotID := range slotIDs {
		if slotLeaders[slotID] == localNodeID {
			add(slotID)
			break
		}
	}
	for _, slotID := range slotIDs {
		leader := slotLeaders[slotID]
		if leader != 0 && leader != localNodeID {
			add(slotID)
			break
		}
	}
	for _, slotID := range slotIDs {
		add(slotID)
	}

	out := make([]uint32, 0, len(selected))
	for slotID := range selected {
		out = append(out, slotID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (n *Node) probeChannelPlacementReady() error {
	replicaCount := int(n.cfg.Channel.ReplicaCount)
	candidates := n.channelDataNodes.DataNodes()
	seen := make(map[uint64]struct{}, len(candidates))
	for _, nodeID := range candidates {
		seen[nodeID] = struct{}{}
	}
	if len(seen) < replicaCount {
		return fmt.Errorf("%w: channel placement candidates %d below replica count %d", ErrRouteNotReady, len(seen), replicaCount)
	}
	return nil
}
