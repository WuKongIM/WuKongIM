package channelmeta

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrLeaderTransferTargetNotReplica means the requested node is not in authoritative Replicas.
	ErrLeaderTransferTargetNotReplica = errors.New("channelmeta: leader transfer target is not a replica")
	// ErrLeaderTransferTargetNotISR means the requested node is not in authoritative ISR.
	ErrLeaderTransferTargetNotISR = errors.New("channelmeta: leader transfer target is not in isr")
	// ErrLeaderTransferInactiveChannel means explicit transfer requires an active channel.
	ErrLeaderTransferInactiveChannel = errors.New("channelmeta: leader transfer requires active channel")
)

// TransferIfSafe routes an explicit leader transfer to the current authoritative slot leader.
func (r *LeaderRepairer) TransferIfSafe(ctx context.Context, meta metadb.ChannelRuntimeMeta, targetNodeID uint64) (metadb.ChannelRuntimeMeta, bool, error) {
	if r == nil {
		return meta, false, nil
	}
	req := LeaderTransferRequest{
		ChannelID:            channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)},
		ObservedChannelEpoch: meta.ChannelEpoch,
		ObservedLeaderEpoch:  meta.LeaderEpoch,
		TargetNodeID:         targetNodeID,
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
		result, err := r.TransferChannelLeaderAuthoritative(ctx, req)
		if errors.Is(err, raftcluster.ErrNotLeader) {
			leaderID, err = r.cluster.LeaderOf(slotID)
			if err != nil {
				return metadb.ChannelRuntimeMeta{}, false, err
			}
			if r.cluster.IsLocal(leaderID) {
				result, err = r.TransferChannelLeaderAuthoritative(ctx, req)
			} else if r.remote != nil {
				result, err = r.remote.TransferChannelLeader(ctx, req)
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
	result, err := r.remote.TransferChannelLeader(ctx, req)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, false, err
	}
	return result.Meta, result.Changed, nil
}

// TransferChannelLeaderAuthoritative transfers channel leadership from the authoritative slot leader.
func (r *LeaderRepairer) TransferChannelLeaderAuthoritative(ctx context.Context, req LeaderTransferRequest) (LeaderTransferResult, error) {
	if r == nil || r.store == nil {
		return LeaderTransferResult{}, channel.ErrInvalidConfig
	}
	key := channelhandler.KeyFromChannelID(req.ChannelID)
	value, err, _ := r.sf.Do("transfer:"+string(key), func() (any, error) {
		latest, err := r.store.GetChannelRuntimeMeta(ctx, req.ChannelID.ID, int64(req.ChannelID.Type))
		if err != nil {
			return nil, err
		}
		if observedTransferEpochsStale(req, latest) {
			return LeaderTransferResult{Meta: latest, Changed: false}, nil
		}
		if err := validateLeaderTransferTarget(latest, req.TargetNodeID); err != nil {
			return nil, err
		}
		if latest.Leader == req.TargetNodeID {
			return LeaderTransferResult{Meta: latest, Changed: false}, nil
		}

		report, err := r.evaluateLeaderCandidate(ctx, req.TargetNodeID, latest, "")
		if err != nil {
			return nil, err
		}
		if report.ChannelEpoch != 0 && report.ChannelEpoch != latest.ChannelEpoch {
			return nil, channel.ErrNoSafeChannelLeader
		}
		if !report.CanLead {
			return nil, channel.ErrNoSafeChannelLeader
		}

		updated := latest
		updated.Leader = req.TargetNodeID
		updated.LeaderEpoch++
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
		return LeaderTransferResult{Meta: authoritative, Changed: true}, nil
	})
	if err != nil {
		return LeaderTransferResult{}, err
	}
	result, ok := value.(LeaderTransferResult)
	if !ok {
		return LeaderTransferResult{}, fmt.Errorf("channelmeta transfer: unexpected singleflight result %T", value)
	}
	return result, nil
}

func validateLeaderTransferTarget(meta metadb.ChannelRuntimeMeta, targetNodeID uint64) error {
	if meta.Status != uint8(channel.StatusActive) {
		return ErrLeaderTransferInactiveChannel
	}
	if targetNodeID == 0 || !containsUint64(meta.Replicas, targetNodeID) {
		return ErrLeaderTransferTargetNotReplica
	}
	if !containsUint64(meta.ISR, targetNodeID) {
		return ErrLeaderTransferTargetNotISR
	}
	return nil
}

func observedTransferEpochsStale(req LeaderTransferRequest, latest metadb.ChannelRuntimeMeta) bool {
	if req.ObservedChannelEpoch != 0 && req.ObservedChannelEpoch != latest.ChannelEpoch {
		return true
	}
	if req.ObservedLeaderEpoch != 0 && req.ObservedLeaderEpoch != latest.LeaderEpoch {
		return true
	}
	return false
}
