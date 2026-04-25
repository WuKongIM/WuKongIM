package delivery

import (
	"context"
	"sync"
	"time"
)

type shard struct {
	mu      sync.Mutex
	manager *Manager
	actors  map[ChannelKey]*actor
	wheel   *RetryWheel
}

func newShard(manager *Manager) *shard {
	return &shard{
		manager: manager,
		actors:  make(map[ChannelKey]*actor),
		wheel:   NewRetryWheel(),
	}
}

func (s *shard) submit(ctx context.Context, env CommittedEnvelope) error {
	s.mu.Lock()
	key := ChannelKey{ChannelID: env.ChannelID, ChannelType: env.ChannelType}
	act := s.actorFor(key)
	act.mu.Lock()
	s.mu.Unlock()
	defer act.mu.Unlock()
	return act.handleStartDispatch(ctx, env)
}

func (s *shard) routeAcked(ctx context.Context, binding AckBinding) error {
	s.mu.Lock()
	act := s.actors[ChannelKey{ChannelID: binding.ChannelID, ChannelType: binding.ChannelType}]
	s.mu.Unlock()
	if act == nil {
		return nil
	}
	act.mu.Lock()
	defer act.mu.Unlock()
	return act.handleRouteAck(ctx, RouteAcked{
		MessageID: binding.MessageID,
		Route:     binding.Route,
	})
}

func (s *shard) routeOffline(ctx context.Context, binding AckBinding) error {
	s.mu.Lock()
	act := s.actors[ChannelKey{ChannelID: binding.ChannelID, ChannelType: binding.ChannelType}]
	s.mu.Unlock()
	if act == nil {
		return nil
	}
	act.mu.Lock()
	defer act.mu.Unlock()
	return act.handleRouteOffline(ctx, RouteOffline{
		MessageID: binding.MessageID,
		Route:     binding.Route,
	})
}

func (s *shard) processRetryTicks(ctx context.Context) error {
	s.mu.Lock()
	due := s.wheel.PopDue(s.manager.clock.Now())
	actors := make([]*actor, 0, len(s.actors))
	for _, act := range s.actors {
		actors = append(actors, act)
	}
	s.mu.Unlock()
	for _, entry := range due {
		act := s.actor(ChannelKey{ChannelID: entry.ChannelID, ChannelType: entry.ChannelType})
		if act == nil {
			continue
		}
		act.mu.Lock()
		if err := act.handleRetryTick(ctx, RetryTick{Entry: entry}); err != nil {
			act.mu.Unlock()
			return err
		}
		act.mu.Unlock()
	}
	now := s.manager.clock.Now()
	for _, act := range actors {
		act.mu.Lock()
		if act.hasDueResolveRetry(now) {
			if err := act.resumeResolvable(ctx); err != nil {
				act.mu.Unlock()
				return err
			}
		}
		act.mu.Unlock()
	}
	return nil
}

func (s *shard) sweepIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	nowUnixNano := s.manager.clock.Now().UnixNano()
	for key, act := range s.actors {
		act.mu.Lock()
		if act.isIdle(nowUnixNano, s.manager.idleTimeout.Nanoseconds()) {
			act.mu.Unlock()
			delete(s.actors, key)
			continue
		}
		act.mu.Unlock()
	}
}

func (s *shard) actorFor(key ChannelKey) *actor {
	act := s.actors[key]
	if act != nil {
		return act
	}
	act = newActor(s, key)
	s.actors[key] = act
	return act
}

func (s *shard) actor(key ChannelKey) *actor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.actors[key]
}

func (s *shard) nextRetryDelay(attempt int) (time.Duration, bool) {
	if attempt <= 1 {
		return 0, false
	}
	if s.manager.maxRetryAttempts > 0 && attempt > s.manager.maxRetryAttempts {
		return 0, false
	}
	if len(s.manager.retryDelays) == 0 {
		return 0, false
	}
	return cappedBackoffWithJitter(s.manager.retryDelays, attempt), true
}
