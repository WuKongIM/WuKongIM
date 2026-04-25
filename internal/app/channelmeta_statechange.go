package app

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const slotLeaderRefreshTimeout = 5 * time.Second

// scheduleSlotLeaderRefresh refreshes active local channel metas in the slot
// after the authoritative slot leader changes.
func (s *channelMetaSync) scheduleSlotLeaderRefresh(slotID multiraft.SlotID) {
	if s == nil {
		return
	}
	keys := s.snapshotAppliedLocalKeysForSlot(slotID)
	if len(keys) == 0 {
		return
	}

	s.mu.Lock()
	runCtx := s.runCtx
	if runCtx == nil {
		s.mu.Unlock()
		return
	}
	if s.pendingSlots == nil {
		s.pendingSlots = make(map[multiraft.SlotID]struct{})
	}
	if _, ok := s.pendingSlots[slotID]; ok {
		if s.dirtySlots == nil {
			s.dirtySlots = make(map[multiraft.SlotID]struct{})
		}
		s.dirtySlots[slotID] = struct{}{}
		s.mu.Unlock()
		return
	}
	s.pendingSlots[slotID] = struct{}{}
	s.refreshWG.Add(1)
	s.mu.Unlock()

	go func(runCtx context.Context) {
		defer s.refreshWG.Done()
		for {
			for _, key := range s.snapshotAppliedLocalKeysForSlot(slotID) {
				ctx, cancel := context.WithTimeout(runCtx, slotLeaderRefreshTimeout)
				_, _ = s.refreshAuthoritativeByKey(ctx, key)
				cancel()
				if runCtx.Err() != nil {
					s.clearPendingSlotRefresh(slotID)
					return
				}
			}
			if !s.advancePendingSlotRefresh(slotID) {
				return
			}
		}
	}(runCtx)
}

func (s *channelMetaSync) clearPendingSlotRefresh(slotID multiraft.SlotID) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.pendingSlots != nil {
		delete(s.pendingSlots, slotID)
	}
	if s.dirtySlots != nil {
		delete(s.dirtySlots, slotID)
	}
	s.mu.Unlock()
}

func (s *channelMetaSync) advancePendingSlotRefresh(slotID multiraft.SlotID) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirtySlots != nil {
		if _, ok := s.dirtySlots[slotID]; ok {
			delete(s.dirtySlots, slotID)
			return true
		}
	}
	if s.pendingSlots != nil {
		delete(s.pendingSlots, slotID)
	}
	return false
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
	handle, ok := s.localRuntime.Channel(key)
	if !ok {
		return
	}
	state := handle.Status()
	if state.Role == channel.ReplicaRoleTombstoned {
		s.mu.Lock()
		s.untrackAppliedLocalKeyLocked(key)
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.trackAppliedLocalKeyLocked(key)
	s.mu.Unlock()
	if observedLeaderRepairReason(handle.Meta(), state) == "" {
		return
	}
	slotID, ok := s.slotForChannelKey(key)
	if !ok {
		return
	}
	s.scheduleSlotLeaderRefresh(slotID)
}

func (s *channelMetaSync) enqueueLocalReplicaStateChange(key channel.ChannelKey) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	stateChanges := s.stateChanges
	s.mu.Unlock()
	if stateChanges == nil {
		return
	}
	select {
	case stateChanges <- key:
	default:
	}
}

func (s *channelMetaSync) watchLocalReplicaStateChanges(ctx context.Context) {
	defer s.refreshWG.Done()
	if s == nil {
		return
	}
	s.mu.Lock()
	stateChanges := s.stateChanges
	s.mu.Unlock()
	if stateChanges == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case key := <-stateChanges:
			s.observeLocalReplicaStateChange(key)
		}
	}
}
