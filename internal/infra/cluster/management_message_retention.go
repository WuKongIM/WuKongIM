package cluster

import (
	"context"
	"errors"
	"strings"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// MessageRetentionNode is the cluster surface required to advance message log compaction boundaries.
type MessageRetentionNode interface {
	NodeID() uint64
	GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
	ChannelRetentionView(context.Context, channelruntime.ChannelID) (channelruntime.RetentionView, error)
	ReadChannelCommitted(context.Context, channelruntime.ChannelID, channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error)
	AdvanceChannelRetentionThroughSeq(context.Context, metadb.ChannelRetentionAdvance) error
}

type messageRetentionRPCNode interface {
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// PermanentMessageErasureRecorder durably records a deletion before live retention advances.
type PermanentMessageErasureRecorder interface {
	RecordPermanentMessageErasure(context.Context, backupcontract.PermanentMessageErasure) (backupcontract.ErasureLedgerReceipt, error)
}

// ManagementMessageRetentionOperator advances channel message retention through Slot metadata.
type ManagementMessageRetentionOperator struct {
	node            MessageRetentionNode
	remote          *accessnode.Client
	erasureRecorder PermanentMessageErasureRecorder
	now             func() time.Time
}

// NewManagementMessageRetentionOperator creates a Slot-backed manager retention operator that may forward to the channel leader.
func NewManagementMessageRetentionOperator(node MessageRetentionNode, erasureRecorders ...PermanentMessageErasureRecorder) *ManagementMessageRetentionOperator {
	operator := NewLocalManagementMessageRetentionOperator(node, erasureRecorders...)
	if rpcNode, ok := node.(messageRetentionRPCNode); ok {
		operator.remote = accessnode.NewClient(rpcNode)
	}
	return operator
}

// NewLocalManagementMessageRetentionOperator creates a Slot-backed retention operator without remote forwarding.
func NewLocalManagementMessageRetentionOperator(node MessageRetentionNode, erasureRecorders ...PermanentMessageErasureRecorder) *ManagementMessageRetentionOperator {
	operator := &ManagementMessageRetentionOperator{node: node, now: time.Now}
	if len(erasureRecorders) > 0 {
		operator.erasureRecorder = erasureRecorders[0]
	}
	return operator
}

// AdvanceMessageRetention evaluates and advances one channel's logical compaction boundary.
func (o *ManagementMessageRetentionOperator) AdvanceMessageRetention(ctx context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error) {
	if o == nil || o.node == nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, managementusecase.ErrMessageRetentionUnavailable
	}
	req.ChannelID = strings.TrimSpace(req.ChannelID)
	if req.ChannelID == "" || req.ChannelType <= 0 || req.ChannelType > 255 || req.ThroughSeq == 0 {
		return managementusecase.AdvanceMessageRetentionResponse{}, metadb.ErrInvalidArgument
	}
	meta, err := o.node.GetChannelRuntimeMeta(ctx, req.ChannelID, req.ChannelType)
	if err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, mapMessageRetentionError(err)
	}
	current := meta.RetentionThroughSeq
	if req.ThroughSeq <= current {
		return retentionResponse(req, current, managementusecase.MessageRetentionStatusNoop, managementusecase.MessageRetentionBlockedReasonCurrentBoundary), nil
	}
	if meta.Leader == 0 {
		return managementusecase.AdvanceMessageRetentionResponse{}, channelappend.ErrRouteNotReady
	}
	if meta.Leader != o.node.NodeID() {
		if o.remote != nil {
			return o.remote.AdvanceManagerMessageRetention(ctx, meta.Leader, req)
		}
		return managementusecase.AdvanceMessageRetentionResponse{}, channelappend.ErrNotLeader
	}

	channelID := channelruntime.ChannelID{ID: req.ChannelID, Type: uint8(req.ChannelType)}
	view, err := o.node.ChannelRetentionView(ctx, channelID)
	if err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, mapMessageRetentionError(err)
	}
	latestSeq, err := o.latestCommittedSeq(ctx, req, current)
	if err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	safeSeq, blockedReason := retentionSafeBoundary(req.ThroughSeq, latestSeq, view)
	if safeSeq < req.ThroughSeq {
		if blockedReason == managementusecase.MessageRetentionBlockedReasonNone {
			blockedReason = managementusecase.MessageRetentionBlockedReasonCurrentBoundary
		}
		if safeSeq < current {
			safeSeq = current
		}
		return retentionResponse(req, safeSeq, managementusecase.MessageRetentionStatusBlocked, blockedReason), nil
	}
	if req.DryRun {
		return retentionResponse(req, safeSeq, managementusecase.MessageRetentionStatusWouldAdvance, managementusecase.MessageRetentionBlockedReasonNone), nil
	}
	acceptedAt := o.currentTime().UTC()
	if o.erasureRecorder != nil {
		if _, err := o.erasureRecorder.RecordPermanentMessageErasure(ctx, backupcontract.PermanentMessageErasure{
			ChannelID: req.ChannelID, ChannelType: uint8(req.ChannelType), ThroughSeq: safeSeq, RequestedAtUnixMillis: acceptedAt.UnixMilli(),
		}); err != nil {
			return managementusecase.AdvanceMessageRetentionResponse{}, err
		}
	}

	advance := metadb.ChannelRetentionAdvance{
		ChannelID:            req.ChannelID,
		ChannelType:          req.ChannelType,
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedLeaseUntilMS: meta.LeaseUntilMS,
		RetentionThroughSeq:  safeSeq,
		RetentionUpdatedAtMS: acceptedAt.UnixMilli(),
	}
	if err := o.node.AdvanceChannelRetentionThroughSeq(ctx, advance); err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, mapMessageRetentionError(err)
	}
	return retentionResponse(req, safeSeq, managementusecase.MessageRetentionStatusAdvanced, managementusecase.MessageRetentionBlockedReasonNone), nil
}

func (o *ManagementMessageRetentionOperator) latestCommittedSeq(ctx context.Context, req managementusecase.AdvanceMessageRetentionRequest, current uint64) (uint64, error) {
	read, err := o.node.ReadChannelCommitted(ctx, channelruntime.ChannelID{ID: req.ChannelID, Type: uint8(req.ChannelType)}, channelstore.ReadCommittedRequest{
		FromSeq:  maxUint64(),
		MaxSeq:   maxUint64(),
		Limit:    1,
		MaxBytes: maxInt(),
		Reverse:  true,
		MinSeq:   minAvailableMessageSeq(current),
	})
	if err != nil {
		return 0, mapAppendError(err)
	}
	if len(read.Messages) == 0 {
		return 0, nil
	}
	return read.Messages[0].MessageSeq, nil
}

func retentionSafeBoundary(requestedThroughSeq uint64, latestCommittedSeq uint64, view channelruntime.RetentionView) (uint64, managementusecase.MessageRetentionBlockedReason) {
	if latestCommittedSeq == 0 {
		return 0, managementusecase.MessageRetentionBlockedReasonNoCommittedMessage
	}
	safeSeq := requestedThroughSeq
	reason := managementusecase.MessageRetentionBlockedReasonNone
	if latestCommittedSeq < safeSeq {
		safeSeq = latestCommittedSeq
		reason = managementusecase.MessageRetentionBlockedReasonHW
	}
	if view.HW < safeSeq {
		safeSeq = view.HW
		reason = managementusecase.MessageRetentionBlockedReasonHW
	}
	if view.MinISRMatchOffset < safeSeq {
		safeSeq = view.MinISRMatchOffset
		reason = managementusecase.MessageRetentionBlockedReasonMinISRMatchOffset
	}
	return safeSeq, reason
}

func (o *ManagementMessageRetentionOperator) currentTime() time.Time {
	if o == nil || o.now == nil {
		return time.Now()
	}
	return o.now()
}

func retentionResponse(req managementusecase.AdvanceMessageRetentionRequest, throughSeq uint64, status managementusecase.MessageRetentionStatus, reason managementusecase.MessageRetentionBlockedReason) managementusecase.AdvanceMessageRetentionResponse {
	return managementusecase.AdvanceMessageRetentionResponse{
		ChannelID:           req.ChannelID,
		ChannelType:         req.ChannelType,
		RequestedThroughSeq: req.ThroughSeq,
		AdvancedThroughSeq:  throughSeq,
		MinAvailableSeq:     minAvailableMessageSeq(throughSeq),
		Status:              status,
		BlockedReason:       reason,
	}
}

func minAvailableMessageSeq(retentionThroughSeq uint64) uint64 {
	if retentionThroughSeq == maxUint64() {
		return retentionThroughSeq
	}
	return retentionThroughSeq + 1
}

func mapMessageRetentionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, metadb.ErrInvalidArgument) || errors.Is(err, metadb.ErrNotFound) {
		return err
	}
	if errors.Is(err, metadb.ErrStaleMeta) {
		return errors.Join(channelappend.ErrStaleRoute, err)
	}
	return mapAppendError(err)
}
