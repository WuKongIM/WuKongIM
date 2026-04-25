package wklog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewNopImplementsLoggerContract(t *testing.T) {
	logger := NewNop()
	require.NotNil(t, logger)

	logger.Debug("debug", String("module", "test"))
	logger.Info("info")
	logger.Warn("warn")
	logger.Error("error")
	require.NotPanics(t, func() {
		logger.Fatal("fatal")
	})

	require.Same(t, logger, logger.Named("child"))
	require.Same(t, logger, logger.With(String("fixed", "value")))
	require.NoError(t, logger.Sync())
}
