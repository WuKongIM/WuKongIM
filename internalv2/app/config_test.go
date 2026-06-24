package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPluginConfigDefaultsDerivePathsWhenEnabled(t *testing.T) {
	cfg := Config{DataDir: t.TempDir(), Plugin: PluginConfig{Enable: true}}
	app := &App{cfg: cfg}
	require.NoError(t, app.applyConfigDefaults())

	require.True(t, app.cfg.Plugin.Enable)
	require.Equal(t, filepath.Join(cfg.DataDir, "plugins"), app.cfg.Plugin.Dir)
	require.Equal(t, filepath.Join(cfg.DataDir, "run", "plugin.sock"), app.cfg.Plugin.SocketPath)
	require.Equal(t, filepath.Join(cfg.DataDir, "plugin-sandbox"), app.cfg.Plugin.SandboxDir)
	require.Equal(t, filepath.Join(cfg.DataDir, "plugin-state"), app.cfg.Plugin.StateDir)
	require.Equal(t, 5*time.Second, app.cfg.Plugin.Timeout)
	require.True(t, app.cfg.Plugin.HotReload)
	require.Equal(t, 1024, app.cfg.Plugin.PersistAfterQueueSize)
	require.Equal(t, 16, app.cfg.Plugin.PersistAfterWorkers)
}

func TestPluginConfigExplicitHotReloadFalse(t *testing.T) {
	plugin := PluginConfig{Enable: true, HotReload: false}
	plugin.SetExplicitFlags(true)
	app := &App{cfg: Config{DataDir: t.TempDir(), Plugin: plugin}}
	require.NoError(t, app.applyConfigDefaults())
	require.False(t, app.cfg.Plugin.HotReload)
}

func TestPluginConfigDefaultsKeepPluginsDisabled(t *testing.T) {
	app := &App{cfg: Config{DataDir: t.TempDir()}}
	require.NoError(t, app.applyConfigDefaults())
	require.False(t, app.cfg.Plugin.Enable)
}

func TestPluginConfigValidationRejectsInvalidBounds(t *testing.T) {
	cases := []PluginConfig{
		{Enable: true, Timeout: -time.Second},
		{Enable: true, PersistAfterQueueSize: -1},
		{Enable: true, PersistAfterWorkers: -1},
	}
	for _, cfg := range cases {
		app := &App{cfg: Config{DataDir: t.TempDir(), Plugin: cfg}}
		require.Error(t, app.applyConfigDefaults())
	}
}

func TestChannelMessageRetentionConfigDefaults(t *testing.T) {
	app := &App{cfg: Config{DataDir: t.TempDir()}}
	require.NoError(t, app.applyConfigDefaults())

	require.False(t, app.cfg.ChannelMessageRetention.PhysicalGCEnabled)
	require.Equal(t, time.Minute, app.cfg.ChannelMessageRetention.ScanInterval)
	require.Equal(t, 128, app.cfg.ChannelMessageRetention.ChannelBatchSize)
	require.Equal(t, 1000, app.cfg.ChannelMessageRetention.MaxTrimMessages)
	require.Equal(t, 0, app.cfg.ChannelMessageRetention.MaxTrimBytes)

	cluster := defaultClusterConfig(app.cfg)
	require.Equal(t, app.cfg.ChannelMessageRetention.PhysicalGCEnabled, cluster.ChannelRetention.PhysicalGCEnabled)
	require.Equal(t, app.cfg.ChannelMessageRetention.ScanInterval, cluster.ChannelRetention.ScanInterval)
	require.Equal(t, app.cfg.ChannelMessageRetention.ChannelBatchSize, cluster.ChannelRetention.ChannelBatchSize)
	require.Equal(t, app.cfg.ChannelMessageRetention.MaxTrimMessages, cluster.ChannelRetention.MaxTrimMessages)
	require.Equal(t, app.cfg.ChannelMessageRetention.MaxTrimBytes, cluster.ChannelRetention.MaxTrimBytes)
}

func TestChannelMessageRetentionConfigValidationRejectsInvalidBounds(t *testing.T) {
	cases := []ChannelMessageRetentionConfig{
		{ScanInterval: -time.Second},
		{ChannelBatchSize: -1},
		{MaxTrimMessages: -1},
		{MaxTrimBytes: -1},
	}
	for _, cfg := range cases {
		app := &App{cfg: Config{DataDir: t.TempDir(), ChannelMessageRetention: cfg}}
		require.Error(t, app.applyConfigDefaults())
	}
}
