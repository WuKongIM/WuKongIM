package app

import (
	"context"
	"errors"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type managerMessageRetentionMetas interface {
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
}

type managerMessageRetentionRuntime interface {
	RetentionView(key channel.ChannelKey) (channel.RetentionView, error)
	ApplyRetentionBoundary(ctx context.Context, key channel.ChannelKey, throughSeq uint64) error
}

type managerMessageRetentionStoreProvider interface {
	// LoadCommittedDispatchCursor reads replay progress for dry-run planning without mutating storage.
	LoadCommittedDispatchCursor(ctx context.Context, id channel.ChannelID, cursorName string) (uint64, bool, error)
	// ConfirmCommittedDispatchCursorDurable syncs replay progress before a real metadata advance.
	ConfirmCommittedDispatchCursorDurable(ctx context.Context, id channel.ChannelID, cursorName string, minSeq uint64) (uint64, error)
}

type managerMessageRetentionMetadata interface {
	AdvanceChannelRetentionThroughSeq(ctx context.Context, req metadb.ChannelRetentionAdvance) error
}

type managerMessageRetentionRemote interface {
	AdvanceChannelRetention(ctx context.Context, nodeID uint64, req accessnode.ChannelRetentionAdvanceRequest) (accessnode.ChannelRetentionAdvanceResult, error)
}

type managerMessageRetentionOperator struct {
	localNodeID uint64
	metas       managerMessageRetentionMetas
	runtime     managerMessageRetentionRuntime
	stores      managerMessageRetentionStoreProvider
	metadata    managerMessageRetentionMetadata
	remote      managerMessageRetentionRemote
	now         func() time.Time
}

func (o managerMessageRetentionOperator) AdvanceMessageRetention(ctx context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error) {
	if err := contextError(ctx); err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	if o.metas == nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, channel.ErrInvalidConfig
	}

	id := channel.ChannelID{ID: req.ChannelID, Type: uint8(req.ChannelType)}
	meta, err := o.metas.GetChannelRuntimeMeta(ctx, req.ChannelID, req.ChannelType)
	if err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	if meta.Leader == 0 {
		return managementusecase.AdvanceMessageRetentionResponse{}, raftcluster.ErrNoLeader
	}
	if meta.Leader != o.localNodeID {
		if o.remote == nil {
			return managementusecase.AdvanceMessageRetentionResponse{}, channel.ErrStaleMeta
		}
		result, err := o.remote.AdvanceChannelRetention(ctx, meta.Leader, accessnode.ChannelRetentionAdvanceRequest{
			ChannelID:  id,
			ThroughSeq: req.ThroughSeq,
			DryRun:     req.DryRun,
		})
		if err != nil {
			return managementusecase.AdvanceMessageRetentionResponse{}, err
		}
		return managerRetentionFromAccessNode(result), nil
	}
	return o.advanceLocal(ctx, id, req)
}

func (o managerMessageRetentionOperator) advanceLocal(ctx context.Context, id channel.ChannelID, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error) {
	if o.runtime == nil || o.stores == nil || o.metadata == nil || o.localNodeID == 0 {
		return managementusecase.AdvanceMessageRetentionResponse{}, channel.ErrInvalidConfig
	}

	key := channelhandler.KeyFromChannelID(id)
	view, err := o.runtime.RetentionView(key)
	if err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	if err := o.validateRetentionLeaderView(view); err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	if req.ThroughSeq <= view.RetentionThroughSeq {
		return managerMessageRetentionResponse(req, view.RetentionThroughSeq, managementusecase.MessageRetentionStatusNoop, managementusecase.MessageRetentionBlockedReasonNone), nil
	}

	cursor, ok, err := o.retentionReplayCursor(ctx, id, view, req.DryRun)
	if err != nil {
		if managerRetentionCursorBlocked(err) {
			return managerMessageRetentionResponse(req, view.RetentionThroughSeq, managementusecase.MessageRetentionStatusBlocked, managementusecase.MessageRetentionBlockedReasonReplayCursor), nil
		}
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	if !ok || cursor <= view.RetentionThroughSeq {
		return managerMessageRetentionResponse(req, view.RetentionThroughSeq, managementusecase.MessageRetentionStatusBlocked, managementusecase.MessageRetentionBlockedReasonReplayCursor), nil
	}

	latest := view
	if !req.DryRun {
		latest, err = o.runtime.RetentionView(key)
		if err != nil {
			return managementusecase.AdvanceMessageRetentionResponse{}, err
		}
		if err := o.validateRetentionLeaderView(latest); err != nil {
			return managementusecase.AdvanceMessageRetentionResponse{}, err
		}
	}

	throughSeq, reason := managerRetentionDecision(req.ThroughSeq, latest.RetentionThroughSeq, []managerRetentionGate{
		{seq: cursor, reason: managementusecase.MessageRetentionBlockedReasonReplayCursor},
		{seq: latest.MinISRMatchOffset, reason: managementusecase.MessageRetentionBlockedReasonMinISRMatchOffset},
		{seq: latest.HW, reason: managementusecase.MessageRetentionBlockedReasonHW},
		{seq: latest.CheckpointHW, reason: managementusecase.MessageRetentionBlockedReasonCheckpointHW},
	})
	if throughSeq <= latest.RetentionThroughSeq {
		status := managementusecase.MessageRetentionStatusNoop
		blockedReason := managementusecase.MessageRetentionBlockedReasonNone
		if reason != managementusecase.MessageRetentionBlockedReasonCurrentBoundary {
			status = managementusecase.MessageRetentionStatusBlocked
			blockedReason = reason
		}
		return managerMessageRetentionResponse(req, latest.RetentionThroughSeq, status, blockedReason), nil
	}
	if req.DryRun {
		return managerMessageRetentionResponse(req, throughSeq, managementusecase.MessageRetentionStatusWouldAdvance, managementusecase.MessageRetentionBlockedReasonNone), nil
	}

	if err := o.metadata.AdvanceChannelRetentionThroughSeq(ctx, managerRetentionAdvanceRequest(id, latest, throughSeq, o.clockNow())); err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	if err := o.runtime.ApplyRetentionBoundary(ctx, key, throughSeq); err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	return managerMessageRetentionResponse(req, throughSeq, managementusecase.MessageRetentionStatusAdvanced, managementusecase.MessageRetentionBlockedReasonNone), nil
}

func (o managerMessageRetentionOperator) retentionReplayCursor(ctx context.Context, id channel.ChannelID, view channel.RetentionView, dryRun bool) (uint64, bool, error) {
	if dryRun {
		return o.stores.LoadCommittedDispatchCursor(ctx, id, appChannelRetentionCursorName)
	}
	cursor, err := o.stores.ConfirmCommittedDispatchCursorDurable(ctx, id, appChannelRetentionCursorName, managerRetentionNextSeq(view.RetentionThroughSeq))
	if err != nil {
		return 0, false, err
	}
	return cursor, true, nil
}

func (o managerMessageRetentionOperator) validateRetentionLeaderView(view channel.RetentionView) error {
	if view.Leader != channel.NodeID(o.localNodeID) || !view.CommitReady {
		return channel.ErrStaleMeta
	}
	if leaseUntil := view.LeaseUntil; !leaseUntil.IsZero() && !o.clockNow().Before(leaseUntil) {
		return channel.ErrStaleMeta
	}
	return nil
}

func (o managerMessageRetentionOperator) clockNow() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

type managerRetentionGate struct {
	seq    uint64
	reason managementusecase.MessageRetentionBlockedReason
}

func managerRetentionDecision(requested uint64, current uint64, gates []managerRetentionGate) (uint64, managementusecase.MessageRetentionBlockedReason) {
	through := requested
	reason := managementusecase.MessageRetentionBlockedReasonNone
	for _, gate := range gates {
		if gate.seq < through {
			through = gate.seq
			reason = gate.reason
		}
	}
	if through <= current && reason == managementusecase.MessageRetentionBlockedReasonNone {
		reason = managementusecase.MessageRetentionBlockedReasonCurrentBoundary
	}
	return through, reason
}

func managerMessageRetentionResponse(req managementusecase.AdvanceMessageRetentionRequest, throughSeq uint64, status managementusecase.MessageRetentionStatus, reason managementusecase.MessageRetentionBlockedReason) managementusecase.AdvanceMessageRetentionResponse {
	return managementusecase.AdvanceMessageRetentionResponse{
		ChannelID:           req.ChannelID,
		ChannelType:         req.ChannelType,
		RequestedThroughSeq: req.ThroughSeq,
		AdvancedThroughSeq:  throughSeq,
		MinAvailableSeq:     channel.EffectiveMinAvailableSeq(throughSeq, 0),
		Status:              status,
		BlockedReason:       reason,
	}
}

func managerRetentionAdvanceRequest(id channel.ChannelID, view channel.RetentionView, throughSeq uint64, now time.Time) metadb.ChannelRetentionAdvance {
	return metadb.ChannelRetentionAdvance{
		ChannelID:            id.ID,
		ChannelType:          int64(id.Type),
		ExpectedChannelEpoch: view.Epoch,
		ExpectedLeaderEpoch:  view.LeaderEpoch,
		ExpectedLeader:       uint64(view.Leader),
		ExpectedLeaseUntilMS: managerRetentionLeaseUntilMS(view.LeaseUntil),
		RetentionThroughSeq:  throughSeq,
		RetentionUpdatedAtMS: now.UnixMilli(),
	}
}

func managerRetentionLeaseUntilMS(leaseUntil time.Time) int64 {
	if leaseUntil.IsZero() {
		return 0
	}
	return leaseUntil.UnixMilli()
}

func managerRetentionNextSeq(seq uint64) uint64 {
	if seq == ^uint64(0) {
		return seq
	}
	return seq + 1
}

func managerRetentionCursorBlocked(err error) bool {
	return errors.Is(err, channel.ErrEmptyState) || errors.Is(err, channel.ErrCorruptState)
}

type managerMessageRetentionStores struct {
	engine *channelstore.Engine
}

func (s managerMessageRetentionStores) LoadCommittedDispatchCursor(ctx context.Context, id channel.ChannelID, cursorName string) (uint64, bool, error) {
	if err := contextError(ctx); err != nil {
		return 0, false, err
	}
	if s.engine == nil {
		return 0, false, channel.ErrInvalidConfig
	}
	st := s.engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
	return st.LoadCommittedDispatchCursor(cursorName)
}

func (s managerMessageRetentionStores) ConfirmCommittedDispatchCursorDurable(ctx context.Context, id channel.ChannelID, cursorName string, minSeq uint64) (uint64, error) {
	if err := contextError(ctx); err != nil {
		return 0, err
	}
	if s.engine == nil {
		return 0, channel.ErrInvalidConfig
	}
	st := s.engine.ForChannel(channelhandler.KeyFromChannelID(id), id)
	return st.ConfirmCommittedDispatchCursorDurable(cursorName, minSeq)
}

// managerMessageRetentionNodeProvider adapts the app retention operator to node RPC.
type managerMessageRetentionNodeProvider struct {
	target *managerMessageRetentionOperator
}

func (p managerMessageRetentionNodeProvider) AdvanceChannelRetention(ctx context.Context, req accessnode.ChannelRetentionAdvanceRequest) (accessnode.ChannelRetentionAdvanceResult, error) {
	if p.target == nil {
		return accessnode.ChannelRetentionAdvanceResult{}, channel.ErrInvalidConfig
	}
	result, err := p.target.AdvanceMessageRetention(ctx, managementusecase.AdvanceMessageRetentionRequest{
		ChannelID:   req.ChannelID.ID,
		ChannelType: int64(req.ChannelID.Type),
		ThroughSeq:  req.ThroughSeq,
		DryRun:      req.DryRun,
	})
	if err != nil {
		return accessnode.ChannelRetentionAdvanceResult{}, err
	}
	return managerRetentionToAccessNode(result), nil
}

func managerRetentionToAccessNode(resp managementusecase.AdvanceMessageRetentionResponse) accessnode.ChannelRetentionAdvanceResult {
	return accessnode.ChannelRetentionAdvanceResult{
		ChannelID: channel.ChannelID{
			ID:   resp.ChannelID,
			Type: uint8(resp.ChannelType),
		},
		RequestedThroughSeq: resp.RequestedThroughSeq,
		AdvancedThroughSeq:  resp.AdvancedThroughSeq,
		MinAvailableSeq:     resp.MinAvailableSeq,
		Status:              string(resp.Status),
		BlockedReason:       string(resp.BlockedReason),
	}
}

func managerRetentionFromAccessNode(result accessnode.ChannelRetentionAdvanceResult) managementusecase.AdvanceMessageRetentionResponse {
	return managementusecase.AdvanceMessageRetentionResponse{
		ChannelID:           result.ChannelID.ID,
		ChannelType:         int64(result.ChannelID.Type),
		RequestedThroughSeq: result.RequestedThroughSeq,
		AdvancedThroughSeq:  result.AdvancedThroughSeq,
		MinAvailableSeq:     result.MinAvailableSeq,
		Status:              managementusecase.MessageRetentionStatus(result.Status),
		BlockedReason:       managementusecase.MessageRetentionBlockedReason(result.BlockedReason),
	}
}
