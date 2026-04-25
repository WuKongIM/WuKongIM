package app

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

// RenewChannelLeaderLease refreshes only the current channel leader lease.
func (b *channelMetaBootstrapper) RenewChannelLeaderLease(ctx context.Context, meta metadb.ChannelRuntimeMeta, localNode uint64, renewBefore time.Duration) (metadb.ChannelRuntimeMeta, bool, error) {
	if b == nil || b.store == nil || b.cluster == nil {
		return meta, false, nil
	}
	if meta.Status != uint8(channel.StatusActive) {
		return meta, false, nil
	}
	if meta.Leader != localNode {
		return meta, false, nil
	}

	now := b.now().UTC()
	if !runtimeMetaLeaseNeedsRenewal(meta.LeaseUntilMS, now, renewBefore) {
		return meta, false, nil
	}
	candidate := meta
	candidate.LeaseUntilMS = now.Add(channelMetaBootstrapLease).UnixMilli()
	if err := b.store.UpsertChannelRuntimeMeta(ctx, candidate); err != nil {
		return metadb.ChannelRuntimeMeta{}, false, fmt.Errorf("channelmeta lifecycle: upsert runtime metadata: %w", err)
	}

	authoritative, err := b.store.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, false, fmt.Errorf("channelmeta lifecycle: reread runtime metadata: %w", err)
	}
	return authoritative, true, nil
}

func runtimeMetaLeaseNeedsRenewal(leaseUntilMS int64, now time.Time, renewBefore time.Duration) bool {
	if leaseUntilMS <= 0 {
		return true
	}
	leaseUntil := time.UnixMilli(leaseUntilMS).UTC()
	deadline := now.UTC().Add(renewBefore)
	return !leaseUntil.After(deadline)
}
