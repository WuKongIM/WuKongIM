package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const channelMetaBootstrapLease = 30 * time.Second

type channelMetaBootstrapCluster interface {
	SlotForKey(key string) multiraft.SlotID
	PeersForSlot(slotID multiraft.SlotID) []multiraft.NodeID
	LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error)
}

type channelMetaBootstrapStore interface {
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
	UpsertChannelRuntimeMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) error
}

type channelMetaBootstrapper struct {
	cluster       channelMetaBootstrapCluster
	store         channelMetaBootstrapStore
	defaultMinISR int
	now           func() time.Time
	logger        wklog.Logger
}

func newChannelMetaBootstrapper(cluster channelMetaBootstrapCluster, store channelMetaBootstrapStore, defaultMinISR int, now func() time.Time, logger wklog.Logger) *channelMetaBootstrapper {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = wklog.NewNop()
	}
	if defaultMinISR <= 0 {
		defaultMinISR = 2
	}
	return &channelMetaBootstrapper{
		cluster:       cluster,
		store:         store,
		defaultMinISR: defaultMinISR,
		now:           now,
		logger:        logger.Named("channelmeta.bootstrap"),
	}
}

func (b *channelMetaBootstrapper) EnsureChannelRuntimeMeta(ctx context.Context, id channel.ChannelID) (metadb.ChannelRuntimeMeta, bool, error) {
	if b == nil || b.store == nil {
		return metadb.ChannelRuntimeMeta{}, false, fmt.Errorf("channelmeta bootstrap: store is nil")
	}
	existing, err := b.store.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, metadb.ErrNotFound) {
		return metadb.ChannelRuntimeMeta{}, false, err
	}
	if b.cluster == nil {
		return metadb.ChannelRuntimeMeta{}, false, fmt.Errorf("channelmeta bootstrap: cluster is nil")
	}

	slotID := b.cluster.SlotForKey(id.ID)
	replicas := projectBootstrapReplicaIDs(b.cluster.PeersForSlot(slotID))
	minISR := int64(clampBootstrapMinISR(b.defaultMinISR, len(replicas)))
	b.logger.Info("missing runtime metadata; bootstrapping",
		wklog.Event("app.channelmeta.bootstrap.missing"),
		wklog.ChannelID(id.ID),
		wklog.ChannelType(int64(id.Type)),
		wklog.SlotID(uint64(slotID)),
		wklog.Uint64("leader", 0),
		wklog.Int("replicaCount", len(replicas)),
		wklog.Int64("minISR", minISR),
		wklog.Bool("created", false),
	)

	if len(replicas) == 0 {
		bootstrapErr := fmt.Errorf("channelmeta bootstrap: slot peers empty for slot %d", slotID)
		b.logBootstrapFailure(id, slotID, 0, 0, 0, bootstrapErr)
		return metadb.ChannelRuntimeMeta{}, false, bootstrapErr
	}

	leader, err := b.cluster.LeaderOf(slotID)
	if err != nil {
		if errors.Is(err, raftcluster.ErrNoLeader) || errors.Is(err, raftcluster.ErrSlotNotFound) {
			b.logBootstrapFailure(id, slotID, 0, len(replicas), minISR, err)
			return metadb.ChannelRuntimeMeta{}, false, err
		}
		wrappedErr := fmt.Errorf("channelmeta bootstrap: resolve leader for slot %d: %w", slotID, err)
		b.logBootstrapFailure(id, slotID, 0, len(replicas), minISR, wrappedErr)
		return metadb.ChannelRuntimeMeta{}, false, wrappedErr
	}

	candidate := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 1,
		LeaderEpoch:  1,
		Replicas:     replicas,
		ISR:          append([]uint64(nil), replicas...),
		Leader:       uint64(leader),
		MinISR:       minISR,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: b.now().UTC().Add(channelMetaBootstrapLease).UnixMilli(),
	}

	if err := b.store.UpsertChannelRuntimeMeta(ctx, candidate); err != nil {
		wrappedErr := fmt.Errorf("channelmeta bootstrap: upsert runtime metadata: %w", err)
		b.logBootstrapFailure(id, slotID, candidate.Leader, len(candidate.Replicas), candidate.MinISR, wrappedErr)
		return metadb.ChannelRuntimeMeta{}, false, wrappedErr
	}

	authoritative, err := b.store.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		wrappedErr := fmt.Errorf("channelmeta bootstrap: reread runtime metadata: %w", err)
		b.logBootstrapFailure(id, slotID, candidate.Leader, len(candidate.Replicas), candidate.MinISR, wrappedErr)
		return metadb.ChannelRuntimeMeta{}, false, wrappedErr
	}

	b.logger.Info("bootstrapped runtime metadata",
		wklog.Event("app.channelmeta.bootstrap.bootstrapped"),
		wklog.ChannelID(id.ID),
		wklog.ChannelType(int64(id.Type)),
		wklog.SlotID(uint64(slotID)),
		wklog.Uint64("leader", authoritative.Leader),
		wklog.Int("replicaCount", len(authoritative.Replicas)),
		wklog.Int64("minISR", authoritative.MinISR),
		wklog.Bool("created", true),
	)
	return authoritative, true, nil
}

func (b *channelMetaBootstrapper) logBootstrapFailure(id channel.ChannelID, slotID multiraft.SlotID, leader uint64, replicaCount int, minISR int64, err error) {
	if b == nil || b.logger == nil {
		return
	}
	fields := []wklog.Field{
		wklog.Event("app.channelmeta.bootstrap.failed"),
		wklog.ChannelID(id.ID),
		wklog.ChannelType(int64(id.Type)),
		wklog.SlotID(uint64(slotID)),
		wklog.Uint64("leader", leader),
		wklog.Int("replicaCount", replicaCount),
		wklog.Int64("minISR", minISR),
		wklog.Error(err),
	}
	b.logger.Error("failed to bootstrap runtime metadata", fields...)
}

func projectBootstrapReplicaIDs(ids []multiraft.NodeID) []uint64 {
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		out = append(out, uint64(id))
	}
	return out
}

func clampBootstrapMinISR(configured int, replicaCount int) int {
	if replicaCount <= 0 {
		return 0
	}
	if configured <= 0 || configured > replicaCount {
		return replicaCount
	}
	return configured
}
