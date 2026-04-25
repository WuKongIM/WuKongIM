package handler

import (
	"slices"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

type Config struct {
	Runtime    channel.HandlerRuntime
	Store      *store.Engine
	MessageIDs channel.MessageIDGenerator
}

type Service interface{ channel.MetaRollbackService }

type service struct {
	cfg Config

	mu    sync.RWMutex
	metas map[channel.ChannelKey]channel.Meta
}

func New(cfg Config) (Service, error) {
	if cfg.Runtime == nil || cfg.Store == nil || cfg.MessageIDs == nil {
		return nil, channel.ErrInvalidConfig
	}
	return &service{
		cfg:   cfg,
		metas: make(map[channel.ChannelKey]channel.Meta),
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
		s.metas[key] = next
		return nil
	}

	switch {
	case next.Epoch < local.Epoch:
		return channel.ErrStaleMeta
	case next.Epoch == local.Epoch && next.LeaderEpoch < local.LeaderEpoch:
		return channel.ErrStaleMeta
	case next.Epoch == local.Epoch && next.LeaderEpoch == local.LeaderEpoch:
		if metaEqual(local, next) {
			return nil
		}
		return channel.ErrConflictingMeta
	default:
		s.metas[key] = next
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
		delete(s.metas, key)
		return
	}
	s.metas[key] = cloneMeta(meta)
}

func (s *service) Status(id channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	key := KeyFromChannelID(id)
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
		Key:          key,
		ID:           meta.ID,
		Status:       meta.Status,
		Leader:       meta.Leader,
		LeaderEpoch:  meta.LeaderEpoch,
		HW:           state.HW,
		CommittedSeq: state.HW,
	}, nil
}

func (s *service) metaForKey(key channel.ChannelKey) (channel.Meta, error) {
	s.mu.RLock()
	meta, ok := s.metas[key]
	s.mu.RUnlock()
	if !ok {
		return channel.Meta{}, channel.ErrStaleMeta
	}
	return cloneMeta(meta), nil
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
