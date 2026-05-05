package raft

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeLogCompactionConfigDefaultsEnabled(t *testing.T) {
	got := NormalizeLogCompactionConfig(LogCompactionConfig{})
	require.True(t, got.Enabled)
	require.Equal(t, uint64(10000), got.TriggerEntries)
	require.Equal(t, 30*time.Second, got.CheckInterval)
}

func TestNormalizeLogCompactionConfigPreservesExplicitDisabled(t *testing.T) {
	got := NormalizeLogCompactionConfig(LogCompactionConfig{Enabled: false, EnabledSet: true})
	require.False(t, got.Enabled)
}

func TestValidateLogCompactionConfigRejectsEnabledInvalidValues(t *testing.T) {
	require.Error(t, ValidateLogCompactionConfig(LogCompactionConfig{Enabled: true, TriggerEntries: 0, CheckInterval: time.Second}))
	require.Error(t, ValidateLogCompactionConfig(LogCompactionConfig{Enabled: true, TriggerEntries: 1, CheckInterval: 0}))
}
