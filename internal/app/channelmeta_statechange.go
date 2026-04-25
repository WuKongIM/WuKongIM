package app

import (
	"context"

	runtimechannelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const slotLeaderRefreshTimeout = runtimechannelmeta.SlotLeaderRefreshTimeout

// scheduleSlotLeaderRefresh refreshes active local channel metas in the slot
// after the authoritative slot leader changes.
func (s *channelMetaSync) scheduleSlotLeaderRefresh(slotID multiraft.SlotID) {
	if s == nil {
		return
	}
	s.mu.Lock()
	runCtx := s.runCtx
	s.mu.Unlock()
	if runCtx == nil {
		return
	}
	s.slotRefresh.Schedule(runCtx, &s.refreshWG, slotID, s.snapshotAppliedLocalKeysForSlot, func(ctx context.Context, key channel.ChannelKey) {
		_, _ = s.refreshAuthoritativeByKey(ctx, key)
	}, slotLeaderRefreshTimeout)
}

func (s *channelMetaSync) snapshotAppliedLocalKeysForSlot(slotID multiraft.SlotID) []channel.ChannelKey {
	if s == nil || s.bootstrap == nil || s.bootstrap.cluster == nil {
		return nil
	}
	applied := s.snapshotAppliedLocal()
	if len(applied) == 0 {
		return nil
	}
	keys := make([]channel.ChannelKey, 0, len(applied))
	for key := range applied {
		id, err := channelhandler.ParseChannelKey(key)
		if err != nil {
			continue
		}
		if s.bootstrap.cluster.SlotForKey(id.ID) != slotID {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

func (s *channelMetaSync) refreshAuthoritativeByKey(ctx context.Context, key channel.ChannelKey) (channel.Meta, error) {
	if s == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	s.observeHashSlotTableVersion()
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return channel.Meta{}, err
	}
	return s.cache.runSingleflight(key, func() (channel.Meta, error) {
		meta, err := s.source.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		if err != nil {
			s.cache.storeNegative(key, err, s.now())
			return channel.Meta{}, err
		}
		meta, err = s.reconcileChannelRuntimeMeta(ctx, meta)
		if err != nil {
			return channel.Meta{}, err
		}
		applied, err := s.applyAuthoritativeMeta(meta)
		if err != nil {
			return channel.Meta{}, err
		}
		s.cache.storePositive(key, applied, s.now())
		return applied, nil
	})
}

// observeLocalReplicaStateChange marks the owning slot dirty when runtime state
// shows the local authoritative leader has drifted from the active replica view.
func (s *channelMetaSync) observeLocalReplicaStateChange(key channel.ChannelKey) {
	if s == nil || s.localRuntime == nil {
		return
	}
	runtimechannelmeta.ObserveLocalReplicaStateChange(runtimechannelmeta.LocalReplicaStateChange{
		Key:     key,
		Runtime: channelMetaLocalRuntime{runtime: s.localRuntime},
		Track: func(key channel.ChannelKey) {
			s.mu.Lock()
			s.trackAppliedLocalKeyLocked(key)
			s.mu.Unlock()
		},
		Untrack: func(key channel.ChannelKey) {
			s.mu.Lock()
			s.untrackAppliedLocalKeyLocked(key)
			s.mu.Unlock()
		},
		SlotForKey:          s.slotForChannelKey,
		ScheduleSlotRefresh: s.scheduleSlotLeaderRefresh,
	})
}

func (s *channelMetaSync) enqueueLocalReplicaStateChange(key channel.ChannelKey) {
	if s == nil {
		return
	}
	s.mu.Lock()
	stateChanges := s.stateChanges
	s.mu.Unlock()
	runtimechannelmeta.EnqueueLocalReplicaStateChange(stateChanges, key)
}

func (s *channelMetaSync) watchLocalReplicaStateChanges(ctx context.Context) {
	if s == nil {
		return
	}
	defer s.refreshWG.Done()
	s.mu.Lock()
	stateChanges := s.stateChanges
	s.mu.Unlock()
	runtimechannelmeta.WatchLocalReplicaStateChanges(ctx, stateChanges, s.observeLocalReplicaStateChange)
}

type channelMetaLocalRuntime struct {
	runtime channel.HandlerRuntime
}

func (r channelMetaLocalRuntime) Channel(key channel.ChannelKey) (runtimechannelmeta.ChannelObserver, bool) {
	if r.runtime == nil {
		return nil, false
	}
	handle, ok := r.runtime.Channel(key)
	if !ok {
		return nil, false
	}
	return handle, true
}
