package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelreplica "github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	"github.com/stretchr/testify/require"
)

func TestChannelStoreAppendReopenRecoversLEOAndRecords(t *testing.T) {
	dir := t.TempDir()
	engine, st := openDurabilityStore(t, dir, "append-reopen")

	mustAppendRecords(t, st, []string{"one", "two"})
	require.Equal(t, uint64(2), st.LEO())
	require.NoError(t, engine.Close())

	reopened, reloaded := openDurabilityStore(t, dir, "append-reopen")
	defer func() { require.NoError(t, reopened.Close()) }()

	require.Equal(t, uint64(2), reloaded.LEO())
	msg, ok, err := reloaded.GetMessageBySeq(2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []byte("two"), msg.Payload)
}

func TestChannelStoreBeginEpochReopenRequiresExpectedLEO(t *testing.T) {
	dir := t.TempDir()
	engine, st := openDurabilityStore(t, dir, "begin-epoch-reopen")
	mustAppendRecords(t, st, []string{"one", "two"})

	require.NoError(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 7, StartOffset: 2}, 2))
	require.ErrorIs(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 8, StartOffset: 3}, 3), channel.ErrCorruptState)
	require.NoError(t, engine.Close())

	reopened, reloaded := openDurabilityStore(t, dir, "begin-epoch-reopen")
	defer func() { require.NoError(t, reopened.Close()) }()

	require.Equal(t, uint64(2), reloaded.LEO())
	history, err := reloaded.LoadHistory()
	require.NoError(t, err)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 2}}, history)
}

func TestChannelStoreApplyFetchWithCheckpointAndEpochReopensTogether(t *testing.T) {
	dir := t.TempDir()
	engine, st := openDurabilityStore(t, dir, "apply-fetch-reopen")
	require.NoError(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 7, StartOffset: 0}, 0))
	mustAppendRecords(t, st, []string{"one"})

	checkpoint := channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 2}
	nextLEO, err := st.StoreApplyFetchWithEpoch(channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 1,
		Records: []channel.Record{
			recordForStoreMessage(t, st, 200, "client-2", "two"),
		},
		Checkpoint: &checkpoint,
	}, &channel.EpochPoint{Epoch: 8, StartOffset: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(2), nextLEO)
	require.NoError(t, engine.Close())

	reopened, reloaded := openDurabilityStore(t, dir, "apply-fetch-reopen")
	defer func() { require.NoError(t, reopened.Close()) }()

	require.Equal(t, uint64(2), reloaded.LEO())
	loadedCheckpoint, err := reloaded.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, checkpoint, loadedCheckpoint)
	history, err := reloaded.LoadHistory()
	require.NoError(t, err)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, {Epoch: 8, StartOffset: 1}}, history)
	msg, ok, err := reloaded.GetMessageBySeq(2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(200), msg.MessageID)
	require.Equal(t, []byte("two"), msg.Payload)
}

func TestChannelStoreTruncateLogAndHistoryReopensConsistently(t *testing.T) {
	dir := t.TempDir()
	engine, st := openDurabilityStore(t, dir, "truncate-reopen")
	require.NoError(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 7, StartOffset: 0}, 0))
	mustAppendRecords(t, st, []string{"one", "two"})
	require.NoError(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 8, StartOffset: 2}, 2))
	_, err := st.Append([]channel.Record{recordForStoreMessage(t, st, 300, "client-3", "three")})
	require.NoError(t, err)
	require.NoError(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 9, StartOffset: 3}, 3))

	require.NoError(t, st.TruncateLogAndHistory(context.Background(), 2))
	require.NoError(t, engine.Close())

	reopened, reloaded := openDurabilityStore(t, dir, "truncate-reopen")
	defer func() { require.NoError(t, reopened.Close()) }()

	require.Equal(t, uint64(2), reloaded.LEO())
	history, err := reloaded.LoadHistory()
	require.NoError(t, err)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, {Epoch: 8, StartOffset: 2}}, history)
	_, ok, err := reloaded.GetMessageBySeq(3)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestChannelStoreInstallSnapshotAtomicallyReopensCheckpointHistoryAndPayload(t *testing.T) {
	dir := t.TempDir()
	engine, st := openDurabilityStore(t, dir, "snapshot-reopen")
	require.NoError(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 7, StartOffset: 0}, 0))
	mustAppendRecords(t, st, []string{"one", "two", "three"})

	snap := channel.Snapshot{ChannelKey: st.key, Epoch: 8, EndOffset: 2, Payload: []byte("snapshot-payload")}
	checkpoint := channel.Checkpoint{Epoch: 8, LogStartOffset: 2, HW: 2}
	leo, err := st.InstallSnapshotAtomically(context.Background(), snap, checkpoint, channel.EpochPoint{Epoch: 8, StartOffset: 2})
	require.NoError(t, err)
	require.Equal(t, uint64(3), leo)
	require.NoError(t, engine.Close())

	reopened, reloaded := openDurabilityStore(t, dir, "snapshot-reopen")
	defer func() { require.NoError(t, reopened.Close()) }()

	require.Equal(t, uint64(3), reloaded.LEO())
	loadedCheckpoint, err := reloaded.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, checkpoint, loadedCheckpoint)
	history, err := reloaded.LoadHistory()
	require.NoError(t, err)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, {Epoch: 8, StartOffset: 2}}, history)
	payload, err := reloaded.LoadSnapshotPayload()
	require.NoError(t, err)
	require.Equal(t, snap.Payload, payload)
}

func TestChannelStoreStoreCheckpointMonotonicRejectsRegressions(t *testing.T) {
	st := newTestChannelStore(t)
	mustAppendRecords(t, st, []string{"one", "two", "three"})

	initial := channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 2}
	require.NoError(t, st.StoreCheckpointMonotonic(context.Background(), initial, 2, 3))

	tests := []struct {
		name      string
		next      channel.Checkpoint
		visibleHW uint64
		leo       uint64
	}{
		{name: "hw regression", next: channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 1}, visibleHW: 2, leo: 3},
		{name: "above visible", next: channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}, visibleHW: 2, leo: 3},
		{name: "above leo", next: channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 4}, visibleHW: 4, leo: 3},
		{name: "log start regression", next: channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 3}, visibleHW: 3, leo: 3},
		{name: "epoch regression", next: channel.Checkpoint{Epoch: 6, LogStartOffset: 1, HW: 3}, visibleHW: 3, leo: 3},
	}
	require.NoError(t, st.StoreCheckpointMonotonic(context.Background(), channel.Checkpoint{Epoch: 8, LogStartOffset: 1, HW: 3}, 3, 3))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := st.StoreCheckpointMonotonic(context.Background(), tt.next, tt.visibleHW, tt.leo)
			require.ErrorIs(t, err, channel.ErrCorruptState)
			loaded, loadErr := st.LoadCheckpoint()
			require.NoError(t, loadErr)
			require.Equal(t, channel.Checkpoint{Epoch: 8, LogStartOffset: 1, HW: 3}, loaded)
		})
	}
}

func TestChannelStoreStoreCheckpointMonotonicDoesNotQueueBehindAppendCommitCoordinator(t *testing.T) {
	engine := openTestEngine(t)
	st := openTestChannelStoresOnEngine(t, engine, "monotonic-checkpoint-hol")[0]

	appendStarted := make(chan struct{})
	releaseAppend := make(chan struct{})
	t.Cleanup(func() {
		closeOnce(releaseAppend)
	})

	installTestCommitCoordinator(t, engine, func(coordinator *commitCoordinator, req commitRequest) {
		closeOnce(appendStarted)
		select {
		case <-releaseAppend:
			req.done <- nil
		case <-coordinator.stopCh:
			req.done <- channel.ErrInvalidArgument
		}
	})

	appendDone := make(chan appendResult, 1)
	go func() {
		payload := mustEncodeStoreMessage(t, channel.Message{MessageID: 1, ClientMsgNo: "append", FromUID: "u1", ChannelID: st.id.ID, ChannelType: st.id.Type, Payload: []byte("one")})
		base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
		appendDone <- appendResult{base: base, err: err}
	}()

	select {
	case <-appendStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for append to occupy the commit coordinator")
	}

	checkpointDone := make(chan error, 1)
	go func() {
		checkpointDone <- st.StoreCheckpointMonotonic(context.Background(), channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}, 0, 0)
	}()

	select {
	case err := <-checkpointDone:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("StoreCheckpointMonotonic() was blocked behind the append coordinator")
	}

	closeOnce(releaseAppend)
	select {
	case result := <-appendDone:
		require.NoError(t, result.err)
		require.Equal(t, uint64(0), result.base)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for append after release")
	}
}

func TestChannelStoreApplyFetchWithEpochRejectsCheckpointRegressionWithoutMutation(t *testing.T) {
	st := newTestChannelStore(t)
	require.NoError(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 7, StartOffset: 0}, 0))
	mustAppendRecords(t, st, []string{"one", "two", "three"})
	current := channel.Checkpoint{Epoch: 8, LogStartOffset: 1, HW: 3}
	require.NoError(t, st.StoreCheckpointMonotonic(context.Background(), current, 3, 3))

	stale := channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 2}
	_, err := st.StoreApplyFetchWithEpoch(channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 2,
		Records: []channel.Record{
			recordForStoreMessage(t, st, 400, "client-4", "four"),
		},
		Checkpoint: &stale,
	}, &channel.EpochPoint{Epoch: 8, StartOffset: 3})

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Equal(t, uint64(3), st.LEO())
	loaded, loadErr := st.LoadCheckpoint()
	require.NoError(t, loadErr)
	require.Equal(t, current, loaded)
	history, historyErr := st.LoadHistory()
	require.NoError(t, historyErr)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}, history)
	_, ok, msgErr := st.GetMessageBySeq(4)
	require.NoError(t, msgErr)
	require.False(t, ok)
}

func TestChannelStoreInstallSnapshotAtomicallyRejectsCheckpointRegressionWithoutMutation(t *testing.T) {
	st := newTestChannelStore(t)
	require.NoError(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 7, StartOffset: 0}, 0))
	mustAppendRecords(t, st, []string{"one", "two", "three"})
	current := channel.Checkpoint{Epoch: 9, LogStartOffset: 2, HW: 3}
	require.NoError(t, st.StoreSnapshotPayload([]byte("existing-snapshot")))
	require.NoError(t, st.StoreCheckpointMonotonic(context.Background(), current, 3, 3))

	staleSnapshot := channel.Snapshot{ChannelKey: st.key, Epoch: 8, EndOffset: 2, Payload: []byte("stale-snapshot")}
	_, err := st.InstallSnapshotAtomically(
		context.Background(),
		staleSnapshot,
		channel.Checkpoint{Epoch: 8, LogStartOffset: 2, HW: 2},
		channel.EpochPoint{Epoch: 8, StartOffset: 2},
	)

	require.ErrorIs(t, err, channel.ErrCorruptState)
	loaded, loadErr := st.LoadCheckpoint()
	require.NoError(t, loadErr)
	require.Equal(t, current, loaded)
	payload, payloadErr := st.LoadSnapshotPayload()
	require.NoError(t, payloadErr)
	require.Equal(t, []byte("existing-snapshot"), payload)
	history, historyErr := st.LoadHistory()
	require.NoError(t, historyErr)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}, history)
}

func TestChannelStoreReplicaRecoverRejectsInjectedPartialDurableState(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, st *ChannelStore)
	}{
		{
			name: "history start beyond log end",
			setup: func(t *testing.T, st *ChannelStore) {
				require.NoError(t, st.AppendHistory(channel.EpochPoint{Epoch: 7, StartOffset: 4}))
			},
		},
		{
			name: "checkpoint beyond log end",
			setup: func(t *testing.T, st *ChannelStore) {
				require.NoError(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 7, StartOffset: 0}, 0))
				mustAppendRecords(t, st, []string{"one"})
				require.NoError(t, st.StoreCheckpoint(channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 2}))
			},
		},
		{
			name: "snapshot checkpoint without payload",
			setup: func(t *testing.T, st *ChannelStore) {
				require.NoError(t, st.BeginEpoch(context.Background(), channel.EpochPoint{Epoch: 7, StartOffset: 0}, 0))
				mustAppendRecords(t, st, []string{"one"})
				require.NoError(t, st.StoreCheckpoint(channel.Checkpoint{Epoch: 7, LogStartOffset: 1, HW: 1}))
			},
		},
		{
			name: "snapshot payload without checkpoint",
			setup: func(t *testing.T, st *ChannelStore) {
				mustAppendRecords(t, st, []string{"one"})
				require.NoError(t, st.StoreSnapshotPayload([]byte("orphan-snapshot")))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			engine, st := openDurabilityStore(t, dir, "partial-"+tt.name)
			tt.setup(t, st)
			require.NoError(t, engine.Close())

			reopened, reloaded := openDurabilityStore(t, dir, "partial-"+tt.name)
			defer func() { require.NoError(t, reopened.Close()) }()
			replica, err := newReplicaFromChannelStore(reloaded)
			if replica != nil {
				require.NoError(t, replica.Close())
			}
			require.True(t, errors.Is(err, channel.ErrCorruptState), "NewReplica() error = %v", err)
		})
	}
}

func openDurabilityStore(t *testing.T, dir string, name string) (*Engine, *ChannelStore) {
	t.Helper()
	engine, err := Open(dir)
	require.NoError(t, err)
	key, id := testChannelStoreIdentity(name)
	return engine, engine.ForChannel(key, id)
}

func recordForStoreMessage(t *testing.T, st *ChannelStore, messageID uint64, clientMsgNo string, body string) channel.Record {
	t.Helper()
	payload := mustEncodeStoreMessage(t, channel.Message{
		MessageID:   messageID,
		ClientMsgNo: clientMsgNo,
		FromUID:     "u1",
		ChannelID:   st.id.ID,
		ChannelType: st.id.Type,
		Payload:     []byte(body),
	})
	return channel.Record{Payload: payload, SizeBytes: len(payload)}
}

func newReplicaFromChannelStore(st *ChannelStore) (channelreplica.Replica, error) {
	return channelreplica.NewReplica(channelreplica.ReplicaConfig{
		LocalNode:         1,
		LogStore:          st,
		CheckpointStore:   durabilityCheckpointStore{store: st},
		ApplyFetchStore:   st,
		EpochHistoryStore: durabilityEpochHistoryStore{store: st},
		SnapshotApplier:   durabilitySnapshotApplier{store: st},
	})
}

type durabilityCheckpointStore struct{ store *ChannelStore }

func (s durabilityCheckpointStore) Load() (channel.Checkpoint, error) {
	return s.store.LoadCheckpoint()
}
func (s durabilityCheckpointStore) Store(cp channel.Checkpoint) error {
	return s.store.StoreCheckpoint(cp)
}

type durabilityEpochHistoryStore struct{ store *ChannelStore }

func (s durabilityEpochHistoryStore) Load() ([]channel.EpochPoint, error) {
	return s.store.LoadHistory()
}
func (s durabilityEpochHistoryStore) Append(point channel.EpochPoint) error {
	return s.store.AppendHistory(point)
}
func (s durabilityEpochHistoryStore) TruncateTo(leo uint64) error {
	return s.store.TruncateHistoryTo(leo)
}

type durabilitySnapshotApplier struct{ store *ChannelStore }

func (s durabilitySnapshotApplier) InstallSnapshot(_ context.Context, snap channel.Snapshot) error {
	return s.store.StoreSnapshotPayload(snap.Payload)
}

func (s durabilitySnapshotApplier) LoadSnapshotPayload(_ context.Context) ([]byte, error) {
	payload, err := s.store.LoadSnapshotPayload()
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, channel.ErrEmptyState
	}
	return payload, nil
}
