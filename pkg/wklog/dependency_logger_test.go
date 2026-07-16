package wklog

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDependencyLoggerDemotesInfoAndPreservesFailures(t *testing.T) {
	recorder := newRecordingRaftLogger("root")
	logger := NewDependencyLogger(recorder, "pebble")

	logger.Infof("replayed %d keys", 16)
	logger.Warnf("slow recovery: %s", "2s")
	logger.Errorf("recovery failed: %v", errors.New("corrupt WAL"))

	entries := recorder.entries()
	require.Len(t, entries, 3)
	require.Equal(t, "DEBUG", entries[0].level)
	require.Equal(t, "replayed 16 keys", entries[0].msg)
	require.Equal(t, "WARN", entries[1].level)
	require.Equal(t, "ERROR", entries[2].level)
	for _, entry := range entries {
		field, ok := entry.field("sourceModule")
		require.True(t, ok)
		require.Equal(t, "pebble", field.Value)
	}
}
