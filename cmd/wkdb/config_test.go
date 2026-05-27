package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigFromDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg, err := resolveCLIConfig(cliFlags{dataDir: dir}, nil)
	if err != nil {
		t.Fatalf("resolveCLIConfig(): %v", err)
	}
	if cfg.options.MetaPath != filepath.Join(dir, "data") {
		t.Fatalf("MetaPath = %q", cfg.options.MetaPath)
	}
	if cfg.options.MessagePath != filepath.Join(dir, "channellog") {
		t.Fatalf("MessagePath = %q", cfg.options.MessagePath)
	}
}

func TestResolveConfigFileStorageKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "wukongim.conf")
	content := "WK_NODE_DATA_DIR=" + filepath.Join(dir, "node-1") + "\nWK_CLUSTER_HASH_SLOT_COUNT=256\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	cfg, err := resolveCLIConfig(cliFlags{configPath: configPath}, nil)
	if err != nil {
		t.Fatalf("resolveCLIConfig(): %v", err)
	}
	if cfg.options.HashSlotCount != 256 {
		t.Fatalf("HashSlotCount = %d", cfg.options.HashSlotCount)
	}
}

func TestResolveConfigEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "wukongim.conf")
	fileDir := filepath.Join(dir, "file-node")
	envDir := filepath.Join(dir, "env-node")
	content := "WK_NODE_DATA_DIR=" + fileDir + "\nWK_CLUSTER_HASH_SLOT_COUNT=16\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	cfg, err := resolveCLIConfig(cliFlags{configPath: configPath}, []string{
		"WK_NODE_DATA_DIR=" + envDir,
		"WK_CLUSTER_HASH_SLOT_COUNT=32",
	})
	if err != nil {
		t.Fatalf("resolveCLIConfig(): %v", err)
	}
	if cfg.options.MetaPath != filepath.Join(envDir, "data") {
		t.Fatalf("MetaPath = %q", cfg.options.MetaPath)
	}
	if cfg.options.HashSlotCount != 32 {
		t.Fatalf("HashSlotCount = %d", cfg.options.HashSlotCount)
	}
}

func TestResolveConfigExplicitPathsOverrideDataDir(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "meta")
	messagePath := filepath.Join(dir, "messages")
	cfg, err := resolveCLIConfig(cliFlags{
		dataDir:     filepath.Join(dir, "node"),
		metaPath:    metaPath,
		messagePath: messagePath,
	}, nil)
	if err != nil {
		t.Fatalf("resolveCLIConfig(): %v", err)
	}
	if cfg.options.MetaPath != metaPath || cfg.options.MessagePath != messagePath {
		t.Fatalf("paths = %q/%q, want explicit paths", cfg.options.MetaPath, cfg.options.MessagePath)
	}
}

func TestResolveConfigFlagOverridesEnv(t *testing.T) {
	dir := t.TempDir()
	flagMeta := filepath.Join(dir, "flag-meta")
	envMeta := filepath.Join(dir, "env-meta")
	cfg, err := resolveCLIConfig(cliFlags{metaPath: flagMeta, hashSlotCount: 16}, []string{
		"WK_STORAGE_DB_PATH=" + envMeta,
		"WK_CLUSTER_HASH_SLOT_COUNT=32",
	})
	if err != nil {
		t.Fatalf("resolveCLIConfig(): %v", err)
	}
	if cfg.options.MetaPath != flagMeta {
		t.Fatalf("MetaPath = %q, want flag path", cfg.options.MetaPath)
	}
	if cfg.options.HashSlotCount != 16 {
		t.Fatalf("HashSlotCount = %d, want flag value", cfg.options.HashSlotCount)
	}
}

func TestResolveConfigRejectsMissingStoragePath(t *testing.T) {
	_, err := resolveCLIConfig(cliFlags{}, nil)
	if err == nil || !strings.Contains(err.Error(), "storage path") {
		t.Fatalf("resolveCLIConfig() err = %v, want storage path error", err)
	}
}

func TestResolveConfigRejectsInvalidHashSlotCount(t *testing.T) {
	_, err := resolveCLIConfig(cliFlags{dataDir: t.TempDir()}, []string{"WK_CLUSTER_HASH_SLOT_COUNT=bad"})
	if err == nil || !strings.Contains(err.Error(), "WK_CLUSTER_HASH_SLOT_COUNT") {
		t.Fatalf("resolveCLIConfig() err = %v, want hash-slot key error", err)
	}
}
