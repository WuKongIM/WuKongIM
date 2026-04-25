package wklog

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordedRaftLogEntry struct {
	level  string
	module string
	msg    string
	fields []Field
}

func (e recordedRaftLogEntry) field(key string) (Field, bool) {
	for _, field := range e.fields {
		if field.Key == key {
			return field, true
		}
	}
	return Field{}, false
}

type recordingRaftLoggerSink struct {
	mu      sync.Mutex
	entries []recordedRaftLogEntry
}

type recordingRaftLogger struct {
	module string
	base   []Field
	sink   *recordingRaftLoggerSink
}

func newRecordingRaftLogger(module string) *recordingRaftLogger {
	return &recordingRaftLogger{module: module, sink: &recordingRaftLoggerSink{}}
}

func (r *recordingRaftLogger) Debug(msg string, fields ...Field) { r.log("DEBUG", msg, fields...) }
func (r *recordingRaftLogger) Info(msg string, fields ...Field)  { r.log("INFO", msg, fields...) }
func (r *recordingRaftLogger) Warn(msg string, fields ...Field)  { r.log("WARN", msg, fields...) }
func (r *recordingRaftLogger) Error(msg string, fields ...Field) { r.log("ERROR", msg, fields...) }
func (r *recordingRaftLogger) Fatal(msg string, fields ...Field) { r.log("FATAL", msg, fields...) }

func (r *recordingRaftLogger) Named(name string) Logger {
	if name == "" {
		return r
	}
	module := name
	if r.module != "" {
		module = r.module + "." + name
	}
	return &recordingRaftLogger{module: module, base: append([]Field(nil), r.base...), sink: r.sink}
}

func (r *recordingRaftLogger) With(fields ...Field) Logger {
	merged := append(append([]Field(nil), r.base...), fields...)
	return &recordingRaftLogger{module: r.module, base: merged, sink: r.sink}
}

func (r *recordingRaftLogger) Sync() error { return nil }

func (r *recordingRaftLogger) log(level, msg string, fields ...Field) {
	if r == nil || r.sink == nil {
		return
	}
	entry := recordedRaftLogEntry{
		level:  level,
		module: r.module,
		msg:    msg,
		fields: append(append([]Field(nil), r.base...), fields...),
	}
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	r.sink.entries = append(r.sink.entries, entry)
}

func (r *recordingRaftLogger) entries() []recordedRaftLogEntry {
	if r == nil || r.sink == nil {
		return nil
	}
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	out := make([]recordedRaftLogEntry, len(r.sink.entries))
	copy(out, r.sink.entries)
	return out
}

func TestRaftLoggerInfofAddsStructuredContext(t *testing.T) {
	base := newRecordingRaftLogger("cluster.slot").Named("raft")

	logger := NewRaftLogger(base, RaftScope("slot"), NodeID(7), SlotID(11))
	logger.Infof("peer %d became leader", 7)

	entries := base.(*recordingRaftLogger).entries()
	require.Len(t, entries, 1)
	entry := entries[0]
	require.Equal(t, "INFO", entry.level)
	require.Equal(t, "cluster.slot.raft", entry.module)
	require.Equal(t, "peer 7 became leader", entry.msg)

	field, ok := entry.field("event")
	require.True(t, ok)
	require.Equal(t, "raft.log", field.Value)

	field, ok = entry.field("raftScope")
	require.True(t, ok)
	require.Equal(t, "slot", field.Value)

	field, ok = entry.field("nodeID")
	require.True(t, ok)
	require.Equal(t, uint64(7), field.Value)

	field, ok = entry.field("slotID")
	require.True(t, ok)
	require.Equal(t, uint64(11), field.Value)
}

func TestRaftLoggerWarningMapsToWarnLevel(t *testing.T) {
	base := newRecordingRaftLogger("cluster.controller").Named("raft")

	logger := NewRaftLogger(base, RaftScope("controller"), NodeID(3))
	logger.Warning("leader lost")

	entries := base.(*recordingRaftLogger).entries()
	require.Len(t, entries, 1)
	require.Equal(t, "WARN", entries[0].level)
	require.Equal(t, "leader lost", entries[0].msg)
}

func TestRaftLoggerClassifiesLeaderChange(t *testing.T) {
	recorder := newRecordingRaftLogger("cluster.slot")
	base := recorder.Named("raft")

	logger := NewRaftLogger(base, RaftScope("slot"), NodeID(7), SlotID(11))
	logger.Infof("7 became leader at term 3")

	entries := recorder.entries()
	require.Len(t, entries, 1)

	field, ok := entries[0].field("raftEvent")
	require.True(t, ok)
	require.Equal(t, "leader_change", field.Value)
}

func TestRaftLoggerClassifiesCampaign(t *testing.T) {
	recorder := newRecordingRaftLogger("cluster.controller")
	base := recorder.Named("raft")

	logger := NewRaftLogger(base, RaftScope("controller"), NodeID(2))
	logger.Infof("2 is starting a new election at term 9")

	entries := recorder.entries()
	require.Len(t, entries, 1)

	field, ok := entries[0].field("raftEvent")
	require.True(t, ok)
	require.Equal(t, "campaign", field.Value)
}

func TestRaftLoggerKeepsHeartbeatNoiseAtDebug(t *testing.T) {
	recorder := newRecordingRaftLogger("cluster.slot")
	base := recorder.Named("raft")

	logger := NewRaftLogger(base, RaftScope("slot"), NodeID(4), SlotID(22))
	logger.Infof("4 [logterm: 1, index: 10] sent MsgHeartbeat to 5")

	entries := recorder.entries()
	require.Len(t, entries, 1)
	require.Equal(t, "DEBUG", entries[0].level)

	field, ok := entries[0].field("raftEvent")
	require.True(t, ok)
	require.Equal(t, "heartbeat", field.Value)
}

func TestRaftLoggerClassifiesConfigChange(t *testing.T) {
	recorder := newRecordingRaftLogger("cluster.slot")
	base := recorder.Named("raft")

	logger := NewRaftLogger(base, RaftScope("slot"), NodeID(1), SlotID(3))
	logger.Infof("switched to configuration voters=(1 2 3)")

	entries := recorder.entries()
	require.Len(t, entries, 1)

	field, ok := entries[0].field("raftEvent")
	require.True(t, ok)
	require.Equal(t, "config_change", field.Value)
}

func TestRaftLoggerDefaultsUnknownMessage(t *testing.T) {
	recorder := newRecordingRaftLogger("cluster.slot")
	base := recorder.Named("raft")

	logger := NewRaftLogger(base, RaftScope("slot"), NodeID(1), SlotID(9))
	logger.Infof("some rare raft message")

	entries := recorder.entries()
	require.Len(t, entries, 1)

	field, ok := entries[0].field("raftEvent")
	require.True(t, ok)
	require.Equal(t, "general", field.Value)
}
