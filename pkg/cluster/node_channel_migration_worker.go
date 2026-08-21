package cluster

import (
	"context"
	"sort"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelwrapper "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

// ListRunnableMigrationTasks lists active migration tasks owned by locally led physical Slots.
func (n *Node) ListRunnableMigrationTasks(ctx context.Context, localNode uint64, limit int) ([]metadb.ChannelMigrationTask, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if n == nil || localNode != n.cfg.NodeID || limit <= 0 {
		return nil, nil
	}
	if n.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	slotIDs, err := n.LocalLeaderSlotIDs(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := n.LocalControlSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]metadb.ChannelMigrationTask, 0, limit)
	for _, slotID := range slotIDs {
		hashSlots := hashSlotsOfPhysicalSlot(snapshot.HashSlots, slotID)
		for _, hashSlot := range hashSlots {
			remaining := limit - len(out)
			if remaining <= 0 {
				return out, nil
			}
			tasks, err := n.defaultSlotMetaDB.ForHashSlot(hashSlot).ListActiveChannelMigrationTasks(ctx, remaining)
			if err != nil {
				return nil, err
			}
			out = append(out, tasks...)
		}
	}
	return out, nil
}

// LocalLeaderSlotIDs returns physical Slot IDs currently led by this node.
func (n *Node) LocalLeaderSlotIDs(ctx context.Context) ([]uint32, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotRuntime == nil || n.defaultSlotProposer == nil {
		return nil, ErrNotStarted
	}
	slotIDs := n.defaultSlotRuntime.Slots()
	out := make([]uint32, 0, len(slotIDs))
	for _, slotID := range slotIDs {
		id := uint32(slotID)
		if n.defaultSlotProposer.IsLocalLeader(id) {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// LocalLeaderHashSlots returns physical hash slots whose logical Slot Raft
// groups are currently led by this node.
func (n *Node) LocalLeaderHashSlots(ctx context.Context) ([]metadb.HashSlot, error) {
	slotIDs, err := n.LocalLeaderSlotIDs(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := n.LocalControlSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]metadb.HashSlot, 0, int(snapshot.HashSlots.Count))
	for _, slotID := range slotIDs {
		for _, hashSlot := range hashSlotsOfPhysicalSlot(snapshot.HashSlots, slotID) {
			result = append(result, metadb.HashSlot(hashSlot))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

// IsLocalLeaderHashSlot reports whether this node currently leads the logical
// Slot Raft group that owns hashSlot.
func (n *Node) IsLocalLeaderHashSlot(ctx context.Context, hashSlot metadb.HashSlot) (bool, error) {
	if err := ctxErr(ctx); err != nil {
		return false, err
	}
	if err := n.ensureForeground(); err != nil {
		return false, err
	}
	if n.defaultSlotProposer == nil {
		return false, ErrNotStarted
	}
	route, err := n.RouteHashSlot(uint16(hashSlot))
	if err != nil {
		return false, err
	}
	return n.defaultSlotProposer.IsLocalLeader(route.SlotID), nil
}

// ListPersonDirectoryTaskPage reads one locally led source hash-slot page.
func (n *Node) ListPersonDirectoryTaskPage(ctx context.Context, hashSlot metadb.HashSlot, after metadb.PersonDirectoryTaskCursor, limit int) ([]metadb.PersonDirectoryTask, metadb.PersonDirectoryTaskCursor, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, metadb.PersonDirectoryTaskCursor{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, metadb.PersonDirectoryTaskCursor{}, false, err
	}
	if n.defaultSlotMetaDB == nil || n.defaultSlotProposer == nil {
		return nil, metadb.PersonDirectoryTaskCursor{}, false, ErrNotStarted
	}
	route, err := n.RouteHashSlot(uint16(hashSlot))
	if err != nil {
		return nil, metadb.PersonDirectoryTaskCursor{}, false, err
	}
	if !n.defaultSlotProposer.IsLocalLeader(route.SlotID) {
		return nil, metadb.PersonDirectoryTaskCursor{}, false, ErrNotLeader
	}
	return n.defaultSlotMetaDB.ForHashSlot(uint16(hashSlot)).ListPersonDirectoryTaskPage(ctx, after, limit)
}

// ValidatePersonDirectoryTasks rechecks exact source generations immediately
// before UID-owned membership writes. Results remain aligned so one stale
// source task cannot suppress independent projections in the same worker page.
func (n *Node) ValidatePersonDirectoryTasks(ctx context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	results := make([]error, len(tasks))
	if err := ctxErr(ctx); err != nil {
		return fillPersonDirectoryTaskErrors(results, err)
	}
	if err := n.ensureForeground(); err != nil {
		return fillPersonDirectoryTaskErrors(results, err)
	}
	if len(tasks) == 0 || len(tasks) > metafsm.MaxPersonDirectoryBatchItems || n.defaultSlotMetaDB == nil || n.defaultSlotProposer == nil {
		return fillPersonDirectoryTaskErrors(results, metadb.ErrInvalidArgument)
	}
	channelIDs := make([]string, len(tasks))
	for i, task := range tasks {
		if task.ChannelID == "" || task.ChannelType != 1 || task.Generation == 0 {
			results[i] = metadb.ErrInvalidArgument
		}
		channelIDs[i] = task.ChannelID
	}
	routes, err := n.RouteKeysPartial(channelIDs)
	if err != nil {
		return fillUnsetPersonDirectoryTaskErrors(results, err)
	}
	for i, routed := range routes {
		if results[i] != nil {
			continue
		}
		if routed.Err != nil {
			results[i] = routed.Err
			continue
		}
		route := routed.Route
		if route.HashSlot != uint16(tasks[i].HashSlot) || !n.defaultSlotProposer.IsLocalLeader(route.SlotID) {
			results[i] = metadb.ErrStaleMeta
			continue
		}
		current, ok, readErr := n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetPersonDirectoryTask(ctx, tasks[i].ChannelID, tasks[i].ChannelType)
		switch {
		case readErr != nil:
			results[i] = readErr
		case !ok || current.Generation != tasks[i].Generation:
			results[i] = metadb.ErrStaleMeta
		}
	}
	return results
}

// CompletePersonDirectoryTasks commits task deletion and ready state in
// bounded commands grouped by the current source Slot leaders.
func (n *Node) CompletePersonDirectoryTasks(ctx context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	results := make([]error, len(tasks))
	if err := ctxErr(ctx); err != nil {
		return fillPersonDirectoryTaskErrors(results, err)
	}
	if err := n.ensureForeground(); err != nil {
		return fillPersonDirectoryTaskErrors(results, err)
	}
	if len(tasks) == 0 || len(tasks) > metafsm.MaxPersonDirectoryBatchItems {
		return fillPersonDirectoryTaskErrors(results, metadb.ErrInvalidArgument)
	}
	channelIDs := make([]string, len(tasks))
	for i, task := range tasks {
		if task.ChannelID == "" || task.ChannelType != 1 || task.Generation == 0 {
			results[i] = metadb.ErrInvalidArgument
		}
		channelIDs[i] = task.ChannelID
	}
	routes, err := n.RouteKeysPartial(channelIDs)
	if err != nil {
		return fillUnsetPersonDirectoryTaskErrors(results, err)
	}
	type completionItem struct {
		item  metafsm.PersonDirectoryCompletionBatchItem
		index int
	}
	groups := make(map[uint32][]completionItem)
	for i, routed := range routes {
		if results[i] != nil {
			continue
		}
		if routed.Err != nil {
			results[i] = routed.Err
			continue
		}
		route := routed.Route
		if route.HashSlot != uint16(tasks[i].HashSlot) {
			results[i] = metadb.ErrStaleMeta
			continue
		}
		groups[route.SlotID] = append(groups[route.SlotID], completionItem{
			item: metafsm.PersonDirectoryCompletionBatchItem{HashSlot: route.HashSlot, ChannelID: tasks[i].ChannelID, ChannelType: tasks[i].ChannelType, Generation: tasks[i].Generation}, index: i,
		})
	}
	slotIDs := make([]uint32, 0, len(groups))
	for slotID := range groups {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	for _, slotID := range slotIDs {
		group := groups[slotID]
		items := make([]metafsm.PersonDirectoryCompletionBatchItem, len(group))
		for i := range group {
			items[i] = group[i].item
		}
		command, err := metafsm.EncodeCompletePersonDirectoryTaskBatchCommandChecked(items)
		if err == nil {
			err = n.Propose(ctx, ProposeRequest{Command: command, Target: ProposeTarget{
				HashSlot: items[0].HashSlot, HasHashSlot: true, SlotID: slotID, HasSlotID: true,
			}})
		}
		for _, grouped := range group {
			results[grouped.index] = err
		}
	}
	return results
}

func fillPersonDirectoryTaskErrors(results []error, err error) []error {
	for i := range results {
		results[i] = err
	}
	return results
}

func fillUnsetPersonDirectoryTaskErrors(results []error, err error) []error {
	for i := range results {
		if results[i] == nil {
			results[i] = err
		}
	}
	return results
}

// ListChannelRuntimeMetaPage reads runtime metadata rows for legacy callers that do not need hash-slot provenance.
func (n *Node) ListChannelRuntimeMetaPage(ctx context.Context, slotID uint32, cursor metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	return n.ScanChannelRuntimeMetaSlotPage(ctx, slotID, cursor, limit)
}

// ActiveChannelMigration reports whether id already has an active migration task via the routed migration store.
func (n *Node) ActiveChannelMigration(ctx context.Context, id ch.ChannelID) (bool, error) {
	store := n.ChannelMigrationStore()
	if store == nil {
		return false, ErrNotStarted
	}
	_, ok, err := store.GetActive(ctx, id)
	return ok, err
}

// ActiveChannelMigrationInHashSlot reports whether id has an active task in a locally led hash-slot shard.
func (n *Node) ActiveChannelMigrationInHashSlot(ctx context.Context, hashSlot uint16, id ch.ChannelID) (bool, error) {
	_, ok, err := n.getActiveChannelMigrationLocalTask(ctx, hashSlot, id.ID, int64(id.Type))
	return ok, err
}

// ControlSnapshot adapts LocalControlSnapshot to the repair scanner source contract.
func (n *Node) ControlSnapshot(ctx context.Context) (control.Snapshot, error) {
	return n.LocalControlSnapshot(ctx)
}

// ProbeChannel reads one local or remote Channel runtime proof.
func (n *Node) ProbeChannel(ctx context.Context, nodeID uint64, channelID string, channelType uint8) (ch.RuntimeProbeChannel, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.RuntimeProbeChannel{}, err
	}
	if n == nil || nodeID == 0 {
		return ch.RuntimeProbeChannel{}, ErrNotStarted
	}
	if nodeID == n.cfg.NodeID {
		return n.probeLocalChannelRuntime(ctx, channelID, channelType)
	}
	resp, err := n.callChannelMigrationMetaRPC(ctx, nodeID, channelMigrationMetaRPCRequest{
		Op:          channelMigrationMetaOpRuntimeProbe,
		ChannelID:   channelID,
		ChannelType: int64(channelType),
	})
	if err != nil {
		return ch.RuntimeProbeChannel{}, err
	}
	if resp.RuntimeProbe == nil {
		return ch.RuntimeProbeChannel{}, ch.ErrChannelNotFound
	}
	return *resp.RuntimeProbe, nil
}

// DrainChannel reads one local or remote Channel drain proof.
func (n *Node) DrainChannel(ctx context.Context, nodeID uint64, req ch.DrainChannelRequest) (ch.DrainChannelResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.DrainChannelResult{}, err
	}
	if n == nil || nodeID == 0 {
		return ch.DrainChannelResult{}, ErrNotStarted
	}
	if nodeID == n.cfg.NodeID {
		return n.drainLocalChannelRuntime(ctx, req)
	}
	resp, err := n.callChannelMigrationMetaRPC(ctx, nodeID, channelMigrationMetaRPCRequest{
		Op:           channelMigrationMetaOpRuntimeDrain,
		DrainRequest: &req,
	})
	if err != nil {
		return ch.DrainChannelResult{}, err
	}
	if resp.DrainResult == nil {
		return ch.DrainChannelResult{}, ch.ErrChannelNotFound
	}
	return *resp.DrainResult, nil
}

// ApplyChannelMeta applies authoritative runtime metadata to a local or remote Channel runtime.
func (n *Node) ApplyChannelMeta(ctx context.Context, nodeID uint64, meta metadb.ChannelRuntimeMeta) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil || nodeID == 0 {
		return ErrNotStarted
	}
	if nodeID == n.cfg.NodeID {
		return n.applyChannelMigrationLocalRuntimeMeta(ctx, meta)
	}
	_, err := n.callChannelMigrationMetaRPC(ctx, nodeID, channelMigrationMetaRPCRequest{
		Op:          channelMigrationMetaOpRuntimeApply,
		RuntimeMeta: &meta,
	})
	return err
}

func (n *Node) probeLocalChannelRuntime(ctx context.Context, channelID string, channelType uint8) (ch.RuntimeProbeChannel, error) {
	id := ch.ChannelID{ID: channelID, Type: channelType}
	result, err := n.ChannelRuntimeProbe(ctx, ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{id}})
	if err != nil {
		return ch.RuntimeProbeChannel{}, err
	}
	for _, probe := range result.Channels {
		if probe.ChannelID == id {
			return probe, nil
		}
	}
	return ch.RuntimeProbeChannel{}, ch.ErrChannelNotFound
}

func (n *Node) applyChannelMigrationLocalRuntimeMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := n.ensureForeground(); err != nil {
		return err
	}
	if n.channels == nil {
		return ErrNotStarted
	}
	service, ok := n.channels.(interface {
		ApplyMeta(ch.Meta) error
	})
	if !ok {
		return ErrNotStarted
	}
	return service.ApplyMeta(channelwrapper.ProjectRuntimeMeta(meta))
}

func (n *Node) drainLocalChannelRuntime(ctx context.Context, req ch.DrainChannelRequest) (ch.DrainChannelResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.DrainChannelResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.DrainChannelResult{}, err
	}
	if n.channels == nil {
		return ch.DrainChannelResult{}, ErrNotStarted
	}
	return n.channels.DrainChannel(ctx, req)
}
