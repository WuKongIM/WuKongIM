package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetServerURL_FlagOverridesConfig(t *testing.T) {
	oldServer := flagServer
	oldCfg := cfg
	defer func() {
		flagServer = oldServer
		cfg = oldCfg
	}()

	cfg = Config{
		Current: "default",
		Contexts: map[string]*Context{
			"default": {Server: "http://config-server:5001"},
		},
	}
	flagServer = "http://flag-server:5001"

	assert.Equal(t, "http://flag-server:5001", getServerURL())
}

func TestGetServerURL_FromConfig(t *testing.T) {
	oldServer := flagServer
	oldCfg := cfg
	defer func() {
		flagServer = oldServer
		cfg = oldCfg
	}()

	flagServer = ""
	cfg = Config{
		Current: "default",
		Contexts: map[string]*Context{
			"default": {Server: "http://config-server:5001"},
		},
	}

	assert.Equal(t, "http://config-server:5001", getServerURL())
}

func TestGetServerURL_Empty(t *testing.T) {
	oldServer := flagServer
	oldCfg := cfg
	defer func() {
		flagServer = oldServer
		cfg = oldCfg
	}()

	flagServer = ""
	cfg = Config{Current: "default", Contexts: map[string]*Context{}}

	assert.Equal(t, "", getServerURL())
}

func TestGetToken_FlagOverridesConfig(t *testing.T) {
	oldToken := flagToken
	oldCfg := cfg
	defer func() {
		flagToken = oldToken
		cfg = oldCfg
	}()

	cfg = Config{
		Current: "default",
		Contexts: map[string]*Context{
			"default": {Token: "config-token"},
		},
	}
	flagToken = "flag-token"

	assert.Equal(t, "flag-token", getToken())
}

func TestGetToken_FromConfig(t *testing.T) {
	oldToken := flagToken
	oldCfg := cfg
	defer func() {
		flagToken = oldToken
		cfg = oldCfg
	}()

	flagToken = ""
	cfg = Config{
		Current: "default",
		Contexts: map[string]*Context{
			"default": {Token: "config-token"},
		},
	}

	assert.Equal(t, "config-token", getToken())
}

func TestGetToken_Empty(t *testing.T) {
	oldToken := flagToken
	oldCfg := cfg
	defer func() {
		flagToken = oldToken
		cfg = oldCfg
	}()

	flagToken = ""
	cfg = Config{Current: "default", Contexts: map[string]*Context{}}

	assert.Equal(t, "", getToken())
}

func TestGetServerURL_NonDefaultContext(t *testing.T) {
	oldServer := flagServer
	oldCfg := cfg
	defer func() {
		flagServer = oldServer
		cfg = oldCfg
	}()

	flagServer = ""
	cfg = Config{
		Current: "prod",
		Contexts: map[string]*Context{
			"default": {Server: "http://default:5001"},
			"prod":    {Server: "http://prod:5001"},
		},
	}

	assert.Equal(t, "http://prod:5001", getServerURL())
}

func TestSaveAndLoadConfig(t *testing.T) {
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
			"default": {Server: "http://localhost:5001", Token: "mytoken"},
			"prod":    {Server: "http://prod:5001"},
		},
	}

	err := saveConfig()
	require.NoError(t, err)

	// Verify file was created.
	_, err = os.Stat(tmpFile)
	require.NoError(t, err)

	// Load it back.
	loadConfig()

	assert.Equal(t, "default", cfg.Current)
	assert.Equal(t, "http://localhost:5001", cfg.Contexts["default"].Server)
	assert.Equal(t, "mytoken", cfg.Contexts["default"].Token)
	assert.Equal(t, "http://prod:5001", cfg.Contexts["prod"].Server)
}

func TestLoadConfig_MissingFile(t *testing.T) {
	oldCfgPath := cfgPath
	oldCfg := cfg
	defer func() {
		cfgPath = oldCfgPath
		cfg = oldCfg
	}()

	cfgPath = "/tmp/nonexistent_wkcli_test.yaml"
	os.Remove(cfgPath) // Ensure it doesn't exist.

	loadConfig()

	assert.Equal(t, "default", cfg.Current)
	assert.NotNil(t, cfg.Contexts)
	assert.Len(t, cfg.Contexts, 0)
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, ".wkcli.yaml")

	oldCfgPath := cfgPath
	oldCfg := cfg
	defer func() {
		cfgPath = oldCfgPath
		cfg = oldCfg
	}()

	cfgPath = tmpFile
	// Use truly invalid YAML that won't partially unmarshal.
	os.WriteFile(tmpFile, []byte("\t\t\x00\x01\x02"), 0o644)

	loadConfig()

	// On invalid YAML, loadConfig falls back to defaults.
	assert.Equal(t, "default", cfg.Current)
	assert.NotNil(t, cfg.Contexts)
}

func TestLoadConfig_NilContexts(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, ".wkcli.yaml")

	oldCfgPath := cfgPath
	oldCfg := cfg
	defer func() {
		cfgPath = oldCfgPath
		cfg = oldCfg
	}()

	cfgPath = tmpFile
	// YAML with no contexts key.
	os.WriteFile(tmpFile, []byte("current: myctx\n"), 0o644)

	loadConfig()

	assert.Equal(t, "myctx", cfg.Current)
	assert.NotNil(t, cfg.Contexts)
}

func TestConfigPath_Default(t *testing.T) {
	oldCfgPath := cfgPath
	defer func() { cfgPath = oldCfgPath }()

	cfgPath = "" // Reset to force computation.

	path := configPath()
	home, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(home, ".wkcli.yaml"), path)
}

func TestConfigPath_Cached(t *testing.T) {
	oldCfgPath := cfgPath
	defer func() { cfgPath = oldCfgPath }()

	cfgPath = "/custom/path/.wkcli.yaml"
	assert.Equal(t, "/custom/path/.wkcli.yaml", configPath())
}
