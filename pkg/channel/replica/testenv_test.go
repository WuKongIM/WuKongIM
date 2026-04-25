package replica

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

type fakeLogStore struct {
	mu            sync.Mutex
	records       []channel.Record
	leo           uint64
	appendCount   int
	syncCount     int
	truncateCalls []uint64
	appendSignal  chan uint64
	syncStarted   chan struct{}
	syncContinue  chan struct{}
}

func (f *fakeLogStore) LEO() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leo
}

func (f *fakeLogStore) Append(records []channel.Record) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.appendCount++
	base := f.leo
	for _, record := range records {
		f.records = append(f.records, cloneRecord(record))
		f.leo++
	}
	if f.appendSignal != nil {
		select {
		case f.appendSignal <- f.leo:
		default:
		}
	}
	return base, nil
}

func (f *fakeLogStore) Read(from uint64, maxBytes int) ([]channel.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if from >= uint64(len(f.records)) {
		return nil, nil
	}

	var (
		total int
		out   []channel.Record
	)
	for i := from; i < uint64(len(f.records)); i++ {
		record := cloneRecord(f.records[i])
		if len(out) > 0 && total+record.SizeBytes > maxBytes {
			break
		}
		out = append(out, record)
		total += record.SizeBytes
		if len(out) == 1 && total > maxBytes {
			break
		}
	}
	return out, nil
}

func (f *fakeLogStore) Truncate(to uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.truncateCalls = append(f.truncateCalls, to)
	if to < uint64(len(f.records)) {
		f.records = append([]channel.Record(nil), f.records[:to]...)
	} else if to == 0 {
		f.records = nil
	}
	f.leo = to
	return nil
}

func (f *fakeLogStore) Sync() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncCount++
	if f.syncStarted != nil {
		select {
		case f.syncStarted <- struct{}{}:
		default:
		}
	}
	if f.syncContinue != nil {
		<-f.syncContinue
	}
	return nil
}

type fakeCheckpointStore struct {
	mu         sync.Mutex
	checkpoint channel.Checkpoint
	loadErr    error
	stored     []channel.Checkpoint
}

func (f *fakeCheckpointStore) Load() (channel.Checkpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkpoint, f.loadErr
}

func (f *fakeCheckpointStore) Store(checkpoint channel.Checkpoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkpoint = checkpoint
	f.stored = append(f.stored, checkpoint)
	return nil
}

func (f *fakeCheckpointStore) lastStored() channel.Checkpoint {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.stored) == 0 {
		return channel.Checkpoint{}
	}
	return f.stored[len(f.stored)-1]
}

type fakeEpochHistoryStore struct {
	mu       sync.Mutex
	points   []channel.EpochPoint
	loadErr  error
	appended []channel.EpochPoint
	truncate []uint64
}

func (f *fakeEpochHistoryStore) Load() ([]channel.EpochPoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]channel.EpochPoint(nil), f.points...), f.loadErr
}

func (f *fakeEpochHistoryStore) Append(point channel.EpochPoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.points) > 0 {
		last := f.points[len(f.points)-1]
		switch {
		case point.Epoch > last.Epoch:
			if point.StartOffset < last.StartOffset {
				return channel.ErrCorruptState
			}
			f.points = append(f.points, point)
		case point.Epoch == last.Epoch && point.StartOffset == last.StartOffset:
			// idempotent
		default:
			return channel.ErrCorruptState
		}
	} else {
		f.points = append(f.points, point)
	}
	f.appended = append(f.appended, point)
	return nil
}

func (f *fakeEpochHistoryStore) TruncateTo(leo uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	end := 0
	for end < len(f.points) && f.points[end].StartOffset <= leo {
		end++
	}
	f.points = append([]channel.EpochPoint(nil), f.points[:end]...)
	f.truncate = append(f.truncate, leo)
	return nil
}

type fakeSnapshotApplier struct {
	mu        sync.Mutex
	installed []channel.Snapshot
}

func (f *fakeSnapshotApplier) InstallSnapshot(_ context.Context, snap channel.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installed = append(f.installed, cloneSnapshot(snap))
	return nil
}

type fakeApplyFetchStore struct {
	calls     int
	leo       uint64
	lastReq   channel.ApplyFetchStoreRequest
	returnErr error
	returnLEO uint64
}

func (f *fakeApplyFetchStore) StoreApplyFetch(req channel.ApplyFetchStoreRequest) (uint64, error) {
	f.calls++
	f.lastReq = cloneApplyFetchStoreRequest(req)
	if f.returnErr != nil {
		return 0, f.returnErr
	}
	if f.returnLEO != 0 {
		return f.returnLEO, nil
	}
	f.leo += uint64(len(req.Records))
	return f.leo, nil
}

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newManualClock(now time.Time) *manualClock {
	return &manualClock{now: now}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type testEnv struct {
	t           testing.TB
	localNode   channel.NodeID
	log         *fakeLogStore
	checkpoints *fakeCheckpointStore
	history     *fakeEpochHistoryStore
	snapshots   *fakeSnapshotApplier
	applyFetch  *fakeApplyFetchStore
	clock       *manualClock
	// peerProgress captures the quorum-visible offsets future recovery reconcile
	// should use when deciding whether tail above CheckpointHW is safe to keep.
	peerProgress map[channel.NodeID]uint64
	peerProofs   map[channel.NodeID]channel.ReplicaReconcileProof
	replica      *replica
}

type fakeReconcileProbeSource struct {
	env *testEnv
}

func (f fakeReconcileProbeSource) ProbeQuorum(_ context.Context, meta channel.Meta, _ channel.ReplicaState) ([]channel.ReplicaReconcileProof, error) {
	proofs := make([]channel.ReplicaReconcileProof, 0, len(meta.ISR))
	for _, id := range meta.ISR {
		if id == 0 || id == f.env.localNode {
			continue
		}
		if proof, ok := f.env.peerProofs[id]; ok {
			if proof.ChannelKey == "" {
				proof.ChannelKey = meta.Key
			}
			if proof.Epoch == 0 {
				proof.Epoch = meta.Epoch
			}
			if proof.ReplicaID == 0 {
				proof.ReplicaID = id
			}
			proofs = append(proofs, proof)
			continue
		}
		offset, ok := f.env.peerProgress[id]
		if !ok {
			continue
		}
		proofs = append(proofs, channel.ReplicaReconcileProof{
			ChannelKey:   meta.Key,
			Epoch:        meta.Epoch,
			ReplicaID:    id,
			OffsetEpoch:  meta.Epoch,
			LogEndOffset: offset,
			CheckpointHW: offset,
		})
	}
	return proofs, nil
}

func newTestEnv(t testing.TB) *testEnv {
	t.Helper()
	return &testEnv{
		t:           t,
		localNode:   1,
		log:         &fakeLogStore{appendSignal: make(chan uint64, 8)},
		checkpoints: &fakeCheckpointStore{loadErr: channel.ErrEmptyState},
		history:     &fakeEpochHistoryStore{loadErr: channel.ErrEmptyState},
		snapshots:   &fakeSnapshotApplier{},
		clock:       newManualClock(time.Unix(1_700_000_000, 0).UTC()),
	}
}

func (e *testEnv) config() ReplicaConfig {
	cfg := ReplicaConfig{
		LocalNode:         e.localNode,
		LogStore:          e.log,
		CheckpointStore:   e.checkpoints,
		EpochHistoryStore: e.history,
		SnapshotApplier:   e.snapshots,
		Now:               e.clock.Now,
	}
	if e.applyFetch != nil {
		cfg.ApplyFetchStore = e.applyFetch
	}
	if len(e.peerProgress) > 0 || len(e.peerProofs) > 0 {
		cfg.ReconcileProbeSource = fakeReconcileProbeSource{env: e}
	}
	return cfg
}

func newReplicaFromEnv(t testing.TB, env *testEnv) *replica {
	t.Helper()
	got, err := NewReplica(env.config())
	if err != nil {
		t.Fatalf("NewReplica() error = %v", err)
	}
	r, ok := got.(*replica)
	if !ok {
		t.Fatalf("NewReplica() type = %T", got)
	}
	env.replica = r
	return r
}

func requireReplicaStateBoolField(tb testing.TB, state channel.ReplicaState, field string, want bool) {
	tb.Helper()

	got := reflect.ValueOf(state).FieldByName(field)
	if !got.IsValid() {
		tb.Fatalf("ReplicaState.%s missing; dual-watermark contract not implemented", field)
	}
	if got.Kind() != reflect.Bool {
		tb.Fatalf("ReplicaState.%s kind = %s, want bool", field, got.Kind())
	}
	if actual := got.Bool(); actual != want {
		tb.Fatalf("ReplicaState.%s = %v, want %v", field, actual, want)
	}
}

func requireReplicaStateUint64Field(tb testing.TB, state channel.ReplicaState, field string, want uint64) {
	tb.Helper()

	got := reflect.ValueOf(state).FieldByName(field)
	if !got.IsValid() {
		tb.Fatalf("ReplicaState.%s missing; dual-watermark contract not implemented", field)
	}
	if got.Kind() != reflect.Uint64 {
		tb.Fatalf("ReplicaState.%s kind = %s, want uint64", field, got.Kind())
	}
	if actual := got.Uint(); actual != want {
		tb.Fatalf("ReplicaState.%s = %d, want %d", field, actual, want)
	}
}

func setReplicaStateOptionalBoolField(tb testing.TB, state *channel.ReplicaState, field string, want bool) {
	tb.Helper()

	got := reflect.ValueOf(state).Elem().FieldByName(field)
	if !got.IsValid() {
		return
	}
	if got.Kind() != reflect.Bool {
		tb.Fatalf("ReplicaState.%s kind = %s, want bool", field, got.Kind())
	}
	got.SetBool(want)
}

func setReplicaStateOptionalUint64Field(tb testing.TB, state *channel.ReplicaState, field string, want uint64) {
	tb.Helper()

	got := reflect.ValueOf(state).Elem().FieldByName(field)
	if !got.IsValid() {
		return
	}
	if got.Kind() != reflect.Uint64 {
		tb.Fatalf("ReplicaState.%s kind = %s, want uint64", field, got.Kind())
	}
	got.SetUint(want)
}

func newTestReplica(t testing.TB) *replica {
	t.Helper()
	return newReplicaFromEnv(t, newTestEnv(t))
}

func newLeaderReplica(t testing.TB) *replica {
	t.Helper()
	env := newTestEnv(t)
	env.replica = newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(7, 1, 1)
	env.replica.mustApplyMeta(t, meta)
	if err := env.replica.BecomeLeader(meta); err != nil {
		t.Fatalf("BecomeLeader() error = %v", err)
	}
	return env.replica
}

func newFollowerReplica(t testing.TB) *replica {
	t.Helper()
	return newFollowerEnv(t).replica
}

func newRecoveredLeaderEnv(t testing.TB) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	env.log.leo = 4
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 6, LogStartOffset: 0, HW: 4}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 6, StartOffset: 0}}
	return env
}

func newFetchEnvWithHistory(t testing.TB) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	env.log.records = []channel.Record{{Payload: []byte("r0"), SizeBytes: 1}, {Payload: []byte("r1"), SizeBytes: 1}, {Payload: []byte("r2"), SizeBytes: 1}, {Payload: []byte("r3"), SizeBytes: 1}}
	env.log.leo = 4
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 3, LogStartOffset: 0, HW: 4}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 3, StartOffset: 0}}
	env.replica = newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(7, 1, 1)
	env.replica.mustApplyMeta(t, meta)
	if err := env.replica.BecomeLeader(meta); err != nil {
		t.Fatalf("BecomeLeader() error = %v", err)
	}
	env.log.records = append(env.log.records, channel.Record{Payload: []byte("r4"), SizeBytes: 1}, channel.Record{Payload: []byte("r5"), SizeBytes: 1})
	env.log.leo = 6
	env.replica.state.LEO = 6
	env.replica.publishStateLocked()
	return env
}

func newFollowerEnv(t testing.TB) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	env.localNode = 2
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	env.replica = newReplicaFromEnv(t, env)
	env.replica.mustApplyMeta(t, activeMeta(7, 1))
	if err := env.replica.BecomeFollower(activeMeta(7, 1)); err != nil {
		t.Fatalf("BecomeFollower() error = %v", err)
	}
	return env
}

func activeMeta(epoch uint64, leader channel.NodeID) channel.Meta {
	return activeMetaWithMinISR(epoch, leader, 2)
}

func activeMetaWithMinISR(epoch uint64, leader channel.NodeID, minISR int) channel.Meta {
	return channel.Meta{
		Key:        "group-10",
		Epoch:      epoch,
		Leader:     leader,
		Replicas:   []channel.NodeID{1, 2, 3},
		ISR:        []channel.NodeID{1, 2, 3},
		MinISR:     minISR,
		LeaseUntil: time.Unix(1_700_000_300, 0).UTC(),
	}
}

func (r *replica) mustApplyMeta(t testing.TB, meta channel.Meta) {
	t.Helper()
	if err := r.ApplyMeta(meta); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
}

type threeReplicaCluster struct {
	leader    *replica
	follower2 *replica
	follower3 *replica
}

func newThreeReplicaCluster(t testing.TB) *threeReplicaCluster {
	return newThreeReplicaClusterWithMinISR(t, 3)
}

func newThreeReplicaClusterWithMinISR(t testing.TB, minISR int) *threeReplicaCluster {
	t.Helper()
	meta := activeMetaWithMinISR(7, 1, minISR)

	leaderEnv := newTestEnv(t)
	leaderEnv.replica = newReplicaFromEnv(t, leaderEnv)
	leaderEnv.replica.mustApplyMeta(t, meta)
	if err := leaderEnv.replica.BecomeLeader(meta); err != nil {
		t.Fatalf("leader BecomeLeader() error = %v", err)
	}

	follower2Env := newTestEnv(t)
	follower2Env.localNode = 2
	follower2Env.replica = newReplicaFromEnv(t, follower2Env)
	follower2Env.replica.mustApplyMeta(t, meta)
	if err := follower2Env.replica.BecomeFollower(meta); err != nil {
		t.Fatalf("follower2 BecomeFollower() error = %v", err)
	}

	follower3Env := newTestEnv(t)
	follower3Env.localNode = 3
	follower3Env.replica = newReplicaFromEnv(t, follower3Env)
	follower3Env.replica.mustApplyMeta(t, meta)
	if err := follower3Env.replica.BecomeFollower(meta); err != nil {
		t.Fatalf("follower3 BecomeFollower() error = %v", err)
	}

	return &threeReplicaCluster{leader: leaderEnv.replica, follower2: follower2Env.replica, follower3: follower3Env.replica}
}

func (c *threeReplicaCluster) replicateOnce(t testing.TB, follower *replica) {
	t.Helper()
	req := channel.ReplicaFetchRequest{ChannelKey: c.leader.state.ChannelKey, Epoch: c.leader.state.Epoch, ReplicaID: follower.localNode, FetchOffset: follower.state.LEO, OffsetEpoch: follower.state.Epoch, MaxBytes: 1024}
	result, err := c.leader.Fetch(context.Background(), req)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if err := follower.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{ChannelKey: req.ChannelKey, Epoch: result.Epoch, Leader: c.leader.localNode, TruncateTo: result.TruncateTo, Records: result.Records, LeaderHW: result.HW}); err != nil {
		t.Fatalf("ApplyFetch() error = %v", err)
	}
	_, err = c.leader.Fetch(context.Background(), channel.ReplicaFetchRequest{ChannelKey: req.ChannelKey, Epoch: result.Epoch, ReplicaID: follower.localNode, FetchOffset: follower.state.LEO, OffsetEpoch: follower.state.Epoch, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("ack Fetch() error = %v", err)
	}
}

func waitForLogAppend(t testing.TB, log *fakeLogStore, want uint64) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case got := <-log.appendSignal:
			if got == want {
				return
			}
		case <-deadline:
			log.mu.Lock()
			got := log.leo
			log.mu.Unlock()
			t.Fatalf("log LEO = %d, want %d", got, want)
			return
		}
	}
}

func cloneRecord(record channel.Record) channel.Record {
	record.Payload = append([]byte(nil), record.Payload...)
	return record
}

func cloneSnapshot(snap channel.Snapshot) channel.Snapshot {
	snap.Payload = append([]byte(nil), snap.Payload...)
	return snap
}

func cloneApplyFetchStoreRequest(req channel.ApplyFetchStoreRequest) channel.ApplyFetchStoreRequest {
	cloned := channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: req.PreviousCommittedHW,
		Records:             make([]channel.Record, 0, len(req.Records)),
	}
	for _, record := range req.Records {
		cloned.Records = append(cloned.Records, cloneRecord(record))
	}
	if req.Checkpoint != nil {
		value := *req.Checkpoint
		cloned.Checkpoint = &value
	}
	return cloned
}

func setReplicaConfigFieldIfPresent(cfg *ReplicaConfig, field string, value any) {
	switch field {
	case "AppendGroupCommitMaxWait":
		cfg.AppendGroupCommitMaxWait = value.(time.Duration)
	case "AppendGroupCommitMaxRecords":
		cfg.AppendGroupCommitMaxRecords = value.(int)
	case "AppendGroupCommitMaxBytes":
		cfg.AppendGroupCommitMaxBytes = value.(int)
	case "ApplyFetchStore":
		cfg.ApplyFetchStore = value.(*fakeApplyFetchStore)
	default:
		panic(fmt.Sprintf("unsupported field %s", field))
	}
}

func newReplicaFromEnvWithGroupCommit(t testing.TB, env *testEnv, maxWait time.Duration, maxRecords, maxBytes int) *replica {
	t.Helper()
	cfg := env.config()
	cfg.AppendGroupCommitMaxWait = maxWait
	cfg.AppendGroupCommitMaxRecords = maxRecords
	cfg.AppendGroupCommitMaxBytes = maxBytes
	got, err := NewReplica(cfg)
	if err != nil {
		t.Fatalf("NewReplica() error = %v", err)
	}
	r, ok := got.(*replica)
	if !ok {
		t.Fatalf("NewReplica() type = %T", got)
	}
	env.replica = r
	return r
}
