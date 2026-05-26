package service

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// Config wires the v0 channelv2 service facade.
type Config struct {
	LocalNode    ch.NodeID
	ReactorCount int
	MailboxSize  int
	Store        store.Factory
	Transport    transport.Client
	// MetaResolver lazily loads authoritative metadata for PullHint follower activation.
	MetaResolver ch.MetaResolver
	// ReplicationIdlePollInterval delays the next follower poll when a leader has no new records; defaults to 10ms.
	ReplicationIdlePollInterval time.Duration
	// ReplicationMinBackoff is the first retry delay after pull, apply, or ack failures; defaults to 1ms.
	ReplicationMinBackoff time.Duration
	// ReplicationMaxBackoff caps follower replication retry delays after repeated failures; defaults to 100ms.
	ReplicationMaxBackoff time.Duration
	// PullMaxBytes bounds one follower pull response requested from the leader; defaults to 64 KiB.
	PullMaxBytes int
	// LeaderRecentRecordCacheSize bounds recently appended leader log records kept for follower pulls; defaults to 10.
	LeaderRecentRecordCacheSize int
	// LeaderRecentRecordCacheBytes is a retained payload-byte soft cap for the per-channel leader log cache; the newest oversized record may exceed it.
	LeaderRecentRecordCacheBytes int
	// IdleSlowdownAfter is the idle duration after the last Append before follower pull intervals begin increasing.
	IdleSlowdownAfter time.Duration
	// IdleEvictAfter is the idle duration after the last Append before a leader may ask caught-up followers to stop.
	IdleEvictAfter time.Duration
	// IdlePullMinInterval is the shortest no-record follower pull delay returned by a leader.
	IdlePullMinInterval time.Duration
	// IdlePullMaxInterval is the longest parked follower pull delay returned by a leader.
	IdlePullMaxInterval time.Duration
	// IdleEvictCheckInterval is the retry interval for lifecycle checks while eviction is blocked.
	IdleEvictCheckInterval time.Duration
	// PullHintRetryInterval is the retry interval for best-effort PullHint while a follower still needs progress.
	PullHintRetryInterval time.Duration
	// AppendBatchMaxRecords is the queued record count that triggers a store append flush.
	AppendBatchMaxRecords int
	// AppendBatchMaxBytes is the queued payload byte budget that triggers a store append flush.
	AppendBatchMaxBytes int
	// AppendBatchMaxWait is the maximum age of the oldest queued append before flushing.
	AppendBatchMaxWait time.Duration
	// AppendQueueMaxRequests bounds accepted append requests waiting per channel.
	AppendQueueMaxRequests int
	// AppendQueueMaxBytes bounds accepted append payload bytes waiting per channel.
	AppendQueueMaxBytes int
	// AppendStoreRetryBackoff delays retry after the store append worker pool rejects a batch.
	AppendStoreRetryBackoff time.Duration
	// Observer receives lightweight reactor and worker metrics; nil uses a no-op observer.
	Observer reactor.Observer
}

type cluster struct {
	// group owns channel reactor partitions for this service facade.
	group *reactor.Group
	// metaResolver loads authoritative metadata before lazy follower activation.
	metaResolver ch.MetaResolver
	// localNode is this service node id for validating follower-only activation.
	localNode ch.NodeID
}

// New constructs a v0 channelv2 cluster facade.
func New(cfg Config) (ch.Cluster, error) {
	if cfg.LocalNode == 0 || cfg.Store == nil {
		return nil, ch.ErrInvalidConfig
	}
	group, err := reactor.NewGroup(reactor.Config{
		LocalNode: cfg.LocalNode, ReactorCount: cfg.ReactorCount, MailboxSize: cfg.MailboxSize, Store: cfg.Store, Transport: cfg.Transport,
		AppendBatchMaxRecords:        cfg.AppendBatchMaxRecords,
		AppendBatchMaxBytes:          cfg.AppendBatchMaxBytes,
		AppendBatchMaxWait:           cfg.AppendBatchMaxWait,
		AppendQueueMaxRequests:       cfg.AppendQueueMaxRequests,
		AppendQueueMaxBytes:          cfg.AppendQueueMaxBytes,
		AppendStoreRetryBackoff:      cfg.AppendStoreRetryBackoff,
		ReplicationIdlePollInterval:  cfg.ReplicationIdlePollInterval,
		ReplicationMinBackoff:        cfg.ReplicationMinBackoff,
		ReplicationMaxBackoff:        cfg.ReplicationMaxBackoff,
		PullMaxBytes:                 cfg.PullMaxBytes,
		LeaderRecentRecordCacheSize:  cfg.LeaderRecentRecordCacheSize,
		LeaderRecentRecordCacheBytes: cfg.LeaderRecentRecordCacheBytes,
		IdleSlowdownAfter:            cfg.IdleSlowdownAfter,
		IdleEvictAfter:               cfg.IdleEvictAfter,
		IdlePullMinInterval:          cfg.IdlePullMinInterval,
		IdlePullMaxInterval:          cfg.IdlePullMaxInterval,
		IdleEvictCheckInterval:       cfg.IdleEvictCheckInterval,
		PullHintRetryInterval:        cfg.PullHintRetryInterval,
		Observer:                     cfg.Observer,
	})
	if err != nil {
		return nil, err
	}
	return &cluster{group: group, metaResolver: cfg.MetaResolver, localNode: cfg.LocalNode}, nil
}

func (c *cluster) Tick(ctx context.Context) error {
	return c.group.Tick(ctx)
}

func (c *cluster) Close() error {
	if c == nil || c.group == nil {
		return nil
	}
	return c.group.Close()
}
