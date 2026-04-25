package delivery

import (
	"context"
	"hash/fnv"
	"time"
)

const (
	defaultMaxInflightRoutesPerActor      = 4096
	defaultDedicatedLaneActivityThreshold = 8
)

type Manager struct {
	shards           []*shard
	resolver         Resolver
	push             Pusher
	clock            Clock
	ackIdx           *AckIndex
	resolvePageSize  int
	limits           Limits
	idleTimeout      time.Duration
	retryDelays      []time.Duration
	maxRetryAttempts int
}

func NewManager(cfg Config) *Manager {
	if cfg.ShardCount <= 0 {
		cfg.ShardCount = 1
	}
	if cfg.Clock == nil {
		cfg.Clock = defaultClock{}
	}
	if cfg.Resolver == nil {
		cfg.Resolver = noopResolver{}
	}
	if cfg.Push == nil {
		cfg.Push = noopPusher{}
	}
	if cfg.ResolvePageSize <= 0 {
		cfg.ResolvePageSize = 256
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = time.Minute
	}
	if len(cfg.RetryDelays) == 0 {
		cfg.RetryDelays = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
	}
	if cfg.MaxRetryAttempts <= 0 {
		cfg.MaxRetryAttempts = len(cfg.RetryDelays) + 1
	}
	if cfg.Limits.MaxInflightRoutesPerActor <= 0 {
		cfg.Limits.MaxInflightRoutesPerActor = defaultMaxInflightRoutesPerActor
	}
	if cfg.Limits.DedicatedLaneActivityThreshold <= 0 {
		cfg.Limits.DedicatedLaneActivityThreshold = defaultDedicatedLaneActivityThreshold
	}
	m := &Manager{
		resolver:         cfg.Resolver,
		push:             cfg.Push,
		clock:            cfg.Clock,
		ackIdx:           NewAckIndex(),
		resolvePageSize:  cfg.ResolvePageSize,
		limits:           cfg.Limits,
		idleTimeout:      cfg.IdleTimeout,
		retryDelays:      append([]time.Duration(nil), cfg.RetryDelays...),
		maxRetryAttempts: cfg.MaxRetryAttempts,
	}
	m.shards = make([]*shard, cfg.ShardCount)
	for i := range m.shards {
		m.shards[i] = newShard(m)
	}
	return m
}

func (m *Manager) Submit(ctx context.Context, env CommittedEnvelope) error {
	return m.shardFor(ChannelKey{ChannelID: env.ChannelID, ChannelType: env.ChannelType}).submit(ctx, env)
}

func (m *Manager) AckRoute(ctx context.Context, cmd RouteAck) error {
	binding, ok := m.ackIdx.Lookup(cmd.SessionID, cmd.MessageID)
	if !ok {
		return nil
	}
	return m.shardFor(ChannelKey{ChannelID: binding.ChannelID, ChannelType: binding.ChannelType}).routeAcked(ctx, binding)
}

func (m *Manager) SessionClosed(ctx context.Context, cmd SessionClosed) error {
	bindings := m.ackIdx.LookupSession(cmd.SessionID)
	for _, binding := range bindings {
		if err := m.shardFor(ChannelKey{ChannelID: binding.ChannelID, ChannelType: binding.ChannelType}).routeOffline(ctx, binding); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ProcessRetryTicks(ctx context.Context) error {
	for _, shard := range m.shards {
		if err := shard.processRetryTicks(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) SweepIdle() {
	for _, shard := range m.shards {
		shard.sweepIdle()
	}
}

func (m *Manager) shardFor(key ChannelKey) *shard {
	if len(m.shards) == 1 {
		return m.shards[0]
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key.ChannelID))
	_, _ = hasher.Write([]byte{key.ChannelType})
	return m.shards[hasher.Sum32()%uint32(len(m.shards))]
}

func (m *Manager) actorCount() int {
	total := 0
	for _, shard := range m.shards {
		total += len(shard.actors)
	}
	return total
}

func (m *Manager) HasAckBinding(sessionID, messageID uint64) bool {
	if m == nil {
		return false
	}
	_, ok := m.ackIdx.Lookup(sessionID, messageID)
	return ok
}

func (m *Manager) ActorLane(channelID string, channelType uint8) ActorLane {
	if m == nil {
		return LaneShared
	}
	act := m.shardFor(ChannelKey{ChannelID: channelID, ChannelType: channelType}).actor(ChannelKey{ChannelID: channelID, ChannelType: channelType})
	if act == nil {
		return LaneShared
	}
	act.mu.Lock()
	defer act.mu.Unlock()
	return act.lane
}
