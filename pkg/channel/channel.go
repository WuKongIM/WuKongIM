package channel

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"sync"
	"time"
)

type Cluster interface {
	ApplyMeta(meta Meta) error
	Append(ctx context.Context, req AppendRequest) (AppendResult, error)
	Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
	Status(id ChannelID) (ChannelRuntimeStatus, error)
	Close() error
}

type Service interface {
	ApplyMeta(meta Meta) error
	Append(ctx context.Context, req AppendRequest) (AppendResult, error)
	Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
	Status(id ChannelID) (ChannelRuntimeStatus, error)
}

type MetaRollbackService interface {
	Service
	MetaSnapshot(key ChannelKey) (Meta, bool)
	RestoreMeta(key ChannelKey, meta Meta, ok bool)
}

type Runtime interface {
	UpsertMeta(meta Meta) error
	RemoveChannel(key ChannelKey) error
}

type HandlerRuntime interface {
	Channel(key ChannelKey) (HandlerChannel, bool)
}

type HandlerChannel interface {
	ID() ChannelKey
	Meta() Meta
	Status() ReplicaState
	Append(ctx context.Context, records []Record) (CommitResult, error)
}

type MessageIDGenerator interface {
	Next() uint64
}

type Config struct {
	LocalNode           NodeID
	Store               any
	GenerationStore     any
	MessageIDs          MessageIDGenerator
	LongPollLaneCount   int
	LongPollMaxWait     time.Duration
	LongPollMaxBytes    int
	LongPollMaxChannels int
	Transport           TransportConfig
	Runtime             RuntimeConfig
	Handler             HandlerConfig
	Now                 func() time.Time
}

type TransportConfig struct {
	Client              any
	RPCMux              any
	RPCTimeout          time.Duration
	MaxPendingFetchRPC  int
	LongPollLaneCount   int
	LongPollMaxWait     time.Duration
	LongPollMaxBytes    int
	LongPollMaxChannels int
	Build               func(TransportBuildConfig) (any, error)
	BindFetchService    func(transport any, runtime HandlerRuntime) error
}

type RuntimeLimits struct {
	MaxChannels               int
	MaxFetchInflightPeer      int
	MaxSnapshotInflight       int
	MaxRecoveryBytesPerSecond int64
}

type RuntimeTombstones struct {
	TombstoneTTL    time.Duration
	CleanupInterval time.Duration
}

type RuntimeConfig struct {
	AutoRunScheduler                 bool
	FollowerReplicationRetryInterval time.Duration
	LongPollLaneCount                int
	LongPollMaxWait                  time.Duration
	LongPollMaxBytes                 int
	LongPollMaxChannels              int
	Limits                           RuntimeLimits
	Tombstones                       RuntimeTombstones
	Build                            func(RuntimeBuildConfig) (Runtime, HandlerRuntime, error)
}

type HandlerConfig struct {
	Build func(HandlerBuildConfig) (MetaRollbackService, error)
}

type TransportBuildConfig struct {
	LocalNode           NodeID
	Client              any
	RPCMux              any
	RPCTimeout          time.Duration
	MaxPendingFetchRPC  int
	LongPollLaneCount   int
	LongPollMaxWait     time.Duration
	LongPollMaxBytes    int
	LongPollMaxChannels int
}

type RuntimeBuildConfig struct {
	LocalNode                        NodeID
	Store                            any
	GenerationStore                  any
	Transport                        any
	AutoRunScheduler                 bool
	FollowerReplicationRetryInterval time.Duration
	LongPollLaneCount                int
	LongPollMaxWait                  time.Duration
	LongPollMaxBytes                 int
	LongPollMaxChannels              int
	Limits                           RuntimeLimits
	Tombstones                       RuntimeTombstones
	Now                              func() time.Time
}

type HandlerBuildConfig struct {
	Store      any
	Runtime    HandlerRuntime
	MessageIDs MessageIDGenerator
}

type cluster struct {
	service MetaRollbackService
	runtime Runtime
	closers []ownedCloser

	closeOnce sync.Once
	closeErr  error

	applyMu    sync.Mutex
	applyLocks map[ChannelKey]*channelApplyLock
}

type channelApplyLock struct {
	mu   sync.Mutex
	refs int
}

func New(cfg Config) (Cluster, error) {
	if cfg.LocalNode == 0 {
		return nil, ErrInvalidConfig
	}
	if cfg.Store == nil || cfg.GenerationStore == nil || cfg.MessageIDs == nil {
		return nil, ErrInvalidConfig
	}
	if cfg.Transport.Build == nil || cfg.Transport.BindFetchService == nil || cfg.Runtime.Build == nil || cfg.Handler.Build == nil {
		return nil, ErrInvalidConfig
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	transportValue, err := cfg.Transport.Build(TransportBuildConfig{
		LocalNode:           cfg.LocalNode,
		Client:              cfg.Transport.Client,
		RPCMux:              cfg.Transport.RPCMux,
		RPCTimeout:          cfg.Transport.RPCTimeout,
		MaxPendingFetchRPC:  cfg.Transport.MaxPendingFetchRPC,
		LongPollLaneCount:   cfg.LongPollLaneCount,
		LongPollMaxWait:     cfg.LongPollMaxWait,
		LongPollMaxBytes:    cfg.LongPollMaxBytes,
		LongPollMaxChannels: cfg.LongPollMaxChannels,
	})
	if err != nil {
		return nil, err
	}
	transportCloser := buildValueCloser(transportValue)

	runtimeControl, runtimeValue, err := cfg.Runtime.Build(RuntimeBuildConfig{
		LocalNode:                        cfg.LocalNode,
		Store:                            cfg.Store,
		GenerationStore:                  cfg.GenerationStore,
		Transport:                        transportValue,
		AutoRunScheduler:                 cfg.Runtime.AutoRunScheduler,
		FollowerReplicationRetryInterval: cfg.Runtime.FollowerReplicationRetryInterval,
		LongPollLaneCount:                cfg.LongPollLaneCount,
		LongPollMaxWait:                  cfg.LongPollMaxWait,
		LongPollMaxBytes:                 cfg.LongPollMaxBytes,
		LongPollMaxChannels:              cfg.LongPollMaxChannels,
		Limits:                           cfg.Runtime.Limits,
		Tombstones:                       cfg.Runtime.Tombstones,
		Now:                              cfg.Now,
	})
	if err != nil {
		return nil, joinBuildError(err, transportCloser())
	}
	runtimeCloser := buildRuntimeCloser(runtimeControl, runtimeValue)
	if err := cfg.Transport.BindFetchService(transportValue, runtimeValue); err != nil {
		return nil, joinBuildError(err, runtimeCloser(), transportCloser())
	}

	service, err := cfg.Handler.Build(HandlerBuildConfig{
		Store:      cfg.Store,
		Runtime:    runtimeValue,
		MessageIDs: cfg.MessageIDs,
	})
	if err != nil {
		return nil, joinBuildError(err, runtimeCloser(), transportCloser())
	}

	return &cluster{
		service: service,
		runtime: runtimeControl,
		closers: []ownedCloser{
			buildValueCloser(service),
			runtimeCloser,
			transportCloser,
		},
	}, nil
}

func (c *cluster) ApplyMeta(meta Meta) error {
	meta.Key = effectiveChannelKey(meta)
	unlock := c.lockApplyMeta(meta.Key)
	defer unlock()

	previous, ok := c.service.MetaSnapshot(meta.Key)
	if err := c.service.ApplyMeta(meta); err != nil {
		return err
	}
	var runtimeErr error
	if meta.Status == StatusDeleted {
		runtimeErr = c.runtime.RemoveChannel(meta.Key)
	} else {
		runtimeErr = c.runtime.UpsertMeta(meta)
	}
	if runtimeErr == nil {
		return nil
	}
	c.service.RestoreMeta(meta.Key, previous, ok)
	return runtimeErr
}

func (c *cluster) lockApplyMeta(key ChannelKey) func() {
	c.applyMu.Lock()
	if c.applyLocks == nil {
		c.applyLocks = make(map[ChannelKey]*channelApplyLock)
	}
	lock, ok := c.applyLocks[key]
	if !ok {
		lock = &channelApplyLock{}
		c.applyLocks[key] = lock
	}
	lock.refs++
	c.applyMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()

		c.applyMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(c.applyLocks, key)
		}
		c.applyMu.Unlock()
	}
}

func (c *cluster) Append(ctx context.Context, req AppendRequest) (AppendResult, error) {
	return c.service.Append(ctx, req)
}

func (c *cluster) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	return c.service.Fetch(ctx, req)
}

func (c *cluster) Status(id ChannelID) (ChannelRuntimeStatus, error) {
	return c.service.Status(id)
}

func (c *cluster) Close() error {
	c.closeOnce.Do(func() {
		for _, closeFn := range c.closers {
			if closeFn == nil {
				continue
			}
			c.closeErr = errors.Join(c.closeErr, closeFn())
		}
	})
	return c.closeErr
}

func effectiveChannelKey(meta Meta) ChannelKey {
	if meta.Key != "" {
		return meta.Key
	}
	encodedID := base64.RawURLEncoding.EncodeToString([]byte(meta.ID.ID))
	buf := make([]byte, 0, len("channel/")+4+1+len(encodedID))
	buf = append(buf, "channel/"...)
	buf = strconv.AppendUint(buf, uint64(meta.ID.Type), 10)
	buf = append(buf, '/')
	buf = append(buf, encodedID...)
	return ChannelKey(buf)
}

type buildCloser interface {
	Close() error
}

type ownedCloser func() error

func buildRuntimeCloser(control Runtime, value HandlerRuntime) ownedCloser {
	return func() error {
		return closeBuiltRuntime(control, value)
	}
}

func buildValueCloser(value any) ownedCloser {
	return func() error {
		return closeBuildError(value)
	}
}

func closeBuiltRuntime(control Runtime, value HandlerRuntime) error {
	if err, ok := closeBuildValue(value); ok {
		return err
	}
	if err, ok := closeBuildValue(control); ok {
		return err
	}
	return nil
}

func closeBuildValue(value any) (error, bool) {
	closer, ok := value.(buildCloser)
	if !ok || closer == nil {
		return nil, false
	}
	return closer.Close(), true
}

func closeBuildError(value any) error {
	err, _ := closeBuildValue(value)
	return err
}

func joinBuildError(primary error, cleanup ...error) error {
	err := primary
	for _, cleanupErr := range cleanup {
		if cleanupErr == nil {
			continue
		}
		err = errors.Join(err, cleanupErr)
	}
	return err
}
