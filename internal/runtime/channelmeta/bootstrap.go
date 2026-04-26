package channelmeta

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

// BootstrapLease is the leader lease duration assigned to bootstrapped or renewed channel metadata.
const BootstrapLease = 30 * time.Second

// BootstrapOptions configures the channel runtime metadata bootstrapper.
type BootstrapOptions struct {
	// Cluster provides slot topology and current slot leader observations.
	Cluster BootstrapCluster
	// Store reads and writes authoritative channel runtime metadata.
	Store BootstrapStore
	// DefaultMinISR is the configured MinISR used for new channel metadata.
	DefaultMinISR int
	// Now returns the current time used to derive leader leases.
	Now func() time.Time
	// Logger records bootstrap events and failures.
	Logger wklog.Logger
}

// RuntimeBootstrapper creates and renews authoritative channel runtime metadata.
type RuntimeBootstrapper struct {
	cluster       BootstrapCluster
	store         BootstrapStore
	defaultMinISR int
	now           func() time.Time
	logger        wklog.Logger
}

var _ Bootstrapper = (*RuntimeBootstrapper)(nil)

// Keep the historical app event prefix while this runtime primitive is migrated
// behind existing dashboards and log queries.
const bootstrapEventPrefix = "app.channelmeta.bootstrap."

// NewBootstrapper constructs a channel runtime metadata bootstrapper.
func NewBootstrapper(opts BootstrapOptions) *RuntimeBootstrapper {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = wklog.NewNop()
	}
	defaultMinISR := opts.DefaultMinISR
	if defaultMinISR <= 0 {
		defaultMinISR = 2
	}
	return &RuntimeBootstrapper{
		cluster:       opts.Cluster,
		store:         opts.Store,
		defaultMinISR: defaultMinISR,
		now:           now,
		logger:        logger.Named("channelmeta.bootstrap"),
	}
}

// EnsureChannelRuntimeMeta creates missing authoritative runtime metadata from slot topology.
func (b *RuntimeBootstrapper) EnsureChannelRuntimeMeta(ctx context.Context, id channel.ChannelID) (metadb.ChannelRuntimeMeta, bool, error) {
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
		wklog.Event(bootstrapEventPrefix+"missing"),
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
		LeaseUntilMS: b.now().UTC().Add(BootstrapLease).UnixMilli(),
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
		wklog.Event(bootstrapEventPrefix+"bootstrapped"),
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

// RenewChannelLeaderLease refreshes only the current channel leader lease.
func (b *RuntimeBootstrapper) RenewChannelLeaderLease(ctx context.Context, meta metadb.ChannelRuntimeMeta, localNode uint64, renewBefore time.Duration) (metadb.ChannelRuntimeMeta, bool, error) {
	if b == nil || b.store == nil {
		return meta, false, nil
	}
	if meta.Status != uint8(channel.StatusActive) {
		return meta, false, nil
	}
	if meta.Leader != localNode {
		return meta, false, nil
	}

	now := b.now().UTC()
	if !MetaLeaseNeedsRenewal(meta.LeaseUntilMS, now, renewBefore) {
		return meta, false, nil
	}
	candidate := meta
	candidate.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	if err := b.store.UpsertChannelRuntimeMeta(ctx, candidate); err != nil {
		return metadb.ChannelRuntimeMeta{}, false, fmt.Errorf("channelmeta lifecycle: upsert runtime metadata: %w", err)
	}

	authoritative, err := b.store.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, false, fmt.Errorf("channelmeta lifecycle: reread runtime metadata: %w", err)
	}
	return authoritative, true, nil
}

// MetaLeaseNeedsRenewal reports whether a leader lease is expired or within the renewal lead time.
func MetaLeaseNeedsRenewal(leaseUntilMS int64, now time.Time, renewBefore time.Duration) bool {
	if leaseUntilMS <= 0 {
		return true
	}
	leaseUntil := time.UnixMilli(leaseUntilMS).UTC()
	deadline := now.UTC().Add(renewBefore)
	return !leaseUntil.After(deadline)
}

func (b *RuntimeBootstrapper) logBootstrapFailure(id channel.ChannelID, slotID multiraft.SlotID, leader uint64, replicaCount int, minISR int64, err error) {
	if b == nil || b.logger == nil {
		return
	}
	fields := []wklog.Field{
		wklog.Event(bootstrapEventPrefix + "failed"),
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
