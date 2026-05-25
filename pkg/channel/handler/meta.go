package handler

import (
	"slices"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

type Config struct {
	Runtime    channel.HandlerRuntime
	Store      *store.Engine
	MessageIDs channel.MessageIDGenerator
}

type Service interface{ channel.MetaRollbackService }

type service struct {
	cfg Config

	mu sync.RWMutex
	// metas holds defensive copies of authoritative channel metadata by runtime key.
	metas map[channel.ChannelKey]channel.Meta
	// keys caches logical channel IDs to runtime keys for append/fetch hot paths.
	keys map[channel.ChannelID]channel.ChannelKey
	// appendIdempotencyLocks serialize unresolved appends that share one idempotency key.
	appendIdempotencyLocks map[channel.IdempotencyKey]*appendIdempotencyLock
}

func New(cfg Config) (Service, error) {
	if cfg.Runtime == nil || cfg.Store == nil || cfg.MessageIDs == nil {
		return nil, channel.ErrInvalidConfig
	}
	return &service{
		cfg:                    cfg,
		metas:                  make(map[channel.ChannelKey]channel.Meta),
		keys:                   make(map[channel.ChannelID]channel.ChannelKey),
		appendIdempotencyLocks: make(map[channel.IdempotencyKey]*appendIdempotencyLock),
	}, nil
}

func (s *service) ApplyMeta(meta channel.Meta) error {
	key, next, err := normalizeMeta(meta)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	local, ok := s.metas[key]
	if !ok {
		s.storeMetaLocked(key, next)
		return nil
	}
	if staleWriteFence(local.WriteFence, next.WriteFence) {
		return channel.ErrStaleMeta
	}

	switch {
	case next.Epoch < local.Epoch:
		return channel.ErrStaleMeta
	case next.Epoch == local.Epoch && next.LeaderEpoch < local.LeaderEpoch:
		return channel.ErrStaleMeta
	case next.Epoch == local.Epoch && next.LeaderEpoch == local.LeaderEpoch:
		if metaEqual(local, next) {
			s.keys[next.ID] = key
			return nil
		}
		if metaEqualExceptRetention(local, next) {
			s.storeMetaLocked(key, next)
			return nil
		}
		return channel.ErrConflictingMeta
	default:
		s.storeMetaLocked(key, next)
		return nil
	}
}

func (s *service) MetaSnapshot(key channel.ChannelKey) (channel.Meta, bool) {
	s.mu.RLock()
	meta, ok := s.metas[key]
	s.mu.RUnlock()
	if !ok {
		return channel.Meta{}, false
	}
	return cloneMeta(meta), true
}

func (s *service) RestoreMeta(key channel.ChannelKey, meta channel.Meta, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ok {
		if previous, exists := s.metas[key]; exists {
			delete(s.keys, previous.ID)
		}
		delete(s.metas, key)
		return
	}
	s.storeMetaLocked(key, cloneMeta(meta))
}

func (s *service) Status(id channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	key := s.channelKeyForID(id)
	meta, err := s.metaForKey(key)
	if err != nil {
		return channel.ChannelRuntimeStatus{}, err
	}
	group, ok := s.cfg.Runtime.Channel(key)
	if !ok {
		return channel.ChannelRuntimeStatus{}, channel.ErrStaleMeta
	}
	state := group.Status()
	if !state.CommitReady {
		return channel.ChannelRuntimeStatus{}, channel.ErrNotReady
	}
	return channel.ChannelRuntimeStatus{
		Key:                 key,
		ID:                  meta.ID,
		Status:              meta.Status,
		Leader:              meta.Leader,
		LeaderEpoch:         meta.LeaderEpoch,
		HW:                  state.HW,
		CommittedSeq:        state.HW,
		MinAvailableSeq:     state.MinAvailableSeq,
		RetentionThroughSeq: state.RetentionThroughSeq,
	}, nil
}

func (s *service) metaForKey(key channel.ChannelKey) (channel.Meta, error) {
	s.mu.RLock()
	meta, ok := s.metas[key]
	s.mu.RUnlock()
	if !ok {
		return channel.Meta{}, channel.ErrStaleMeta
	}
	// metaForKey is only used by read-only hot paths; returning the stored slice
	// headers avoids per-append ISR/replica clones while ApplyMeta still stores
	// defensive copies at the mutation boundary.
	return meta, nil
}

// channelKeyForID returns the cached runtime key installed with authoritative metadata.
func (s *service) channelKeyForID(id channel.ChannelID) channel.ChannelKey {
	s.mu.RLock()
	key, ok := s.keys[id]
	s.mu.RUnlock()
	if ok {
		return key
	}
	return KeyFromChannelID(id)
}

func (s *service) storeMetaLocked(key channel.ChannelKey, meta channel.Meta) {
	if previous, ok := s.metas[key]; ok && previous.ID != meta.ID {
		delete(s.keys, previous.ID)
	}
	s.metas[key] = meta
	s.keys[meta.ID] = key
}

func normalizeMeta(meta channel.Meta) (channel.ChannelKey, channel.Meta, error) {
	if meta.ID.ID == "" {
		return "", channel.Meta{}, channel.ErrInvalidMeta
	}
	key := KeyFromChannelID(meta.ID)
	if meta.Key != "" && meta.Key != key {
		return "", channel.Meta{}, channel.ErrInvalidMeta
	}
	meta.Key = key
	return key, cloneMeta(meta), nil
}

func compatibleWithExpectation(meta channel.Meta, expectedChannelEpoch, expectedLeaderEpoch uint64) error {
	if expectedChannelEpoch == 0 && expectedLeaderEpoch == 0 {
		return nil
	}
	if expectedChannelEpoch != 0 && meta.Epoch != expectedChannelEpoch {
		return channel.ErrStaleMeta
	}
	if expectedLeaderEpoch != 0 && meta.LeaderEpoch != expectedLeaderEpoch {
		return channel.ErrStaleMeta
	}
	return nil
}

func metaEqual(a, b channel.Meta) bool {
	return metaEqualExceptRetention(a, b) &&
		a.RetentionThroughSeq == b.RetentionThroughSeq &&
		a.WriteFence == b.WriteFence
}

func metaEqualExceptRetention(a, b channel.Meta) bool {
	return a.Key == b.Key &&
		a.ID == b.ID &&
		a.Epoch == b.Epoch &&
		a.LeaderEpoch == b.LeaderEpoch &&
		a.Leader == b.Leader &&
		slices.Equal(a.Replicas, b.Replicas) &&
		slices.Equal(a.ISR, b.ISR) &&
		a.MinISR == b.MinISR &&
		a.Status == b.Status &&
		a.Features == b.Features
}

func cloneMeta(meta channel.Meta) channel.Meta {
	meta.Replicas = slices.Clone(meta.Replicas)
	meta.ISR = slices.Clone(meta.ISR)
	return meta
}

func staleWriteFence(current, next channel.WriteFence) bool {
	if next.Version < current.Version {
		return true
	}
	return next.Version == current.Version && next != current
}
