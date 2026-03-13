package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextSetCmd_SetServer(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, ".wkcli.yaml")

	oldCfgPath := cfgPath
	oldCfg := cfg
	defer func() {
		cfgPath = oldCfgPath
		cfg = oldCfg
	}()

	cfgPath = tmpFile
	cfg = Config{Current: "default", Contexts: map[string]*Context{}}

	cmd := contextSetCmd
	cmd.Flags().Set("server", "http://localhost:5001")
	cmd.Flags().Set("token", "")
	cmd.Flags().Set("name", "")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)

	assert.Equal(t, "http://localhost:5001", cfg.Contexts["default"].Server)
}

func TestContextSetCmd_SetToken(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, ".wkcli.yaml")

	oldCfgPath := cfgPath
	oldCfg := cfg
	defer func() {
		cfgPath = oldCfgPath
		cfg = oldCfg
	}()

	cfgPath = tmpFile
	cfg = Config{
		Current: "default",
		Contexts: map[string]*Context{
			"default": {Server: "http://localhost:5001"},
		},
	}

	cmd := contextSetCmd
	cmd.Flags().Set("server", "")
	cmd.Flags().Set("token", "mytoken")
	cmd.Flags().Set("name", "")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)

	assert.Equal(t, "mytoken", cfg.Contexts["default"].Token)
	// Server should remain unchanged.
	assert.Equal(t, "http://localhost:5001", cfg.Contexts["default"].Server)
}

func TestContextSetCmd_NamedContext(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, ".wkcli.yaml")

	oldCfgPath := cfgPath
	oldCfg := cfg
	defer func() {
		cfgPath = oldCfgPath
		cfg = oldCfg
	}()

	cfgPath = tmpFile
	cfg = Config{Current: "default", Contexts: map[string]*Context{}}

	cmd := contextSetCmd
	cmd.Flags().Set("server", "http://prod:5001")
	cmd.Flags().Set("token", "prodtoken")
	cmd.Flags().Set("name", "production")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)

	require.Contains(t, cfg.Contexts, "production")
	assert.Equal(t, "http://prod:5001", cfg.Contexts["production"].Server)
	assert.Equal(t, "prodtoken", cfg.Contexts["production"].Token)
}

func TestContextSetCmd_NoFlags(t *testing.T) {
	oldCfg := cfg
	defer func() { cfg = oldCfg }()

	cfg = Config{Current: "default", Contexts: map[string]*Context{}}

	cmd := contextSetCmd
	cmd.Flags().Set("server", "")
	cmd.Flags().Set("token", "")
	cmd.Flags().Set("name", "")

	err := cmd.RunE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one")
}

func TestContextShowCmd(t *testing.T) {
	oldCfg := cfg
	defer func() { cfg = oldCfg }()

	cfg = Config{
		Current: "default",
		Contexts: map[string]*Context{
			"default": {Server: "http://localhost:5001", Token: "longtoken12345"},
			"prod":    {Server: "http://prod:5001"},
		},
	}

	// Should not return an error.
	err := contextShowCmd.RunE(contextShowCmd, nil)
	assert.NoError(t, err)
}

func TestContextShowCmd_Empty(t *testing.T) {
	oldCfg := cfg
	defer func() { cfg = oldCfg }()

	cfg = Config{Current: "default", Contexts: map[string]*Context{}}

	err := contextShowCmd.RunE(contextShowCmd, nil)
	assert.NoError(t, err)
}

func TestContextUseCmd_Success(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, ".wkcli.yaml")

	oldCfgPath := cfgPath
	oldCfg := cfg
	defer func() {
		cfgPath = oldCfgPath
		cfg = oldCfg
	}()

	cfgPath = tmpFile
	cfg = Config{
		Current: "default",
		Contexts: map[string]*Context{
			"default": {Server: "http://localhost:5001"},
			"prod":    {Server: "http://prod:5001"},
		},
	}

	err := contextUseCmd.RunE(contextUseCmd, []string{"prod"})
	assert.NoError(t, err)
	assert.Equal(t, "prod", cfg.Current)
}

func TestContextUseCmd_NotFound(t *testing.T) {
	oldCfg := cfg
	defer func() { cfg = oldCfg }()

	cfg = Config{Current: "default", Contexts: map[string]*Context{
		"default": {Server: "http://localhost:5001"},
	}}

	err := contextUseCmd.RunE(contextUseCmd, []string{"nonexistent"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestContextSetCmd_FirstContext(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, ".wkcli.yaml")

	oldCfgPath := cfgPath
	oldCfg := cfg
	defer func() {
		cfgPath = oldCfgPath
		cfg = oldCfg
	}()

	cfgPath = tmpFile
	cfg = Config{Current: "", Contexts: map[string]*Context{}}

	cmd := contextSetCmd
	cmd.Flags().Set("server", "http://localhost:5001")
	cmd.Flags().Set("token", "")
	cmd.Flags().Set("name", "myctx")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)

	// First context should become current.
	assert.Equal(t, "myctx", cfg.Current)
}
