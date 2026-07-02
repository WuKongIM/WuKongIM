package clusterv2

import (
	"context"
	"sort"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelwrapper "github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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

// ProbeChannel reads one local or remote ChannelV2 runtime proof.
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

// DrainChannel reads one local or remote ChannelV2 drain proof.
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

// ApplyChannelMeta applies authoritative runtime metadata to a local or remote ChannelV2 runtime.
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
