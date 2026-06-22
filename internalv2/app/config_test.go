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
