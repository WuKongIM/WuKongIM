package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
)

// cliFlags contains values parsed from wkdb command-line flags.
type cliFlags struct {
	// configPath points to a wukongim.conf file.
	configPath string
	// dataDir is the node data directory used to derive storage paths.
	dataDir string
	// metaPath overrides the metadata Pebble path.
	metaPath string
	// messagePath overrides the channel log Pebble path.
	messagePath string
	// hashSlotCount overrides configured metadata hash-slot count.
	hashSlotCount uint16
	// format selects the output renderer.
	format string
}

// cliConfig is the resolved runtime configuration for one wkdb invocation.
type cliConfig struct {
	// options configures the read-only inspect store.
	options inspect.Options
	// format selects the output renderer.
	format string
}

func resolveCLIConfig(flags cliFlags, env []string) (cliConfig, error) {
	values := map[string]string{}
	if strings.TrimSpace(flags.configPath) != "" {
		fileValues, err := readKeyValueFile(flags.configPath)
		if err != nil {
			return cliConfig{}, err
		}
		for key, value := range fileValues {
			values[key] = value
		}
	}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok && strings.HasPrefix(key, "WK_") {
			values[key] = value
		}
	}

	flagDataDir := strings.TrimSpace(flags.dataDir)
	dataDir := firstNonEmpty(flagDataDir, values["WK_NODE_DATA_DIR"])
	metaPath := resolveStoragePath(flags.metaPath, flagDataDir, values["WK_STORAGE_DB_PATH"], dataDir, "data")
	messagePath := resolveStoragePath(flags.messagePath, flagDataDir, values["WK_STORAGE_CHANNEL_LOG_PATH"], dataDir, "channellog")
	hashSlotCount := flags.hashSlotCount
	if hashSlotCount == 0 {
		var err error
		hashSlotCount, err = parseHashSlotCount(values)
		if err != nil {
			return cliConfig{}, err
		}
	}
	format := firstNonEmpty(flags.format, "table")
	if !validFormat(format) {
		return cliConfig{}, fmt.Errorf("unknown format %q", format)
	}
	if metaPath == "" && messagePath == "" {
		return cliConfig{}, fmt.Errorf("storage path required: set --data-dir, --meta-path, --message-path, WK_NODE_DATA_DIR, WK_STORAGE_DB_PATH, or WK_STORAGE_CHANNEL_LOG_PATH")
	}

	return cliConfig{
		options: inspect.Options{
			MetaPath:      metaPath,
			MessagePath:   messagePath,
			HashSlotCount: hashSlotCount,
			DefaultLimit:  100,
			MaxLimit:      10000,
		},
		format: format,
	}, nil
}

func resolveStoragePath(flagPath, flagDataDir, configuredPath, dataDir, name string) string {
	if value := strings.TrimSpace(flagPath); value != "" {
		return value
	}
	if flagDataDir != "" {
		return filepath.Join(flagDataDir, name)
	}
	if value := strings.TrimSpace(configuredPath); value != "" {
		return value
	}
	if dataDir != "" {
		return filepath.Join(dataDir, name)
	}
	return ""
}

func validFormat(format string) bool {
	switch format {
	case "table", "json", "jsonl":
		return true
	default:
		return false
	}
}

func readKeyValueFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	defer file.Close()
	return readKeyValues(file)
}

func readKeyValues(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func parseHashSlotCount(values map[string]string) (uint16, error) {
	for _, key := range []string{"WK_CLUSTER_HASH_SLOT_COUNT", "WK_CLUSTER_INITIAL_SLOT_COUNT", "WK_CLUSTER_SLOT_COUNT"} {
		raw := strings.TrimSpace(values[key])
		if raw == "" {
			continue
		}
		n, err := strconv.ParseUint(raw, 10, 16)
		if err != nil {
			return 0, fmt.Errorf("%s must be an unsigned 16-bit integer: %w", key, err)
		}
		return uint16(n), nil
	}
	return 0, nil
}
