package cluster

import (
	"context"
	"errors"
	"strings"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// MessageRetentionNode is the clusterv2 surface required to advance message log compaction boundaries.
type MessageRetentionNode interface {
	NodeID() uint64
	GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
	ReadChannelCommitted(context.Context, channelv2.ChannelID, channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error)
	AdvanceChannelRetentionThroughSeq(context.Context, metadb.ChannelRetentionAdvance) error
}

type messageRetentionRPCNode interface {
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// ManagementMessageRetentionOperator advances channel message retention through Slot metadata.
type ManagementMessageRetentionOperator struct {
	node   MessageRetentionNode
	remote *accessnode.Client
	now    func() time.Time
}

// NewManagementMessageRetentionOperator creates a Slot-backed manager retention operator that may forward to the channel leader.
func NewManagementMessageRetentionOperator(node MessageRetentionNode) *ManagementMessageRetentionOperator {
	operator := NewLocalManagementMessageRetentionOperator(node)
	if rpcNode, ok := node.(messageRetentionRPCNode); ok {
		operator.remote = accessnode.NewClient(rpcNode)
	}
	return operator
}

// NewLocalManagementMessageRetentionOperator creates a Slot-backed retention operator without remote forwarding.
func NewLocalManagementMessageRetentionOperator(node MessageRetentionNode) *ManagementMessageRetentionOperator {
	return &ManagementMessageRetentionOperator{node: node, now: time.Now}
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

	latestSeq, err := o.latestCommittedSeq(ctx, req, current)
	if err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	safeSeq := req.ThroughSeq
	blockedReason := managementusecase.MessageRetentionBlockedReasonNone
	if latestSeq < safeSeq {
		safeSeq = latestSeq
		blockedReason = managementusecase.MessageRetentionBlockedReasonHW
	}
	if safeSeq <= current {
		if blockedReason == managementusecase.MessageRetentionBlockedReasonNone {
			blockedReason = managementusecase.MessageRetentionBlockedReasonCurrentBoundary
		}
		return retentionResponse(req, current, managementusecase.MessageRetentionStatusBlocked, blockedReason), nil
	}
	if req.DryRun {
		return retentionResponse(req, safeSeq, managementusecase.MessageRetentionStatusWouldAdvance, managementusecase.MessageRetentionBlockedReasonNone), nil
	}

	advance := metadb.ChannelRetentionAdvance{
		ChannelID:            req.ChannelID,
		ChannelType:          req.ChannelType,
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedLeaseUntilMS: meta.LeaseUntilMS,
		RetentionThroughSeq:  safeSeq,
		RetentionUpdatedAtMS: o.currentTime().UnixMilli(),
	}
	if err := o.node.AdvanceChannelRetentionThroughSeq(ctx, advance); err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, mapMessageRetentionError(err)
	}
	return retentionResponse(req, safeSeq, managementusecase.MessageRetentionStatusAdvanced, managementusecase.MessageRetentionBlockedReasonNone), nil
}

func (o *ManagementMessageRetentionOperator) latestCommittedSeq(ctx context.Context, req managementusecase.AdvanceMessageRetentionRequest, current uint64) (uint64, error) {
	read, err := o.node.ReadChannelCommitted(ctx, channelv2.ChannelID{ID: req.ChannelID, Type: uint8(req.ChannelType)}, channelstore.ReadCommittedRequest{
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
