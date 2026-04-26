package replica

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
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

func TestAppendWaitsUntilMinISRReplicasAcknowledgeViaFetch(t *testing.T) {
	env := newThreeReplicaCluster(t)
	done := make(chan channel.CommitResult, 1)

	go func() {
		res, err := env.leader.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		if err == nil {
			done <- res
		}
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)

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

func TestAppendWaitsUntilMinISRTwoReplicasAcknowledge(t *testing.T) {
	env := newThreeReplicaClusterWithMinISR(t, 2)
	done := make(chan channel.CommitResult, 1)

	go func() {
		res, err := env.leader.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		if err == nil {
			done <- res
		}
	}()
	waitForLogAppend(t, env.leader.log.(*fakeLogStore), 1)

	select {
	case <-done:
		t.Fatal("append returned before a follower satisfied MinISR=2")
	default:
	}

	env.replicateOnce(t, env.follower2)
	res := <-done
	require.Equal(t, uint64(1), res.NextCommitHW)
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
