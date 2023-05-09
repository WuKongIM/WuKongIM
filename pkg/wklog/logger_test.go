package wklog

import (
	"testing"

	"go.uber.org/zap"
)

func TestLogger(t *testing.T) {
	opts := NewOptions()
	opts.Level = zap.DebugLevel
	opts.LineNum = true
	Configure(opts)

	Info("this is info")
	Debug("this is debug")
	Error("this is error", zap.String("key", "value"))
}
