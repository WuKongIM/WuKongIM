package pluginhost

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	legacywklog "github.com/WuKongIM/wklog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var legacyRPCLogInitOnce sync.Once

// ensureLegacyRPCLogState initializes the legacy global level read that wkrpc
// still performs after every handled request. Debug preserves any existing
// legacy configuration and is silent under the dependency's default level.
func ensureLegacyRPCLogState() {
	legacyRPCLogInitOnce.Do(func() {
		legacywklog.Debug("initialize wkrpc compatibility logger")
	})
}

type legacyRPCLogger struct {
	logger *wklog.DependencyLogger
}

func newLegacyRPCLogger(logger wklog.Logger) *legacyRPCLogger {
	return &legacyRPCLogger{logger: wklog.NewDependencyLogger(logger, "wkrpc")}
}

func (l *legacyRPCLogger) Info(msg string, fields ...zap.Field) {
	l.logger.Info(msg, legacyRPCFields(fields)...)
}

func (l *legacyRPCLogger) MessageTrace(msg, clientMsgNo, operationName string, fields ...zap.Field) {
	out := legacyRPCFields(fields)
	out = append(out, wklog.String("clientMsgNo", clientMsgNo), wklog.String("operation", operationName))
	l.logger.Info(msg, out...)
}

func (l *legacyRPCLogger) Trace(msg, action string, fields ...zap.Field) {
	out := legacyRPCFields(fields)
	out = append(out, wklog.String("action", action))
	l.logger.Info(msg, out...)
}

func (l *legacyRPCLogger) Debug(msg string, fields ...zap.Field) {
	l.logger.Debug(msg, legacyRPCFields(fields)...)
}

func (l *legacyRPCLogger) Error(msg string, fields ...zap.Field) {
	l.logger.Error(msg, legacyRPCFields(fields)...)
}

func (l *legacyRPCLogger) Warn(msg string, fields ...zap.Field) {
	l.logger.Warn(msg, legacyRPCFields(fields)...)
}

func (l *legacyRPCLogger) Fatal(msg string, fields ...zap.Field) {
	l.logger.Fatal(msg, legacyRPCFields(fields)...)
}

func (l *legacyRPCLogger) Panic(msg string, fields ...zap.Field) {
	l.logger.Error(msg, legacyRPCFields(fields)...)
	panic(msg)
}

func (l *legacyRPCLogger) Foucs(msg string, fields ...zap.Field) {
	l.logger.Info(msg, legacyRPCFields(fields)...)
}

func legacyRPCFields(fields []zap.Field) []wklog.Field {
	if len(fields) == 0 {
		return nil
	}
	out := make([]wklog.Field, 0, len(fields))
	for _, field := range fields {
		encoder := zapcore.NewMapObjectEncoder()
		field.AddTo(encoder)
		out = append(out, wklog.Any(field.Key, encoder.Fields[field.Key]))
	}
	return out
}
