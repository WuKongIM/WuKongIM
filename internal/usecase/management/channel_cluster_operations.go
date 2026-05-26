package management

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

var (
	// ErrUnsupportedChannelClusterRepairReason means the requested manager reason is not handled by safe leader repair.
	ErrUnsupportedChannelClusterRepairReason = errors.New("management: unsupported channel cluster repair reason")
	// ErrChannelLeaderTransferTargetNotReplica means the requested node is not configured as a replica.
	ErrChannelLeaderTransferTargetNotReplica = errors.New("management: channel leader transfer target is not a replica")
	// ErrChannelLeaderTransferTargetNotISR means the requested node is not in the authoritative ISR set.
	ErrChannelLeaderTransferTargetNotISR = errors.New("management: channel leader transfer target is not in isr")
	// ErrChannelLeaderTransferInactiveChannel means the channel is not active.
	ErrChannelLeaderTransferInactiveChannel = errors.New("management: channel leader transfer requires active channel")
)

// ChannelClusterReplicaStatus is a manager-facing per-replica status row.
type ChannelClusterReplicaStatus struct {
	// NodeID is the replica node ID.
	NodeID uint64
	// Role is "leader" or "follower" from authoritative metadata.
	Role string
	// IsLeader reports whether this replica is the authoritative leader.
	IsLeader bool
	// InISR reports whether this replica is in the authoritative ISR set.
	InISR bool
	// Reported reports whether live runtime status was proven for this replica.
	Reported bool
	// CommitSeq is the proven committed sequence for this replica, when known.
	CommitSeq *uint64
	// LEO is the proven log end offset for this replica, when known.
	LEO *uint64
	// CheckpointHW is the proven durable checkpoint high watermark, when known.
	CheckpointHW *uint64
	// Lag is leader commit minus replica commit, when both are known.
	Lag *uint64
}

// ChannelClusterReplicaDetail contains authoritative metadata plus proven runtime status.
type ChannelClusterReplicaDetail struct {
	// Channel is authoritative manager channel metadata.
	Channel ChannelRuntimeMetaDetail
	// RuntimeReported reports whether a runtime status source proved a live status.
	RuntimeReported bool
	// CommitSeq is the proven leader/local committed sequence, when known.
	CommitSeq *uint64
	// MinAvailableSeq is the proven minimum readable sequence, when known.
	MinAvailableSeq *uint64
	// RetentionThroughSeq is the proven retention boundary, when known.
	RetentionThroughSeq *uint64
	// Replicas contains one row for each authoritative replica.
	Replicas []ChannelClusterReplicaStatus
}

// RepairChannelClusterLeaderRequest requests a safe channel leader repair.
type RepairChannelClusterLeaderRequest struct {
	// ChannelID identifies the channel to repair.
	ChannelID string
	// ChannelType identifies the channel type to repair.
	ChannelType int64
	// Reason is the manager-facing reason that requested repair.
	Reason string
}

// RepairChannelClusterLeaderResult is returned by the repair operator port.
type RepairChannelClusterLeaderResult struct {
	// Changed reports whether authoritative metadata changed.
	Changed bool
	// Meta is authoritative metadata after repair or validation.
	Meta metadb.ChannelRuntimeMeta
}

// RepairChannelClusterLeaderResponse is the manager-facing repair response.
type RepairChannelClusterLeaderResponse struct {
	// Changed reports whether authoritative metadata changed.
	Changed bool
	// Channel is authoritative manager metadata after repair or validation.
	Channel ChannelRuntimeMetaDetail
}

// TransferChannelClusterLeaderRequest requests explicit safe channel leader transfer.
type TransferChannelClusterLeaderRequest struct {
	// ChannelID identifies the channel whose leader should move.
	ChannelID string
	// ChannelType identifies the channel type.
	ChannelType int64
	// TargetNodeID is the replica node that should become leader.
	TargetNodeID uint64
}

// TransferChannelClusterLeaderResult is returned by the transfer operator port.
type TransferChannelClusterLeaderResult struct {
	// Changed reports whether authoritative metadata changed.
	Changed bool
	// Meta is authoritative metadata after transfer or validation.
	Meta metadb.ChannelRuntimeMeta
}

// TransferChannelClusterLeaderResponse is the manager-facing transfer response.
type TransferChannelClusterLeaderResponse struct {
	// Changed reports whether authoritative metadata changed.
	Changed bool
	// Channel is authoritative manager metadata after transfer or validation.
	Channel ChannelRuntimeMetaDetail
}

// GetChannelClusterReplicaDetail returns truthful authoritative and proven runtime replica detail.
func (a *App) GetChannelClusterReplicaDetail(ctx context.Context, channelID string, channelType int64) (ChannelClusterReplicaDetail, error) {
	detail, err := a.GetChannelRuntimeMeta(ctx, channelID, channelType)
	if err != nil {
		return ChannelClusterReplicaDetail{}, err
	}
	out := ChannelClusterReplicaDetail{
		Channel:  detail,
		Replicas: channelClusterReplicaRows(detail.ChannelRuntimeMeta),
	}
	if a == nil || a.channelReplicaStatus == nil {
		return out, nil
	}
	status, err := a.channelReplicaStatus.ChannelRuntimeStatus(ctx, channel.ChannelID{ID: channelID, Type: uint8(channelType)})
	if err != nil {
		if channelClusterReplicaStatusUnreported(err) {
			return out, nil
		}
		return ChannelClusterReplicaDetail{}, err
	}
	out.RuntimeReported = true
	out.CommitSeq = uint64ValuePtr(channelRuntimeCommittedSeq(status))
	out.MinAvailableSeq = uint64ValuePtr(status.MinAvailableSeq)
	out.RetentionThroughSeq = uint64ValuePtr(status.RetentionThroughSeq)
	applyChannelRuntimeStatusToReplicaRows(out.Replicas, detail.Leader, *out.CommitSeq)
	return out, nil
}

func channelClusterReplicaRows(meta ChannelRuntimeMeta) []ChannelClusterReplicaStatus {
	rows := make([]ChannelClusterReplicaStatus, 0, len(meta.Replicas))
	isr := uint64Set(meta.ISR)
	for _, nodeID := range meta.Replicas {
		isLeader := nodeID != 0 && nodeID == meta.Leader
		role := "follower"
		if isLeader {
			role = "leader"
		}
		rows = append(rows, ChannelClusterReplicaStatus{
			NodeID:   nodeID,
			Role:     role,
			IsLeader: isLeader,
			InISR:    isr[nodeID],
		})
	}
	return rows
}

func applyChannelRuntimeStatusToReplicaRows(rows []ChannelClusterReplicaStatus, leaderID uint64, leaderCommit uint64) {
	for i := range rows {
		if leaderID == 0 || rows[i].NodeID != leaderID {
			continue
		}
		rows[i].Reported = true
		rows[i].CommitSeq = uint64ValuePtr(leaderCommit)
		rows[i].Lag = uint64ValuePtr(0)
	}
}

func channelRuntimeCommittedSeq(status channel.ChannelRuntimeStatus) uint64 {
	if status.CommittedSeq != 0 {
		return status.CommittedSeq
	}
	return status.HW
}

func channelClusterReplicaStatusUnreported(err error) bool {
	return errors.Is(err, channel.ErrNotReady) ||
		errors.Is(err, channel.ErrStaleMeta) ||
		errors.Is(err, channel.ErrInvalidConfig)
}

func uint64Set(values []uint64) map[uint64]bool {
	out := make(map[uint64]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func uint64ValuePtr(v uint64) *uint64 { return &v }

// RepairChannelClusterLeader runs policy-driven safe channel leader repair.
func (a *App) RepairChannelClusterLeader(ctx context.Context, req RepairChannelClusterLeaderRequest) (RepairChannelClusterLeaderResponse, error) {
	if req.ChannelID == "" || req.ChannelType <= 0 {
		return RepairChannelClusterLeaderResponse{}, metadb.ErrInvalidArgument
	}
	current, err := a.GetChannelRuntimeMeta(ctx, req.ChannelID, req.ChannelType)
	if err != nil {
		return RepairChannelClusterLeaderResponse{}, err
	}
	mappedReason, err := channelClusterLeaderRepairReason(req.Reason, current.Leader)
	if err != nil {
		return RepairChannelClusterLeaderResponse{}, err
	}
	if a == nil || a.channelLeaderRepair == nil {
		return RepairChannelClusterLeaderResponse{}, channel.ErrInvalidConfig
	}
	repairReq := req
	repairReq.Reason = mappedReason
	result, err := a.channelLeaderRepair.RepairChannelLeader(ctx, repairReq)
	if err != nil {
		return RepairChannelClusterLeaderResponse{}, err
	}
	return RepairChannelClusterLeaderResponse{
		Changed: result.Changed,
		Channel: a.channelRuntimeMetaDetailFromMeta(ctx, result.Meta),
	}, nil
}

// TransferChannelClusterLeader runs explicit safe channel leader transfer.
func (a *App) TransferChannelClusterLeader(ctx context.Context, req TransferChannelClusterLeaderRequest) (TransferChannelClusterLeaderResponse, error) {
	if req.ChannelID == "" || req.ChannelType <= 0 || req.TargetNodeID == 0 {
		return TransferChannelClusterLeaderResponse{}, metadb.ErrInvalidArgument
	}
	current, err := a.GetChannelRuntimeMeta(ctx, req.ChannelID, req.ChannelType)
	if err != nil {
		return TransferChannelClusterLeaderResponse{}, err
	}
	if current.Status != "active" {
		return TransferChannelClusterLeaderResponse{}, ErrChannelLeaderTransferInactiveChannel
	}
	if !containsManagementUint64(current.Replicas, req.TargetNodeID) {
		return TransferChannelClusterLeaderResponse{}, ErrChannelLeaderTransferTargetNotReplica
	}
	if !containsManagementUint64(current.ISR, req.TargetNodeID) {
		return TransferChannelClusterLeaderResponse{}, ErrChannelLeaderTransferTargetNotISR
	}
	if current.Leader == req.TargetNodeID {
		return TransferChannelClusterLeaderResponse{Changed: false, Channel: current}, nil
	}
	if a == nil || a.channelLeaderTransfer == nil {
		return TransferChannelClusterLeaderResponse{}, channel.ErrInvalidConfig
	}
	result, err := a.channelLeaderTransfer.TransferChannelLeader(ctx, req)
	if err != nil {
		return TransferChannelClusterLeaderResponse{}, err
	}
	return TransferChannelClusterLeaderResponse{
		Changed: result.Changed,
		Channel: a.channelRuntimeMetaDetailFromMeta(ctx, result.Meta),
	}, nil
}

func channelClusterLeaderRepairReason(reason string, leader uint64) (string, error) {
	switch reason {
	case "":
		if leader == 0 {
			return channel.LeaderRepairReasonLeaderMissing.String(), nil
		}
		return "", ErrUnsupportedChannelClusterRepairReason
	case ChannelClusterUnhealthyReasonNoLeader:
		return channel.LeaderRepairReasonLeaderMissing.String(), nil
	case ChannelClusterUnhealthyReasonISRInsufficient, ChannelClusterUnhealthyReasonStatusNotActive:
		return "", ErrUnsupportedChannelClusterRepairReason
	default:
		return "", ErrUnsupportedChannelClusterRepairReason
	}
}

func (a *App) channelRuntimeMetaDetailFromMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) ChannelRuntimeMetaDetail {
	var slotID multiraft.SlotID
	var hashSlot uint16
	if a != nil && a.cluster != nil {
		slotID = a.cluster.SlotForKey(meta.ChannelID)
		hashSlot = a.cluster.HashSlotForKey(meta.ChannelID)
	}
	maxMessageSeq, _ := a.channelMaxMessageSeq(ctx, meta.ChannelID, meta.ChannelType)
	return ChannelRuntimeMetaDetail{
		ChannelRuntimeMeta: managerChannelRuntimeMetaWithMaxSeq(slotID, meta, &maxMessageSeq),
		HashSlot:           hashSlot,
		Features:           meta.Features,
		LeaseUntilMS:       meta.LeaseUntilMS,
	}
}

func containsManagementUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
