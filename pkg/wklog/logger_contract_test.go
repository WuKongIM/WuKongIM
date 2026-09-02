package wklog

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRaftLoggerClassifiesLeaderLoss(t *testing.T) {
	recorder := newRecordingRaftLogger("cluster.controller")
	logger := NewRaftLogger(recorder, RaftScope("controller"), NodeID(3))

	logger.Warning("leader lost")

	entries := recorder.entries()
	require.Len(t, entries, 1)
	field, ok := entries[0].field("raftEvent")
	require.True(t, ok)
	require.Equal(t, "leader_change", field.Value)
}

func TestDebugEnabledHonorsOptionalCapability(t *testing.T) {
	require.False(t, DebugEnabled(nil))
	require.False(t, DebugEnabled(NewNop()))
	require.True(t, DebugEnabled(&debugFlagLogger{Logger: NewNop(), enabled: true}))
	require.True(t, DebugEnabled(newRecordingRaftLogger("legacy")), "loggers without the optional capability remain debug-enabled")
}

func TestDependencyLoggerPreservesPublicLevelsFieldsAndFormatting(t *testing.T) {
	recorder := newRecordingRaftLogger("root")
	logger := NewDependencyLogger(recorder, "  pebble  ")

	logger.Debug("  cache opened  ", RequestID("req-1"))
	logger.Fatal("  WAL corrupted  ", ErrorCode("wal_corrupt"))
	logger.Debugf("  compacted %d tables  ", 3)
	logger.Fatalf("  manifest %s missing  ", "CURRENT")

	entries := recorder.entries()
	require.Len(t, entries, 4)
	require.Equal(t, []string{"DEBUG", "FATAL", "DEBUG", "FATAL"}, []string{
		entries[0].level, entries[1].level, entries[2].level, entries[3].level,
	})
	require.Equal(t, []string{"cache opened", "WAL corrupted", "compacted 3 tables", "manifest CURRENT missing"}, []string{
		entries[0].msg, entries[1].msg, entries[2].msg, entries[3].msg,
	})
	for _, entry := range entries {
		event, ok := entry.field("event")
		require.True(t, ok)
		require.Equal(t, "dependency.log", event.Value)
		module, ok := entry.field("sourceModule")
		require.True(t, ok)
		require.Equal(t, "pebble", module.Value)
	}
	requestID, ok := entries[0].field("requestID")
	require.True(t, ok)
	require.Equal(t, "req-1", requestID.Value)
}

func TestDependencyLoggerUsesNopFallback(t *testing.T) {
	logger := NewDependencyLogger(nil, "  optional-store  ")
	require.NotNil(t, logger)
	require.NotPanics(t, func() {
		logger.Debugf("opened shard %d", 1)
		logger.Fatalf("closed shard %d", 1)
	})
}

func TestRaftLoggerPublicMethodsClassifyNoiseAndFailures(t *testing.T) {
	recorder := newRecordingRaftLogger("cluster.slot")
	logger := NewRaftLogger(recorder, RaftScope("slot"), NodeID(2), SlotID(9))

	logger.Debug("snapshot", 7)
	logger.Debugf("probe peer %d", 3)
	logger.Info("read index ready")
	logger.Info()
	logger.Error("proposal rejected")
	logger.Errorf("snapshot failed at %d", 11)
	logger.Warningf("heartbeat delayed by %d ticks", 2)

	entries := recorder.entries()
	require.Len(t, entries, 7)
	want := []struct {
		level string
		msg   string
		event string
	}{
		{level: "DEBUG", msg: "snapshot 7", event: "snapshot"},
		{level: "DEBUG", msg: "probe peer 3", event: "probe"},
		{level: "DEBUG", msg: "read index ready", event: "read_index"},
		{level: "DEBUG", msg: "", event: "general"},
		{level: "ERROR", msg: "proposal rejected", event: "proposal"},
		{level: "ERROR", msg: "snapshot failed at 11", event: "snapshot"},
		{level: "WARN", msg: "heartbeat delayed by 2 ticks", event: "heartbeat"},
	}
	for i, expected := range want {
		require.Equal(t, expected.level, entries[i].level)
		require.Equal(t, expected.msg, entries[i].msg)
		field, ok := entries[i].field("raftEvent")
		require.True(t, ok)
		require.Equal(t, expected.event, field.Value)
	}
}

func TestRaftLoggerFatalAndPanicMethodsLogBeforeTerminating(t *testing.T) {
	tests := []struct {
		name      string
		wantLevel string
		wantPanic string
		invoke    func(*RaftLogger)
	}{
		{name: "fatal", wantLevel: "FATAL", wantPanic: "fatal 7", invoke: func(logger *RaftLogger) { logger.Fatal("fatal", 7) }},
		{name: "fatal formatted", wantLevel: "FATAL", wantPanic: "fatal 8", invoke: func(logger *RaftLogger) { logger.Fatalf("fatal %d", 8) }},
		{name: "panic", wantLevel: "ERROR", wantPanic: "panic 9", invoke: func(logger *RaftLogger) { logger.Panic("panic", 9) }},
		{name: "panic formatted", wantLevel: "ERROR", wantPanic: "panic 10", invoke: func(logger *RaftLogger) { logger.Panicf("panic %d", 10) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newRecordingRaftLogger("cluster.controller")
			logger := NewRaftLogger(recorder, RaftScope("controller"))

			require.PanicsWithValue(t, tt.wantPanic, func() { tt.invoke(logger) })
			entries := recorder.entries()
			require.Len(t, entries, 1)
			require.Equal(t, tt.wantLevel, entries[0].level)
			require.Equal(t, tt.wantPanic, entries[0].msg)
			event, ok := entries[0].field("event")
			require.True(t, ok)
			require.Equal(t, "raft.log", event.Value)
		})
	}
}

func TestRaftLoggerConcurrentWritesKeepBaseFieldsIsolated(t *testing.T) {
	recorder := newRecordingRaftLogger("cluster.slot")
	logger := NewRaftLogger(recorder, RaftScope("slot"), NodeID(5), SlotID(12))
	const writers = 32

	var wg sync.WaitGroup
	wg.Add(writers)
	for writer := 0; writer < writers; writer++ {
		go func(writer int) {
			defer wg.Done()
			logger.Debugf("proposal from worker %d", writer)
		}(writer)
	}
	wg.Wait()

	entries := recorder.entries()
	require.Len(t, entries, writers)
	for _, entry := range entries {
		require.Equal(t, "DEBUG", entry.level)
		scope, ok := entry.field("raftScope")
		require.True(t, ok)
		require.Equal(t, "slot", scope.Value)
		node, ok := entry.field("nodeID")
		require.True(t, ok)
		require.Equal(t, uint64(5), node.Value)
		slot, ok := entry.field("slotID")
		require.True(t, ok)
		require.Equal(t, uint64(12), slot.Value)
	}
}

type debugFlagLogger struct {
	Logger
	enabled bool
}

func (l *debugFlagLogger) DebugEnabled() bool {
	return l.enabled
}
