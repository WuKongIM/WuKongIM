package capacity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	require.Equal(t, ProfileMixed, cfg.Profile)
	require.Equal(t, 100.0, cfg.StartQPS)
	require.Equal(t, 5000.0, cfg.MaxQPS)
	require.Equal(t, 1.5, cfg.StepFactor)
	require.Equal(t, 30*time.Second, cfg.Duration)
	require.Equal(t, 10*time.Second, cfg.Warmup)
	require.Equal(t, 3*time.Second, cfg.Cooldown)
	require.Equal(t, 200*time.Millisecond, cfg.StableP99)
	require.Equal(t, 0.95, cfg.MinActualRatio)
	require.True(t, cfg.BinarySearch)
	require.Equal(t, 0.05, cfg.BinarySearchMinDeltaRatio)
	require.Equal(t, 10, cfg.GroupMembers)
}

func TestConfigValidateRequiresAPIAddrs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = nil

	require.ErrorContains(t, cfg.Validate(), "api")
}

func TestConfigValidateRejectsInvalidProfile(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.Profile = "bad"

	require.ErrorContains(t, cfg.Validate(), "profile")
}

func TestDefaultHotChannelConfig(t *testing.T) {
	cfg := DefaultHotChannelConfig()

	require.Equal(t, ProfileGroup, cfg.Profile)
	require.Equal(t, 16, cfg.Senders)
	require.Equal(t, "./tmp/wkbench-hot-channel", cfg.ReportDir)
	require.Equal(t, 50000.0, cfg.MaxQPS)
}

func TestHotChannelConfigValidateRequiresSenderFanIn(t *testing.T) {
	cfg := DefaultHotChannelConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
	cfg.Senders = 0

	require.ErrorContains(t, cfg.Validate(), "senders")
}
