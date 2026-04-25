package channelmeta

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// SlotLeaderRefreshTimeout bounds one authoritative refresh attempt during slot leader refresh.
const SlotLeaderRefreshTimeout = 5 * time.Second

// SlotRefreshScheduler coalesces slot refreshes and records dirty reruns while a refresh is in flight.
type SlotRefreshScheduler struct {
	mu           sync.Mutex
	pendingSlots map[multiraft.SlotID]struct{}
	dirtySlots   map[multiraft.SlotID]struct{}
}

// Schedule starts a refresh for a slot unless one is already pending.
func (s *SlotRefreshScheduler) Schedule(ctx context.Context, wg *sync.WaitGroup, slotID multiraft.SlotID, keys func(multiraft.SlotID) []channel.ChannelKey, refresh func(context.Context, channel.ChannelKey), timeout time.Duration) {
	if s == nil || ctx == nil || wg == nil || keys == nil || refresh == nil {
		return
	}
	if len(keys(slotID)) == 0 {
		return
	}
	if timeout <= 0 {
		timeout = SlotLeaderRefreshTimeout
	}

	s.mu.Lock()
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
	wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer wg.Done()
		for {
			for _, key := range keys(slotID) {
				refreshCtx, cancel := context.WithTimeout(ctx, timeout)
				refresh(refreshCtx, key)
				cancel()
				if ctx.Err() != nil {
					s.clear(slotID)
					return
				}
			}
			if !s.advance(slotID) {
				return
			}
		}
	}()
}

func (s *SlotRefreshScheduler) clear(slotID multiraft.SlotID) {
	s.mu.Lock()
	if s.pendingSlots != nil {
		delete(s.pendingSlots, slotID)
	}
	if s.dirtySlots != nil {
		delete(s.dirtySlots, slotID)
	}
	s.mu.Unlock()
}

func (s *SlotRefreshScheduler) advance(slotID multiraft.SlotID) bool {
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

// LocalReplicaStateChange describes callbacks needed to react to local replica state changes.
type LocalReplicaStateChange struct {
	// Key identifies the local channel whose replica state changed.
	Key channel.ChannelKey
	// Runtime exposes the current local replica state for Key.
	Runtime LocalReplicaRuntime
	// Track records Key as locally applied when the replica is active.
	Track func(channel.ChannelKey)
	// Untrack removes Key from local tracking when the replica is tombstoned.
	Untrack func(channel.ChannelKey)
	// SlotForKey maps Key to its authoritative slot.
	SlotForKey func(channel.ChannelKey) (multiraft.SlotID, bool)
	// ScheduleSlotRefresh schedules an authoritative refresh for the slot.
	ScheduleSlotRefresh func(multiraft.SlotID)
}

// LocalReplicaRuntime exposes read-only local replica state for state watchers.
type LocalReplicaRuntime interface {
	// Channel returns a local channel observer by key.
	Channel(key channel.ChannelKey) (ChannelObserver, bool)
}

// ObserveLocalReplicaStateChange tracks local active state and schedules refresh on leader drift.
func ObserveLocalReplicaStateChange(change LocalReplicaStateChange) {
	if change.Key == "" || change.Runtime == nil {
		return
	}
	handle, ok := change.Runtime.Channel(change.Key)
	if !ok {
		return
	}
	state := handle.Status()
	if state.Role == channel.ReplicaRoleTombstoned {
		if change.Untrack != nil {
			change.Untrack(change.Key)
		}
		return
	}
	if change.Track != nil {
		change.Track(change.Key)
	}
	if ObservedLeaderRepairReason(handle.Meta(), state) == "" {
		return
	}
	if change.SlotForKey == nil || change.ScheduleSlotRefresh == nil {
		return
	}
	slotID, ok := change.SlotForKey(change.Key)
	if !ok {
		return
	}
	change.ScheduleSlotRefresh(slotID)
}

// EnqueueLocalReplicaStateChange enqueues a key without blocking.
func EnqueueLocalReplicaStateChange(ch chan<- channel.ChannelKey, key channel.ChannelKey) {
	if ch == nil || key == "" {
		return
	}
	select {
	case ch <- key:
	default:
	}
}

// WatchLocalReplicaStateChanges consumes local replica state changes until ctx is canceled.
func WatchLocalReplicaStateChanges(ctx context.Context, ch <-chan channel.ChannelKey, observe func(channel.ChannelKey)) {
	if ctx == nil || ch == nil || observe == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case key := <-ch:
			observe(key)
		}
	}
}
