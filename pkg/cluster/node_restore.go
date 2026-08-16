package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ResetLocalRestoreCaches discards node-local projections that are not owned
// by the reloaded Slot and Channel runtimes.
func (n *Node) ResetLocalRestoreCaches() {
	if n == nil {
		return
	}
	n.messageEventStreamCache.resetAfterRestore()
}

// PauseLocalRestoreRuntime drains Channel background work and fences mutable
// node-local projections before restore can replace durable business data.
func (n *Node) PauseLocalRestoreRuntime() {
	if n == nil {
		return
	}
	n.stopChannelTickLoop()
	n.stopChannelRetentionGCLoop()
	n.stopChannelMigrationLoop()
	n.messageEventStreamCache.pauseForRestore()
}

// ResumeLocalRestoreRuntime clears maintenance-local observations and restarts
// Channel background work against the activated restored business data.
func (n *Node) ResumeLocalRestoreRuntime() {
	if n == nil {
		return
	}
	n.messageEventStreamCache.resumeAfterRestore()
	n.startChannelTickLoop()
	n.startChannelRetentionGCLoop()
	n.startChannelMigrationLoop()
}

// RestoreMessageStream is one seekable portable message snapshot.
type RestoreMessageStream struct {
	Reader io.ReadSeeker
	Size   int64
}

// RestoreMaintenanceReady reports whether Controller state has fenced this
// node's foreground traffic before local restore storage is touched.
func (n *Node) RestoreMaintenanceReady() bool {
	return n != nil && n.started.Load() && !n.stopping.Load() &&
		n.maintenance.Load() && n.defaultSlotMetaDB != nil &&
		n.defaultChannelStore != nil
}

// ListRestoreChannelSubscribersPage reads only node-local restored metadata
// under the active maintenance fence. It exists for rebuilding privilege
// caches and must never be used as an ordinary foreground read path.
func (n *Node) ListRestoreChannelSubscribersPage(
	ctx context.Context,
	channelID string,
	channelType int64,
	afterUID string,
	limit int,
) ([]string, string, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, "", false, err
	}
	if n == nil || !n.RestoreMaintenanceReady() || n.router == nil ||
		n.defaultSlotMetaDB == nil {
		return nil, "", false, ErrMaintenance
	}
	route, err := n.router.RouteKey(channelID)
	if err != nil {
		return nil, "", false, mapRouteError(err)
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).
		ListSubscribersPage(ctx, channelID, channelType, afterUID, limit)
}

// OpenLocalRestoreMetadataSnapshot captures the current local business
// metadata needed to roll back one Hash Slot.
func (n *Node) OpenLocalRestoreMetadataSnapshot(
	ctx context.Context,
	hashSlot uint16,
) (io.ReadCloser, error) {
	if err := n.validateRestoreStorage(hashSlot); err != nil {
		return nil, err
	}
	return n.defaultSlotMetaDB.OpenBackupHashSlotSnapshot(
		ctx, []uint16{hashSlot},
	)
}

// OpenLocalRestoreMessageSnapshot captures every locally stored Channel in
// one Hash Slot. It does not require Channel leadership while maintenance is
// active.
func (n *Node) OpenLocalRestoreMessageSnapshot(
	ctx context.Context,
	hashSlot uint16,
) (BackupMessageSnapshot, error) {
	if err := n.validateRestoreStorage(hashSlot); err != nil {
		return BackupMessageSnapshot{}, err
	}
	var cursor channelruntime.ChannelKey
	cuts := make([]channelstore.BackupChannelCut, 0)
	boundaries := make([]BackupChannelBoundary, 0)
	for {
		entries, next, more, err := n.defaultChannelStore.ListChannelsPage(
			ctx, cursor, maxBackupMessageChannelsPerRequest,
		)
		if err != nil {
			return BackupMessageSnapshot{}, err
		}
		for _, entry := range entries {
			route, err := n.router.RouteKey(entry.ID.ID)
			if err != nil {
				return BackupMessageSnapshot{}, err
			}
			if route.HashSlot != hashSlot {
				continue
			}
			metadata, err := n.defaultSlotMetaDB.ForHashSlot(hashSlot).
				GetChannelRuntimeMeta(ctx, entry.ID.ID, int64(entry.ID.Type))
			if err != nil {
				return BackupMessageSnapshot{}, err
			}
			store, err := n.defaultChannelStore.ChannelStore(entry.Key, entry.ID)
			if err != nil {
				return BackupMessageSnapshot{}, err
			}
			state, stateErr := store.Load(ctx)
			retention, retentionErr := store.LoadRetentionState(ctx)
			closeErr := store.Close()
			if stateErr != nil || retentionErr != nil || closeErr != nil {
				return BackupMessageSnapshot{}, errors.Join(
					stateErr, retentionErr, closeErr,
				)
			}
			hw := min(state.HW, state.LEO)
			logStart := min(retention.LocalRetentionThroughSeq, hw)
			cuts = append(cuts, channelstore.BackupChannelCut{
				Key: entry.Key, ID: entry.ID, Epoch: metadata.ChannelEpoch,
				LogStartOffset: logStart, HW: hw,
			})
			boundaries = append(boundaries, BackupChannelBoundary{
				ChannelID: entry.ID.ID, ChannelType: entry.ID.Type,
				Epoch: metadata.ChannelEpoch, LogStartOffset: logStart, HW: hw,
			})
		}
		if !more {
			break
		}
		if next == "" || next == cursor {
			return BackupMessageSnapshot{},
				fmt.Errorf("cluster: restore message catalog cursor did not advance")
		}
		cursor = next
	}
	reader, stats, err := n.defaultChannelStore.OpenBackupSnapshotWithStats(
		ctx,
		channelstore.BackupSnapshotRequest{
			HashSlot: hashSlot,
			Channels: cuts,
		},
	)
	if err != nil {
		return BackupMessageSnapshot{}, err
	}
	return BackupMessageSnapshot{
		Reader: reader, Boundaries: boundaries,
		MessageRecords: stats.MessageCount, MaxMessageID: stats.MaxMessageID,
	}, nil
}

// VerifyLocalRestorePartitionStreams validates staged target or rollback files
// without mutating live storage.
func (n *Node) VerifyLocalRestorePartitionStreams(
	ctx context.Context,
	hashSlot uint16,
	metadata io.ReadSeeker,
	metadataSize int64,
	messages []RestoreMessageStream,
) (uint64, error) {
	if err := n.validateRestoreStorage(hashSlot); err != nil {
		return 0, err
	}
	stats, err := metadb.VerifyBackupHashSlotSnapshotReader(
		ctx, []uint16{hashSlot}, metadata, metadataSize,
	)
	if err != nil {
		return 0, err
	}
	logicalBytes := uint64(metadataSize)
	seen := make(map[channelruntime.ChannelID]struct{})
	for _, stream := range messages {
		if stream.Reader == nil || stream.Size <= 0 {
			return 0, ErrInvalidConfig
		}
		messageStats, err := messagedb.ReplayBackupSnapshotReader(
			ctx, stream.Reader, stream.Size,
			func(boundary messagedb.BackupSnapshotBoundary) error {
				id := channelruntime.ChannelID{
					ID: boundary.ChannelID, Type: boundary.ChannelType,
				}
				if id.ID == "" {
					return ErrInvalidConfig
				}
				if _, exists := seen[id]; exists {
					return fmt.Errorf("cluster: duplicate restored Channel")
				}
				seen[id] = struct{}{}
				route, err := n.router.RouteKey(id.ID)
				if err != nil {
					return err
				}
				if route.HashSlot != hashSlot {
					return ErrInvalidConfig
				}
				return nil
			},
			func(messagedb.BackupSnapshotRecord) error { return nil },
		)
		if err != nil {
			return 0, err
		}
		if messageStats.HashSlot != hashSlot {
			return 0, ErrInvalidConfig
		}
		logicalBytes += uint64(stream.Size)
	}
	if stats.EntryCount == 0 && len(seen) == 0 && logicalBytes == 0 {
		return 0, ErrInvalidConfig
	}
	return logicalBytes, nil
}

// InstallLocalRestorePartition replaces one node-local Hash Slot only while
// foreground traffic is fenced. Client authentication tokens are restored at
// the archive point in time; Manager sessions are invalidated separately.
func (n *Node) InstallLocalRestorePartition(
	ctx context.Context,
	hashSlot uint16,
	metadata io.ReadSeeker,
	metadataSize int64,
	messages []RestoreMessageStream,
) error {
	if _, err := n.VerifyLocalRestorePartitionStreams(
		ctx, hashSlot, metadata, metadataSize, messages,
	); err != nil {
		return err
	}
	boundaries, err := restoreMessageBoundaries(ctx, messages)
	if err != nil {
		return err
	}
	if err := n.DiscardLocalRestorePartition(ctx, hashSlot); err != nil {
		return err
	}
	if _, err := metadata.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := n.defaultSlotMetaDB.
		ImportHashSlotSnapshotReaderForRestoreWithStats(
			ctx, []uint16{hashSlot}, metadata, metadataSize, false,
		); err != nil {
		return err
	}
	for _, stream := range messages {
		if _, err := stream.Reader.Seek(0, io.SeekStart); err != nil {
			return err
		}
		stats, err := n.defaultChannelStore.ImportBackupSnapshotReader(
			ctx, stream.Reader, stream.Size,
		)
		if err != nil {
			return err
		}
		if stats.HashSlot != hashSlot {
			return ErrInvalidConfig
		}
	}
	if err := n.installRestoreChannelRuntimeMeta(
		ctx, hashSlot, boundaries,
	); err != nil {
		return err
	}
	return nil
}

// ActivateLocalRestore rebuilds in-memory Slot and Channel runtimes from the
// newly durable generation while Controller maintenance still fences traffic.
func (n *Node) ActivateLocalRestore(ctx context.Context) error {
	if n == nil || !n.RestoreMaintenanceReady() ||
		n.defaultSlotRuntime == nil {
		return ErrMaintenance
	}
	// The app-level quiescence callback normally stops these loops before any
	// partition switch. Repeat the idempotent drain here so a direct node-local
	// activation can never close n.channels beneath the tick/GC/migration work.
	n.stopChannelTickLoop()
	n.stopChannelRetentionGCLoop()
	n.stopChannelMigrationLoop()
	slotIDs := n.defaultSlotRuntime.Slots()
	for _, slotID := range slotIDs {
		if err := n.defaultSlotRuntime.InstallExternalStateSnapshot(
			ctx, slotID,
		); err != nil {
			return err
		}
	}
	for _, slotID := range slotIDs {
		if err := n.defaultSlotRuntime.ReloadSlot(ctx, slotID); err != nil {
			return err
		}
	}
	return n.rebuildDefaultChannelRuntimeForRestore()
}

func (n *Node) rebuildDefaultChannelRuntimeForRestore() error {
	n.controlApplyMu.Lock()
	defer n.controlApplyMu.Unlock()

	var closeErr error
	if n.channels != nil {
		if n.channelRPCGateway != nil {
			n.channelRPCGateway.Clear()
		}
		if n.channelQuorumGateway != nil {
			n.channelQuorumGateway.Clear()
		}
		closeErr = errors.Join(closeErr, n.channels.Close())
		n.channels = nil
	}
	if n.defaultChannelReplication != nil {
		closeErr = errors.Join(closeErr, n.defaultChannelReplication.Close(context.Background()))
		n.defaultChannelReplication = nil
	}
	if n.defaultChannelStore != nil {
		closeErr = errors.Join(closeErr, n.defaultChannelStore.Close())
		n.defaultChannelStore = nil
	}
	n.defaultChannels = false
	created, err := n.ensureDefaultRuntime()
	if err == nil && (!created || n.channels == nil || n.defaultChannelStore == nil) {
		err = ErrNotStarted
	}
	if err != nil {
		// Keep a bare message store available so rollback can reinstall durable
		// partitions even when rebuilding the Channel runtime failed.
		if n.defaultChannelStore == nil {
			n.defaultChannelStore = n.newDefaultChannelStore()
		}
		return errors.Join(closeErr, err)
	}
	n.markChannelsReady(true)
	return closeErr
}

// CheckLocalRestoreHealth proves that every reloaded local Raft group and the
// Channel runtime are ready while Controller maintenance still hides them.
func (n *Node) CheckLocalRestoreHealth(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		lastErr = n.checkLocalRestoreHealthOnce()
		if lastErr == nil {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return errors.Join(lastErr, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (n *Node) checkLocalRestoreHealthOnce() error {
	if n == nil || !n.RestoreMaintenanceReady() ||
		n.channels == nil || !n.defaultChannels ||
		n.defaultSlotRuntime == nil {
		return ErrNotStarted
	}
	slotIDs := n.defaultSlotRuntime.Slots()
	if len(slotIDs) == 0 {
		return ErrNotStarted
	}
	for _, slotID := range slotIDs {
		status, err := n.defaultSlotRuntime.Status(slotID)
		if err != nil {
			return err
		}
		if status.AppliedIndex == 0 || status.LeaderID == 0 ||
			len(status.CurrentVoters) == 0 {
			return fmt.Errorf(
				"%w: restored Slot %d is not ready",
				ErrRouteNotReady, slotID,
			)
		}
	}
	return nil
}

// DiscardLocalRestorePartition removes all local business data for one Hash
// Slot while maintenance remains active.
func (n *Node) DiscardLocalRestorePartition(
	ctx context.Context,
	hashSlot uint16,
) error {
	if err := n.validateRestoreStorage(hashSlot); err != nil {
		return err
	}
	var cursor channelruntime.ChannelKey
	for {
		entries, next, more, err := n.defaultChannelStore.ListChannelsPage(
			ctx, cursor, maxBackupMessageChannelsPerRequest,
		)
		if err != nil {
			return err
		}
		channelsToDelete := make(
			[]channelstore.RestoreChannelBoundary, 0, len(entries),
		)
		for _, entry := range entries {
			route, err := n.router.RouteKey(entry.ID.ID)
			if err != nil {
				return err
			}
			if route.HashSlot == hashSlot {
				channelsToDelete = append(
					channelsToDelete,
					channelstore.RestoreChannelBoundary{ID: entry.ID},
				)
			}
		}
		if err := n.defaultChannelStore.DiscardRestoreChannels(
			ctx, channelsToDelete,
		); err != nil {
			return err
		}
		if !more {
			break
		}
		if next == "" || next == cursor {
			return fmt.Errorf("cluster: restore discard cursor did not advance")
		}
		cursor = next
	}
	return n.defaultSlotMetaDB.DeleteHashSlotData(ctx, hashSlot)
}

func (n *Node) validateRestoreStorage(hashSlot uint16) error {
	if n == nil || !n.RestoreMaintenanceReady() || n.router == nil ||
		int(hashSlot) >= int(n.cfg.Slots.HashSlotCount) {
		return ErrMaintenance
	}
	return nil
}

func restoreMessageBoundaries(
	ctx context.Context,
	streams []RestoreMessageStream,
) ([]BackupChannelBoundary, error) {
	boundaries := make([]BackupChannelBoundary, 0)
	for _, stream := range streams {
		if _, err := stream.Reader.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		_, err := messagedb.ReplayBackupSnapshotReader(
			ctx, stream.Reader, stream.Size,
			func(boundary messagedb.BackupSnapshotBoundary) error {
				boundaries = append(boundaries, BackupChannelBoundary{
					ChannelID: boundary.ChannelID, ChannelType: boundary.ChannelType,
					Epoch: boundary.Epoch, LogStartOffset: boundary.LogStartOffset,
					HW: boundary.HW,
				})
				return nil
			},
			func(messagedb.BackupSnapshotRecord) error { return nil },
		)
		if err != nil {
			return nil, err
		}
	}
	return boundaries, nil
}

func (n *Node) installRestoreChannelRuntimeMeta(
	ctx context.Context,
	hashSlot uint16,
	boundaries []BackupChannelBoundary,
) error {
	if len(boundaries) == 0 {
		return nil
	}
	placement, err := n.restoreChannelPlacement(hashSlot)
	if err != nil {
		return err
	}
	batch := n.defaultSlotMetaDB.NewWriteBatch()
	defer batch.Close()
	seen := make(map[channelruntime.ChannelID]struct{}, len(boundaries))
	for _, boundary := range boundaries {
		id := channelruntime.ChannelID{
			ID: boundary.ChannelID, Type: boundary.ChannelType,
		}
		if id.ID == "" || boundary.Epoch == 0 ||
			boundary.LogStartOffset > boundary.HW {
			return ErrInvalidConfig
		}
		if _, exists := seen[id]; exists {
			return ErrInvalidConfig
		}
		seen[id] = struct{}{}
		target, err := placement.ResolveChannelPlacement(ctx, id)
		if err != nil {
			return err
		}
		replicas := make([]uint64, len(target.Replicas))
		for index, replica := range target.Replicas {
			replicas[index] = uint64(replica)
		}
		metadata := metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{
			ChannelID: id.ID, ChannelType: int64(id.Type),
			ChannelEpoch: boundary.Epoch, LeaderEpoch: 1,
			Leader: uint64(target.Leader), Replicas: replicas,
			ISR: append([]uint64(nil), replicas...), MinISR: int64(target.MinISR),
			Status:              uint8(channelruntime.StatusActive),
			RetentionThroughSeq: boundary.LogStartOffset,
		})
		if err := batch.UpsertChannelRuntimeMeta(hashSlot, metadata); err != nil {
			return err
		}
	}
	return batch.Commit()
}

type restoreSlotDataNodes struct {
	revision uint64
	nodes    []uint64
}

func (n restoreSlotDataNodes) PlacementDataNodes(_ context.Context, expectedRevision uint64) ([]uint64, error) {
	if n.revision != expectedRevision {
		return nil, channelruntime.ErrStaleMeta
	}
	return append([]uint64(nil), n.nodes...), nil
}

func (n *Node) restoreChannelPlacement(
	hashSlot uint16,
) (*channels.SlotPlacementResolver, error) {
	if n == nil || n.router == nil || n.cfg.Channel.ReplicaCount == 0 {
		return nil, ErrInvalidConfig
	}
	route, err := n.router.RouteHashSlot(hashSlot)
	if err != nil {
		return nil, err
	}
	if route.HashSlot != hashSlot ||
		len(route.Peers) < int(n.cfg.Channel.ReplicaCount) {
		return nil, ErrInvalidConfig
	}
	peers := append([]uint64(nil), route.Peers...)
	slices.Sort(peers)
	return channels.NewSlotPlacementResolver(
		n.router, restoreSlotDataNodes{revision: route.Revision, nodes: peers},
		int(n.cfg.Channel.ReplicaCount),
	), nil
}
