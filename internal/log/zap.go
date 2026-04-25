package log

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type zapLogger struct {
	log *zap.Logger
}

func NewLogger(cfg Config) (wklog.Logger, error) {
	cfg = cfg.withDefaults()
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	atomicLevel := zap.NewAtomicLevelAt(level)
	encoder := buildEncoder(cfg.Format)

	cores := []zapcore.Core{
		zapcore.NewCore(
			encoder,
			newRotateWriter(cfg, "app.log"),
			zap.LevelEnablerFunc(func(l zapcore.Level) bool {
				return atomicLevel.Enabled(l) && l >= zapcore.InfoLevel
			}),
		),
		zapcore.NewCore(
			encoder,
			newRotateWriter(cfg, "error.log"),
			zap.LevelEnablerFunc(func(l zapcore.Level) bool {
				return atomicLevel.Enabled(l) && l >= zapcore.ErrorLevel
			}),
		),
	}

	if atomicLevel.Enabled(zapcore.DebugLevel) {
		cores = append(cores, zapcore.NewCore(
			encoder,
			newRotateWriter(cfg, "debug.log"),
			zap.LevelEnablerFunc(func(l zapcore.Level) bool {
				return atomicLevel.Enabled(l) && l >= zapcore.DebugLevel
			}),
		))
	}

	if cfg.Console {
		cores = append(cores, zapcore.NewCore(buildConsoleEncoder(), zapcore.AddSync(os.Stdout), atomicLevel))
	}

	return &zapLogger{
		log: zap.New(
			zapcore.NewTee(cores...),
			zap.AddCaller(),
			zap.AddCallerSkip(1),
			zap.AddStacktrace(zapcore.ErrorLevel),
		),
	}, nil
}

func (z *zapLogger) Debug(msg string, fields ...wklog.Field) {
	z.log.Debug(msg, toZapFields(fields)...)
}

func (z *zapLogger) Info(msg string, fields ...wklog.Field) {
	z.log.Info(msg, toZapFields(fields)...)
}

func (z *zapLogger) Warn(msg string, fields ...wklog.Field) {
	z.log.Warn(msg, toZapFields(fields)...)
}

func (z *zapLogger) Error(msg string, fields ...wklog.Field) {
	z.log.Error(msg, toZapFields(fields)...)
}

func (z *zapLogger) Fatal(msg string, fields ...wklog.Field) {
	z.log.Fatal(msg, toZapFields(fields)...)
}

func (z *zapLogger) Named(name string) wklog.Logger {
	if strings.TrimSpace(name) == "" {
		return z
	}
	return &zapLogger{log: z.log.Named(name)}
}

func (z *zapLogger) With(fields ...wklog.Field) wklog.Logger {
	if len(fields) == 0 {
		return z
	}
	return &zapLogger{log: z.log.With(toZapFields(fields)...)}
}

func (z *zapLogger) Sync() error {
	if z == nil || z.log == nil {
		return nil
	}
	err := z.log.Sync()
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrInvalid) || errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EBADF) {
		return nil
	}
	return err
}

func parseLevel(level string) (zapcore.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return zapcore.InfoLevel, nil
	case "debug":
		return zapcore.DebugLevel, nil
	case "warn", "warning":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return zapcore.InfoLevel, fmt.Errorf("invalid log level %q", level)
	}
}

func toZapFields(fields []wklog.Field) []zap.Field {
	if len(fields) == 0 {
		return nil
	}
	out := make([]zap.Field, 0, len(fields))
	for _, field := range fields {
		switch field.Type {
		case wklog.StringType:
			out = append(out, zap.String(field.Key, field.Value.(string)))
		case wklog.IntType:
			out = append(out, zap.Int(field.Key, field.Value.(int)))
		case wklog.Int64Type:
			out = append(out, zap.Int64(field.Key, field.Value.(int64)))
		case wklog.Uint64Type:
			out = append(out, zap.Uint64(field.Key, field.Value.(uint64)))
		case wklog.Float64Type:
			out = append(out, zap.Float64(field.Key, field.Value.(float64)))
		case wklog.BoolType:
			out = append(out, zap.Bool(field.Key, field.Value.(bool)))
		case wklog.ErrorType:
			out = append(out, zap.Error(field.Value.(error)))
		case wklog.DurationType:
			out = append(out, zap.Duration(field.Key, field.Value.(time.Duration)))
		default:
			out = append(out, zap.Any(field.Key, field.Value))
		}
	}
	return out
}

func buildEncoder(format string) zapcore.Encoder {
	cfg := baseEncoderConfig()
	if strings.EqualFold(format, "json") {
		cfg.EncodeName = zapcore.FullNameEncoder
		return zapcore.NewJSONEncoder(cfg)
	}
	cfg.EncodeName = bracketNameEncoder
	return zapcore.NewConsoleEncoder(cfg)
}

func buildConsoleEncoder() zapcore.Encoder {
	cfg := baseEncoderConfig()
	cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.EncodeName = bracketNameEncoder
	return zapcore.NewConsoleEncoder(cfg)
}

func baseEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "module",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000"),
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
}

func bracketNameEncoder(name string, enc zapcore.PrimitiveArrayEncoder) {
	if name == "" {
		return
	}
	enc.AppendString("[" + name + "]")
}

func logPath(cfg Config, filename string) string {
	return filepath.Join(cfg.Dir, filename)
}
