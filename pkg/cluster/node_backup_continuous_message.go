package cluster

import (
	"context"
	"fmt"
	"math"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// MaxCaptureBackupRecordBytes is the hard portable record/page safety limit.
const MaxCaptureBackupRecordBytes int64 = 256 << 20

const (
	backupContinuousMessageActionObserve      = "observe"
	backupContinuousMessageActionObserveBatch = "observe_batch"
	backupContinuousMessageActionRead         = "read"
	backupContinuousMessageChunkBytes         = 32 << 20
	backupContinuousMessageBatchChannels      = 256
)

// BackupMessageChannelRequest fences one committed Channel log observation.
type BackupMessageChannelRequest struct {
	// HashSlot identifies the Channel's logical metadata partition.
	HashSlot uint16
	// ChannelID and ChannelType identify the committed message log.
	ChannelID   string
	ChannelType uint8
	// LeaderNodeID and epochs fence routing against concurrent placement.
	LeaderNodeID uint64
	ChannelEpoch uint64
	LeaderEpoch  uint64
	// MinISR selects the safe unloaded-single-replica fallback.
	MinISR int
	// RetentionSeq is the authoritative metadata retention boundary.
	RetentionSeq uint64
}

// BackupMessageChannelBoundary is one exact committed source cut.
type BackupMessageChannelBoundary struct {
	// HashSlot, ChannelID, and ChannelType identify the source log.
	HashSlot    uint16
	ChannelID   string
	ChannelType uint8
	// Epoch and LogStartOffset fence the retained log generation.
	Epoch          uint64
	LogStartOffset uint64
	// HW is the exact durable committed high watermark.
	HW uint64
	// ObservedAtUnixMillis is the UTC boundary observation time.
	ObservedAtUnixMillis int64
}

// BackupMessageLogPageRequest selects a bounded committed Channel range.
type BackupMessageLogPageRequest struct {
	// Channel is the fence-checked source identity.
	Channel BackupMessageChannelRequest
	// FromSeq and ThroughSeq select the pinned committed range.
	FromSeq    uint64
	ThroughSeq uint64
	// TargetBytes is the rolling page target; MaxBytes permits one oversized row.
	TargetBytes int64
	MaxBytes    int64
	// MaxRecords bounds rows returned by this call.
	MaxRecords int
}

// BackupMessageLogPage contains portable rows from one exact Channel cut.
type BackupMessageLogPage struct {
	// Records contains ordered portable committed message rows.
	Records [][]byte
	// Boundary is the exact retained committed cut used for the read.
	Boundary BackupMessageChannelBoundary
	// NextSeq is the first sequence not represented in Records.
	NextSeq uint64
	// Done reports that every row through the requested cut is represented.
	Done bool
}

// ObserveBackupMessageChannel routes to the authoritative Channel leader and
// returns a fence-checked durable committed cut.
func (n *Node) ObserveBackupMessageChannel(ctx context.Context, request BackupMessageChannelRequest) (BackupMessageChannelBoundary, error) {
	if err := validateBackupMessageChannelRequest(n, request); err != nil {
		return BackupMessageChannelBoundary{}, err
	}
	if request.LeaderNodeID != n.NodeID() {
		response, err := n.callBackupContinuousMessage(ctx, request.LeaderNodeID, backupContinuousMessageRPCRequest{
			Action: backupContinuousMessageActionObserve, Channel: request,
		})
		if err != nil {
			return BackupMessageChannelBoundary{}, err
		}
		if err := validateBackupMessageChannelBoundary(request, response.Boundary, true); err != nil {
			return BackupMessageChannelBoundary{}, err
		}
		return response.Boundary, nil
	}
	return n.observeBackupMessageChannelLocal(ctx, request)
}

// ObserveBackupMessageChannels batches fence-checked observations by Channel
// leader while preserving request order.
func (n *Node) ObserveBackupMessageChannels(ctx context.Context, requests []BackupMessageChannelRequest) ([]BackupMessageChannelBoundary, error) {
	if len(requests) == 0 || len(requests) > backupContinuousMessageBatchChannels {
		return nil, channelruntime.ErrInvalidConfig
	}
	type indexedRequest struct {
		index   int
		request BackupMessageChannelRequest
	}
	byLeader := make(map[uint64][]indexedRequest)
	for index, request := range requests {
		if err := validateBackupMessageChannelRequest(n, request); err != nil {
			return nil, err
		}
		byLeader[request.LeaderNodeID] = append(byLeader[request.LeaderNodeID], indexedRequest{
			index: index, request: request,
		})
	}
	out := make([]BackupMessageChannelBoundary, len(requests))
	for leaderNodeID, group := range byLeader {
		groupRequests := make([]BackupMessageChannelRequest, len(group))
		for index := range group {
			groupRequests[index] = group[index].request
		}
		var boundaries []BackupMessageChannelBoundary
		if leaderNodeID == n.NodeID() {
			var err error
			boundaries, err = n.observeBackupMessageChannelsLocal(ctx, groupRequests)
			if err != nil {
				return nil, err
			}
		} else {
			response, err := n.callBackupContinuousMessage(ctx, leaderNodeID, backupContinuousMessageRPCRequest{
				Action: backupContinuousMessageActionObserveBatch, Channels: groupRequests,
			})
			if err != nil {
				return nil, err
			}
			boundaries = response.Boundaries
		}
		if len(boundaries) != len(group) {
			return nil, channelruntime.ErrStaleMeta
		}
		for index, item := range group {
			if err := validateBackupMessageChannelBoundary(item.request, boundaries[index], true); err != nil {
				return nil, err
			}
			out[item.index] = boundaries[index]
		}
	}
	return out, nil
}

// ReadBackupMessageLogPage routes one bounded committed row page to the
// authoritative Channel leader.
func (n *Node) ReadBackupMessageLogPage(ctx context.Context, request BackupMessageLogPageRequest) (BackupMessageLogPage, error) {
	if err := validateBackupMessageLogPageRequest(n, request); err != nil {
		return BackupMessageLogPage{}, err
	}
	if request.Channel.LeaderNodeID != n.NodeID() {
		request.TargetBytes = min(request.TargetBytes, int64(backupContinuousMessageChunkBytes))
		return n.callBackupContinuousMessagePage(ctx, request)
	}
	return n.readBackupMessageLogPageLocal(ctx, request)
}

func (n *Node) observeBackupMessageChannelLocal(ctx context.Context, request BackupMessageChannelRequest) (BackupMessageChannelBoundary, error) {
	boundaries, err := n.observeBackupMessageChannelsLocal(ctx, []BackupMessageChannelRequest{request})
	if err != nil {
		return BackupMessageChannelBoundary{}, err
	}
	return boundaries[0], nil
}

func (n *Node) observeBackupMessageChannelsLocal(ctx context.Context, requests []BackupMessageChannelRequest) ([]BackupMessageChannelBoundary, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if n == nil || n.channels == nil || n.localChannelStoreFactory() == nil {
		return nil, ErrNotStarted
	}
	if len(requests) == 0 || len(requests) > backupContinuousMessageBatchChannels {
		return nil, channelruntime.ErrInvalidConfig
	}
	ids := make([]channelruntime.ChannelID, len(requests))
	seen := make(map[channelruntime.ChannelID]struct{}, len(requests))
	for index, request := range requests {
		ids[index] = channelruntime.ChannelID{ID: request.ChannelID, Type: request.ChannelType}
		if _, exists := seen[ids[index]]; exists {
			return nil, channelruntime.ErrInvalidConfig
		}
		seen[ids[index]] = struct{}{}
	}
	probe, err := n.channels.RuntimeProbe(ctx, channelruntime.RuntimeSelector{ChannelIDs: ids})
	if err != nil {
		return nil, err
	}
	loaded := make(map[channelruntime.ChannelID]channelruntime.RuntimeProbeChannel, len(probe.Channels))
	for _, item := range probe.Channels {
		if _, exists := loaded[item.ChannelID]; exists {
			return nil, channelruntime.ErrStaleMeta
		}
		loaded[item.ChannelID] = item
	}
	observedAt := time.Now().UTC().UnixMilli()
	out := make([]BackupMessageChannelBoundary, len(requests))
	for index, request := range requests {
		id := ids[index]
		store, err := n.localChannelStoreFactory().ChannelStore(channelruntime.ChannelKeyForID(id), id)
		if err != nil {
			return nil, err
		}
		state, loadErr := store.Load(ctx)
		retention, retentionErr := store.LoadRetentionState(ctx)
		closeErr := store.Close()
		if loadErr != nil {
			return nil, loadErr
		}
		if retentionErr != nil {
			return nil, retentionErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		hw := state.HW
		if item, ok := loaded[id]; ok {
			if item.Role != channelruntime.RoleLeader ||
				item.ChannelEpoch != request.ChannelEpoch || item.LeaderEpoch != request.LeaderEpoch {
				return nil, channelruntime.ErrStaleMeta
			}
			hw = item.HW
		} else if request.MinISR <= 1 {
			hw = state.LEO
		}
		if retention.LocalRetentionThroughSeq > request.RetentionSeq {
			return nil, channelruntime.ErrStaleMeta
		}
		logStart := request.RetentionSeq
		if logStart > hw {
			logStart = hw
		}
		out[index] = BackupMessageChannelBoundary{
			HashSlot: request.HashSlot, ChannelID: request.ChannelID, ChannelType: request.ChannelType,
			Epoch: request.ChannelEpoch, LogStartOffset: logStart, HW: hw,
			ObservedAtUnixMillis: observedAt,
		}
	}
	return out, nil
}

func (n *Node) readBackupMessageLogPageLocal(ctx context.Context, request BackupMessageLogPageRequest) (BackupMessageLogPage, error) {
	boundary, err := n.observeBackupMessageChannelLocal(ctx, request.Channel)
	if err != nil {
		return BackupMessageLogPage{}, err
	}
	if request.ThroughSeq > boundary.HW || request.FromSeq > request.ThroughSeq {
		return BackupMessageLogPage{}, channelruntime.ErrStaleMeta
	}
	if request.FromSeq <= boundary.LogStartOffset {
		request.FromSeq = boundary.LogStartOffset + 1
	}
	if request.FromSeq > request.ThroughSeq {
		return BackupMessageLogPage{Boundary: boundary, NextSeq: request.FromSeq, Done: true}, nil
	}
	id := channelruntime.ChannelID{ID: request.Channel.ChannelID, Type: request.Channel.ChannelType}
	store, err := n.localChannelStoreFactory().ChannelStore(channelruntime.ChannelKeyForID(id), id)
	if err != nil {
		return BackupMessageLogPage{}, err
	}
	defer func() { _ = store.Close() }()
	read, err := store.ReadCommitted(ctx, channelstore.ReadCommittedRequest{
		FromSeq: request.FromSeq, MaxSeq: request.ThroughSeq,
		MinSeq: boundary.LogStartOffset + 1, Limit: request.MaxRecords, MaxBytes: int(request.TargetBytes),
	})
	if err != nil {
		return BackupMessageLogPage{}, err
	}
	if len(read.Messages) == 0 {
		return BackupMessageLogPage{}, fmt.Errorf("cluster: committed message page made no progress")
	}
	page := BackupMessageLogPage{Records: make([][]byte, 0, len(read.Messages)), Boundary: boundary}
	var pageBytes int64
	for _, message := range read.Messages {
		record, err := backupartifact.MarshalMessageLogRecord(backupartifact.MessageLogRecord{
			Kind: backupartifact.MessageLogRecordMessage, HashSlot: request.Channel.HashSlot,
			ChannelID: request.Channel.ChannelID, ChannelType: request.Channel.ChannelType,
			Epoch: request.Channel.ChannelEpoch, LogStartOffset: boundary.LogStartOffset, HW: request.ThroughSeq,
			MessageSeq: message.MessageSeq, MessageID: message.MessageID, Setting: message.Setting,
			FromUID: message.FromUID, ClientMsgNo: message.ClientMsgNo,
			ServerTimestampMS: message.ServerTimestampMS, SyncOnce: message.SyncOnce,
			Payload: message.Payload,
		})
		if err != nil {
			return BackupMessageLogPage{}, err
		}
		recordBytes := int64(4 + len(record))
		if recordBytes > request.MaxBytes {
			return BackupMessageLogPage{}, fmt.Errorf("cluster: committed message record exceeds hard limit")
		}
		if len(page.Records) > 0 && pageBytes > request.TargetBytes-recordBytes {
			break
		}
		page.Records = append(page.Records, record)
		pageBytes += recordBytes
		page.NextSeq = message.MessageSeq + 1
		if pageBytes >= request.TargetBytes {
			break
		}
	}
	if len(page.Records) == 0 || page.NextSeq <= request.FromSeq {
		return BackupMessageLogPage{}, fmt.Errorf("cluster: committed message page made no progress")
	}
	page.Done = page.NextSeq > request.ThroughSeq
	if page.Done {
		page.Boundary.HW = request.ThroughSeq
	} else {
		page.Boundary.HW = page.NextSeq - 1
	}
	return page, nil
}

func validateBackupMessageChannelRequest(n *Node, request BackupMessageChannelRequest) error {
	if n == nil || request.HashSlot >= n.cfg.Slots.HashSlotCount || request.ChannelID == "" ||
		len(request.ChannelID) > 4<<10 || request.LeaderNodeID == 0 ||
		request.ChannelEpoch == 0 || request.LeaderEpoch == 0 || request.MinISR <= 0 {
		return channelruntime.ErrInvalidConfig
	}
	route, err := n.RouteKey(request.ChannelID)
	if err != nil {
		return err
	}
	if route.HashSlot != request.HashSlot {
		return channelruntime.ErrStaleMeta
	}
	return nil
}

func validateBackupMessageChannelBoundary(request BackupMessageChannelRequest, boundary BackupMessageChannelBoundary, requireObservedAt bool) error {
	if boundary.HashSlot != request.HashSlot ||
		boundary.ChannelID != request.ChannelID ||
		boundary.ChannelType != request.ChannelType ||
		boundary.Epoch != request.ChannelEpoch ||
		boundary.LogStartOffset > boundary.HW ||
		boundary.LogStartOffset != min(request.RetentionSeq, boundary.HW) ||
		(requireObservedAt && boundary.ObservedAtUnixMillis <= 0) ||
		(!requireObservedAt && boundary.ObservedAtUnixMillis != 0) {
		return channelruntime.ErrStaleMeta
	}
	return nil
}

func validateBackupMessageLogPageRequest(n *Node, request BackupMessageLogPageRequest) error {
	if err := validateBackupMessageChannelRequest(n, request.Channel); err != nil {
		return err
	}
	if request.FromSeq == 0 || request.ThroughSeq == 0 || request.ThroughSeq == math.MaxUint64 ||
		request.TargetBytes <= 0 || request.TargetBytes > request.MaxBytes ||
		request.MaxBytes <= 0 || request.MaxBytes > MaxCaptureBackupRecordBytes ||
		request.MaxBytes > int64(math.MaxInt) ||
		request.MaxRecords <= 0 || request.MaxRecords > 1<<20 {
		return channelruntime.ErrInvalidConfig
	}
	return nil
}
