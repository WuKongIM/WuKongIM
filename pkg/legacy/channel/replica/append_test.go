package replica

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

type replicaRecordedLogEntry struct {
	level  string
	module string
	msg    string
	fields []wklog.Field
}

func (e replicaRecordedLogEntry) field(key string) (wklog.Field, bool) {
	for _, field := range e.fields {
		if field.Key == key {
			return field, true
		}
	}
	return wklog.Field{}, false
}

type replicaRecordingLoggerSink struct {
	mu      sync.Mutex
	entries []replicaRecordedLogEntry
}

type replicaRecordingLogger struct {
	module string
	base   []wklog.Field
	sink   *replicaRecordingLoggerSink
}

type corruptAppendDurableStore struct {
	durableReplicaStore
}

func (s corruptAppendDurableStore) AppendLeaderBatch(ctx context.Context, records []channel.Record) (uint64, uint64, error) {
	return 0, uint64(len(records) + 1), nil
}

func newReplicaRecordingLogger(module string) *replicaRecordingLogger {
	return &replicaRecordingLogger{module: module, sink: &replicaRecordingLoggerSink{}}
}

func (l *replicaRecordingLogger) Debug(msg string, fields ...wklog.Field) {
	l.log("DEBUG", msg, fields...)
}
func (l *replicaRecordingLogger) Info(msg string, fields ...wklog.Field) {
	l.log("INFO", msg, fields...)
}
func (l *replicaRecordingLogger) Warn(msg string, fields ...wklog.Field) {
	l.log("WARN", msg, fields...)
}
func (l *replicaRecordingLogger) Error(msg string, fields ...wklog.Field) {
	l.log("ERROR", msg, fields...)
}
func (l *replicaRecordingLogger) Fatal(msg string, fields ...wklog.Field) {
	l.log("FATAL", msg, fields...)
}

func (l *replicaRecordingLogger) Named(name string) wklog.Logger {
	if name == "" {
		return l
	}
	module := name
	if l.module != "" {
		module = l.module + "." + name
	}
	return &replicaRecordingLogger{module: module, base: append([]wklog.Field(nil), l.base...), sink: l.sink}
}

func (l *replicaRecordingLogger) With(fields ...wklog.Field) wklog.Logger {
	merged := append(append([]wklog.Field(nil), l.base...), fields...)
	return &replicaRecordingLogger{module: l.module, base: merged, sink: l.sink}
}

func (l *replicaRecordingLogger) Sync() error { return nil }

func (l *replicaRecordingLogger) log(level, msg string, fields ...wklog.Field) {
	if l == nil || l.sink == nil {
		return
	}
	entry := replicaRecordedLogEntry{
		level:  level,
		module: l.module,
		msg:    msg,
		fields: append(append([]wklog.Field(nil), l.base...), fields...),
	}
	l.sink.mu.Lock()
	defer l.sink.mu.Unlock()
	l.sink.entries = append(l.sink.entries, entry)
}

func (l *replicaRecordingLogger) entries() []replicaRecordedLogEntry {
	if l == nil || l.sink == nil {
		return nil
	}
	l.sink.mu.Lock()
	defer l.sink.mu.Unlock()
	out := make([]replicaRecordedLogEntry, len(l.sink.entries))
	copy(out, l.sink.entries)
	return out
}

func requireReplicaLogEntry(t *testing.T, logger *replicaRecordingLogger, level, module, event string) replicaRecordedLogEntry {
	t.Helper()
	for _, entry := range logger.entries() {
		if entry.level != level || entry.module != module {
			continue
		}
		field, ok := entry.field("event")
		if ok && field.Value == event {
			return entry
		}
	}
	t.Fatalf("log entry not found: level=%s module=%s event=%s entries=%#v", level, module, event, logger.entries())
	return replicaRecordedLogEntry{}
}

func requireReplicaFieldValue[T any](t *testing.T, entry replicaRecordedLogEntry, key string) T {
	t.Helper()
	field, ok := entry.field(key)
	require.True(t, ok, "field %q not found in entry %#v", key, entry)
	value, ok := field.Value.(T)
	require.True(t, ok, "field %q has type %T, want %T", key, field.Value, *new(T))
	return value
}

func TestAppendPipelineBatchesBurstAppends(t *testing.T) {
	env := newTestEnv(t)
	env.replica = newReplicaFromEnvWithGroupCommit(t, env, 25*time.Millisecond, 8, 1024)
	meta := activeMetaWithMinISR(7, 1, 1)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))

	done := make(chan error, 2)
	for _, payload := range []string{"a", "b"} {
		payload := payload
		go func() {
			_, err := env.replica.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte(payload), SizeBytes: 1}})
			done <- err
		}()
	}

	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("append request did not complete")
		}
	}

	require.Equal(t, 1, env.log.appendCount)
	require.Equal(t, uint64(2), env.replica.Status().LEO)
}

func TestEmitAppendBatchUsesReusableMetadataBuffers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := &replica{
		localNode: 1,
		now:       func() time.Time { return now },
		meta: channel.Meta{
			Key:         "group-10",
			Epoch:       7,
			LeaderEpoch: 70,
			Leader:      1,
			ISR:         []channel.NodeID{1},
			MinISR:      1,
			LeaseUntil:  now.Add(time.Minute),
		},
		state: channel.ReplicaState{
			ChannelKey:  "group-10",
			Epoch:       7,
			Leader:      1,
			Role:        channel.ReplicaRoleLeader,
			CommitReady: true,
		},
		roleGeneration: 1,
		appendGroupCommit: appendGroupCommitConfig{
			maxWait:    time.Hour,
			maxRecords: 16,
			maxBytes:   1024,
		},
		appendRequests: make(map[uint64]*appendRequest, 4),
		appendEffects:  make(chan appendLeaderBatchEffect, 1024),
	}

	ctx := context.Background()
	reqs := make([]appendRequest, 4)
	waiters := make([]appendWaiter, len(reqs))
	pending := make([]*appendRequest, len(reqs))
	for i := range reqs {
		req := &reqs[i]
		req.requestID = uint64(i + 1)
		req.ctx = ctx
		req.batch = []channel.Record{{SizeBytes: 1}}
		req.byteCount = 1
		req.commitMode = channel.CommitModeLocal
		req.waiter = &waiters[i]
		req.enqueuedAt = now
		waiters[i].request = req
		pending[i] = req
		r.appendRequests[req.requestID] = req
	}

	allocs := testing.AllocsPerRun(1000, func() {
		for i := range reqs {
			reqs[i].stage = appendRequestQueued
			reqs[i].completed = false
		}
		r.appendPending = pending[:]
		r.emitAppendBatchLocked()

		var effect appendLeaderBatchEffect
		select {
		case effect = <-r.appendEffects:
		default:
			t.Fatal("append batch effect was not emitted")
		}
		_, matched, err := r.takeAppendInFlightResultLocked(machineLeaderAppendCommittedEvent{
			EffectID:   effect.EffectID,
			RequestIDs: effect.RequestIDs,
		})
		require.True(t, matched)
		require.NoError(t, err)
	})

	require.LessOrEqual(t, allocs, 2.0)
}

func TestEmitAppendBatchAvoidsCloningLoopOwnedPayloads(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := &replica{
		localNode: 1,
		now:       func() time.Time { return now },
		meta: channel.Meta{
			Key:         "group-10",
			Epoch:       7,
			LeaderEpoch: 70,
			Leader:      1,
			ISR:         []channel.NodeID{1},
			MinISR:      1,
			LeaseUntil:  now.Add(time.Minute),
		},
		state: channel.ReplicaState{
			ChannelKey:  "group-10",
			Epoch:       7,
			Leader:      1,
			Role:        channel.ReplicaRoleLeader,
			CommitReady: true,
		},
		roleGeneration: 1,
		appendGroupCommit: appendGroupCommitConfig{
			maxWait:    time.Hour,
			maxRecords: 16,
			maxBytes:   1024,
		},
		appendRequests: make(map[uint64]*appendRequest, 4),
		appendEffects:  make(chan appendLeaderBatchEffect, 1024),
	}

	ctx := context.Background()
	reqs := make([]appendRequest, 4)
	waiters := make([]appendWaiter, len(reqs))
	pending := make([]*appendRequest, len(reqs))
	for i := range reqs {
		payload := []byte{byte('a' + i)}
		req := &reqs[i]
		req.requestID = uint64(i + 1)
		req.ctx = ctx
		req.batch = []channel.Record{{Payload: payload, SizeBytes: len(payload)}}
		req.byteCount = len(payload)
		req.commitMode = channel.CommitModeLocal
		req.waiter = &waiters[i]
		req.enqueuedAt = now
		waiters[i].request = req
		pending[i] = req
		r.appendRequests[req.requestID] = req
	}

	allocs := testing.AllocsPerRun(1000, func() {
		for i := range reqs {
			reqs[i].stage = appendRequestQueued
			reqs[i].completed = false
		}
		r.appendPending = pending[:]
		r.emitAppendBatchLocked()

		var effect appendLeaderBatchEffect
		select {
		case effect = <-r.appendEffects:
		default:
			t.Fatal("append batch effect was not emitted")
		}
		for i := range effect.Records {
			if len(effect.Records[i].Payload) != 1 || effect.Records[i].Payload[0] != byte('a'+i) {
				t.Fatalf("effect record %d payload = %q", i, effect.Records[i].Payload)
			}
		}
		_, matched, err := r.takeAppendInFlightResultLocked(machineLeaderAppendCommittedEvent{
			EffectID:   effect.EffectID,
			RequestIDs: effect.RequestIDs,
		})
		require.True(t, matched)
		require.NoError(t, err)
	})

	require.LessOrEqual(t, allocs, 2.0)
}

func TestAppendRequestPoolResetsReusableState(t *testing.T) {
	req := acquireAppendRequest()
	waiter := acquireAppendWaiter()
	req.requestID = 42
	req.ctx = context.Background()
	req.batch = []channel.Record{{Payload: []byte("x"), SizeBytes: 1}}
	req.byteCount = 1
	req.commitMode = channel.CommitModeLocal
	req.waiter = waiter
	req.stage = appendRequestWaitingQuorum
	req.completed = true
	waiter.request = req
	waiter.target = 9
	waiter.ch <- appendCompletion{result: channel.CommitResult{BaseOffset: 1}}

	releaseAppendRequest(req)
	reused := acquireAppendRequest()
	defer releaseAppendRequest(reused)

	if reused.requestID != 0 || reused.ctx != nil || reused.batch != nil || reused.waiter != nil || reused.completed {
		t.Fatalf("reused request was not reset: %+v", reused)
	}
	reusedWaiter := acquireAppendWaiter()
	defer releaseAppendWaiter(reusedWaiter)
	select {
	case completion := <-reusedWaiter.ch:
		t.Fatalf("reused waiter channel still had completion: %+v", completion)
	default:
	}
	if reusedWaiter.request != nil || reusedWaiter.target != 0 {
		t.Fatalf("reused waiter was not reset: %+v", reusedWaiter)
	}
}

func TestAppendQuorumModeWaitsForHWAdvance(t *testing.T) {
	env := newThreeReplicaCluster(t)
	done := make(chan channel.CommitResult, 1)

	go func() {
		res, err := env.leader.Append(channel.WithCommitMode(context.Background(), channel.CommitModeQuorum), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		if err == nil {
			done <- res
		}
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)

	select {
	case <-done:
		t.Fatal("append returned before quorum HW advanced")
	default:
	}

	env.replicateOnce(t, env.follower2)
	select {
	case <-done:
		t.Fatal("append returned before MinISR was satisfied")
	default:
	}

	env.replicateOnce(t, env.follower3)
	res := <-done
	require.Equal(t, uint64(1), res.NextCommitHW)
}

func TestAppendLocalModeCompletesAfterLeaderDurableAppend(t *testing.T) {
	env := newThreeReplicaCluster(t)
	done := make(chan channel.CommitResult, 1)

	go func() {
		res, err := env.leader.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		if err == nil {
			done <- res
		}
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("local commit append did not complete after durable write")
	}
	require.Equal(t, uint64(0), env.leader.Status().HW)
}

func TestAppendRejectsReplicaThatIsNotLeader(t *testing.T) {
	r := newFollowerReplica(t)
	_, err := r.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
	require.ErrorIs(t, err, channel.ErrNotLeader)
}

func TestReplicaFenceRejectsAppendDespiteStaleHandlerMeta(t *testing.T) {
	r := newLeaderReplica(t)
	fenced := activeMetaWithMinISR(7, 1, 1)
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-replica-fence",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, r.ApplyMeta(fenced))

	_, err := r.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
	require.ErrorIs(t, err, channel.ErrWriteFenced)
}

func TestFenceAndDrainRejectsNewAppends(t *testing.T) {
	r := newLeaderReplica(t)
	fenced := activeMetaWithMinISR(7, 1, 1)
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-drain",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, r.ApplyMeta(fenced))

	drain, err := r.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
		ChannelKey:           fenced.Key,
		WriteFenceToken:      "task-drain",
		WriteFenceVersion:    1,
		ExpectedChannelEpoch: 7,
		ExpectedLeaderEpoch:  0,
		ExpectedLeader:       1,
	})
	require.NoError(t, err)
	require.Equal(t, fenced.Key, drain.ChannelKey)
	require.Equal(t, uint64(7), drain.ChannelEpoch)
	require.Equal(t, uint64(1), drain.WriteFenceVersion)

	_, err = r.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
	require.ErrorIs(t, err, channel.ErrWriteFenced)
}

func TestFenceAndDrainWaitsForInflightAppend(t *testing.T) {
	r := newLeaderReplica(t)
	r.durableMu.Lock()
	appendDone := make(chan error, 1)
	go func() {
		_, err := r.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		appendDone <- err
	}()
	require.Eventually(t, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.appendInFlightEffectID != 0
	}, time.Second, time.Millisecond)

	fenced := activeMetaWithMinISR(7, 1, 1)
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-inflight-drain",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, r.ApplyMeta(fenced))
	drainDone := make(chan error, 1)
	go func() {
		_, err := r.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
			ChannelKey:           fenced.Key,
			WriteFenceToken:      "task-inflight-drain",
			WriteFenceVersion:    1,
			ExpectedChannelEpoch: 7,
			ExpectedLeader:       1,
		})
		drainDone <- err
	}()

	select {
	case err := <-drainDone:
		r.durableMu.Unlock()
		t.Fatalf("FenceAndDrain returned before in-flight append completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	r.durableMu.Unlock()
	require.Error(t, <-appendDone)
	require.NoError(t, <-drainDone)
}

func TestFenceAndDrainWaitsForQuorumWaitersAndCheckpoint(t *testing.T) {
	env := newThreeReplicaCluster(t)
	appendDone := make(chan appendTestResult, 1)
	go func() {
		res, err := env.leader.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		appendDone <- appendTestResult{result: res, err: err}
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)
	waitForLeaderLEO(t, env.leader, 1)
	env.replicateOnce(t, env.follower2)

	fenced := activeMetaWithMinISR(7, 1, 3)
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-quorum-drain",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, env.leader.ApplyMeta(fenced))

	drainDone := make(chan channel.DrainResult, 1)
	drainErr := make(chan error, 1)
	go func() {
		drain, err := env.leader.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
			ChannelKey:           fenced.Key,
			WriteFenceToken:      "task-quorum-drain",
			WriteFenceVersion:    1,
			ExpectedChannelEpoch: 7,
			ExpectedLeaderEpoch:  fenced.LeaderEpoch,
			ExpectedLeader:       1,
		})
		if err != nil {
			drainErr <- err
			return
		}
		drainDone <- drain
	}()

	select {
	case drain := <-drainDone:
		t.Fatalf("FenceAndDrain returned before quorum waiter completed: %+v", drain)
	case err := <-drainErr:
		t.Fatalf("FenceAndDrain failed before quorum waiter completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	env.replicateOnce(t, env.follower3)
	gotAppend := receiveAppendResult(t, appendDone, "append did not complete after final follower replication")
	require.NoError(t, gotAppend.err)
	gotDrain := receiveDrainResult(t, drainDone, drainErr, "drain did not complete after quorum waiter and checkpoint settled")
	require.Equal(t, uint64(1), gotDrain.LEO)
	require.Equal(t, uint64(1), gotDrain.HW)
	require.Equal(t, gotDrain.HW, gotDrain.CheckpointHW)
}

func TestFenceAndDrainWaitsForLocalCommitTailToBecomeStable(t *testing.T) {
	env := newThreeReplicaCluster(t)
	appendDone := make(chan appendTestResult, 1)
	go func() {
		res, err := env.leader.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		appendDone <- appendTestResult{result: res, err: err}
	}()
	gotAppend := receiveAppendResult(t, appendDone, "local commit append did not complete")
	require.NoError(t, gotAppend.err)
	require.Equal(t, uint64(0), gotAppend.result.BaseOffset)
	require.Equal(t, uint64(1), env.leader.Status().LEO)
	require.Equal(t, uint64(0), env.leader.Status().HW)

	fenced := activeMetaWithMinISR(7, 1, 3)
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-local-tail-drain",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, env.leader.ApplyMeta(fenced))

	drainDone := make(chan channel.DrainResult, 1)
	drainErr := make(chan error, 1)
	go func() {
		drain, err := env.leader.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
			ChannelKey:           fenced.Key,
			WriteFenceToken:      "task-local-tail-drain",
			WriteFenceVersion:    1,
			ExpectedChannelEpoch: 7,
			ExpectedLeaderEpoch:  fenced.LeaderEpoch,
			ExpectedLeader:       1,
		})
		if err != nil {
			drainErr <- err
			return
		}
		drainDone <- drain
	}()

	select {
	case drain := <-drainDone:
		t.Fatalf("FenceAndDrain returned before local tail became quorum-stable: %+v", drain)
	case err := <-drainErr:
		t.Fatalf("FenceAndDrain failed before local tail became quorum-stable: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	env.replicateOnce(t, env.follower2)
	env.replicateOnce(t, env.follower3)
	gotDrain := receiveDrainResult(t, drainDone, drainErr, "drain did not complete after local tail became stable")
	require.Equal(t, uint64(1), gotDrain.LEO)
	require.Equal(t, uint64(1), gotDrain.HW)
	require.Equal(t, gotDrain.HW, gotDrain.CheckpointHW)
}

func TestFenceAndDrainAllowsFencedLeaderWhenMigrationFenceMatches(t *testing.T) {
	env := newTestEnv(t)
	meta := activeMetaWithMinISR(7, 1, 1)
	meta.LeaseUntil = env.clock.Now().Add(time.Second)
	env.replica = newReplicaFromEnv(t, env)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))
	env.clock.Advance(2 * time.Second)
	_, err := env.replica.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
	require.ErrorIs(t, err, channel.ErrLeaseExpired)

	fenced := meta
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-fenced-leader-drain",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, env.replica.ApplyMeta(fenced))
	drain, err := env.replica.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
		ChannelKey:           fenced.Key,
		WriteFenceToken:      "task-fenced-leader-drain",
		WriteFenceVersion:    1,
		ExpectedChannelEpoch: 7,
		ExpectedLeaderEpoch:  fenced.LeaderEpoch,
		ExpectedLeader:       1,
	})

	require.NoError(t, err)
	require.Equal(t, fenced.Key, drain.ChannelKey)
	require.Equal(t, fenced.LeaderEpoch, drain.LeaderEpoch)
	require.Equal(t, uint64(1), drain.WriteFenceVersion)
}

func TestFenceAndDrainRejectsNonLeaderOrFenceMismatch(t *testing.T) {
	follower := newFollowerReplica(t)
	_, err := follower.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
		ChannelKey:           "group-10",
		WriteFenceToken:      "task",
		WriteFenceVersion:    1,
		ExpectedChannelEpoch: 7,
		ExpectedLeader:       1,
	})
	require.ErrorIs(t, err, channel.ErrNotLeader)

	leader := newLeaderReplica(t)
	fenced := activeMetaWithMinISR(7, 1, 1)
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-expected",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, leader.ApplyMeta(fenced))
	_, err = leader.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
		ChannelKey:           fenced.Key,
		WriteFenceToken:      "task-other",
		WriteFenceVersion:    1,
		ExpectedChannelEpoch: 7,
		ExpectedLeader:       1,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)
}

func TestFenceAndDrainRequiresExactLeaderEpoch(t *testing.T) {
	leader := newLeaderReplica(t)
	fenced := activeMetaWithMinISR(7, 1, 1)
	fenced.LeaderEpoch = 2
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-leader-epoch",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, leader.ApplyMeta(fenced))

	_, err := leader.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
		ChannelKey:           fenced.Key,
		WriteFenceToken:      "task-leader-epoch",
		WriteFenceVersion:    1,
		ExpectedChannelEpoch: 7,
		ExpectedLeader:       1,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)
}

func TestFenceAndDrainAllowsExpiredLeaderWhenMigrationFenceMatches(t *testing.T) {
	leader := newLeaderReplica(t)
	fenced := activeMetaWithMinISR(7, 1, 1)
	fenced.LeaseUntil = leader.now().Add(-time.Millisecond)
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-expired-lease",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, leader.ApplyMeta(fenced))

	drain, err := leader.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
		ChannelKey:           fenced.Key,
		WriteFenceToken:      "task-expired-lease",
		WriteFenceVersion:    1,
		ExpectedChannelEpoch: 7,
		ExpectedLeaderEpoch:  fenced.LeaderEpoch,
		ExpectedLeader:       1,
	})

	require.NoError(t, err)
	require.Equal(t, fenced.Key, drain.ChannelKey)
	require.Equal(t, fenced.LeaderEpoch, drain.LeaderEpoch)
	require.Equal(t, uint64(1), drain.WriteFenceVersion)
}

func TestFenceAndDrainAllowsAuthoritativeFenceBeforeLocalMetaApplies(t *testing.T) {
	leader := newLeaderReplica(t)
	meta := activeMetaWithMinISR(7, 1, 1)
	require.NoError(t, leader.ApplyMeta(meta))

	drain, err := leader.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
		ChannelKey:           meta.Key,
		WriteFenceToken:      "task-authoritative-fence",
		WriteFenceVersion:    3,
		ExpectedChannelEpoch: 7,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       1,
	})

	require.NoError(t, err)
	require.Equal(t, meta.Key, drain.ChannelKey)
	require.Equal(t, uint64(3), drain.WriteFenceVersion)
	_, err = leader.Append(context.Background(), []channel.Record{{Payload: []byte("blocked"), SizeBytes: 7}})
	require.ErrorIs(t, err, channel.ErrWriteFenced)
}

func TestFenceAndDrainAllowsNewerAuthoritativeFenceForSameTaskWhenLocalFenceStale(t *testing.T) {
	leader := newLeaderReplica(t)
	meta := activeMetaWithMinISR(7, 1, 1)
	meta.WriteFence = channel.WriteFence{
		Token:   "task-same-fence",
		Version: 3,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   leader.now().Add(time.Minute),
	}
	require.NoError(t, leader.ApplyMeta(meta))

	drain, err := leader.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
		ChannelKey:           meta.Key,
		WriteFenceToken:      "task-same-fence",
		WriteFenceVersion:    5,
		ExpectedChannelEpoch: 7,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       1,
	})

	require.NoError(t, err)
	require.Equal(t, meta.Key, drain.ChannelKey)
	require.Equal(t, uint64(5), drain.WriteFenceVersion)
	_, err = leader.Append(context.Background(), []channel.Record{{Payload: []byte("blocked"), SizeBytes: 7}})
	require.ErrorIs(t, err, channel.ErrWriteFenced)
}

func TestDrainedFailClosedReopensOnlyAfterNewerVersionClear(t *testing.T) {
	r := newLeaderReplica(t)
	fenced := activeMetaWithMinISR(7, 1, 1)
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-drain-clear",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, r.ApplyMeta(fenced))
	_, err := r.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
		ChannelKey:           fenced.Key,
		WriteFenceToken:      "task-drain-clear",
		WriteFenceVersion:    1,
		ExpectedChannelEpoch: 7,
		ExpectedLeader:       1,
	})
	require.NoError(t, err)

	sameVersionClear := fenced
	sameVersionClear.WriteFence = channel.WriteFence{Version: 1}
	require.NoError(t, r.ApplyMeta(sameVersionClear))
	_, err = r.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("blocked"), SizeBytes: 7}})
	require.ErrorIs(t, err, channel.ErrWriteFenced)

	newerClear := fenced
	newerClear.WriteFence = channel.WriteFence{Version: 2}
	require.NoError(t, r.ApplyMeta(newerClear))
	_, err = r.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("open"), SizeBytes: 4}})
	require.NoError(t, err)
}

func TestDrainedFailClosedIgnoresSameVersionTTLExpiry(t *testing.T) {
	r := newLeaderReplica(t)
	fenced := activeMetaWithMinISR(7, 1, 1)
	fenced.WriteFence = channel.WriteFence{
		Token:   "task-drain-ttl",
		Version: 1,
		Reason:  channel.WriteFenceReasonMigration,
		Until:   r.now().Add(time.Second),
	}
	require.NoError(t, r.ApplyMeta(fenced))
	_, err := r.FenceAndDrain(context.Background(), channel.FenceAndDrainRequest{
		ChannelKey:           fenced.Key,
		WriteFenceToken:      "task-drain-ttl",
		WriteFenceVersion:    1,
		ExpectedChannelEpoch: 7,
		ExpectedLeaderEpoch:  fenced.LeaderEpoch,
		ExpectedLeader:       1,
	})
	require.NoError(t, err)

	expiredSameVersion := fenced
	expiredSameVersion.WriteFence.Until = r.now().Add(-time.Millisecond)
	require.NoError(t, r.ApplyMeta(expiredSameVersion))
	_, err = r.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("blocked"), SizeBytes: 7}})
	require.ErrorIs(t, err, channel.ErrWriteFenced)
}

func TestAppendWaitsUntilMinISRReplicasAcknowledgeViaFetch(t *testing.T) {
	env := newThreeReplicaCluster(t)
	done := make(chan appendTestResult, 1)

	go func() {
		res, err := env.leader.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- appendTestResult{result: res, err: err}
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)
	waitForLeaderLEO(t, env.leader, 1)

	env.replicateOnce(t, env.follower2)
	select {
	case got := <-done:
		require.NoError(t, got.err)
		t.Fatal("append returned before MinISR was satisfied")
	default:
	}

	env.replicateOnce(t, env.follower3)
	got := receiveAppendResult(t, done, "append did not return after MinISR was satisfied")
	require.NoError(t, got.err)
	require.Equal(t, uint64(1), got.result.NextCommitHW)
}

func TestAppendWaitsUntilMinISRTwoReplicasAcknowledge(t *testing.T) {
	env := newThreeReplicaClusterWithMinISR(t, 2)
	done := make(chan appendTestResult, 1)

	go func() {
		res, err := env.leader.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- appendTestResult{result: res, err: err}
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)
	waitForLeaderLEO(t, env.leader, 1)

	select {
	case got := <-done:
		require.NoError(t, got.err)
		t.Fatal("append returned before a follower satisfied MinISR=2")
	default:
	}

	env.replicateOnce(t, env.follower2)
	got := receiveAppendResult(t, done, "append did not return after a follower satisfied MinISR=2")
	require.NoError(t, got.err)
	require.Equal(t, uint64(1), got.result.NextCommitHW)
}

func TestLearnerInReplicasReceivesReplicationButNotHWQuorum(t *testing.T) {
	meta := activeMetaWithMinISR(7, 1, 3)
	meta.Replicas = []channel.NodeID{1, 2, 3, 4}
	meta.ISR = []channel.NodeID{1, 2, 3}

	env := newThreeReplicaClusterWithMeta(t, meta)
	learner := newFollowerEnvWithMeta(t, 4, meta)
	done := make(chan appendTestResult, 1)

	go func() {
		res, err := env.leader.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- appendTestResult{result: res, err: err}
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)
	waitForLeaderLEO(t, env.leader, 1)

	env.replicateOnce(t, learner)
	require.Equal(t, uint64(1), learner.Status().LEO, "learner should receive replicated records")
	require.Equal(t, uint64(0), env.leader.Status().HW, "learner progress must not advance ISR quorum HW")
	select {
	case got := <-done:
		require.NoError(t, got.err)
		t.Fatal("append returned after learner ack but before ISR quorum")
	default:
	}

	env.replicateOnce(t, env.follower2)
	env.replicateOnce(t, env.follower3)
	got := receiveAppendResult(t, done, "append did not return after ISR quorum was satisfied")
	require.NoError(t, got.err)
	require.Equal(t, uint64(1), got.result.NextCommitHW)
}

func TestAddLearnerDoesNotReduceWriteAvailability(t *testing.T) {
	initial := activeMetaWithMinISR(7, 1, 2)
	env := newThreeReplicaClusterWithMeta(t, initial)
	withLearner := initial
	withLearner.Replicas = []channel.NodeID{1, 2, 3, 4}

	require.NoError(t, env.leader.ApplyMeta(withLearner))
	done := make(chan appendTestResult, 1)
	go func() {
		res, err := env.leader.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- appendTestResult{result: res, err: err}
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)
	waitForLeaderLEO(t, env.leader, 1)

	select {
	case got := <-done:
		require.NoError(t, got.err)
		t.Fatal("append returned before an ISR follower acknowledged")
	default:
	}

	env.replicateOnce(t, env.follower2)
	got := receiveAppendResult(t, done, "append did not return after unchanged ISR quorum was satisfied")
	require.NoError(t, got.err)
	require.Equal(t, uint64(1), got.result.NextCommitHW)
	require.Equal(t, uint64(1), env.leader.Status().HW)
}

type appendTestResult struct {
	result channel.CommitResult
	err    error
}

func waitForLeaderLEO(t testing.TB, r *replica, want uint64) {
	t.Helper()
	require.Eventually(t, func() bool {
		return r.Status().LEO == want
	}, time.Second, 10*time.Millisecond)
}

func receiveAppendResult(t testing.TB, done <-chan appendTestResult, timeoutMessage string) appendTestResult {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(time.Second):
		t.Fatal(timeoutMessage)
		return appendTestResult{}
	}
}

func receiveDrainResult(t testing.TB, done <-chan channel.DrainResult, errs <-chan error, timeoutMessage string) channel.DrainResult {
	t.Helper()
	select {
	case got := <-done:
		return got
	case err := <-errs:
		t.Fatalf("FenceAndDrain() error = %v", err)
		return channel.DrainResult{}
	case <-time.After(time.Second):
		t.Fatal(timeoutMessage)
		return channel.DrainResult{}
	}
}

func TestLeaderLeaseExpiryFencesAppend(t *testing.T) {
	env := newTestEnv(t)
	meta := activeMeta(7, 1)
	meta.LeaseUntil = env.clock.Now().Add(time.Second)
	env.replica = newReplicaFromEnv(t, env)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))
	beforeGeneration := env.replica.roleGeneration

	env.clock.Advance(2 * time.Second)
	_, err := env.replica.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
	require.ErrorIs(t, err, channel.ErrLeaseExpired)
	require.Equal(t, channel.ReplicaRoleFencedLeader, env.replica.state.Role)
	require.Greater(t, env.replica.roleGeneration, beforeGeneration)
}

func TestAppendableLeaseExpiryAdvancesRoleGeneration(t *testing.T) {
	r := newLeaderReplica(t)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.meta.LeaseUntil = time.Unix(1_699_999_999, 0).UTC()
	beforeGeneration := r.roleGeneration

	err := r.appendableLocked()

	require.ErrorIs(t, err, channel.ErrLeaseExpired)
	require.Equal(t, channel.ReplicaRoleFencedLeader, r.state.Role)
	require.Greater(t, r.roleGeneration, beforeGeneration)
}

func TestAppendContextCancellationReturnsPromptly(t *testing.T) {
	env := newThreeReplicaCluster(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := env.leader.Append(ctx, []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- err
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)
	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Append() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("append did not return after context cancellation")
	}
}

func TestAppendQueuedCancellationCompletesBeforeDurableWrite(t *testing.T) {
	env := newTestEnv(t)
	env.replica = newReplicaFromEnvWithGroupCommit(t, env, time.Second, 8, 1024)
	meta := activeMetaWithMinISR(7, 1, 1)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := env.replica.Append(ctx, []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- err
	}()

	require.Eventually(t, func() bool {
		env.replica.mu.RLock()
		defer env.replica.mu.RUnlock()
		return len(env.replica.appendPending) == 1
	}, time.Second, 10*time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("queued append did not return after cancellation")
	}
	require.Equal(t, 0, env.log.appendCount)
}

func TestAppendDurableInFlightCancellationReturnsBeforeSyncCompletes(t *testing.T) {
	env := newTestEnv(t)
	env.replica = newReplicaFromEnvWithGroupCommit(t, env, time.Millisecond, 1, 1024)
	meta := activeMetaWithMinISR(7, 1, 1)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))
	env.log.syncStarted = make(chan struct{}, 1)
	env.log.syncContinue = make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := env.replica.Append(ctx, []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- err
	}()

	select {
	case <-env.log.syncStarted:
	case <-time.After(time.Second):
		t.Fatal("durable append did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("durable in-flight append cancellation waited for sync")
	}
	close(env.log.syncContinue)
	require.Eventually(t, func() bool {
		return env.replica.Status().LEO == 1
	}, time.Second, 10*time.Millisecond)
}

func TestAppendDurableResultAfterLeaseExpiryDoesNotPublishLEO(t *testing.T) {
	env := newTestEnv(t)
	meta := activeMetaWithMinISR(7, 1, 1)
	meta.LeaseUntil = env.clock.Now().Add(time.Second)
	env.replica = newReplicaFromEnvWithGroupCommit(t, env, time.Millisecond, 1, 1024)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))
	env.log.syncStarted = make(chan struct{}, 1)
	env.log.syncContinue = make(chan struct{})

	done := make(chan error, 1)
	go func() {
		_, err := env.replica.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- err
	}()
	select {
	case <-env.log.syncStarted:
	case <-time.After(time.Second):
		t.Fatal("durable append did not start")
	}
	env.clock.Advance(2 * time.Second)
	close(env.log.syncContinue)

	select {
	case err := <-done:
		require.ErrorIs(t, err, channel.ErrLeaseExpired)
	case <-time.After(time.Second):
		t.Fatal("append did not fail after lease expiry")
	}
	st := env.replica.Status()
	require.Equal(t, channel.ReplicaRoleFencedLeader, st.Role)
	require.Equal(t, uint64(0), st.LEO)
}

func TestAppendDurableResultAfterLeaseRenewalPublishesLEO(t *testing.T) {
	env := newTestEnv(t)
	meta := activeMetaWithMinISR(7, 1, 1)
	meta.LeaseUntil = env.clock.Now().Add(time.Second)
	env.replica = newReplicaFromEnvWithGroupCommit(t, env, time.Millisecond, 1, 1024)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))
	env.log.syncStarted = make(chan struct{}, 2)
	env.log.syncContinue = make(chan struct{})

	firstDone := make(chan error, 1)
	go func() {
		_, err := env.replica.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("first"), SizeBytes: 5}})
		firstDone <- err
	}()
	select {
	case <-env.log.syncStarted:
	case <-time.After(time.Second):
		t.Fatal("durable append did not start")
	}

	env.clock.Advance(2 * time.Second)
	renewed := meta
	renewed.LeaseUntil = env.clock.Now().Add(time.Minute)
	require.NoError(t, env.replica.ApplyMeta(renewed))
	close(env.log.syncContinue)

	select {
	case err := <-firstDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("append did not finish after lease renewal")
	}
	st := env.replica.Status()
	require.Equal(t, channel.ReplicaRoleLeader, st.Role)
	require.Equal(t, uint64(1), st.LEO)

	_, err := env.replica.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("second"), SizeBytes: 6}})
	require.NoError(t, err)
	require.Equal(t, uint64(2), env.replica.Status().LEO)
}

func TestAppendPendingAfterLeaseExpiryFailsBeforeDurableWrite(t *testing.T) {
	env := newTestEnv(t)
	meta := activeMetaWithMinISR(7, 1, 1)
	meta.LeaseUntil = env.clock.Now().Add(time.Second)
	env.replica = newReplicaFromEnvWithGroupCommit(t, env, time.Millisecond, 1, 1024)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))
	env.log.syncStarted = make(chan struct{}, 2)
	env.log.syncContinue = make(chan struct{})

	firstDone := make(chan error, 1)
	go func() {
		_, err := env.replica.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("first"), SizeBytes: 5}})
		firstDone <- err
	}()
	select {
	case <-env.log.syncStarted:
	case <-time.After(time.Second):
		t.Fatal("first durable append did not start")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := env.replica.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("second"), SizeBytes: 6}})
		secondDone <- err
	}()
	require.Eventually(t, func() bool {
		env.replica.mu.RLock()
		defer env.replica.mu.RUnlock()
		return len(env.replica.appendPending) == 1
	}, time.Second, 10*time.Millisecond)

	env.clock.Advance(2 * time.Second)
	close(env.log.syncContinue)

	select {
	case err := <-firstDone:
		require.ErrorIs(t, err, channel.ErrLeaseExpired)
	case <-time.After(time.Second):
		t.Fatal("first append did not fail after lease expiry")
	}
	select {
	case err := <-secondDone:
		require.ErrorIs(t, err, channel.ErrLeaseExpired)
	case <-time.After(time.Second):
		t.Fatal("queued append did not fail after lease expiry")
	}
	require.Equal(t, 1, env.log.appendCount)
	require.Equal(t, uint64(0), env.replica.Status().LEO)
}

func TestAppendDurableWrongNewLEOFailsAsCorruptState(t *testing.T) {
	env := newTestEnv(t)
	env.replica = newReplicaFromEnvWithGroupCommit(t, env, time.Millisecond, 1, 1024)
	env.replica.durable = corruptAppendDurableStore{durableReplicaStore: env.replica.durable}
	meta := activeMetaWithMinISR(7, 1, 1)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))

	_, err := env.replica.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Equal(t, uint64(0), env.replica.Status().LEO)
}

func TestAppendContextCancellationLogsTimeoutSnapshot(t *testing.T) {
	env := newThreeReplicaCluster(t)
	logger := newReplicaRecordingLogger("channel")
	env.leader.logger = logger
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := env.leader.Append(ctx, []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- err
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("append did not return after deadline exceeded")
	}

	entry := requireReplicaLogEntry(t, logger, "DEBUG", "channel.replica", "channel.replica.append.timeout")
	require.Equal(t, "append wait timed out before quorum commit", entry.msg)
	require.Equal(t, "group-10", requireReplicaFieldValue[string](t, entry, "channelKey"))
	require.Equal(t, uint64(1), requireReplicaFieldValue[uint64](t, entry, "nodeID"))
	require.Equal(t, uint64(1), requireReplicaFieldValue[uint64](t, entry, "leaderNodeID"))
	require.Equal(t, true, requireReplicaFieldValue[bool](t, entry, "commitReady"))
	require.Equal(t, uint64(0), requireReplicaFieldValue[uint64](t, entry, "hw"))
	require.Equal(t, uint64(1), requireReplicaFieldValue[uint64](t, entry, "leo"))
	require.Equal(t, uint64(1), requireReplicaFieldValue[uint64](t, entry, "targetOffset"))
}

func TestBecomeFollowerFailsOutstandingAppendWaiters(t *testing.T) {
	cluster := newThreeReplicaCluster(t)
	done := make(chan error, 1)

	go func() {
		_, err := cluster.leader.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- err
	}()
	waitForLogAppend(t, cluster.leader.log.(*fakeLogStore), 1)

	err := cluster.leader.BecomeFollower(activeMetaWithMinISR(8, 2, 3))
	require.NoError(t, err)

	select {
	case appendErr := <-done:
		require.ErrorIs(t, appendErr, channel.ErrNotLeader)
	case <-time.After(time.Second):
		t.Fatal("append waiter did not fail after leadership loss")
	}
}

func TestTombstoneFailsOutstandingAppendWaiters(t *testing.T) {
	cluster := newThreeReplicaCluster(t)
	done := make(chan error, 1)

	go func() {
		_, err := cluster.leader.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- err
	}()
	waitForLogAppend(t, cluster.leader.log.(*fakeLogStore), 1)

	require.NoError(t, cluster.leader.Tombstone())

	select {
	case appendErr := <-done:
		require.ErrorIs(t, appendErr, channel.ErrTombstoned)
	case <-time.After(time.Second):
		t.Fatal("append waiter did not fail after tombstone")
	}
}

func TestCloseFailsPendingAppendRequests(t *testing.T) {
	env := newTestEnv(t)
	env.replica = newReplicaFromEnvWithGroupCommit(t, env, time.Second, 8, 1024)
	meta := activeMetaWithMinISR(7, 1, 1)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))

	done := make(chan error, 1)
	go func() {
		_, err := env.replica.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, env.replica.Close())

	select {
	case appendErr := <-done:
		require.ErrorIs(t, appendErr, channel.ErrNotLeader)
	case <-time.After(time.Second):
		t.Fatal("pending append did not fail after close")
	}
}
