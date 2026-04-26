package channelmeta

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelreplica "github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"golang.org/x/sync/singleflight"
)

// LeaderRepairerOptions wires the neutral dependencies required to repair channel leader metadata.
type LeaderRepairerOptions struct {
	// Store reads and conditionally writes authoritative channel runtime metadata.
	Store RepairStore
	// Cluster routes repair work to the authoritative slot leader.
	Cluster RepairCluster
	// Remote sends repair and promotion-evaluation requests to peer nodes.
	Remote RepairRemote
	// Evaluator evaluates the local node as a channel leader candidate.
	Evaluator LocalEvaluator
	// LocalNode is the numeric ID of this node in the cluster.
	LocalNode uint64
	// Now returns the current time used for leader lease renewal.
	Now func() time.Time
	// ApplyAuthoritative applies freshly repaired authoritative metadata to local runtime state.
	ApplyAuthoritative func(metadb.ChannelRuntimeMeta) error
	// RepairPolicy rechecks whether the latest authoritative metadata still needs repair.
	RepairPolicy RepairPolicy
}

// LeaderRepairer repairs stale authoritative channel leader metadata through neutral runtime ports.
type LeaderRepairer struct {
	store              RepairStore
	cluster            RepairCluster
	remote             RepairRemote
	evaluator          LocalEvaluator
	localNode          uint64
	now                func() time.Time
	applyAuthoritative func(metadb.ChannelRuntimeMeta) error
	needsRepair        RepairPolicy
	sf                 singleflight.Group
}

// NewLeaderRepairer creates a channel leader repairer from neutral runtime dependencies.
func NewLeaderRepairer(opts LeaderRepairerOptions) *LeaderRepairer {
	return &LeaderRepairer{
		store:              opts.Store,
		cluster:            opts.Cluster,
		remote:             opts.Remote,
		evaluator:          opts.Evaluator,
		localNode:          opts.LocalNode,
		now:                opts.Now,
		applyAuthoritative: opts.ApplyAuthoritative,
		needsRepair:        opts.RepairPolicy,
	}
}

// RepairIfNeeded routes a repair request to the current authoritative slot leader.
func (r *LeaderRepairer) RepairIfNeeded(ctx context.Context, meta metadb.ChannelRuntimeMeta, reason string) (metadb.ChannelRuntimeMeta, bool, error) {
	if r == nil {
		return meta, false, nil
	}
	req := LeaderRepairRequest{
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

// RepairChannelLeaderAuthoritative repairs leader metadata on the authoritative slot leader.
func (r *LeaderRepairer) RepairChannelLeaderAuthoritative(ctx context.Context, req LeaderRepairRequest) (LeaderRepairResult, error) {
	if r == nil || r.store == nil {
		return LeaderRepairResult{}, channel.ErrInvalidConfig
	}
	key := channelhandler.KeyFromChannelID(req.ChannelID)
	value, err, _ := r.sf.Do(string(key), func() (any, error) {
		latest, err := r.store.GetChannelRuntimeMeta(ctx, req.ChannelID.ID, int64(req.ChannelID.Type))
		if err != nil {
			return nil, err
		}
		if observedRepairEpochsStale(req, latest) {
			return LeaderRepairResult{
				Meta:    latest,
				Changed: false,
			}, nil
		}
		currentReason := req.Reason
		if r.needsRepair != nil {
			need, reason := r.needsRepair(latest)
			if !need {
				return LeaderRepairResult{
					Meta:    latest,
					Changed: false,
				}, nil
			}
			if reason != "" {
				currentReason = reason
			}
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
		updated.LeaseUntilMS = r.currentTime().Add(BootstrapLease).UnixMilli()
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
		return LeaderRepairResult{
			Meta:    authoritative,
			Changed: true,
		}, nil
	})
	if err != nil {
		return LeaderRepairResult{}, err
	}
	result, ok := value.(LeaderRepairResult)
	if !ok {
		return LeaderRepairResult{}, fmt.Errorf("channelmeta repair: unexpected singleflight result %T", value)
	}
	return result, nil
}

func (r *LeaderRepairer) selectLeaderCandidate(ctx context.Context, meta metadb.ChannelRuntimeMeta, reason string) (LeaderPromotionReport, error) {
	var best LeaderPromotionReport
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
		return LeaderPromotionReport{}, firstErr
	}
	return LeaderPromotionReport{}, channel.ErrNoSafeChannelLeader
}

func (r *LeaderRepairer) shouldSkipRepairCandidate(meta metadb.ChannelRuntimeMeta, reason string, replicaID uint64) bool {
	switch reason {
	case channel.LeaderRepairReasonLeaderDead.String(),
		channel.LeaderRepairReasonLeaderDraining.String():
		return replicaID == meta.Leader
	default:
		return false
	}
}

func (r *LeaderRepairer) evaluateLeaderCandidate(ctx context.Context, nodeID uint64, meta metadb.ChannelRuntimeMeta, reason string) (LeaderPromotionReport, error) {
	req := LeaderEvaluateRequest{Meta: evaluationMetaForCandidate(meta, reason, nodeID)}
	if nodeID == r.localNode && r.evaluator != nil {
		return r.evaluator.EvaluateChannelLeaderCandidate(ctx, req)
	}
	if r.remote == nil {
		return LeaderPromotionReport{}, channel.ErrInvalidConfig
	}
	return r.remote.EvaluateChannelLeaderCandidate(ctx, nodeID, req)
}

func (r *LeaderRepairer) currentTime() time.Time {
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

func evaluationMetaForCandidate(meta metadb.ChannelRuntimeMeta, reason string, candidate uint64) metadb.ChannelRuntimeMeta {
	projected := evaluationMetaForRepair(meta, reason)
	if candidate == 0 {
		return projected
	}
	if !containsUint64(projected.Replicas, candidate) || !containsUint64(projected.ISR, candidate) {
		return projected
	}
	projected.Leader = candidate
	return projected
}

func observedRepairEpochsStale(req LeaderRepairRequest, latest metadb.ChannelRuntimeMeta) bool {
	if req.ObservedChannelEpoch != 0 && req.ObservedChannelEpoch != latest.ChannelEpoch {
		return true
	}
	if req.ObservedLeaderEpoch != 0 && req.ObservedLeaderEpoch != latest.LeaderEpoch {
		return true
	}
	return false
}

func betterChannelLeaderReport(left, right LeaderPromotionReport) bool {
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

// LeaderPromotionProbeClient probes peer replicas for durable reconcile information.
type LeaderPromotionProbeClient interface {
	// Probe requests a reconcile snapshot from a peer replica.
	Probe(ctx context.Context, peer channel.NodeID, req channelruntime.ReconcileProbeRequestEnvelope) (channelruntime.ReconcileProbeResponseEnvelope, error)
}

// LeaderPromotionEvaluatorOptions wires local durable state dependencies for promotion evaluation.
type LeaderPromotionEvaluatorOptions struct {
	// DB provides access to the local durable channel log store.
	DB *channelstore.Engine
	// LocalNode is this node's cluster ID.
	LocalNode uint64
	// Probe reads durable reconcile proofs from peer replicas.
	Probe LeaderPromotionProbeClient
}

// LeaderPromotionEvaluator evaluates whether local durable channel state can safely become leader.
type LeaderPromotionEvaluator struct {
	db        *channelstore.Engine
	localNode uint64
	probe     LeaderPromotionProbeClient
}

// NewLeaderPromotionEvaluator creates a local candidate evaluator from durable log dependencies.
func NewLeaderPromotionEvaluator(opts LeaderPromotionEvaluatorOptions) *LeaderPromotionEvaluator {
	return &LeaderPromotionEvaluator{
		db:        opts.DB,
		localNode: opts.LocalNode,
		probe:     opts.Probe,
	}
}

// EvaluateChannelLeaderCandidate dry-runs local channel leader promotion.
func (e *LeaderPromotionEvaluator) EvaluateChannelLeaderCandidate(ctx context.Context, req LeaderEvaluateRequest) (LeaderPromotionReport, error) {
	if e == nil || e.db == nil {
		return LeaderPromotionReport{}, channel.ErrInvalidConfig
	}
	meta := ProjectChannelMeta(req.Meta)
	key := channelhandler.KeyFromChannelID(meta.ID)
	store := e.db.ForChannel(key, meta.ID)
	view, exists, err := loadDurableReplicaView(store)
	if err != nil {
		return LeaderPromotionReport{}, err
	}
	proofs := e.collectPromotionProofs(ctx, meta)
	promotion, err := channelreplica.EvaluateLeaderPromotion(meta, view, proofs)
	if err != nil {
		return LeaderPromotionReport{}, err
	}
	return LeaderPromotionReport{
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

func (e *LeaderPromotionEvaluator) collectPromotionProofs(ctx context.Context, meta channel.Meta) []channel.ReplicaReconcileProof {
	if e == nil || e.probe == nil {
		return nil
	}
	proofs := make([]channel.ReplicaReconcileProof, 0, len(meta.ISR))
	for _, replicaID := range meta.ISR {
		if replicaID == 0 || uint64(replicaID) == e.localNode {
			continue
		}
		req := channelruntime.ReconcileProbeRequestEnvelope{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Generation:  0,
			ReplicaID:   channel.NodeID(e.localNode),
		}
		resp, err := e.probe.Probe(ctx, replicaID, req)
		if err != nil {
			continue
		}
		if !validPromotionProbeResponse(req, replicaID, resp) {
			continue
		}
		proofs = append(proofs, channel.ReplicaReconcileProof{
			ChannelKey:   meta.Key,
			Epoch:        meta.Epoch,
			LeaderEpoch:  meta.LeaderEpoch,
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
func validPromotionProbeResponse(req channelruntime.ReconcileProbeRequestEnvelope, expectedReplicaID channel.NodeID, resp channelruntime.ReconcileProbeResponseEnvelope) bool {
	if resp.ChannelKey != req.ChannelKey {
		return false
	}
	if resp.Epoch != req.Epoch {
		return false
	}
	if resp.LeaderEpoch != req.LeaderEpoch {
		return false
	}
	if req.Generation != 0 && resp.Generation != req.Generation {
		return false
	}
	if resp.ReplicaID != expectedReplicaID {
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
		EpochHistory:   append([]channel.EpochPoint(nil), history...),
		LogStartOffset: checkpoint.LogStartOffset,
		LEO:            leo,
		HW:             checkpoint.HW,
		CheckpointHW:   checkpoint.HW,
		OffsetEpoch:    offsetEpochForRepair(history, leo),
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
	_ Repairer                   = (*LeaderRepairer)(nil)
	_ AuthoritativeRepairer      = (*LeaderRepairer)(nil)
	_ LocalEvaluator             = (*LeaderPromotionEvaluator)(nil)
	_ RepairCluster              = (*raftcluster.Cluster)(nil)
	_ LeaderPromotionProbeClient = (*channeltransport.ProbeClient)(nil)
)
