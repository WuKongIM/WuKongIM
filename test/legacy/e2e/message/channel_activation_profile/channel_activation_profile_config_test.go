//go:build e2e && legacy_e2e

package channel_activation_profile

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelActivationProfileConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv(channelActivationProfileEnabledEnv, "")
	t.Setenv(channelActivationProfileArtifactDirEnv, "")
	t.Setenv(channelActivationProfileChannelCountEnv, "")
	t.Setenv(channelActivationProfileRoundCountEnv, "")
	t.Setenv(channelActivationProfileCPUSecondsEnv, "")
	t.Setenv(channelActivationProfileTestTimeoutEnv, "")

	cfg, err := loadChannelActivationProfileConfig()
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.Equal(t, 100, cfg.ChannelCount)
	require.Equal(t, 5, cfg.RoundCount)
	require.Equal(t, 3, cfg.CPUSeconds)
	require.Equal(t, filepath.Join("tmp", "profiles", "e2e-channel-activation-100"), cfg.ArtifactDir)
	require.Equal(t, 120*time.Second, cfg.TestTimeout)

	t.Setenv(channelActivationProfileEnabledEnv, "yes")
	t.Setenv(channelActivationProfileArtifactDirEnv, filepath.Join("tmp", "profiles", "custom-channel-activation"))
	t.Setenv(channelActivationProfileChannelCountEnv, "12")
	t.Setenv(channelActivationProfileRoundCountEnv, "3")
	t.Setenv(channelActivationProfileCPUSecondsEnv, "2")
	t.Setenv(channelActivationProfileTestTimeoutEnv, "45s")

	cfg, err = loadChannelActivationProfileConfig()
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
	require.Equal(t, 12, cfg.ChannelCount)
	require.Equal(t, 3, cfg.RoundCount)
	require.Equal(t, 2, cfg.CPUSeconds)
	require.Equal(t, filepath.Join("tmp", "profiles", "custom-channel-activation"), cfg.ArtifactDir)
	require.Equal(t, 45*time.Second, cfg.TestTimeout)
}

func TestChannelActivationProfileConfigRejectsInvalidPositiveKnobs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		envName  string
		envValue string
		want     string
	}{
		{name: "channels", envName: channelActivationProfileChannelCountEnv, envValue: "0", want: channelActivationProfileChannelCountEnv},
		{name: "rounds", envName: channelActivationProfileRoundCountEnv, envValue: "0", want: channelActivationProfileRoundCountEnv},
		{name: "cpu_seconds", envName: channelActivationProfileCPUSecondsEnv, envValue: "0", want: channelActivationProfileCPUSecondsEnv},
		{name: "timeout", envName: channelActivationProfileTestTimeoutEnv, envValue: "0s", want: channelActivationProfileTestTimeoutEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(channelActivationProfileEnabledEnv, "")
			t.Setenv(channelActivationProfileArtifactDirEnv, "")
			t.Setenv(channelActivationProfileChannelCountEnv, "")
			t.Setenv(channelActivationProfileRoundCountEnv, "")
			t.Setenv(channelActivationProfileCPUSecondsEnv, "")
			t.Setenv(channelActivationProfileTestTimeoutEnv, "")
			t.Setenv(tc.envName, tc.envValue)

			_, err := loadChannelActivationProfileConfig()
			require.ErrorContains(t, err, tc.want)
		})
	}
}
