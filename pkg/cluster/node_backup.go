package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const maxBackupMessageChannelsPerRequest = 4096

// RestoreTargetLocalState is one node's semantic storage-emptiness evidence.
type RestoreTargetLocalState struct {
	// NodeID identifies the inspected storage owner.
	NodeID uint64
	// Empty is true only when both semantic metadata and the message catalog are empty.
	Empty bool
	// MetadataEmpty reports absence of recovery-authoritative metadata rows.
	MetadataEmpty bool
	// MessagesEmpty reports absence of all durable message channels.
	MessagesEmpty bool
}

// RestoreVerifyBoundary is one authenticated expected Channel cut.
type RestoreVerifyBoundary struct {
	// ChannelID is the durable business identity.
	ChannelID string
	// ChannelType is the durable business type.
	ChannelType uint8
	// Epoch is the restored Channel epoch.
	Epoch uint64
	// LogStartOffset is the restored retained prefix boundary.
	LogStartOffset uint64
	// HW is the restored committed high watermark.
	HW uint64
}

// BackupChannelFence identifies one Channel leader and metadata generation
// selected by a stable hash-slot view.
type BackupChannelFence struct {
	// ChannelID is the durable business channel identity.
	ChannelID string
	// ChannelType is the durable business channel type.
	ChannelType uint8
	// LeaderNodeID is the selected committed source replica.
	LeaderNodeID uint64
	// ChannelEpoch fences membership changes.
	ChannelEpoch uint64
	// LeaderEpoch fences leader changes.
	LeaderEpoch uint64
	// MinISR records the quorum policy represented by the metadata cut.
	MinISR int64
	// RetentionThroughSeq records the permanent retained-prefix boundary.
	RetentionThroughSeq uint64
	// FromExclusive is the prior published committed high watermark.
	FromExclusive uint64
}

// BackupMessageSnapshot owns a pinned message stream and its exact resolved cuts.
type BackupMessageSnapshot struct {
	// Reader streams the pinned committed message data and must be closed.
	Reader io.ReadCloser
	// Boundaries contains the exact resolved cut for every requested Channel.
	Boundaries []BackupChannelBoundary
	// MessageRecords is the exact message-row count encoded by Reader.
	MessageRecords uint64
	// MaxMessageID is the greatest durable message ID encoded by Reader.
	MaxMessageID uint64
}

// BackupChannelBoundary is the exact committed cut encoded for one Channel.
type BackupChannelBoundary struct {
	// ChannelID is the durable business channel identity.
	ChannelID string
	// ChannelType is the durable business channel type.
	ChannelType uint8
	// Epoch is the captured Channel generation.
	Epoch uint64
	// LogStartOffset is the captured retained-prefix boundary.
	LogStartOffset uint64
	// HW is the captured committed high watermark.
	HW uint64
}

type backupCoordinationController interface {
	LocalControllerState(context.Context) (controller.ClusterState, error)
	ReplaceBackupCoordinationState(context.Context, uint64, controller.BackupCoordinationState) error
}

type restoreCoordinationController interface {
	LocalControllerState(context.Context) (controller.ClusterState, error)
	ReplaceRestoreCoordinationState(context.Context, uint64, controller.RestoreCoordinationState) error
}

// BackupControllerLeaderID returns the best-known Controller leader for coordinator election.
func (n *Node) BackupControllerLeaderID() uint64 {
	if n == nil || n.control == nil {
		return 0
	}
	return n.control.LeaderID()
}

// LoadBackupCoordinationState returns the exact locally visible Controller state.
func (n *Node) LoadBackupCoordinationState(ctx context.Context) (controller.ClusterState, error) {
	if n == nil || n.control == nil {
		return controller.ClusterState{}, ErrNotStarted
	}
	controlRuntime, ok := n.control.(backupCoordinationController)
	if !ok {
		return controller.ClusterState{}, fmt.Errorf("cluster: backup coordination is unsupported")
	}
	return controlRuntime.LocalControllerState(ctx)
}

// ReplaceBackupCoordinationState commits one revision-fenced bounded coordination state.
func (n *Node) ReplaceBackupCoordinationState(ctx context.Context, expectedRevision uint64, replacement controller.BackupCoordinationState) error {
	if n == nil || n.control == nil {
		return ErrNotStarted
	}
	controlRuntime, ok := n.control.(backupCoordinationController)
	if !ok {
		return fmt.Errorf("cluster: backup coordination is unsupported")
	}
	return controlRuntime.ReplaceBackupCoordinationState(ctx, expectedRevision, replacement)
}

// LoadRestoreCoordinationState returns the exact locally visible Controller state.
func (n *Node) LoadRestoreCoordinationState(ctx context.Context) (controller.ClusterState, error) {
	if n == nil || n.control == nil {
		return controller.ClusterState{}, ErrNotStarted
	}
	runtime, ok := n.control.(restoreCoordinationController)
	if !ok {
		return controller.ClusterState{}, fmt.Errorf("cluster: restore coordination is unsupported")
	}
	return runtime.LocalControllerState(ctx)
}

// ReplaceRestoreCoordinationState commits one revision-fenced recovery state.
func (n *Node) ReplaceRestoreCoordinationState(ctx context.Context, expectedRevision uint64, replacement controller.RestoreCoordinationState) error {
	if n == nil || n.control == nil {
		return ErrNotStarted
	}
	runtime, ok := n.control.(restoreCoordinationController)
	if !ok {
		return fmt.Errorf("cluster: restore coordination is unsupported")
	}
	return runtime.ReplaceRestoreCoordinationState(ctx, expectedRevision, replacement)
}

// InspectLocalRestoreTarget proves whether this node has any durable business
// state. Restore mode may already have bootstrapped Raft and runtime metadata;
// those coordination-only rows do not make the target semantically non-empty.
func (n *Node) InspectLocalRestoreTarget(ctx context.Context) (RestoreTargetLocalState, error) {
	if err := ctxErr(ctx); err != nil {
		return RestoreTargetLocalState{}, err
	}
	if n == nil || n.defaultSlotMetaDB == nil || n.defaultChannelStore == nil || n.cfg.NodeID == 0 || n.cfg.Slots.HashSlotCount == 0 {
		return RestoreTargetLocalState{}, ErrNotStarted
	}
	hashSlots := make([]uint16, n.cfg.Slots.HashSlotCount)
	for hashSlot := range hashSlots {
		hashSlots[hashSlot] = uint16(hashSlot)
	}
	metadataPresent, err := n.defaultSlotMetaDB.HasBackupBusinessData(ctx, hashSlots)
	if err != nil {
		return RestoreTargetLocalState{}, err
	}
	channels, _, _, err := n.defaultChannelStore.ListChannelsPage(ctx, "", 1)
	if err != nil {
		return RestoreTargetLocalState{}, err
	}
	state := RestoreTargetLocalState{
		NodeID: n.cfg.NodeID, MetadataEmpty: !metadataPresent, MessagesEmpty: len(channels) == 0,
	}
	state.Empty = state.MetadataEmpty && state.MessagesEmpty
	return state, nil
}

// InstallRestoreHashSlotMetadata installs one validated semantic metadata
// stream only while the node is fenced in explicit restore mode.
func (n *Node) InstallRestoreHashSlotMetadata(ctx context.Context, hashSlot uint16, reader io.ReadSeeker, size int64, invalidateTokens bool) (uint64, error) {
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	if n == nil || !n.cfg.RestoreMode || n.defaultSlotMetaDB == nil || hashSlot >= n.cfg.Slots.HashSlotCount {
		return 0, ErrInvalidConfig
	}
	stats, err := n.defaultSlotMetaDB.ImportHashSlotSnapshotReaderForRestoreWithStats(ctx, []uint16{hashSlot}, reader, size, invalidateTokens)
	return stats.EntryCount, err
}

// InstallRestoreMessageStream installs one validated base or delta message
// stream only while the node is fenced in explicit restore mode.
func (n *Node) InstallRestoreMessageStream(ctx context.Context, reader io.ReadSeeker, size int64) (channelstore.BackupSnapshotStats, error) {
	if err := ctxErr(ctx); err != nil {
		return channelstore.BackupSnapshotStats{}, err
	}
	if n == nil || !n.cfg.RestoreMode || n.defaultChannelStore == nil {
		return channelstore.BackupSnapshotStats{}, ErrInvalidConfig
	}
	return n.defaultChannelStore.ImportBackupSnapshotReader(ctx, reader, size)
}

// InstallRestoreChannelRuntimeMeta reconstructs target-topology Channel
// routing metadata from authenticated durable message boundaries. Source
// topology is never copied into the successor cluster.
func (n *Node) InstallRestoreChannelRuntimeMeta(ctx context.Context, hashSlot uint16, boundaries []RestoreVerifyBoundary) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil || !n.cfg.RestoreMode || n.defaultSlotMetaDB == nil || n.router == nil || hashSlot >= n.cfg.Slots.HashSlotCount || len(boundaries) > maxBackupMessageChannelsPerRequest {
		return ErrInvalidConfig
	}
	if len(boundaries) == 0 {
		return nil
	}
	placement, err := n.restoreChannelPlacement(hashSlot)
	if err != nil {
		return err
	}
	batch := n.defaultSlotMetaDB.NewWriteBatch()
	defer func() { _ = batch.Close() }()
	seen := make(map[channelruntime.ChannelID]struct{}, len(boundaries))
	for _, boundary := range boundaries {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		meta, id, err := n.buildRestoreChannelRuntimeMeta(ctx, hashSlot, boundary, placement)
		if err != nil {
			return err
		}
		if _, exists := seen[id]; exists {
			return ErrInvalidConfig
		}
		seen[id] = struct{}{}
		if err := batch.UpsertChannelRuntimeMeta(hashSlot, meta); err != nil {
			return err
		}
	}
	return batch.Commit()
}

// RestoreHashSlotMetadataDigest returns the canonical semantic snapshot digest
// after restore-time transforms such as token invalidation have been applied.
func (n *Node) RestoreHashSlotMetadataDigest(ctx context.Context, hashSlot uint16) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	if n == nil || !n.cfg.RestoreMode || n.defaultSlotMetaDB == nil || hashSlot >= n.cfg.Slots.HashSlotCount {
		return "", ErrInvalidConfig
	}
	reader, err := n.defaultSlotMetaDB.OpenBackupHashSlotSnapshot(ctx, []uint16{hashSlot})
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, reader)
	closeErr := reader.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// VerifyLocalRestorePartition validates one bounded batch of authenticated
// Channel cuts against node-local durable state in explicit restore mode.
func (n *Node) VerifyLocalRestorePartition(ctx context.Context, hashSlot uint16, metadataSHA256 string, boundaries []RestoreVerifyBoundary) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil || !n.cfg.RestoreMode || n.defaultChannelStore == nil || n.defaultSlotMetaDB == nil || (len(boundaries) > 0 && n.router == nil) || hashSlot >= n.cfg.Slots.HashSlotCount || len(boundaries) > maxBackupMessageChannelsPerRequest {
		return ErrInvalidConfig
	}
	if metadataSHA256 != "" {
		digest, err := n.RestoreHashSlotMetadataDigest(ctx, hashSlot)
		if err != nil {
			return err
		}
		if digest != metadataSHA256 {
			return fmt.Errorf("cluster: restored metadata digest mismatch")
		}
	}
	cuts := make([]channelstore.BackupChannelCut, len(boundaries))
	var placement *channels.SlotPlacementResolver
	if len(boundaries) > 0 {
		var err error
		placement, err = n.restoreChannelPlacement(hashSlot)
		if err != nil {
			return err
		}
	}
	for index, boundary := range boundaries {
		expected, id, err := n.buildRestoreChannelRuntimeMeta(ctx, hashSlot, boundary, placement)
		if err != nil {
			return err
		}
		actual, err := n.defaultSlotMetaDB.ForHashSlot(hashSlot).GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		if err != nil {
			return fmt.Errorf("cluster: restored channel runtime metadata missing: %w", err)
		}
		actual = metadb.NormalizeChannelRuntimeMeta(actual)
		if !sameRestoreChannelRuntimeMeta(actual, expected) {
			return fmt.Errorf("cluster: restored channel runtime metadata mismatch")
		}
		cuts[index] = channelstore.BackupChannelCut{
			Key: channelruntime.ChannelKeyForID(id), ID: id, Epoch: boundary.Epoch,
			LogStartOffset: boundary.LogStartOffset, HW: boundary.HW,
		}
	}
	return n.defaultChannelStore.VerifyRestoreBoundaries(ctx, cuts)
}

func (n *Node) buildRestoreChannelRuntimeMeta(ctx context.Context, hashSlot uint16, boundary RestoreVerifyBoundary, placement *channels.SlotPlacementResolver) (metadb.ChannelRuntimeMeta, channelruntime.ChannelID, error) {
	id := channelruntime.ChannelID{ID: boundary.ChannelID, Type: boundary.ChannelType}
	if id.ID == "" || boundary.Epoch == 0 || boundary.LogStartOffset > boundary.HW || placement == nil {
		return metadb.ChannelRuntimeMeta{}, channelruntime.ChannelID{}, ErrInvalidConfig
	}
	route, err := n.router.RouteKey(boundary.ChannelID)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, channelruntime.ChannelID{}, err
	}
	if route.HashSlot != hashSlot {
		return metadb.ChannelRuntimeMeta{}, channelruntime.ChannelID{}, ErrInvalidConfig
	}
	target, err := placement.ResolveChannelPlacement(ctx, id)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, channelruntime.ChannelID{}, err
	}
	replicas := make([]uint64, len(target.Replicas))
	for index, replica := range target.Replicas {
		replicas[index] = uint64(replica)
	}
	meta := metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{
		ChannelID: id.ID, ChannelType: int64(id.Type), ChannelEpoch: boundary.Epoch, LeaderEpoch: 1,
		Leader: uint64(target.Leader), Replicas: replicas, ISR: append([]uint64(nil), replicas...), MinISR: int64(target.MinISR),
		Status: uint8(channelruntime.StatusActive), RetentionThroughSeq: boundary.LogStartOffset,
	})
	return meta, id, nil
}

type restoreSlotDataNodes []uint64

func (n restoreSlotDataNodes) DataNodes() []uint64 {
	return append([]uint64(nil), n...)
}

// restoreChannelPlacement keeps restored Channel replicas on the physical
// Slot replicas that received the partition. Normal runtime placement remains
// free to use the full active data-node set for newly created Channels.
func (n *Node) restoreChannelPlacement(hashSlot uint16) (*channels.SlotPlacementResolver, error) {
	if n == nil || n.router == nil || n.cfg.Channel.ReplicaCount == 0 {
		return nil, ErrInvalidConfig
	}
	route, err := n.router.RouteHashSlot(hashSlot)
	if err != nil {
		return nil, err
	}
	if route.HashSlot != hashSlot || len(route.Peers) < int(n.cfg.Channel.ReplicaCount) {
		return nil, ErrInvalidConfig
	}
	return channels.NewSlotPlacementResolver(n.router, restoreSlotDataNodes(route.Peers), int(n.cfg.Channel.ReplicaCount)), nil
}

func sameRestoreChannelRuntimeMeta(actual, expected metadb.ChannelRuntimeMeta) bool {
	return actual.ChannelID == expected.ChannelID && actual.ChannelType == expected.ChannelType &&
		actual.ChannelEpoch == expected.ChannelEpoch && actual.LeaderEpoch == expected.LeaderEpoch && actual.RouteGeneration == expected.RouteGeneration &&
		actual.Leader == expected.Leader && actual.MinISR == expected.MinISR && actual.Status == expected.Status && actual.Features == expected.Features &&
		actual.LeaseUntilMS == expected.LeaseUntilMS && actual.RetentionThroughSeq == expected.RetentionThroughSeq && actual.RetentionUpdatedAtMS == expected.RetentionUpdatedAtMS &&
		actual.WriteFenceToken == expected.WriteFenceToken && actual.WriteFenceVersion == expected.WriteFenceVersion && actual.WriteFenceReason == expected.WriteFenceReason &&
		actual.WriteFenceUntilMS == expected.WriteFenceUntilMS &&
		slices.Equal(actual.Replicas, expected.Replicas) && slices.Equal(actual.ISR, expected.ISR)
}

// CaptureBackupHashSlotSnapshot pins one logical metadata partition at the local Slot leader's applied boundary.
func (n *Node) CaptureBackupHashSlotSnapshot(ctx context.Context, hashSlot uint16) (multiraft.CapturedHashSlotSnapshot, error) {
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
	return n.defaultSlotRuntime.CaptureHashSlotSnapshot(ctx, multiraft.SlotID(route.SlotID), hashSlot)
}

// ListBackupChannelRuntimeMetaPage reads one exact hash-slot metadata page.
// Callers that need a stable view must fence the scan with equal applied
// indexes before and after the complete scan.
func (n *Node) ListBackupChannelRuntimeMetaPage(ctx context.Context, hashSlot uint16, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
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
	return n.defaultSlotMetaDB.ForHashSlot(hashSlot).ListChannelRuntimeMetaPage(ctx, after, limit)
}

type backupMessageSnapshotFactory interface {
	OpenBackupSnapshotWithStats(context.Context, channelstore.BackupSnapshotRequest) (io.ReadCloser, channelstore.BackupSnapshotStats, error)
}

// OpenBackupMessageSnapshot resolves committed local Channel cuts and opens one
// pinned portable message snapshot. It never activates a Channel runtime.
func (n *Node) OpenBackupMessageSnapshot(ctx context.Context, hashSlot uint16, fences []BackupChannelFence) (BackupMessageSnapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return BackupMessageSnapshot{}, err
	}
	if n == nil || n.channels == nil || len(fences) == 0 || len(fences) > maxBackupMessageChannelsPerRequest {
		return BackupMessageSnapshot{}, channelruntime.ErrInvalidConfig
	}
	factory, ok := n.localChannelStoreFactory().(backupMessageSnapshotFactory)
	if !ok {
		return BackupMessageSnapshot{}, channelruntime.ErrInvalidConfig
	}
	ids := make([]channelruntime.ChannelID, len(fences))
	for index, fence := range fences {
		if fence.ChannelID == "" || fence.ChannelEpoch == 0 || fence.LeaderEpoch == 0 || fence.LeaderNodeID != n.NodeID() || fence.MinISR <= 0 {
			return BackupMessageSnapshot{}, channelruntime.ErrStaleMeta
		}
		route, err := n.RouteKey(fence.ChannelID)
		if err != nil {
			return BackupMessageSnapshot{}, err
		}
		if route.HashSlot != hashSlot {
			return BackupMessageSnapshot{}, channelruntime.ErrStaleMeta
		}
		ids[index] = channelruntime.ChannelID{ID: fence.ChannelID, Type: fence.ChannelType}
	}
	probe, err := n.channels.RuntimeProbe(ctx, channelruntime.RuntimeSelector{ChannelIDs: ids})
	if err != nil {
		return BackupMessageSnapshot{}, err
	}
	loaded := make(map[channelruntime.ChannelID]channelruntime.RuntimeProbeChannel, len(probe.Channels))
	for _, item := range probe.Channels {
		loaded[item.ChannelID] = item
	}
	cuts := make([]channelstore.BackupChannelCut, len(fences))
	boundaries := make([]BackupChannelBoundary, len(fences))
	for index, fence := range fences {
		id := ids[index]
		store, err := n.localChannelStoreFactory().ChannelStore(channelruntime.ChannelKeyForID(id), id)
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
			if item.Role != channelruntime.RoleLeader || item.ChannelEpoch != fence.ChannelEpoch || item.LeaderEpoch != fence.LeaderEpoch {
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
		if fence.FromExclusive > hw {
			return BackupMessageSnapshot{}, channelruntime.ErrStaleMeta
		}
		cuts[index] = channelstore.BackupChannelCut{Key: channelruntime.ChannelKeyForID(id), ID: id, Epoch: fence.ChannelEpoch, LogStartOffset: logStart, HW: hw, FromExclusive: fence.FromExclusive}
		boundaries[index] = BackupChannelBoundary{ChannelID: id.ID, ChannelType: id.Type, Epoch: fence.ChannelEpoch, LogStartOffset: logStart, HW: hw}
	}
	reader, stats, err := factory.OpenBackupSnapshotWithStats(ctx, channelstore.BackupSnapshotRequest{HashSlot: hashSlot, Channels: cuts})
	if err != nil {
		return BackupMessageSnapshot{}, err
	}
	if stats.HashSlot != hashSlot || stats.ChannelCount != uint64(len(cuts)) {
		_ = reader.Close()
		return BackupMessageSnapshot{}, channelruntime.ErrStaleMeta
	}
	return BackupMessageSnapshot{Reader: reader, Boundaries: boundaries, MessageRecords: stats.MessageCount, MaxMessageID: stats.MaxMessageID}, nil
}
