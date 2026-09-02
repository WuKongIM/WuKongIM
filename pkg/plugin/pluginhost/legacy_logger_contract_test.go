package pluginhost

import (
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestLegacyRPCLoggerMapsLevelsAndStructuredContext(t *testing.T) {
	base := newRecordingPluginLogger()
	logger := newLegacyRPCLogger(base)

	logger.Info("connected", zap.String("uid", "alpha"))
	logger.MessageTrace("handled", "client-1", "Send", zap.Int("bytes", 12))
	logger.Trace("dispatch", "request", zap.Bool("cached", true))
	logger.Debug("debug")
	logger.Warn("slow")
	logger.Error("failed")
	logger.Foucs("focus")
	logger.Fatal("fatal")

	entries := base.entries()
	require.Len(t, entries, 8)
	require.Equal(t, []string{"DEBUG", "DEBUG", "DEBUG", "DEBUG", "WARN", "ERROR", "DEBUG", "FATAL"}, pluginLogLevels(entries))
	for _, entry := range entries {
		require.Equal(t, "dependency.log", entry.fieldValue("event"))
		require.Equal(t, "wkrpc", entry.fieldValue("sourceModule"))
	}
	require.Equal(t, "alpha", entries[0].fieldValue("uid"))
	require.Equal(t, "client-1", entries[1].fieldValue("clientMsgNo"))
	require.Equal(t, "Send", entries[1].fieldValue("operation"))
	require.Equal(t, "request", entries[2].fieldValue("action"))
	require.Nil(t, legacyRPCFields(nil))
}

type recordedPluginLog struct {
	level  string
	msg    string
	fields []wklog.Field
}

func (e recordedPluginLog) fieldValue(key string) any {
	for _, field := range e.fields {
		if field.Key == key {
			return field.Value
		}
	}
	return nil
}

type recordingPluginLoggerSink struct {
	mu      sync.Mutex
	entries []recordedPluginLog
}

type recordingPluginLogger struct {
	base []wklog.Field
	sink *recordingPluginLoggerSink
}

func newRecordingPluginLogger() *recordingPluginLogger {
	return &recordingPluginLogger{sink: &recordingPluginLoggerSink{}}
}

func (l *recordingPluginLogger) Debug(msg string, fields ...wklog.Field) {
	l.record("DEBUG", msg, fields...)
}
func (l *recordingPluginLogger) Info(msg string, fields ...wklog.Field) {
	l.record("INFO", msg, fields...)
}
func (l *recordingPluginLogger) Warn(msg string, fields ...wklog.Field) {
	l.record("WARN", msg, fields...)
}
func (l *recordingPluginLogger) Error(msg string, fields ...wklog.Field) {
	l.record("ERROR", msg, fields...)
}
func (l *recordingPluginLogger) Fatal(msg string, fields ...wklog.Field) {
	l.record("FATAL", msg, fields...)
}

func (l *recordingPluginLogger) Named(string) wklog.Logger { return l }

func (l *recordingPluginLogger) With(fields ...wklog.Field) wklog.Logger {
	return &recordingPluginLogger{base: append(append([]wklog.Field(nil), l.base...), fields...), sink: l.sink}
}

func (l *recordingPluginLogger) Sync() error { return nil }

func (l *recordingPluginLogger) record(level, msg string, fields ...wklog.Field) {
	entry := recordedPluginLog{
		level:  level,
		msg:    msg,
		fields: append(append([]wklog.Field(nil), l.base...), fields...),
	}
	l.sink.mu.Lock()
	l.sink.entries = append(l.sink.entries, entry)
	l.sink.mu.Unlock()
}

func (l *recordingPluginLogger) entries() []recordedPluginLog {
	l.sink.mu.Lock()
	defer l.sink.mu.Unlock()
	return append([]recordedPluginLog(nil), l.sink.entries...)
}

func pluginLogLevels(entries []recordedPluginLog) []string {
	levels := make([]string, 0, len(entries))
	for _, entry := range entries {
		levels = append(levels, entry.level)
	}
	return levels
}
