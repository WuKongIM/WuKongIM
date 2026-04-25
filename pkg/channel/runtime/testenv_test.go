package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	"github.com/stretchr/testify/require"
)

func newTestRuntime(t *testing.T) *runtime {
	return newTestRuntimeWithOptions(t)
}

type testRuntimeOption func(*testRuntimeOptions)

type testRuntimeOptions struct {
	generationStoreDelay time.Duration
	replicaFactoryDelay  time.Duration
	maxChannels          int
	tombstoneErrors      map[core.ChannelKey]error
	becomeLeaderErrors   map[core.ChannelKey]error
	tombstoneTTL         time.Duration
	tombstoneCleanup     time.Duration
	beforeTombstoneAdd   func()
	tombstoneDropHook    func()
}

func withGenerationStoreDelay(delay time.Duration) testRuntimeOption {
	return func(opts *testRuntimeOptions) {
		opts.generationStoreDelay = delay
	}
}

func withReplicaFactoryDelay(delay time.Duration) testRuntimeOption {
	return func(opts *testRuntimeOptions) {
		opts.replicaFactoryDelay = delay
	}
}

func withMaxChannels(limit int) testRuntimeOption {
	return func(opts *testRuntimeOptions) {
		opts.maxChannels = limit
	}
}

func withTombstoneError(key core.ChannelKey, err error) testRuntimeOption {
	return func(opts *testRuntimeOptions) {
		if opts.tombstoneErrors == nil {
			opts.tombstoneErrors = make(map[core.ChannelKey]error)
		}
		opts.tombstoneErrors[key] = err
	}
}

func withBecomeLeaderError(key core.ChannelKey, err error) testRuntimeOption {
	return func(opts *testRuntimeOptions) {
		if opts.becomeLeaderErrors == nil {
			opts.becomeLeaderErrors = make(map[core.ChannelKey]error)
		}
		opts.becomeLeaderErrors[key] = err
	}
}

func withTombstoneTTL(ttl time.Duration) testRuntimeOption {
	return func(opts *testRuntimeOptions) {
		opts.tombstoneTTL = ttl
	}
}

func withTombstoneCleanupInterval(interval time.Duration) testRuntimeOption {
	return func(opts *testRuntimeOptions) {
		opts.tombstoneCleanup = interval
	}
}

func withTombstoneAddHook(fn func()) testRuntimeOption {
	return func(opts *testRuntimeOptions) {
		opts.beforeTombstoneAdd = fn
	}
}

func withTombstoneDropHook(fn func()) testRuntimeOption {
	return func(opts *testRuntimeOptions) {
		opts.tombstoneDropHook = fn
	}
}

func newTestRuntimeWithOptions(t *testing.T, options ...testRuntimeOption) *runtime {
	t.Helper()

	opts := testRuntimeOptions{}
	for _, apply := range options {
		apply(&opts)
	}

	store := newFakeGenerationStore()
	store.delay = opts.generationStoreDelay
	factory := newFakeReplicaFactory()
	factory.delay = opts.replicaFactoryDelay
	factory.tombstoneErrors = opts.tombstoneErrors
	factory.becomeLeaderErrors = opts.becomeLeaderErrors
	ttl := 30 * time.Second
	if opts.tombstoneTTL > 0 {
		ttl = opts.tombstoneTTL
	}

	rt, err := New(Config{
		LocalNode:       1,
		ReplicaFactory:  factory,
		GenerationStore: store,
		Limits: Limits{
			MaxChannels: opts.maxChannels,
		},
		Tombstones: TombstonePolicy{
			TombstoneTTL:    ttl,
			CleanupInterval: opts.tombstoneCleanup,
		},
		Now: time.Now,
	})
	require.NoError(t, err)

	impl, ok := rt.(*runtime)
	require.True(t, ok)
	impl.tombstones.beforeAdd = opts.beforeTombstoneAdd
	impl.tombstones.onDrop = opts.tombstoneDropHook
	t.Cleanup(func() {
		impl.stopTombstoneCleanup()
	})
	return impl
}

func testMeta(key string) core.Meta {
	return core.Meta{
		Key:      core.ChannelKey(key),
		Epoch:    1,
		Leader:   1,
		Replicas: []core.NodeID{1, 2},
		ISR:      []core.NodeID{1, 2},
		MinISR:   1,
	}
}

type fakeGenerationStore struct {
	mu     sync.Mutex
	values map[core.ChannelKey]uint64
	stored map[core.ChannelKey]uint64
	delay  time.Duration
}

func newFakeGenerationStore() *fakeGenerationStore {
	return &fakeGenerationStore{
		values: make(map[core.ChannelKey]uint64),
		stored: make(map[core.ChannelKey]uint64),
	}
}

func (s *fakeGenerationStore) Load(key core.ChannelKey) (uint64, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key], nil
}

func (s *fakeGenerationStore) Store(key core.ChannelKey, generation uint64) error {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = generation
	s.stored[key] = generation
	return nil
}

type fakeReplicaFactory struct {
	mu                 sync.Mutex
	created            []ChannelConfig
	replicas           []*fakeReplica
	delay              time.Duration
	tombstoneErrors    map[core.ChannelKey]error
	becomeLeaderErrors map[core.ChannelKey]error
}

func newFakeReplicaFactory() *fakeReplicaFactory {
	return &fakeReplicaFactory{}
}

func (f *fakeReplicaFactory) New(cfg ChannelConfig) (replica.Replica, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	r := &fakeReplica{
		state:           core.ReplicaState{ChannelKey: cfg.ChannelKey, Epoch: cfg.Meta.Epoch, Leader: cfg.Meta.Leader, Role: core.ReplicaRoleFollower, CommitReady: true},
		tombstoneErr:    f.tombstoneErrors[cfg.ChannelKey],
		becomeLeaderErr: f.becomeLeaderErrors[cfg.ChannelKey],
	}
	f.created = append(f.created, cfg)
	f.replicas = append(f.replicas, r)
	return r, nil
}

type fakeReplica struct {
	mu              sync.Mutex
	state           core.ReplicaState
	tombstone       int
	tombstoneErr    error
	becomeLeaderErr error
	closeCount      int
}

func (r *fakeReplica) ApplyMeta(meta core.Meta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.ChannelKey = meta.Key
	r.state.Epoch = meta.Epoch
	r.state.Leader = meta.Leader
	return nil
}

func (r *fakeReplica) BecomeLeader(meta core.Meta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.becomeLeaderErr != nil {
		return r.becomeLeaderErr
	}
	r.state.ChannelKey = meta.Key
	r.state.Epoch = meta.Epoch
	r.state.Leader = meta.Leader
	r.state.Role = core.ReplicaRoleLeader
	return nil
}

func (r *fakeReplica) BecomeFollower(meta core.Meta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.ChannelKey = meta.Key
	r.state.Epoch = meta.Epoch
	r.state.Leader = meta.Leader
	r.state.Role = core.ReplicaRoleFollower
	return nil
}

func (r *fakeReplica) Tombstone() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tombstoneErr != nil {
		return r.tombstoneErr
	}
	r.tombstone++
	r.state.Role = core.ReplicaRoleTombstoned
	return nil
}

func (r *fakeReplica) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeCount++
	return nil
}

func (r *fakeReplica) InstallSnapshot(context.Context, core.Snapshot) error {
	return nil
}

func (r *fakeReplica) Append(context.Context, []core.Record) (core.CommitResult, error) {
	return core.CommitResult{}, nil
}

func (r *fakeReplica) Fetch(context.Context, core.ReplicaFetchRequest) (core.ReplicaFetchResult, error) {
	return core.ReplicaFetchResult{}, nil
}

func (r *fakeReplica) ApplyFetch(context.Context, core.ReplicaApplyFetchRequest) error {
	return nil
}

func (r *fakeReplica) ApplyProgressAck(context.Context, core.ReplicaProgressAckRequest) error {
	return nil
}

func (r *fakeReplica) ApplyReconcileProof(context.Context, core.ReplicaReconcileProof) error {
	return nil
}

func (r *fakeReplica) Status() core.ReplicaState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *fakeReplica) closeCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeCount
}
