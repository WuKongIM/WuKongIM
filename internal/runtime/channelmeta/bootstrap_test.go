package channelmeta

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

func TestRuntimeBootstrapperBootstrapsAuthoritativeMissFromSlotTopology(t *testing.T) {
	now := time.UnixMilli(1_700_000_123_000).UTC()
	id := channel.ChannelID{ID: "u2@u1", Type: 1}
	store := &bootstrapStoreFake{
		getErr: metadb.ErrNotFound,
		authoritativeMeta: metadb.ChannelRuntimeMeta{
			ChannelID:    id.ID,
			ChannelType:  int64(id.Type),
			ChannelEpoch: 1,
			LeaderEpoch:  1,
			Replicas:     []uint64{1, 2, 3},
			ISR:          []uint64{1, 2, 3},
			Leader:       2,
			MinISR:       2,
			Status:       uint8(channel.StatusActive),
			Features:     uint64(channel.MessageSeqFormatLegacyU32),
			LeaseUntilMS: now.Add(BootstrapLease).UnixMilli(),
		},
	}
	bootstrapper := NewBootstrapper(BootstrapOptions{
		Cluster:       &bootstrapClusterFake{slotID: 9, peers: []uint64{1, 2, 3}, leader: 2},
		Store:         store,
		DefaultMinISR: 2,
		Now:           func() time.Time { return now },
		Logger:        wklog.NewNop(),
	})

	meta, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), id)

	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, store.authoritativeMeta, meta)
	require.Len(t, store.upserts, 1)
	require.Equal(t, []uint64{1, 2, 3}, store.upserts[0].Replicas)
	require.Equal(t, []uint64{1, 2, 3}, store.upserts[0].ISR)
	require.Equal(t, uint64(2), store.upserts[0].Leader)
	require.Equal(t, int64(2), store.upserts[0].MinISR)
	require.Equal(t, uint64(1), store.upserts[0].ChannelEpoch)
	require.Equal(t, uint64(1), store.upserts[0].LeaderEpoch)
	require.Equal(t, uint8(channel.StatusActive), store.upserts[0].Status)
	require.Equal(t, uint64(channel.MessageSeqFormatLegacyU32), store.upserts[0].Features)
	require.Equal(t, now.Add(BootstrapLease).UnixMilli(), store.upserts[0].LeaseUntilMS)
}

func TestRuntimeBootstrapperDoesNotReturnPartialMetadataWhenBootstrapWriteFails(t *testing.T) {
	writeErr := errors.New("write failed")
	store := &bootstrapStoreFake{getErr: metadb.ErrNotFound, upsertErr: writeErr}
	bootstrapper := NewBootstrapper(BootstrapOptions{
		Cluster:       &bootstrapClusterFake{slotID: 7, peers: []uint64{2, 4}, leader: 2},
		Store:         store,
		DefaultMinISR: 2,
		Logger:        wklog.NewNop(),
	})

	meta, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), channel.ChannelID{ID: "g1", Type: 2})

	require.ErrorContains(t, err, "upsert runtime metadata")
	require.ErrorIs(t, err, writeErr)
	require.False(t, created)
	require.Equal(t, metadb.ChannelRuntimeMeta{}, meta)
	require.Len(t, store.upserts, 1)
	require.Equal(t, 1, store.getCalls)
}

func TestRuntimeBootstrapperClampsMinISRForSingleNodeCluster(t *testing.T) {
	id := channel.ChannelID{ID: "solo", Type: 1}
	store := &bootstrapStoreFake{
		getErr: metadb.ErrNotFound,
		authoritativeMeta: metadb.ChannelRuntimeMeta{
			ChannelID:   id.ID,
			ChannelType: int64(id.Type),
			Replicas:    []uint64{7},
			ISR:         []uint64{7},
			Leader:      7,
			MinISR:      1,
			Status:      uint8(channel.StatusActive),
			Features:    uint64(channel.MessageSeqFormatLegacyU32),
		},
	}
	bootstrapper := NewBootstrapper(BootstrapOptions{
		Cluster:       &bootstrapClusterFake{slotID: 3, peers: []uint64{7}, leader: 7},
		Store:         store,
		DefaultMinISR: 2,
		Logger:        wklog.NewNop(),
	})

	meta, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), id)

	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, int64(1), meta.MinISR)
	require.Len(t, store.upserts, 1)
	require.Equal(t, int64(1), store.upserts[0].MinISR)
}

func TestRuntimeBootstrapperDefaultsMinISRToTwo(t *testing.T) {
	id := channel.ChannelID{ID: "defaulted", Type: 1}
	store := &bootstrapStoreFake{
		getErr: metadb.ErrNotFound,
		authoritativeMeta: metadb.ChannelRuntimeMeta{
			ChannelID:   id.ID,
			ChannelType: int64(id.Type),
			Replicas:    []uint64{4, 5, 6},
			ISR:         []uint64{4, 5, 6},
			Leader:      5,
			MinISR:      2,
			Status:      uint8(channel.StatusActive),
			Features:    uint64(channel.MessageSeqFormatLegacyU32),
		},
	}
	bootstrapper := NewBootstrapper(BootstrapOptions{
		Cluster: &bootstrapClusterFake{slotID: 7, peers: []uint64{4, 5, 6}, leader: 5},
		Store:   store,
		Logger:  wklog.NewNop(),
	})

	meta, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), id)

	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, int64(2), meta.MinISR)
	require.Len(t, store.upserts, 1)
	require.Equal(t, int64(2), store.upserts[0].MinISR)
}

func TestRuntimeBootstrapperFailsWhenSlotHasNoPeers(t *testing.T) {
	store := &bootstrapStoreFake{getErr: metadb.ErrNotFound}
	bootstrapper := NewBootstrapper(BootstrapOptions{
		Cluster:       &bootstrapClusterFake{slotID: 5},
		Store:         store,
		DefaultMinISR: 2,
		Logger:        wklog.NewNop(),
	})

	_, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), channel.ChannelID{ID: "empty", Type: 1})

	require.Error(t, err)
	require.ErrorContains(t, err, "slot peers")
	require.False(t, created)
	require.Empty(t, store.upserts)
}

func TestRuntimeBootstrapperPropagatesRetryableClusterErrors(t *testing.T) {
	testCases := []struct {
		name string
		err  error
	}{
		{name: "no leader", err: raftcluster.ErrNoLeader},
		{name: "slot not found", err: raftcluster.ErrSlotNotFound},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			store := &bootstrapStoreFake{getErr: metadb.ErrNotFound}
			bootstrapper := NewBootstrapper(BootstrapOptions{
				Cluster:       &bootstrapClusterFake{slotID: 11, peers: []uint64{1, 2, 3}, leaderErr: tt.err},
				Store:         store,
				DefaultMinISR: 2,
				Logger:        wklog.NewNop(),
			})

			_, created, err := bootstrapper.EnsureChannelRuntimeMeta(context.Background(), channel.ChannelID{ID: "unstable", Type: 1})

			require.ErrorIs(t, err, tt.err)
			require.False(t, created)
			require.Empty(t, store.upserts)
		})
	}
}

func TestRuntimeBootstrapperLogsMissingBootstrappedAndFailedEvents(t *testing.T) {
	logger := newBootstrapRecordingLogger("app")
	id := channel.ChannelID{ID: "u2@u1", Type: 1}

	successStore := &bootstrapStoreFake{
		getErr: metadb.ErrNotFound,
		authoritativeMeta: metadb.ChannelRuntimeMeta{
			ChannelID:   id.ID,
			ChannelType: int64(id.Type),
			Replicas:    []uint64{1, 2, 3},
			ISR:         []uint64{1, 2, 3},
			Leader:      2,
			MinISR:      2,
			Status:      uint8(channel.StatusActive),
			Features:    uint64(channel.MessageSeqFormatLegacyU32),
		},
	}
	successBootstrapper := NewBootstrapper(BootstrapOptions{
		Cluster:       &bootstrapClusterFake{slotID: 21, peers: []uint64{1, 2, 3}, leader: 2},
		Store:         successStore,
		DefaultMinISR: 2,
		Logger:        logger,
	})

	_, created, err := successBootstrapper.EnsureChannelRuntimeMeta(context.Background(), id)
	require.NoError(t, err)
	require.True(t, created)

	failureStore := &bootstrapStoreFake{getErr: metadb.ErrNotFound, upsertErr: errors.New("write failed")}
	failureBootstrapper := NewBootstrapper(BootstrapOptions{
		Cluster:       &bootstrapClusterFake{slotID: 22, peers: []uint64{9, 10}, leader: 9},
		Store:         failureStore,
		DefaultMinISR: 2,
		Logger:        logger,
	})

	_, created, err = failureBootstrapper.EnsureChannelRuntimeMeta(context.Background(), channel.ChannelID{ID: "failed", Type: 2})
	require.Error(t, err)
	require.False(t, created)

	entries := logger.entries()
	require.Len(t, entries, 4)

	require.Equal(t, "INFO", entries[0].level)
	require.Equal(t, "missing runtime metadata; bootstrapping", entries[0].msg)
	requireBootstrapFieldEquals(t, entries[0], "event", "app.channelmeta.bootstrap.missing")
	requireBootstrapFieldEquals(t, entries[0], "channelID", "u2@u1")
	requireBootstrapFieldEquals(t, entries[0], "channelType", int64(1))
	requireBootstrapFieldEquals(t, entries[0], "slotID", uint64(21))
	requireBootstrapFieldEquals(t, entries[0], "leader", uint64(0))
	requireBootstrapFieldEquals(t, entries[0], "replicaCount", 3)
	requireBootstrapFieldEquals(t, entries[0], "minISR", int64(2))
	requireBootstrapFieldEquals(t, entries[0], "created", false)

	require.Equal(t, "INFO", entries[1].level)
	require.Equal(t, "bootstrapped runtime metadata", entries[1].msg)
	requireBootstrapFieldEquals(t, entries[1], "event", "app.channelmeta.bootstrap.bootstrapped")
	requireBootstrapFieldEquals(t, entries[1], "channelID", "u2@u1")
	requireBootstrapFieldEquals(t, entries[1], "channelType", int64(1))
	requireBootstrapFieldEquals(t, entries[1], "slotID", uint64(21))
	requireBootstrapFieldEquals(t, entries[1], "leader", uint64(2))
	requireBootstrapFieldEquals(t, entries[1], "replicaCount", 3)
	requireBootstrapFieldEquals(t, entries[1], "minISR", int64(2))
	requireBootstrapFieldEquals(t, entries[1], "created", true)

	require.Equal(t, "INFO", entries[2].level)
	require.Equal(t, "missing runtime metadata; bootstrapping", entries[2].msg)
	requireBootstrapFieldEquals(t, entries[2], "event", "app.channelmeta.bootstrap.missing")
	requireBootstrapFieldEquals(t, entries[2], "channelID", "failed")
	requireBootstrapFieldEquals(t, entries[2], "channelType", int64(2))
	requireBootstrapFieldEquals(t, entries[2], "slotID", uint64(22))
	requireBootstrapFieldEquals(t, entries[2], "leader", uint64(0))
	requireBootstrapFieldEquals(t, entries[2], "replicaCount", 2)
	requireBootstrapFieldEquals(t, entries[2], "minISR", int64(2))
	requireBootstrapFieldEquals(t, entries[2], "created", false)

	require.Equal(t, "ERROR", entries[3].level)
	require.Equal(t, "failed to bootstrap runtime metadata", entries[3].msg)
	requireBootstrapFieldEquals(t, entries[3], "event", "app.channelmeta.bootstrap.failed")
	requireBootstrapFieldEquals(t, entries[3], "channelID", "failed")
	requireBootstrapFieldEquals(t, entries[3], "channelType", int64(2))
	requireBootstrapFieldEquals(t, entries[3], "slotID", uint64(22))
	requireBootstrapFieldEquals(t, entries[3], "leader", uint64(9))
	requireBootstrapFieldEquals(t, entries[3], "replicaCount", 2)
	requireBootstrapFieldEquals(t, entries[3], "minISR", int64(2))
}

type bootstrapStoreFake struct {
	getErr            error
	upsertErr         error
	authoritativeMeta metadb.ChannelRuntimeMeta
	getCalls          int
	upserts           []metadb.ChannelRuntimeMeta
}

func (f *bootstrapStoreFake) GetChannelRuntimeMeta(_ context.Context, _ string, _ int64) (metadb.ChannelRuntimeMeta, error) {
	f.getCalls++
	if f.getErr != nil && len(f.upserts) == 0 {
		return metadb.ChannelRuntimeMeta{}, f.getErr
	}
	return f.authoritativeMeta, nil
}

func (f *bootstrapStoreFake) UpsertChannelRuntimeMeta(_ context.Context, meta metadb.ChannelRuntimeMeta) error {
	f.upserts = append(f.upserts, meta)
	return f.upsertErr
}

type bootstrapClusterFake struct {
	slotID    multiraft.SlotID
	peers     []uint64
	leader    uint64
	leaderErr error
}

func (f *bootstrapClusterFake) SlotForKey(string) multiraft.SlotID {
	return f.slotID
}

func (f *bootstrapClusterFake) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	out := make([]multiraft.NodeID, 0, len(f.peers))
	for _, peer := range f.peers {
		out = append(out, multiraft.NodeID(peer))
	}
	return out
}

func (f *bootstrapClusterFake) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	if f.leaderErr != nil {
		return 0, f.leaderErr
	}
	return multiraft.NodeID(f.leader), nil
}

type bootstrapRecordedLogEntry struct {
	level  string
	module string
	msg    string
	fields []wklog.Field
}

func (e bootstrapRecordedLogEntry) field(key string) (wklog.Field, bool) {
	for _, field := range e.fields {
		if field.Key == key {
			return field, true
		}
	}
	return wklog.Field{}, false
}

type bootstrapRecordingLoggerSink struct {
	mu      sync.Mutex
	entries []bootstrapRecordedLogEntry
}

type bootstrapRecordingLogger struct {
	module string
	base   []wklog.Field
	sink   *bootstrapRecordingLoggerSink
}

func newBootstrapRecordingLogger(module string) *bootstrapRecordingLogger {
	return &bootstrapRecordingLogger{module: module, sink: &bootstrapRecordingLoggerSink{}}
}

func (r *bootstrapRecordingLogger) Debug(msg string, fields ...wklog.Field) {
	r.log("DEBUG", msg, fields...)
}
func (r *bootstrapRecordingLogger) Info(msg string, fields ...wklog.Field) {
	r.log("INFO", msg, fields...)
}
func (r *bootstrapRecordingLogger) Warn(msg string, fields ...wklog.Field) {
	r.log("WARN", msg, fields...)
}
func (r *bootstrapRecordingLogger) Error(msg string, fields ...wklog.Field) {
	r.log("ERROR", msg, fields...)
}
func (r *bootstrapRecordingLogger) Fatal(msg string, fields ...wklog.Field) {
	r.log("FATAL", msg, fields...)
}

func (r *bootstrapRecordingLogger) Named(name string) wklog.Logger {
	if name == "" {
		return r
	}
	module := name
	if r.module != "" {
		module = r.module + "." + name
	}
	return &bootstrapRecordingLogger{module: module, base: append([]wklog.Field(nil), r.base...), sink: r.sink}
}

func (r *bootstrapRecordingLogger) With(fields ...wklog.Field) wklog.Logger {
	return &bootstrapRecordingLogger{
		module: r.module,
		base:   append(append([]wklog.Field(nil), r.base...), fields...),
		sink:   r.sink,
	}
}

func (r *bootstrapRecordingLogger) Sync() error { return nil }

func (r *bootstrapRecordingLogger) log(level, msg string, fields ...wklog.Field) {
	if r == nil || r.sink == nil {
		return
	}
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	r.sink.entries = append(r.sink.entries, bootstrapRecordedLogEntry{
		level:  level,
		module: r.module,
		msg:    msg,
		fields: append(append([]wklog.Field(nil), r.base...), fields...),
	})
}

func (r *bootstrapRecordingLogger) entries() []bootstrapRecordedLogEntry {
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	out := make([]bootstrapRecordedLogEntry, len(r.sink.entries))
	copy(out, r.sink.entries)
	return out
}

func requireBootstrapFieldEquals(t *testing.T, entry bootstrapRecordedLogEntry, key string, want any) {
	t.Helper()
	field, ok := entry.field(key)
	require.True(t, ok, "missing log field %s", key)
	require.Equal(t, want, field.Value)
}

func TestRuntimeBootstrapperRenewsLeaderLeaseWithoutClusterDependency(t *testing.T) {
	now := time.UnixMilli(1_700_000_555_000).UTC()
	expired := metadb.ChannelRuntimeMeta{
		ChannelID:    "lease-refresh",
		ChannelType:  1,
		ChannelEpoch: 5,
		LeaderEpoch:  7,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       2,
		MinISR:       2,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: now.Add(-time.Second).UnixMilli(),
	}
	renewed := expired
	renewed.LeaseUntilMS = now.Add(BootstrapLease).UnixMilli()
	store := &bootstrapStoreFake{authoritativeMeta: renewed}
	bootstrapper := NewBootstrapper(BootstrapOptions{
		Store:  store,
		Now:    func() time.Time { return now },
		Logger: wklog.NewNop(),
	})

	got, changed, err := bootstrapper.RenewChannelLeaderLease(context.Background(), expired, 2, time.Second)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, renewed, got)
	require.Len(t, store.upserts, 1)
	require.Equal(t, renewed.LeaseUntilMS, store.upserts[0].LeaseUntilMS)
}
