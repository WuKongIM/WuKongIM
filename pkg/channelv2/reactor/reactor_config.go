package reactor

import "time"

func defaultReactorConfig(cfg ReactorConfig) ReactorConfig {
	if cfg.MaxChannels > 0 {
		cfg.MaxChannelsEnabled = true
	}
	if cfg.MailboxSize <= 0 {
		cfg.MailboxSize = 1024
	}
	if cfg.AppendBatchMaxRecords <= 0 {
		cfg.AppendBatchMaxRecords = 128
	}
	if cfg.AppendBatchMaxBytes <= 0 {
		cfg.AppendBatchMaxBytes = 256 * 1024
	}
	if cfg.AppendBatchMaxWait <= 0 {
		cfg.AppendBatchMaxWait = time.Millisecond
	}
	if cfg.AppendQueueMaxRequests <= 0 {
		cfg.AppendQueueMaxRequests = max(cfg.MailboxSize, 1024)
	}
	if cfg.AppendQueueMaxBytes <= 0 {
		cfg.AppendQueueMaxBytes = 4 * 1024 * 1024
	}
	if cfg.AppendStoreRetryBackoff <= 0 {
		cfg.AppendStoreRetryBackoff = time.Millisecond
	}
	if cfg.ReplicationIdlePollInterval <= 0 {
		cfg.ReplicationIdlePollInterval = defaultReplicationIdlePollInterval
	}
	if cfg.ReplicationMinBackoff <= 0 {
		cfg.ReplicationMinBackoff = time.Millisecond
	}
	if cfg.ReplicationMaxBackoff <= 0 {
		cfg.ReplicationMaxBackoff = 100 * time.Millisecond
	}
	if cfg.ReplicationMaxBackoff < cfg.ReplicationMinBackoff {
		cfg.ReplicationMaxBackoff = cfg.ReplicationMinBackoff
	}
	if cfg.PullMaxBytes <= 0 {
		cfg.PullMaxBytes = 64 * 1024
	}
	if cfg.LeaderRecentRecordCacheSize == 0 {
		cfg.LeaderRecentRecordCacheSize = 128
	}
	if cfg.LeaderRecentRecordCacheSize < 0 {
		cfg.LeaderRecentRecordCacheBytes = 0
	} else if cfg.LeaderRecentRecordCacheBytes <= 0 {
		cfg.LeaderRecentRecordCacheBytes = min(cfg.PullMaxBytes, 256*1024)
	}
	if cfg.IdleSlowdownAfter <= 0 {
		cfg.IdleSlowdownAfter = 30 * time.Second
	}
	if cfg.IdleEvictAfter <= 0 {
		cfg.IdleEvictAfter = 5 * time.Minute
	}
	if cfg.IdlePullMinInterval <= 0 {
		cfg.IdlePullMinInterval = cfg.ReplicationIdlePollInterval
	}
	if cfg.IdlePullMaxInterval <= 0 {
		cfg.IdlePullMaxInterval = 5 * time.Second
	}
	if cfg.IdleEvictCheckInterval <= 0 {
		cfg.IdleEvictCheckInterval = time.Second
	}
	if cfg.PullHintRetryInterval <= 0 {
		cfg.PullHintRetryInterval = time.Second
	}
	if cfg.FollowerRecoveryProbeInterval < 0 {
		cfg.FollowerRecoveryProbeInterval = 0
	}
	if cfg.FollowerRecoveryProbeInterval == 0 {
		cfg.FollowerRecoveryProbeInterval = defaultFollowerRecoveryProbeInterval
	}
	if cfg.FollowerRecoveryProbeJitter < 0 {
		cfg.FollowerRecoveryProbeJitter = 0
	}
	if cfg.FollowerRecoveryProbeJitter == 0 {
		cfg.FollowerRecoveryProbeJitter = defaultFollowerRecoveryProbeJitter
	}
	cfg.Observer = defaultObserver(cfg.Observer)
	return cfg
}
