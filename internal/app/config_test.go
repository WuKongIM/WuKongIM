package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWebhookConfigDefaultsWhenEndpointConfigured(t *testing.T) {
	cfg, err := NormalizeWebhookConfig(WebhookConfig{
		HTTPAddr: "http://127.0.0.1:18080/hook",
	})
	if err != nil {
		t.Fatalf("NormalizeWebhookConfig() error = %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("Enabled = false, want true when HTTPAddr is configured")
	}
	if cfg.QueueSize != 1024 {
		t.Fatalf("QueueSize = %d, want 1024", cfg.QueueSize)
	}
	if cfg.Workers != 16 {
		t.Fatalf("Workers = %d, want 16", cfg.Workers)
	}
	if cfg.NotifyBatchMaxItems != 100 {
		t.Fatalf("NotifyBatchMaxItems = %d, want 100", cfg.NotifyBatchMaxItems)
	}
	if cfg.NotifyBatchMaxWait != 500*time.Millisecond {
		t.Fatalf("NotifyBatchMaxWait = %v, want 500ms", cfg.NotifyBatchMaxWait)
	}
	if cfg.OnlineBatchMaxItems != 512 {
		t.Fatalf("OnlineBatchMaxItems = %d, want 512", cfg.OnlineBatchMaxItems)
	}
	if cfg.OnlineBatchMaxWait != 2*time.Second {
		t.Fatalf("OnlineBatchMaxWait = %v, want 2s", cfg.OnlineBatchMaxWait)
	}
	if cfg.OfflineUIDBatchSize != 512 {
		t.Fatalf("OfflineUIDBatchSize = %d, want 512", cfg.OfflineUIDBatchSize)
	}
	if cfg.RequestTimeout != 5*time.Second {
		t.Fatalf("RequestTimeout = %v, want 5s", cfg.RequestTimeout)
	}
	if cfg.RetryMaxAttempts != 3 {
		t.Fatalf("RetryMaxAttempts = %d, want 3", cfg.RetryMaxAttempts)
	}
}

func TestWebhookConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  WebhookConfig
	}{
		{name: "enabled without endpoint", cfg: WebhookConfig{Enabled: true}},
		{name: "disabled negative queue", cfg: WebhookConfig{QueueSize: -1}},
		{name: "negative queue", cfg: WebhookConfig{HTTPAddr: "http://127.0.0.1/hook", QueueSize: -1}},
		{name: "negative workers", cfg: WebhookConfig{HTTPAddr: "http://127.0.0.1/hook", Workers: -1}},
		{name: "negative notify batch", cfg: WebhookConfig{HTTPAddr: "http://127.0.0.1/hook", NotifyBatchMaxItems: -1}},
		{name: "negative online wait", cfg: WebhookConfig{HTTPAddr: "http://127.0.0.1/hook", OnlineBatchMaxWait: -1}},
		{name: "negative retry", cfg: WebhookConfig{HTTPAddr: "http://127.0.0.1/hook", RetryMaxAttempts: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NormalizeWebhookConfig(tt.cfg); err == nil {
				t.Fatalf("NormalizeWebhookConfig() error = nil, want error")
			}
		})
	}
}

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

func TestPluginConfigDefaultsEnablePlugins(t *testing.T) {
	dataDir := t.TempDir()
	app := &App{cfg: Config{DataDir: dataDir}}
	require.NoError(t, app.applyConfigDefaults())
	require.True(t, app.cfg.Plugin.Enable)
	require.Equal(t, filepath.Join(dataDir, "plugins"), app.cfg.Plugin.Dir)
	require.Equal(t, filepath.Join(dataDir, "run", "plugin.sock"), app.cfg.Plugin.SocketPath)
	require.Equal(t, filepath.Join(dataDir, "plugin-sandbox"), app.cfg.Plugin.SandboxDir)
	require.Equal(t, filepath.Join(dataDir, "plugin-state"), app.cfg.Plugin.StateDir)
}

func TestPluginConfigExplicitEnableFalseDisablesPlugins(t *testing.T) {
	plugin := PluginConfig{Enable: false}
	plugin.SetEnableExplicit(true)
	plugin.SetExplicitFlags(false)
	app := &App{cfg: Config{DataDir: t.TempDir(), Plugin: plugin}}
	require.NoError(t, app.applyConfigDefaults())
	require.False(t, app.cfg.Plugin.Enable)
	require.Empty(t, app.cfg.Plugin.Dir)
	require.Empty(t, app.cfg.Plugin.SocketPath)
	require.Empty(t, app.cfg.Plugin.SandboxDir)
	require.Empty(t, app.cfg.Plugin.StateDir)
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
