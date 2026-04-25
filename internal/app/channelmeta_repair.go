package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelreplica "github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"golang.org/x/sync/singleflight"
)

type channelMetaRepairer interface {
	RepairIfNeeded(ctx context.Context, meta metadb.ChannelRuntimeMeta, reason string) (metadb.ChannelRuntimeMeta, bool, error)
}

type channelRuntimeMetaStore interface {
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
	UpsertChannelRuntimeMetaIfLocalLeader(ctx context.Context, meta metadb.ChannelRuntimeMeta) error
}

type channelLeaderRepairCluster interface {
	SlotForKey(key string) multiraft.SlotID
	LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error)
	IsLocal(nodeID multiraft.NodeID) bool
}

type channelLeaderRepairRemote interface {
	RepairChannelLeader(ctx context.Context, req accessnode.ChannelLeaderRepairRequest) (accessnode.ChannelLeaderRepairResult, error)
	EvaluateChannelLeaderCandidate(ctx context.Context, nodeID uint64, req accessnode.ChannelLeaderEvaluateRequest) (accessnode.ChannelLeaderPromotionReport, error)
}

type channelLeaderLocalEvaluator interface {
	EvaluateChannelLeaderCandidate(ctx context.Context, req accessnode.ChannelLeaderEvaluateRequest) (accessnode.ChannelLeaderPromotionReport, error)
}

type channelLeaderRepairer struct {
	store              channelRuntimeMetaStore
	cluster            channelLeaderRepairCluster
	remote             channelLeaderRepairRemote
	evaluator          channelLeaderLocalEvaluator
	localNode          uint64
	now                func() time.Time
	applyAuthoritative func(metadb.ChannelRuntimeMeta) error
	needsRepair        func(meta metadb.ChannelRuntimeMeta) (bool, string)
	sf                 singleflight.Group
}

func (r *channelLeaderRepairer) RepairIfNeeded(ctx context.Context, meta metadb.ChannelRuntimeMeta, reason string) (metadb.ChannelRuntimeMeta, bool, error) {
	if r == nil {
		return meta, false, nil
	}
	req := accessnode.ChannelLeaderRepairRequest{
		ChannelID:            channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)},
		ObservedChannelEpoch: meta.ChannelEpoch,
		ObservedLeaderEpoch:  meta.LeaderEpoch,
		Reason:               reason,
	}
	if r.cluster == nil {
		return meta, false, channel.ErrInvalidConfig
	}
	slotID := r.cluster.SlotForKey(meta.ChannelID)
	leaderID, err := r.cluster.LeaderOf(slotID)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, false, err
	}
	if r.cluster.IsLocal(leaderID) {
		result, err := r.RepairChannelLeaderAuthoritative(ctx, req)
		if errors.Is(err, raftcluster.ErrNotLeader) {
			leaderID, err = r.cluster.LeaderOf(slotID)
			if err != nil {
				return metadb.ChannelRuntimeMeta{}, false, err
			}
			if r.cluster.IsLocal(leaderID) {
				result, err = r.RepairChannelLeaderAuthoritative(ctx, req)
			} else if r.remote != nil {
				result, err = r.remote.RepairChannelLeader(ctx, req)
			}
		}
		if err != nil {
			return metadb.ChannelRuntimeMeta{}, false, err
		}
		return result.Meta, result.Changed, nil
	}
	if r.remote == nil {
		return metadb.ChannelRuntimeMeta{}, false, channel.ErrInvalidConfig
	}
	result, err := r.remote.RepairChannelLeader(ctx, req)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, false, err
	}
	return result.Meta, result.Changed, nil
}

func (r *channelLeaderRepairer) RepairChannelLeaderAuthoritative(ctx context.Context, req accessnode.ChannelLeaderRepairRequest) (accessnode.ChannelLeaderRepairResult, error) {
	if r == nil || r.store == nil {
		return accessnode.ChannelLeaderRepairResult{}, channel.ErrInvalidConfig
	}
	key := channelhandler.KeyFromChannelID(req.ChannelID)
	value, err, _ := r.sf.Do(string(key), func() (any, error) {
		latest, err := r.store.GetChannelRuntimeMeta(ctx, req.ChannelID.ID, int64(req.ChannelID.Type))
		if err != nil {
			return nil, err
		}
		if observedRepairEpochsStale(req, latest) {
			return accessnode.ChannelLeaderRepairResult{
				Meta:    latest,
				Changed: false,
			}, nil
		}
		currentReason := req.Reason
		if r.needsRepair != nil {
			need, reason := r.needsRepair(latest)
			if !need {
				return accessnode.ChannelLeaderRepairResult{
					Meta:    latest,
					Changed: false,
				}, nil
			}
			if reason != "" {
				currentReason = reason
			}
		}
		if shouldRenewExpiredLeaderLease(latest, currentReason) {
			updated := latest
			updated.LeaseUntilMS = r.currentTime().Add(channelMetaBootstrapLease).UnixMilli()
			if err := r.store.UpsertChannelRuntimeMetaIfLocalLeader(ctx, updated); err != nil {
				return nil, err
			}
			authoritative, err := r.store.GetChannelRuntimeMeta(ctx, req.ChannelID.ID, int64(req.ChannelID.Type))
			if err != nil {
				return nil, err
			}
			if r.applyAuthoritative != nil {
				if err := r.applyAuthoritative(authoritative); err != nil {
					return nil, err
				}
			}
			return accessnode.ChannelLeaderRepairResult{
				Meta:    authoritative,
				Changed: true,
			}, nil
		}

		best, err := r.selectLeaderCandidate(ctx, latest, currentReason)
		if err != nil {
			return nil, err
		}
		if best.NodeID == 0 {
			return nil, channel.ErrNoSafeChannelLeader
		}

		updated := latest
		updated.Leader = best.NodeID
		if updated.Leader != latest.Leader {
			updated.LeaderEpoch++
		}
		updated.LeaseUntilMS = r.currentTime().Add(channelMetaBootstrapLease).UnixMilli()
		if err := r.store.UpsertChannelRuntimeMetaIfLocalLeader(ctx, updated); err != nil {
			return nil, err
		}
		authoritative, err := r.store.GetChannelRuntimeMeta(ctx, req.ChannelID.ID, int64(req.ChannelID.Type))
		if err != nil {
			return nil, err
		}
		if r.applyAuthoritative != nil {
			if err := r.applyAuthoritative(authoritative); err != nil {
				return nil, err
			}
		}
		return accessnode.ChannelLeaderRepairResult{
			Meta:    authoritative,
			Changed: true,
		}, nil
	})
	if err != nil {
		return accessnode.ChannelLeaderRepairResult{}, err
	}
	result, ok := value.(accessnode.ChannelLeaderRepairResult)
	if !ok {
		return accessnode.ChannelLeaderRepairResult{}, fmt.Errorf("channelmeta repair: unexpected singleflight result %T", value)
	}
	return result, nil
}

func shouldRenewExpiredLeaderLease(meta metadb.ChannelRuntimeMeta, reason string) bool {
	return reason == channel.LeaderRepairReasonLeaderLeaseExpired.String() &&
		meta.Leader != 0 &&
		containsUint64(meta.Replicas, meta.Leader)
}

func (r *channelLeaderRepairer) selectLeaderCandidate(ctx context.Context, meta metadb.ChannelRuntimeMeta, reason string) (accessnode.ChannelLeaderPromotionReport, error) {
	var best accessnode.ChannelLeaderPromotionReport
	var firstErr error
	preferCurrentLeader := reason == channel.LeaderRepairReasonLeaderLeaseExpired.String() && meta.Leader != 0
	if preferCurrentLeader {
		report, err := r.evaluateLeaderCandidate(ctx, meta.Leader, meta, reason)
		if err != nil {
			firstErr = err
		} else {
			if report.NodeID == 0 {
				report.NodeID = meta.Leader
			}
			if (report.ChannelEpoch == 0 || report.ChannelEpoch == meta.ChannelEpoch) && report.CanLead {
				return report, nil
			}
		}
	}
	for _, replicaID := range meta.ISR {
		if preferCurrentLeader && replicaID == meta.Leader {
			continue
		}
		if replicaID == 0 || r.shouldSkipRepairCandidate(meta, reason, replicaID) {
			continue
		}
		report, err := r.evaluateLeaderCandidate(ctx, replicaID, meta, reason)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if report.NodeID == 0 {
			report.NodeID = replicaID
		}
		if report.ChannelEpoch != 0 && report.ChannelEpoch != meta.ChannelEpoch {
			continue
		}
		if !report.CanLead {
			continue
		}
		if !best.CanLead || betterChannelLeaderReport(report, best) {
			best = report
		}
	}
	if best.CanLead {
		return best, nil
	}
	if firstErr != nil {
		return accessnode.ChannelLeaderPromotionReport{}, firstErr
	}
	return accessnode.ChannelLeaderPromotionReport{}, channel.ErrNoSafeChannelLeader
}

func (r *channelLeaderRepairer) shouldSkipRepairCandidate(meta metadb.ChannelRuntimeMeta, reason string, replicaID uint64) bool {
	switch reason {
	case channel.LeaderRepairReasonLeaderDead.String(),
		channel.LeaderRepairReasonLeaderDraining.String():
		return replicaID == meta.Leader
	default:
		return false
	}
}

func (r *channelLeaderRepairer) evaluateLeaderCandidate(ctx context.Context, nodeID uint64, meta metadb.ChannelRuntimeMeta, reason string) (accessnode.ChannelLeaderPromotionReport, error) {
	req := accessnode.ChannelLeaderEvaluateRequest{Meta: evaluationMetaForRepair(meta, reason)}
	if nodeID == r.localNode && r.evaluator != nil {
		return r.evaluator.EvaluateChannelLeaderCandidate(ctx, req)
	}
	if r.remote == nil {
		return accessnode.ChannelLeaderPromotionReport{}, channel.ErrInvalidConfig
	}
	return r.remote.EvaluateChannelLeaderCandidate(ctx, nodeID, req)
}

func (r *channelLeaderRepairer) currentTime() time.Time {
	if r != nil && r.now != nil {
		return r.now().UTC()
	}
	return time.Now().UTC()
}

func evaluationMetaForRepair(meta metadb.ChannelRuntimeMeta, reason string) metadb.ChannelRuntimeMeta {
	switch reason {
	case channel.LeaderRepairReasonLeaderDead.String(),
		channel.LeaderRepairReasonLeaderDraining.String():
	default:
		return meta
	}

	if meta.Leader == 0 || len(meta.ISR) == 0 {
		return meta
	}

	trimmedISR := make([]uint64, 0, len(meta.ISR))
	for _, replicaID := range meta.ISR {
		if replicaID == meta.Leader {
			continue
		}
		trimmedISR = append(trimmedISR, replicaID)
	}
	if len(trimmedISR) == 0 {
		return meta
	}

	trimmed := meta
	trimmed.ISR = trimmedISR
	return trimmed
}

func observedRepairEpochsStale(req accessnode.ChannelLeaderRepairRequest, latest metadb.ChannelRuntimeMeta) bool {
	if req.ObservedChannelEpoch != 0 && req.ObservedChannelEpoch != latest.ChannelEpoch {
		return true
	}
	if req.ObservedLeaderEpoch != 0 && req.ObservedLeaderEpoch != latest.LeaderEpoch {
		return true
	}
	return false
}

func betterChannelLeaderReport(left, right accessnode.ChannelLeaderPromotionReport) bool {
	switch {
	case left.ProjectedSafeHW != right.ProjectedSafeHW:
		return left.ProjectedSafeHW > right.ProjectedSafeHW
	case left.ProjectedTruncateTo != right.ProjectedTruncateTo:
		return left.ProjectedTruncateTo > right.ProjectedTruncateTo
	case left.CommitReadyNow != right.CommitReadyNow:
		return left.CommitReadyNow
	case left.LocalCheckpointHW != right.LocalCheckpointHW:
		return left.LocalCheckpointHW > right.LocalCheckpointHW
	default:
		return left.NodeID < right.NodeID
	}
}

type channelLeaderProbeClient interface {
	Probe(ctx context.Context, peer channel.NodeID, req channelruntime.ReconcileProbeRequestEnvelope) (channelruntime.ReconcileProbeResponseEnvelope, error)
}

type channelLeaderPromotionEvaluator struct {
	db        *channelstore.Engine
	localNode uint64
	probe     channelLeaderProbeClient
}

func (e *channelLeaderPromotionEvaluator) EvaluateChannelLeaderCandidate(ctx context.Context, req accessnode.ChannelLeaderEvaluateRequest) (accessnode.ChannelLeaderPromotionReport, error) {
	if e == nil || e.db == nil {
		return accessnode.ChannelLeaderPromotionReport{}, channel.ErrInvalidConfig
	}
	meta := projectChannelMeta(req.Meta)
	key := channelhandler.KeyFromChannelID(meta.ID)
	store := e.db.ForChannel(key, meta.ID)
	view, exists, err := loadDurableReplicaView(store)
	if err != nil {
		return accessnode.ChannelLeaderPromotionReport{}, err
	}
	proofs := e.collectPromotionProofs(ctx, meta)
	promotion, err := channelreplica.EvaluateLeaderPromotion(meta, view, proofs)
	if err != nil {
		return accessnode.ChannelLeaderPromotionReport{}, err
	}
	return accessnode.ChannelLeaderPromotionReport{
		NodeID:              e.localNode,
		Exists:              exists,
		ChannelEpoch:        req.Meta.ChannelEpoch,
		LocalLEO:            view.LEO,
		LocalCheckpointHW:   view.CheckpointHW,
		LocalOffsetEpoch:    view.OffsetEpoch,
		CommitReadyNow:      promotion.CommitReadyNow,
		ProjectedSafeHW:     promotion.ProjectedSafeHW,
		ProjectedTruncateTo: promotion.ProjectedTruncateTo,
		CanLead:             promotion.CanLead,
		Reason:              promotion.Reason,
	}, nil
}

func (e *channelLeaderPromotionEvaluator) collectPromotionProofs(ctx context.Context, meta channel.Meta) []channel.ReplicaReconcileProof {
	if e == nil || e.probe == nil {
		return nil
	}
	proofs := make([]channel.ReplicaReconcileProof, 0, len(meta.ISR))
	for _, replicaID := range meta.ISR {
		if replicaID == 0 || uint64(replicaID) == e.localNode {
			continue
		}
		req := channelruntime.ReconcileProbeRequestEnvelope{
			ChannelKey: meta.Key,
			Epoch:      meta.Epoch,
			Generation: 0,
			ReplicaID:  channel.NodeID(e.localNode),
		}
		resp, err := e.probe.Probe(ctx, replicaID, req)
		if err != nil {
			continue
		}
		if !validPromotionProbeResponse(req, resp) {
			continue
		}
		proofs = append(proofs, channel.ReplicaReconcileProof{
			ChannelKey:   meta.Key,
			Epoch:        meta.Epoch,
			ReplicaID:    replicaID,
			OffsetEpoch:  resp.OffsetEpoch,
			LogEndOffset: resp.LogEndOffset,
			CheckpointHW: resp.CheckpointHW,
		})
	}
	return proofs
}

// validPromotionProbeResponse accepts generation drift only for external
// leader-repair probes, which intentionally send Generation=0 and expect the
// replica to reply with its current runtime generation.
func validPromotionProbeResponse(req channelruntime.ReconcileProbeRequestEnvelope, resp channelruntime.ReconcileProbeResponseEnvelope) bool {
	if resp.ChannelKey != req.ChannelKey {
		return false
	}
	if resp.Epoch != req.Epoch {
		return false
	}
	if req.Generation != 0 && resp.Generation != req.Generation {
		return false
	}
	return true
}

func loadDurableReplicaView(store *channelstore.ChannelStore) (channelreplica.DurableReplicaView, bool, error) {
	if store == nil {
		return channelreplica.DurableReplicaView{}, false, channel.ErrInvalidConfig
	}
	checkpoint, err := store.LoadCheckpoint()
	switch {
	case errors.Is(err, channel.ErrEmptyState):
		checkpoint = channel.Checkpoint{}
	case err != nil:
		return channelreplica.DurableReplicaView{}, false, err
	}
	history, err := store.LoadHistory()
	switch {
	case errors.Is(err, channel.ErrEmptyState):
		history = nil
	case err != nil:
		return channelreplica.DurableReplicaView{}, false, err
	}
	leo := store.LEO()
	exists := leo > 0 || checkpoint != (channel.Checkpoint{}) || len(history) > 0
	view := channelreplica.DurableReplicaView{
		EpochHistory: append([]channel.EpochPoint(nil), history...),
		LEO:          leo,
		HW:           checkpoint.HW,
		CheckpointHW: checkpoint.HW,
		OffsetEpoch:  offsetEpochForRepair(history, leo),
	}
	return view, exists, nil
}

func offsetEpochForRepair(history []channel.EpochPoint, leo uint64) uint64 {
	var epoch uint64
	for _, point := range history {
		if point.StartOffset > leo {
			break
		}
		epoch = point.Epoch
	}
	return epoch
}

var (
	_ channelMetaRepairer               = (*channelLeaderRepairer)(nil)
	_ accessnode.ChannelLeaderRepairer  = (*channelLeaderRepairer)(nil)
	_ accessnode.ChannelLeaderEvaluator = (*channelLeaderPromotionEvaluator)(nil)
	_ channelLeaderProbeClient          = (*channeltransport.ProbeClient)(nil)
	_ channelLeaderRepairCluster        = (*raftcluster.Cluster)(nil)
)
