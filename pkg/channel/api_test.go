package channel_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelreplica "github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	wktransport "github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestNewBuildsClusterWithRuntimeHandlerAndTransport(t *testing.T) {
	engine, err := channelstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Fatalf("engine.Close() error = %v", err)
		}
	}()

	client := wktransport.NewClient(wktransport.NewPool(channelTestDiscovery{addrs: map[uint64]string{}}, 1, time.Second))
	defer client.Stop()

	got, err := channel.New(channel.Config{
		LocalNode:       1,
		Store:           engine,
		GenerationStore: &channelTestGenerationStore{},
		MessageIDs:      &channelTestMessageIDs{},
		Transport: channel.TransportConfig{
			Client:           client,
			RPCMux:           wktransport.NewRPCMux(),
			Build:            buildTestTransport,
			BindFetchService: bindTestTransportFetchService,
		},
		Runtime: channel.RuntimeConfig{
			Limits: channel.RuntimeLimits{
				MaxFetchInflightPeer: 1,
				MaxSnapshotInflight:  1,
			},
			Tombstones: channel.RuntimeTombstones{
				TombstoneTTL:    time.Minute,
				CleanupInterval: time.Minute,
			},
			Build: buildTestRuntime,
		},
		Handler: channel.HandlerConfig{
			Build: buildTestHandler,
		},
		Now: func() time.Time {
			return time.Unix(1700000000, 0)
		},
	})
	if err != nil {
		t.Fatalf("channel.New() error = %v", err)
	}
	if got == nil {
		t.Fatal("channel.New() returned nil cluster")
	}
	t.Cleanup(func() {
		if err := got.Close(); err != nil {
			t.Fatalf("cluster.Close() error = %v", err)
		}
	})

	id := channel.ChannelID{ID: "room-1", Type: 1}
	meta := channel.Meta{
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channel.NodeID{1},
		ISR:         []channel.NodeID{1},
		MinISR:      1,
		Status:      channel.StatusActive,
		Features:    channel.Features{MessageSeqFormat: channel.MessageSeqFormatU64},
		LeaseUntil:  time.Unix(1700000060, 0),
	}
	if err := got.ApplyMeta(meta); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	appendResult, err := got.Append(context.Background(), channel.AppendRequest{
		ChannelID:             id,
		SupportsMessageSeqU64: true,
		Message: channel.Message{
			FromUID: "alice",
			Payload: []byte("hello"),
		},
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if appendResult.MessageSeq != 1 {
		t.Fatalf("Append().MessageSeq = %d, want 1", appendResult.MessageSeq)
	}
	if appendResult.Message.MessageID == 0 {
		t.Fatal("Append().Message.MessageID = 0, want non-zero")
	}

	fetchResult, err := got.Fetch(context.Background(), channel.FetchRequest{
		ChannelID: id,
		Limit:     10,
		MaxBytes:  1024,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(fetchResult.Messages) != 1 {
		t.Fatalf("Fetch().Messages len = %d, want 1", len(fetchResult.Messages))
	}
	if string(fetchResult.Messages[0].Payload) != "hello" {
		t.Fatalf("Fetch().Messages[0].Payload = %q, want %q", fetchResult.Messages[0].Payload, "hello")
	}
	if fetchResult.CommittedSeq != 1 {
		t.Fatalf("Fetch().CommittedSeq = %d, want 1", fetchResult.CommittedSeq)
	}

	status, err := got.Status(id)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Key != channel.ChannelKey("channel/1/cm9vbS0x") {
		t.Fatalf("Status().Key = %q, want %q", status.Key, channel.ChannelKey("channel/1/cm9vbS0x"))
	}
	if status.CommittedSeq != 1 || status.HW != 1 {
		t.Fatalf("Status() commit state = %+v, want committed seq/hw 1", status)
	}
}

func TestChannelAPIFetchReportsRuntimeCommittedSeq(t *testing.T) {
	engine, err := channelstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Fatalf("engine.Close() error = %v", err)
		}
	}()

	rt := newStubHandlerBackedRuntime(engine)
	cluster := mustBuildRuntimeBackedCluster(t, engine, rt)
	t.Cleanup(func() {
		if err := cluster.Close(); err != nil {
			t.Fatalf("cluster.Close() error = %v", err)
		}
	})

	id := channel.ChannelID{ID: "runtime-commit", Type: 1}
	meta := channel.Meta{
		ID:          id,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channel.NodeID{1},
		ISR:         []channel.NodeID{1},
		MinISR:      1,
		Status:      channel.StatusActive,
		Features:    channel.Features{MessageSeqFormat: channel.MessageSeqFormatU64},
	}
	if err := cluster.ApplyMeta(meta); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	_, err = cluster.Append(context.Background(), channel.AppendRequest{
		ChannelID:             id,
		SupportsMessageSeqU64: true,
		Message: channel.Message{
			FromUID: "alice",
			Payload: []byte("hello"),
		},
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	st := engine.ForChannel(encodedTestChannelKey(id), id)
	if _, err := st.LoadCheckpoint(); !errors.Is(err, channel.ErrEmptyState) {
		t.Fatalf("LoadCheckpoint() error = %v, want ErrEmptyState", err)
	}

	result, err := cluster.Fetch(context.Background(), channel.FetchRequest{
		ChannelID: id,
		FromSeq:   1,
		Limit:     10,
		MaxBytes:  1024,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if result.CommittedSeq != 1 {
		t.Fatalf("Fetch().CommittedSeq = %d, want 1", result.CommittedSeq)
	}
	if len(result.Messages) != 1 || string(result.Messages[0].Payload) != "hello" {
		t.Fatalf("Fetch().Messages = %+v, want one committed runtime-backed message", result.Messages)
	}
}

func TestApplyMetaRollsBackHandlerStateWhenRuntimeMutationFails(t *testing.T) {
	meta := channel.Meta{
		ID:          channel.ChannelID{ID: "rollback", Type: 1},
		Epoch:       2,
		LeaderEpoch: 3,
		Leader:      1,
		Replicas:    []channel.NodeID{1},
		ISR:         []channel.NodeID{1},
		MinISR:      1,
		Status:      channel.StatusActive,
		Features:    channel.Features{MessageSeqFormat: channel.MessageSeqFormatU64},
	}

	t.Run("create rollback removes speculative handler meta", func(t *testing.T) {
		service := newStubMetaService()
		cluster := mustBuildStubCluster(t, service, &stubRuntimeControl{
			upsertErr: errors.New("upsert failed"),
		})

		err := cluster.ApplyMeta(meta)
		if err == nil {
			t.Fatal("expected ApplyMeta() to fail")
		}

		key := encodedTestChannelKey(meta.ID)
		if _, ok := service.MetaSnapshot(key); ok {
			t.Fatalf("handler meta for %q remained after runtime upsert failure", key)
		}
	})

	t.Run("delete rollback restores previous handler meta", func(t *testing.T) {
		service := newStubMetaService()
		key := encodedTestChannelKey(meta.ID)
		service.ForceSetMeta(key, meta)
		cluster := mustBuildStubCluster(t, service, &stubRuntimeControl{
			removeErr: errors.New("remove failed"),
		})

		deleted := meta
		deleted.Status = channel.StatusDeleted
		err := cluster.ApplyMeta(deleted)
		if err == nil {
			t.Fatal("expected ApplyMeta() to fail")
		}

		got, ok := service.MetaSnapshot(key)
		if !ok {
			t.Fatalf("handler meta for %q missing after rollback", key)
		}
		if got.Status != channel.StatusActive {
			t.Fatalf("handler meta status = %d, want %d", got.Status, channel.StatusActive)
		}
		if got.Epoch != meta.Epoch || got.LeaderEpoch != meta.LeaderEpoch {
			t.Fatalf("handler meta after rollback = %+v, want %+v", got, meta)
		}
	})
}

func TestApplyMetaKeepsNewerMetaWhenOlderSameChannelUpdateFails(t *testing.T) {
	initial := channel.Meta{
		ID:          channel.ChannelID{ID: "overlap", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channel.NodeID{1},
		ISR:         []channel.NodeID{1},
		MinISR:      1,
		Status:      channel.StatusActive,
		Features:    channel.Features{MessageSeqFormat: channel.MessageSeqFormatU64},
	}
	older := initial
	older.Epoch = 2
	older.LeaderEpoch = 2
	newer := initial
	newer.Epoch = 3
	newer.LeaderEpoch = 3

	service := newStubMetaService()
	key := encodedTestChannelKey(initial.ID)
	service.ForceSetMeta(key, initial)

	runtimeControl := newSequencedRuntimeControl(older, errors.New("older upsert failed"))
	cluster := mustBuildStubCluster(t, service, runtimeControl)

	olderErrCh := make(chan error, 1)
	go func() {
		olderErrCh <- cluster.ApplyMeta(older)
	}()

	runtimeControl.waitForBlockedOlder(t)

	newerErrCh := make(chan error, 1)
	go func() {
		newerErrCh <- cluster.ApplyMeta(newer)
	}()

	runtimeControl.waitForNewerAttempt()

	runtimeControl.releaseOlder()

	if err := <-olderErrCh; err == nil {
		t.Fatal("older ApplyMeta() error = nil, want failure")
	}
	if err := <-newerErrCh; err != nil {
		t.Fatalf("newer ApplyMeta() error = %v", err)
	}

	got, ok := service.MetaSnapshot(key)
	if !ok {
		t.Fatalf("handler meta for %q missing after overlapping ApplyMeta", key)
	}
	if got.Epoch != newer.Epoch || got.LeaderEpoch != newer.LeaderEpoch {
		t.Fatalf("handler meta after overlap = %+v, want %+v", got, newer)
	}
	if applied, ok := runtimeControl.LastMeta(key); !ok {
		t.Fatalf("runtime meta for %q missing after overlapping ApplyMeta", key)
	} else if applied.Epoch != newer.Epoch || applied.LeaderEpoch != newer.LeaderEpoch {
		t.Fatalf("runtime meta after overlap = %+v, want %+v", applied, newer)
	}
}

func TestNewClosesBuiltRuntimeWhenHandlerBuildFails(t *testing.T) {
	runtimeControl := &stubRuntimeControl{}
	runtimeValue := &stubClosableRuntimeValue{}
	transportValue := &stubTransportBuildValue{}

	_, err := channel.New(channel.Config{
		LocalNode:       1,
		Store:           struct{}{},
		GenerationStore: struct{}{},
		MessageIDs:      &channelTestMessageIDs{},
		Transport: channel.TransportConfig{
			Build: func(channel.TransportBuildConfig) (any, error) {
				return transportValue, nil
			},
			BindFetchService: bindStubTransportFetchService,
		},
		Runtime: channel.RuntimeConfig{
			Build: func(channel.RuntimeBuildConfig) (channel.Runtime, channel.HandlerRuntime, error) {
				return runtimeControl, runtimeValue, nil
			},
		},
		Handler: channel.HandlerConfig{
			Build: func(channel.HandlerBuildConfig) (channel.MetaRollbackService, error) {
				return nil, errors.New("handler build failed")
			},
		},
	})
	if err == nil {
		t.Fatal("expected channel.New() to fail")
	}
	if runtimeControl.closeCalls != 0 {
		t.Fatalf("runtime control close calls = %d, want 0 when only runtime value owns cleanup", runtimeControl.closeCalls)
	}
	if runtimeValue.closeCalls != 1 {
		t.Fatalf("runtime value close calls = %d, want 1", runtimeValue.closeCalls)
	}
	if transportValue.closeCalls != 1 {
		t.Fatalf("transport close calls = %d, want 1", transportValue.closeCalls)
	}
}

func TestNewReleasesChannelTransportRPCServicesAfterAssemblyFailure(t *testing.T) {
	client := wktransport.NewClient(wktransport.NewPool(channelTestDiscovery{addrs: map[uint64]string{}}, 1, time.Second))
	defer client.Stop()

	mux := wktransport.NewRPCMux()
	errHandlerBuild := errors.New("handler build failed")
	handlerBuildCalls := 0

	cfg := channel.Config{
		LocalNode:       1,
		Store:           struct{}{},
		GenerationStore: struct{}{},
		MessageIDs:      &channelTestMessageIDs{},
		Transport: channel.TransportConfig{
			Client:           client,
			RPCMux:           mux,
			Build:            buildTestTransport,
			BindFetchService: bindTestTransportFetchService,
		},
		Runtime: channel.RuntimeConfig{
			Build: func(channel.RuntimeBuildConfig) (channel.Runtime, channel.HandlerRuntime, error) {
				return &stubRuntimeControl{}, &stubClosableRuntimeValue{}, nil
			},
		},
		Handler: channel.HandlerConfig{
			Build: func(channel.HandlerBuildConfig) (channel.MetaRollbackService, error) {
				handlerBuildCalls++
				return nil, errHandlerBuild
			},
		},
	}

	if _, err := channel.New(cfg); !errors.Is(err, errHandlerBuild) {
		t.Fatalf("first channel.New() error = %v, want %v", err, errHandlerBuild)
	}

	var retryErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("second channel.New() panicked with reused mux: %v", r)
			}
		}()
		_, retryErr = channel.New(cfg)
	}()

	if !errors.Is(retryErr, errHandlerBuild) {
		t.Fatalf("second channel.New() error = %v, want %v", retryErr, errHandlerBuild)
	}
	if handlerBuildCalls != 2 {
		t.Fatalf("handler build calls = %d, want 2", handlerBuildCalls)
	}
}

func TestNewBindsFetchServiceAndClusterCloseOwnsBuiltResources(t *testing.T) {
	runtimeControl := &stubRuntimeControl{}
	runtimeValue := &stubClosableRuntimeValue{}
	transportValue := &stubTransportBuildValue{}
	service := newStubMetaService()

	got, err := channel.New(channel.Config{
		LocalNode:       1,
		Store:           struct{}{},
		GenerationStore: struct{}{},
		MessageIDs:      &channelTestMessageIDs{},
		Transport: channel.TransportConfig{
			Build: func(channel.TransportBuildConfig) (any, error) {
				return transportValue, nil
			},
			BindFetchService: bindStubTransportFetchService,
		},
		Runtime: channel.RuntimeConfig{
			Build: func(channel.RuntimeBuildConfig) (channel.Runtime, channel.HandlerRuntime, error) {
				return runtimeControl, runtimeValue, nil
			},
		},
		Handler: channel.HandlerConfig{
			Build: func(channel.HandlerBuildConfig) (channel.MetaRollbackService, error) {
				return service, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("channel.New() error = %v", err)
	}

	if transportValue.bindCalls != 1 {
		t.Fatalf("transport bind calls = %d, want 1", transportValue.bindCalls)
	}
	if transportValue.boundService != runtimeValue {
		t.Fatalf("transport bound service = %T, want %T", transportValue.boundService, runtimeValue)
	}

	if err := got.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := got.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	if runtimeControl.closeCalls != 0 {
		t.Fatalf("runtime control close calls = %d, want 0 when only runtime value owns cleanup", runtimeControl.closeCalls)
	}
	if runtimeValue.closeCalls != 1 {
		t.Fatalf("runtime value close calls = %d, want 1", runtimeValue.closeCalls)
	}
	if service.closeCalls != 1 {
		t.Fatalf("service close calls = %d, want 1", service.closeCalls)
	}
	if transportValue.closeCalls != 1 {
		t.Fatalf("transport close calls = %d, want 1", transportValue.closeCalls)
	}
}

func TestNewClosesBuiltRuntimeAndTransportWhenFetchBindingFails(t *testing.T) {
	bindErr := errors.New("bind fetch service failed")
	runtimeControl := &stubRuntimeControl{}
	runtimeValue := &stubClosableRuntimeValue{}
	transportValue := &stubTransportBuildValue{bindErr: bindErr}

	_, err := channel.New(channel.Config{
		LocalNode:       1,
		Store:           struct{}{},
		GenerationStore: struct{}{},
		MessageIDs:      &channelTestMessageIDs{},
		Transport: channel.TransportConfig{
			Build: func(channel.TransportBuildConfig) (any, error) {
				return transportValue, nil
			},
			BindFetchService: bindStubTransportFetchService,
		},
		Runtime: channel.RuntimeConfig{
			Build: func(channel.RuntimeBuildConfig) (channel.Runtime, channel.HandlerRuntime, error) {
				return runtimeControl, runtimeValue, nil
			},
		},
		Handler: channel.HandlerConfig{
			Build: func(channel.HandlerBuildConfig) (channel.MetaRollbackService, error) {
				t.Fatal("handler build should not run when fetch binding fails")
				return nil, nil
			},
		},
	})
	if !errors.Is(err, bindErr) {
		t.Fatalf("channel.New() error = %v, want %v", err, bindErr)
	}
	if transportValue.bindCalls != 1 {
		t.Fatalf("transport bind calls = %d, want 1", transportValue.bindCalls)
	}
	if runtimeControl.closeCalls != 0 {
		t.Fatalf("runtime control close calls = %d, want 0 when only runtime value owns cleanup", runtimeControl.closeCalls)
	}
	if runtimeValue.closeCalls != 1 {
		t.Fatalf("runtime value close calls = %d, want 1", runtimeValue.closeCalls)
	}
	if transportValue.closeCalls != 1 {
		t.Fatalf("transport close calls = %d, want 1", transportValue.closeCalls)
	}
}

func buildTestTransport(cfg channel.TransportBuildConfig) (any, error) {
	client, ok := cfg.Client.(*wktransport.Client)
	if !ok {
		return nil, errors.New("transport client type mismatch")
	}
	mux, ok := cfg.RPCMux.(*wktransport.RPCMux)
	if !ok {
		return nil, errors.New("transport mux type mismatch")
	}
	return channeltransport.New(channeltransport.Options{
		LocalNode:          cfg.LocalNode,
		Client:             client,
		RPCMux:             mux,
		RPCTimeout:         cfg.RPCTimeout,
		MaxPendingFetchRPC: cfg.MaxPendingFetchRPC,
	})
}

func buildTestRuntime(cfg channel.RuntimeBuildConfig) (channel.Runtime, channel.HandlerRuntime, error) {
	engine, ok := cfg.Store.(*channelstore.Engine)
	if !ok {
		return nil, nil, errors.New("runtime store type mismatch")
	}
	generationStore, ok := cfg.GenerationStore.(*channelTestGenerationStore)
	if !ok {
		return nil, nil, errors.New("runtime generation store type mismatch")
	}
	transportValue, ok := cfg.Transport.(*channeltransport.Transport)
	if !ok {
		return nil, nil, errors.New("runtime transport type mismatch")
	}

	rt, err := channelruntime.New(channelruntime.Config{
		LocalNode:                        cfg.LocalNode,
		ReplicaFactory:                   channelTestReplicaFactory{localNode: cfg.LocalNode, store: engine, now: cfg.Now},
		GenerationStore:                  generationStore,
		Transport:                        transportValue,
		PeerSessions:                     transportValue,
		AutoRunScheduler:                 cfg.AutoRunScheduler,
		FollowerReplicationRetryInterval: cfg.FollowerReplicationRetryInterval,
		Limits: channelruntime.Limits{
			MaxChannels:               cfg.Limits.MaxChannels,
			MaxFetchInflightPeer:      cfg.Limits.MaxFetchInflightPeer,
			MaxSnapshotInflight:       cfg.Limits.MaxSnapshotInflight,
			MaxRecoveryBytesPerSecond: cfg.Limits.MaxRecoveryBytesPerSecond,
		},
		Tombstones: channelruntime.TombstonePolicy{
			TombstoneTTL:    cfg.Tombstones.TombstoneTTL,
			CleanupInterval: cfg.Tombstones.CleanupInterval,
		},
		Now: cfg.Now,
	})
	if err != nil {
		return nil, nil, err
	}
	return channelRuntimeControl{runtime: rt}, rt, nil
}

func bindTestTransportFetchService(transport any, runtimeValue channel.HandlerRuntime) error {
	transportValue, ok := transport.(*channeltransport.Transport)
	if !ok {
		return errors.New("transport build value type mismatch")
	}
	service, ok := runtimeValue.(interface {
		ServeFetch(context.Context, channelruntime.FetchRequestEnvelope) (channelruntime.FetchResponseEnvelope, error)
	})
	if !ok {
		return errors.New("runtime fetch service type mismatch")
	}
	transportValue.BindFetchService(service)
	return nil
}

func bindStubTransportFetchService(transport any, runtimeValue channel.HandlerRuntime) error {
	transportValue, ok := transport.(*stubTransportBuildValue)
	if !ok {
		return errors.New("stub transport build value type mismatch")
	}
	return transportValue.BindFetchService(runtimeValue)
}

func buildTestHandler(cfg channel.HandlerBuildConfig) (channel.MetaRollbackService, error) {
	engine, ok := cfg.Store.(*channelstore.Engine)
	if !ok {
		return nil, errors.New("handler store type mismatch")
	}
	rt, ok := cfg.Runtime.(channel.HandlerRuntime)
	if !ok {
		return nil, errors.New("handler runtime type mismatch")
	}
	return channelhandler.New(channelhandler.Config{
		Runtime:    rt,
		Store:      engine,
		MessageIDs: cfg.MessageIDs,
	})
}

type channelRuntimeControl struct {
	runtime channelruntime.Runtime
}

func (c channelRuntimeControl) UpsertMeta(meta channel.Meta) error {
	if err := c.runtime.EnsureChannel(meta); err != nil {
		if errors.Is(err, channelruntime.ErrChannelExists) {
			return c.runtime.ApplyMeta(meta)
		}
		return err
	}
	return nil
}

func (c channelRuntimeControl) RemoveChannel(key channel.ChannelKey) error {
	if err := c.runtime.RemoveChannel(key); err != nil && !errors.Is(err, channel.ErrChannelNotFound) {
		return err
	}
	return nil
}

type channelTestReplicaFactory struct {
	localNode channel.NodeID
	store     *channelstore.Engine
	now       func() time.Time
}

func (f channelTestReplicaFactory) New(cfg channelruntime.ChannelConfig) (channelreplica.Replica, error) {
	store := f.store.ForChannel(cfg.ChannelKey, cfg.Meta.ID)
	return channelreplica.NewReplica(channelreplica.ReplicaConfig{
		LocalNode:         f.localNode,
		LogStore:          store,
		CheckpointStore:   channelCheckpointStore{store: store},
		ApplyFetchStore:   store,
		EpochHistoryStore: channelEpochHistoryStore{store: store},
		SnapshotApplier:   channelSnapshotApplier{store: store},
		Now:               f.now,
	})
}

type channelCheckpointStore struct{ store *channelstore.ChannelStore }

func (s channelCheckpointStore) Load() (channel.Checkpoint, error) { return s.store.LoadCheckpoint() }
func (s channelCheckpointStore) Store(cp channel.Checkpoint) error {
	return s.store.StoreCheckpoint(cp)
}

type channelEpochHistoryStore struct{ store *channelstore.ChannelStore }

func (s channelEpochHistoryStore) Load() ([]channel.EpochPoint, error) { return s.store.LoadHistory() }
func (s channelEpochHistoryStore) Append(point channel.EpochPoint) error {
	return s.store.AppendHistory(point)
}
func (s channelEpochHistoryStore) TruncateTo(leo uint64) error { return s.store.TruncateHistoryTo(leo) }

type channelSnapshotApplier struct{ store *channelstore.ChannelStore }

func (s channelSnapshotApplier) InstallSnapshot(_ context.Context, snap channel.Snapshot) error {
	return s.store.StoreSnapshotPayload(snap.Payload)
}

type channelTestGenerationStore struct{}

func (channelTestGenerationStore) Load(channel.ChannelKey) (uint64, error) { return 0, nil }
func (channelTestGenerationStore) Store(channel.ChannelKey, uint64) error  { return nil }

type channelTestMessageIDs struct{ next uint64 }

func (g *channelTestMessageIDs) Next() uint64 {
	g.next++
	return g.next
}

type channelTestDiscovery struct {
	addrs map[uint64]string
}

func (d channelTestDiscovery) Resolve(nodeID uint64) (string, error) {
	addr, ok := d.addrs[nodeID]
	if !ok {
		return "", wktransport.ErrNodeNotFound
	}
	return addr, nil
}

func mustBuildStubCluster(t *testing.T, service channel.MetaRollbackService, runtimeControl channel.Runtime) channel.Cluster {
	t.Helper()

	got, err := channel.New(channel.Config{
		LocalNode:       1,
		Store:           struct{}{},
		GenerationStore: struct{}{},
		MessageIDs:      &channelTestMessageIDs{},
		Transport: channel.TransportConfig{
			Build: func(channel.TransportBuildConfig) (any, error) {
				return &stubTransportBuildValue{}, nil
			},
			BindFetchService: bindStubTransportFetchService,
		},
		Runtime: channel.RuntimeConfig{
			Build: func(channel.RuntimeBuildConfig) (channel.Runtime, channel.HandlerRuntime, error) {
				return runtimeControl, stubHandlerRuntime{}, nil
			},
		},
		Handler: channel.HandlerConfig{
			Build: func(channel.HandlerBuildConfig) (channel.MetaRollbackService, error) {
				return service, nil
			},
		},
		Now: func() time.Time {
			return time.Unix(1700000000, 0)
		},
	})
	if err != nil {
		t.Fatalf("channel.New() error = %v", err)
	}
	return got
}

func encodedTestChannelKey(id channel.ChannelID) channel.ChannelKey {
	encodedID := base64.RawURLEncoding.EncodeToString([]byte(id.ID))
	return channel.ChannelKey("channel/" + strconv.FormatUint(uint64(id.Type), 10) + "/" + encodedID)
}

type stubRuntimeControl struct {
	upsertErr  error
	removeErr  error
	closeCalls int
}

func (r *stubRuntimeControl) UpsertMeta(channel.Meta) error { return r.upsertErr }
func (r *stubRuntimeControl) RemoveChannel(channel.ChannelKey) error {
	return r.removeErr
}
func (r *stubRuntimeControl) Close() error {
	r.closeCalls++
	return nil
}

type stubHandlerBackedRuntime struct {
	store    *channelstore.Engine
	channels map[channel.ChannelKey]*stubHandlerBackedChannel
}

func newStubHandlerBackedRuntime(store *channelstore.Engine) *stubHandlerBackedRuntime {
	return &stubHandlerBackedRuntime{
		store:    store,
		channels: make(map[channel.ChannelKey]*stubHandlerBackedChannel),
	}
}

func (r *stubHandlerBackedRuntime) UpsertMeta(meta channel.Meta) error {
	key := encodedTestChannelKey(meta.ID)
	handle, ok := r.channels[key]
	if !ok {
		st := r.store.ForChannel(key, meta.ID)
		handle = &stubHandlerBackedChannel{
			id:    key,
			meta:  meta,
			store: st,
			status: channel.ReplicaState{
				ChannelKey:  key,
				Role:        channel.ReplicaRoleLeader,
				Leader:      meta.Leader,
				Epoch:       meta.Epoch,
				CommitReady: true,
			},
		}
		handle.appendFn = func(_ context.Context, records []channel.Record) (channel.CommitResult, error) {
			base, err := st.Append(records)
			if err != nil {
				return channel.CommitResult{}, err
			}
			handle.status.HW = base + uint64(len(records))
			handle.status.LEO = handle.status.HW
			return channel.CommitResult{BaseOffset: base, NextCommitHW: handle.status.HW, RecordCount: len(records)}, nil
		}
		r.channels[key] = handle
		return nil
	}
	handle.meta = meta
	handle.status.Leader = meta.Leader
	handle.status.Epoch = meta.Epoch
	return nil
}

func (r *stubHandlerBackedRuntime) RemoveChannel(key channel.ChannelKey) error {
	delete(r.channels, key)
	return nil
}

func (r *stubHandlerBackedRuntime) Close() error { return nil }

func (r *stubHandlerBackedRuntime) Channel(key channel.ChannelKey) (channel.HandlerChannel, bool) {
	handle, ok := r.channels[key]
	return handle, ok
}

type stubHandlerBackedChannel struct {
	id       channel.ChannelKey
	meta     channel.Meta
	store    *channelstore.ChannelStore
	status   channel.ReplicaState
	appendFn func(context.Context, []channel.Record) (channel.CommitResult, error)
}

func (h *stubHandlerBackedChannel) ID() channel.ChannelKey       { return h.id }
func (h *stubHandlerBackedChannel) Meta() channel.Meta           { return h.meta }
func (h *stubHandlerBackedChannel) Status() channel.ReplicaState { return h.status }
func (h *stubHandlerBackedChannel) Append(ctx context.Context, records []channel.Record) (channel.CommitResult, error) {
	return h.appendFn(ctx, records)
}

func mustBuildRuntimeBackedCluster(t *testing.T, engine *channelstore.Engine, rt *stubHandlerBackedRuntime) channel.Cluster {
	t.Helper()

	got, err := channel.New(channel.Config{
		LocalNode:       1,
		Store:           engine,
		GenerationStore: &channelTestGenerationStore{},
		MessageIDs:      &channelTestMessageIDs{},
		Transport: channel.TransportConfig{
			Build: func(channel.TransportBuildConfig) (any, error) {
				return &stubTransportBuildValue{}, nil
			},
			BindFetchService: bindStubTransportFetchService,
		},
		Runtime: channel.RuntimeConfig{
			Build: func(channel.RuntimeBuildConfig) (channel.Runtime, channel.HandlerRuntime, error) {
				return rt, rt, nil
			},
		},
		Handler: channel.HandlerConfig{
			Build: buildTestHandler,
		},
		Now: func() time.Time {
			return time.Unix(1700000000, 0)
		},
	})
	if err != nil {
		t.Fatalf("channel.New() error = %v", err)
	}
	return got
}

type sequencedRuntimeControl struct {
	mu sync.Mutex

	olderMeta    channel.Meta
	olderErr     error
	olderReady   chan struct{}
	olderRelease chan struct{}
	olderOnce    sync.Once
	newerReady   chan struct{}
	newerOnce    sync.Once
	lastByKey    map[channel.ChannelKey]channel.Meta
}

func newSequencedRuntimeControl(olderMeta channel.Meta, olderErr error) *sequencedRuntimeControl {
	return &sequencedRuntimeControl{
		olderMeta:    olderMeta,
		olderErr:     olderErr,
		olderReady:   make(chan struct{}),
		olderRelease: make(chan struct{}),
		newerReady:   make(chan struct{}),
		lastByKey:    make(map[channel.ChannelKey]channel.Meta),
	}
}

func (r *sequencedRuntimeControl) UpsertMeta(meta channel.Meta) error {
	if meta.Key == "" {
		meta.Key = encodedTestChannelKey(meta.ID)
	}
	if meta.Epoch == r.olderMeta.Epoch && meta.LeaderEpoch == r.olderMeta.LeaderEpoch {
		r.olderOnce.Do(func() { close(r.olderReady) })
		<-r.olderRelease
		return r.olderErr
	}
	r.newerOnce.Do(func() { close(r.newerReady) })

	r.mu.Lock()
	r.lastByKey[meta.Key] = meta
	r.mu.Unlock()
	return nil
}

func (r *sequencedRuntimeControl) RemoveChannel(channel.ChannelKey) error { return nil }

func (r *sequencedRuntimeControl) waitForBlockedOlder(t *testing.T) {
	t.Helper()
	select {
	case <-r.olderReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for older runtime update to block")
	}
}

func (r *sequencedRuntimeControl) releaseOlder() {
	close(r.olderRelease)
}

func (r *sequencedRuntimeControl) waitForNewerAttempt() {
	select {
	case <-r.newerReady:
	case <-time.After(100 * time.Millisecond):
	}
}

func (r *sequencedRuntimeControl) LastMeta(key channel.ChannelKey) (channel.Meta, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	meta, ok := r.lastByKey[key]
	return meta, ok
}

type stubClosableRuntimeValue struct {
	closeCalls int
}

func (r *stubClosableRuntimeValue) Close() error {
	r.closeCalls++
	return nil
}

func (*stubClosableRuntimeValue) Channel(channel.ChannelKey) (channel.HandlerChannel, bool) {
	return nil, false
}

func (*stubClosableRuntimeValue) ServeFetch(context.Context, channelruntime.FetchRequestEnvelope) (channelruntime.FetchResponseEnvelope, error) {
	return channelruntime.FetchResponseEnvelope{}, nil
}

type stubTransportBuildValue struct {
	bindCalls    int
	boundService any
	bindErr      error
	closeCalls   int
}

func (t *stubTransportBuildValue) BindFetchService(service any) error {
	t.bindCalls++
	t.boundService = service
	return t.bindErr
}

func (t *stubTransportBuildValue) Close() error {
	t.closeCalls++
	return nil
}

type stubHandlerRuntime struct{}

func (stubHandlerRuntime) Channel(channel.ChannelKey) (channel.HandlerChannel, bool) {
	return nil, false
}

type stubMetaService struct {
	mu    sync.RWMutex
	metas map[channel.ChannelKey]channel.Meta

	closeCalls int
}

func newStubMetaService() *stubMetaService {
	return &stubMetaService{metas: make(map[channel.ChannelKey]channel.Meta)}
}

func (s *stubMetaService) ApplyMeta(meta channel.Meta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metas[meta.Key] = meta
	return nil
}

func (s *stubMetaService) Append(context.Context, channel.AppendRequest) (channel.AppendResult, error) {
	return channel.AppendResult{}, errors.New("not implemented")
}

func (s *stubMetaService) Fetch(context.Context, channel.FetchRequest) (channel.FetchResult, error) {
	return channel.FetchResult{}, errors.New("not implemented")
}

func (s *stubMetaService) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	return channel.ChannelRuntimeStatus{}, errors.New("not implemented")
}

func (s *stubMetaService) MetaSnapshot(key channel.ChannelKey) (channel.Meta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	meta, ok := s.metas[key]
	return meta, ok
}

func (s *stubMetaService) RestoreMeta(key channel.ChannelKey, meta channel.Meta, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ok {
		delete(s.metas, key)
		return
	}
	s.metas[key] = meta
}

func (s *stubMetaService) ForceSetMeta(key channel.ChannelKey, meta channel.Meta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metas[key] = meta
}

func (s *stubMetaService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCalls++
	return nil
}
