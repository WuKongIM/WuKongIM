package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBackupConfigDefaultsStayDisabled(t *testing.T) {
	app := &App{cfg: Config{DataDir: t.TempDir()}}
	require.NoError(t, app.applyConfigDefaults())
	require.False(t, app.cfg.Backup.Enabled)
	require.Equal(t, BackupProviderAlibaba, app.cfg.Backup.Provider)
	require.Equal(t, 30*time.Second, app.cfg.Backup.CaptureReconcileInterval)
	require.Equal(t, 5*time.Minute, app.cfg.Backup.CheckpointInterval)
	require.Equal(t, uint64(8*1024*1024), app.cfg.Backup.BaselineChunkBytes)
	require.Equal(t, uint64(64*1024*1024), app.cfg.Backup.TargetSegmentBytes)
	require.Equal(t, uint64(256*1024*1024), app.cfg.Backup.MaxSegmentBytes)
	require.Equal(t, 30*time.Second, app.cfg.Backup.MaxSegmentOpenDuration)
	require.Equal(t, 4, app.cfg.Backup.WorkerCount)
	require.Equal(t, 30*time.Minute, app.cfg.Backup.SourcePinMaxAge)
	require.Equal(t, uint64(20*1024*1024*1024), app.cfg.Backup.MaxSourcePinnedBytes)
	require.Equal(t, time.Second, app.cfg.Backup.AuditInterval)
	require.Equal(t, 24*time.Hour, app.cfg.Backup.AuditScrubInterval)
	require.Equal(t, time.Hour, app.cfg.Backup.GarbageCollectionInterval)
	require.Equal(t, 7*24*time.Hour, app.cfg.Backup.GarbageSafetyWindow)
	require.Equal(t, 256, app.cfg.Backup.GarbageMaxRequestsPerRepository)
	require.Equal(t, uint64(1<<30), app.cfg.Backup.GarbageMaxBytesPerRepository)
	require.Zero(t, app.cfg.Backup.RetentionMonthlyMonths)
}

func TestBackupConfigRequiresAlibabaRoleSeparation(t *testing.T) {
	cfg := validEnabledBackupConfig(t)
	cfg.Primary.AccessRoleARN = ""
	cfg.Secondary.AccessRoleARN = ""
	_, err := NormalizeBackupConfig(cfg)
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Contains(t, err.Error(), "ordinary access role ARNs")

	cfg.Primary.AccessRoleARN = "acs:ram::123456789:role/backup-primary"
	cfg.Secondary.AccessRoleARN = "acs:ram::123456789:role/backup-secondary"
	normalized, err := NormalizeBackupConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, BackupProviderAlibaba, normalized.Provider)
}

func TestBackupConfigRejectsUnqualifiedProvider(t *testing.T) {
	cfg := validEnabledBackupConfig(t)
	cfg.Provider = "aws"
	_, err := NormalizeBackupConfig(cfg)
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Contains(t, err.Error(), `provider must be "aliyun"`)
}

func TestBackupConfigRequiresDistinctAlibabaRoles(t *testing.T) {
	cfg := validEnabledBackupConfig(t)
	cfg.Primary.RepairRoleARN = cfg.Primary.AccessRoleARN
	_, err := NormalizeBackupConfig(cfg)
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Contains(t, err.Error(), "roles must be distinct")
}

func TestBackupConfigRequiresSegmentTargetWithinConfiguredMaximum(t *testing.T) {
	cfg := validEnabledBackupConfig(t)
	cfg.TargetSegmentBytes = 4 << 20
	cfg.MaxSegmentBytes = 3 << 20
	_, err := NormalizeBackupConfig(cfg)
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Contains(t, err.Error(), "max segment bytes")

	cfg.MaxSegmentBytes = 8 << 20
	normalized, err := NormalizeBackupConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, uint64(8<<20), normalized.MaxSegmentBytes)
}

func TestBackupConfigRejectsSameRegionRepositories(t *testing.T) {
	cfg := validEnabledBackupConfig(t)
	cfg.Secondary.Region = cfg.Primary.Region
	_, err := NormalizeBackupConfig(cfg)
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Contains(t, err.Error(), "different regions")
}

func validEnabledBackupConfig(t *testing.T) BackupConfig {
	t.Helper()
	return BackupConfig{
		Provider:         BackupProviderAlibaba,
		Enabled:          true,
		RepositoryID:     "cluster-a-dr",
		SourceGeneration: "generation-1",
		StagingDir:       filepath.Join(t.TempDir(), "backup-staging"),
		ObjectLockDays:   7,
		Primary: BackupRepositoryConfig{
			Endpoint:       "https://oss-cn-hangzhou.aliyuncs.com",
			Region:         "cn-hangzhou",
			Bucket:         "primary",
			Prefix:         "cluster-a",
			AccessRoleARN:  "acs:ram::123456789:role/backup-primary",
			RepairRoleARN:  "acs:ram::123456789:role/backup-primary-repair",
			GarbageRoleARN: "acs:ram::123456789:role/backup-primary-garbage",
		},
		Secondary: BackupRepositoryConfig{
			Endpoint:       "https://oss-cn-beijing.aliyuncs.com",
			Region:         "cn-beijing",
			Bucket:         "secondary",
			Prefix:         "cluster-a",
			AccessRoleARN:  "acs:ram::123456789:role/backup-secondary",
			RepairRoleARN:  "acs:ram::123456789:role/backup-secondary-repair",
			GarbageRoleARN: "acs:ram::123456789:role/backup-secondary-garbage",
		},
	}
}

func TestBackupRestoreModeRequiresFreshTargetGenerationInsteadOfSourceGeneration(t *testing.T) {
	cfg := BackupConfig{
		Provider:         BackupProviderAlibaba,
		RestoreMode:      true,
		RepositoryID:     "cluster-a-dr",
		TargetGeneration: "generation-2",
		StagingDir:       filepath.Join(t.TempDir(), "backup-staging"),
		Primary: BackupRepositoryConfig{
			Endpoint:      "https://primary.example",
			Region:        "region-a",
			Bucket:        "primary",
			Prefix:        "cluster-a",
			AccessRoleARN: "acs:ram::123456789:role/backup-primary",
		},
		Secondary: BackupRepositoryConfig{
			Endpoint:      "https://secondary.example",
			Region:        "region-b",
			Bucket:        "secondary",
			Prefix:        "cluster-a",
			AccessRoleARN: "acs:ram::123456789:role/backup-secondary",
		},
	}
	normalized, err := NormalizeBackupConfig(cfg)
	require.NoError(t, err)
	require.Empty(t, normalized.SourceGeneration)
	require.Equal(t, "generation-2", normalized.TargetGeneration)

	cfg.TargetGeneration = ""
	_, err = NormalizeBackupConfig(cfg)
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestRestoreModeManagerRequiresAuthAndExplicitActivationGrant(t *testing.T) {
	backup := BackupConfig{RestoreMode: true}
	manager := ManagerConfig{ListenAddr: "127.0.0.1:5300"}
	require.ErrorIs(t, validateRestoreModeManagerConfig(backup, manager), ErrInvalidConfig)

	manager.AuthOn = true
	manager.Users = []ManagerUserConfig{{
		Username: "admin", Password: "secret",
		Permissions: []ManagerPermissionConfig{{Resource: "*", Actions: []string{"*"}}},
	}}
	require.ErrorIs(t, validateRestoreModeManagerConfig(backup, manager), ErrInvalidConfig)

	manager.Users[0].Permissions = append(manager.Users[0].Permissions, ManagerPermissionConfig{
		Resource: "cluster.restore.activation", Actions: []string{"w"},
	})
	require.NoError(t, validateRestoreModeManagerConfig(backup, manager))
}

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
