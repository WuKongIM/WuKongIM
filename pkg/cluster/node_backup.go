package cluster

import (
	"context"
	"fmt"
	"io"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const maxBackupMessageChannelsPerRequest = 4096

// BackupChannelFence identifies one Channel source and metadata generation.
type BackupChannelFence struct {
	ChannelID           string
	ChannelType         uint8
	LeaderNodeID        uint64
	ChannelEpoch        uint64
	LeaderEpoch         uint64
	MinISR              int64
	RetentionThroughSeq uint64
}

// BackupMessageSnapshot owns a pinned message stream and its exact cuts.
type BackupMessageSnapshot struct {
	Reader         io.ReadCloser
	Boundaries     []BackupChannelBoundary
	MessageRecords uint64
	MaxMessageID   uint64
}

// BackupChannelBoundary is the exact committed cut encoded for one Channel.
type BackupChannelBoundary struct {
	ChannelID      string
	ChannelType    uint8
	Epoch          uint64
	LogStartOffset uint64
	HW             uint64
}

type scheduledBackupController interface {
	LocalControllerState(context.Context) (controller.ClusterState, error)
	ReplaceScheduledBackupState(context.Context, uint64, controller.ScheduledBackupState) error
}

// BackupControllerLeaderID returns the best-known Controller leader for
// coordinator election.
func (n *Node) BackupControllerLeaderID() uint64 {
	if n == nil || n.control == nil {
		return 0
	}
	return n.control.LeaderID()
}

// BackupControllerFence returns the exact locally observed Controller leader
// and term without applying the foreground-maintenance admission check.
func (n *Node) BackupControllerFence(
	ctx context.Context,
) (uint64, uint64, error) {
	if err := ctxErr(ctx); err != nil {
		return 0, 0, err
	}
	if n == nil || !n.started.Load() || n.stopping.Load() {
		return 0, 0, ErrNotStarted
	}
	operator, ok := n.control.(controllerRaftOperator)
	if !ok || operator == nil {
		return 0, 0, ErrNotStarted
	}
	status, err := operator.ControllerRaftStatus(ctx)
	if err != nil {
		return 0, 0, err
	}
	if status.LeaderID == 0 || status.Term == 0 {
		return 0, 0, ErrNotLeader
	}
	return status.LeaderID, status.Term, nil
}

// LocalState returns the exact locally visible Controller state.
func (n *Node) LocalState(ctx context.Context) (controller.ClusterState, error) {
	if n == nil || n.control == nil {
		return controller.ClusterState{}, ErrNotStarted
	}
	runtime, ok := n.control.(scheduledBackupController)
	if !ok {
		return controller.ClusterState{}, fmt.Errorf("cluster: backup coordination is unsupported")
	}
	return runtime.LocalControllerState(ctx)
}

// ReplaceScheduledBackupState commits one complete revision-fenced backup
// subsystem state.
func (n *Node) ReplaceScheduledBackupState(
	ctx context.Context,
	expectedRevision uint64,
	replacement controller.ScheduledBackupState,
) error {
	if n == nil || n.control == nil {
		return ErrNotStarted
	}
	runtime, ok := n.control.(scheduledBackupController)
	if !ok {
		return fmt.Errorf("cluster: backup coordination is unsupported")
	}
	return runtime.ReplaceScheduledBackupState(ctx, expectedRevision, replacement)
}

// CaptureBackupHashSlotSnapshot pins one logical metadata partition at the
// local Slot leader's applied boundary.
func (n *Node) CaptureBackupHashSlotSnapshot(
	ctx context.Context,
	hashSlot uint16,
	expectedLeaderTerm uint64,
) (multiraft.CapturedHashSlotSnapshot, error) {
	if n == nil || n.defaultSlotRuntime == nil {
		return multiraft.CapturedHashSlotSnapshot{}, ErrNotStarted
	}
	route, err := n.RouteHashSlot(hashSlot)
	if err != nil {
		return multiraft.CapturedHashSlotSnapshot{}, err
	}
	if route.Leader != n.NodeID() {
		return multiraft.CapturedHashSlotSnapshot{}, ErrNotLeader
	}
	return n.defaultSlotRuntime.CaptureHashSlotSnapshot(
		ctx, multiraft.SlotID(route.SlotID), hashSlot, expectedLeaderTerm,
	)
}

// ValidateBackupHashSlotAuthority rechecks routing and the local Raft worker
// before an exported Slot manifest may be committed.
func (n *Node) ValidateBackupHashSlotAuthority(
	ctx context.Context,
	hashSlot uint16,
	slotID uint32,
	expectedLeaderTerm uint64,
	expectedConfigurationVersion uint64,
) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil || n.defaultSlotRuntime == nil {
		return ErrNotStarted
	}
	route, err := n.RouteHashSlot(hashSlot)
	if err != nil {
		return err
	}
	if route.SlotID != slotID || route.Leader != n.NodeID() ||
		route.LeaderTerm != expectedLeaderTerm ||
		route.ConfigEpoch != expectedConfigurationVersion {
		return ErrNotLeader
	}
	status, err := n.defaultSlotRuntime.Status(multiraft.SlotID(slotID))
	if err != nil {
		return err
	}
	if status.NodeID != multiraft.NodeID(n.NodeID()) ||
		status.LeaderID != multiraft.NodeID(n.NodeID()) ||
		status.Term != expectedLeaderTerm ||
		status.Role != multiraft.RoleLeader {
		return ErrNotLeader
	}
	return nil
}

// ListBackupChannelRuntimeMetaPage reads one exact Hash Slot metadata page from
// its local Slot leader. Callers fence a complete scan with equal applied
// indexes before and after the scan.
func (n *Node) ListBackupChannelRuntimeMetaPage(
	ctx context.Context,
	hashSlot uint16,
	after metadb.ChannelRuntimeMetaCursor,
	limit int,
) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	if n == nil || n.defaultSlotMetaDB == nil {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, ErrNotStarted
	}
	if limit <= 0 {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, metadb.ErrInvalidArgument
	}
	route, err := n.RouteHashSlot(hashSlot)
	if err != nil {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, err
	}
	if route.Leader != n.NodeID() {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, ErrNotLeader
	}
	return n.defaultSlotMetaDB.ForHashSlot(hashSlot).
		ListChannelRuntimeMetaPage(ctx, after, limit)
}

type backupMessageSnapshotFactory interface {
	OpenBackupSnapshotWithStats(
		context.Context,
		channelstore.BackupSnapshotRequest,
	) (io.ReadCloser, channelstore.BackupSnapshotStats, error)
}

// OpenBackupMessageSnapshot resolves committed local Channel cuts and opens one
// pinned portable message snapshot. It never activates a Channel runtime.
func (n *Node) OpenBackupMessageSnapshot(
	ctx context.Context,
	hashSlot uint16,
	fences []BackupChannelFence,
) (BackupMessageSnapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return BackupMessageSnapshot{}, err
	}
	if n == nil || n.channels == nil || len(fences) == 0 ||
		len(fences) > maxBackupMessageChannelsPerRequest {
		return BackupMessageSnapshot{}, channelruntime.ErrInvalidConfig
	}
	factory, ok := n.localChannelStoreFactory().(backupMessageSnapshotFactory)
	if !ok {
		return BackupMessageSnapshot{}, channelruntime.ErrInvalidConfig
	}
	ids := make([]channelruntime.ChannelID, len(fences))
	for index, fence := range fences {
		if fence.ChannelID == "" || fence.ChannelEpoch == 0 ||
			fence.LeaderEpoch == 0 || fence.LeaderNodeID != n.NodeID() ||
			fence.MinISR <= 0 {
			return BackupMessageSnapshot{}, channelruntime.ErrStaleMeta
		}
		route, err := n.RouteKey(fence.ChannelID)
		if err != nil {
			return BackupMessageSnapshot{}, err
		}
		if route.HashSlot != hashSlot {
			return BackupMessageSnapshot{}, channelruntime.ErrStaleMeta
		}
		ids[index] = channelruntime.ChannelID{
			ID: fence.ChannelID, Type: fence.ChannelType,
		}
	}
	probe, err := n.channels.RuntimeProbe(
		ctx, channelruntime.RuntimeSelector{ChannelIDs: ids},
	)
	if err != nil {
		return BackupMessageSnapshot{}, err
	}
	loaded := make(
		map[channelruntime.ChannelID]channelruntime.RuntimeProbeChannel,
		len(probe.Channels),
	)
	for _, item := range probe.Channels {
		loaded[item.ChannelID] = item
	}
	cuts := make([]channelstore.BackupChannelCut, len(fences))
	boundaries := make([]BackupChannelBoundary, len(fences))
	for index, fence := range fences {
		id := ids[index]
		store, err := n.localChannelStoreFactory().
			ChannelStore(channelruntime.ChannelKeyForID(id), id)
		if err != nil {
			return BackupMessageSnapshot{}, err
		}
		state, loadErr := store.Load(ctx)
		retention, retentionErr := store.LoadRetentionState(ctx)
		closeErr := store.Close()
		if loadErr != nil {
			return BackupMessageSnapshot{}, loadErr
		}
		if retentionErr != nil {
			return BackupMessageSnapshot{}, retentionErr
		}
		if closeErr != nil {
			return BackupMessageSnapshot{}, closeErr
		}
		hw := state.HW
		if item, present := loaded[id]; present {
			if item.Role != channelruntime.RoleLeader ||
				item.ChannelEpoch != fence.ChannelEpoch ||
				item.LeaderEpoch != fence.LeaderEpoch {
				return BackupMessageSnapshot{}, channelruntime.ErrStaleMeta
			}
			hw = item.HW
		} else if fence.MinISR <= 1 {
			hw = state.LEO
		}
		logStart := retention.LocalRetentionThroughSeq
		if logStart > hw {
			logStart = hw
		}
		cuts[index] = channelstore.BackupChannelCut{
			Key:            channelruntime.ChannelKeyForID(id),
			ID:             id,
			Epoch:          fence.ChannelEpoch,
			LogStartOffset: logStart,
			HW:             hw,
		}
		boundaries[index] = BackupChannelBoundary{
			ChannelID:      id.ID,
			ChannelType:    id.Type,
			Epoch:          fence.ChannelEpoch,
			LogStartOffset: logStart,
			HW:             hw,
		}
	}
	reader, stats, err := factory.OpenBackupSnapshotWithStats(
		ctx,
		channelstore.BackupSnapshotRequest{HashSlot: hashSlot, Channels: cuts},
	)
	if err != nil {
		return BackupMessageSnapshot{}, err
	}
	if stats.HashSlot != hashSlot || stats.ChannelCount != uint64(len(cuts)) {
		_ = reader.Close()
		return BackupMessageSnapshot{}, channelruntime.ErrStaleMeta
	}
	return BackupMessageSnapshot{
		Reader:         reader,
		Boundaries:     boundaries,
		MessageRecords: stats.MessageCount,
		MaxMessageID:   stats.MaxMessageID,
	}, nil
}
