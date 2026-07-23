package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	"github.com/pelletier/go-toml/v2"
)

const (
	defaultMetaStoreDirName    = "slotmeta"
	defaultMessageStoreDirName = "messages"
)

// cliFlags contains values parsed from wkdb command-line flags.
type cliFlags struct {
	// configPath points to a wukongim.toml file.
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
	// nodeOptions configures the writable node store for import operations.
	nodeOptions db.NodeStoreOptions
	// format selects the output renderer.
	format string
}

func resolveCLIConfig(flags cliFlags, env []string) (cliConfig, error) {
	values, err := loadCLIValues(flags.configPath, env)
	if err != nil {
		return cliConfig{}, err
	}

	flagDataDir := strings.TrimSpace(flags.dataDir)
	dataDir := firstNonEmpty(flagDataDir, values["WK_NODE_DATA_DIR"])
	metaPath := resolveStoragePath(flags.metaPath, flagDataDir, values["WK_STORAGE_DB_PATH"], dataDir, defaultMetaStoreDirName)
	messagePath := resolveStoragePath(flags.messagePath, flagDataDir, values["WK_STORAGE_CHANNEL_LOG_PATH"], dataDir, defaultMessageStoreDirName)
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
		nodeOptions: db.NodeStoreOptions{
			MetaPath:    metaPath,
			MessagePath: messagePath,
		},
		format: format,
	}, nil
}

func resolveCLIHashSlotCount(flags cliFlags, env []string) (uint16, error) {
	if flags.hashSlotCount != 0 {
		return flags.hashSlotCount, nil
	}
	values, err := loadCLIValues(flags.configPath, env)
	if err != nil {
		return 0, err
	}
	return parseHashSlotCount(values)
}

func loadCLIValues(configPath string, env []string) (map[string]string, error) {
	values := map[string]string{}
	if strings.TrimSpace(configPath) != "" {
		fileValues, err := readTOMLConfigFile(configPath)
		if err != nil {
			return nil, err
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
	return values, nil
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

var wkdbTOMLPaths = map[string]string{
	"node.data_dir":              "WK_NODE_DATA_DIR",
	"storage.db_path":            "WK_STORAGE_DB_PATH",
	"storage.channel_log_path":   "WK_STORAGE_CHANNEL_LOG_PATH",
	"cluster.hash_slot_count":    "WK_CLUSTER_HASH_SLOT_COUNT",
	"cluster.initial_slot_count": "WK_CLUSTER_INITIAL_SLOT_COUNT",
	"cluster.slot_count":         "WK_CLUSTER_SLOT_COUNT",
}

func readTOMLConfigFile(path string) (map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var raw map[string]any
	if err := toml.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse config %s as TOML: %w", path, err)
	}
	flat := map[string]any{}
	flattenTOMLConfig("", raw, flat)
	out := map[string]string{}
	for path, key := range wkdbTOMLPaths {
		value, ok := flat[path]
		if !ok {
			continue
		}
		text, err := wkdbTOMLValueString(path, value)
		if err != nil {
			return nil, err
		}
		out[key] = text
	}
	return out, nil
}

func flattenTOMLConfig(prefix string, value any, out map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flattenTOMLConfig(next, child, out)
		}
	default:
		out[prefix] = typed
	}
}

func wkdbTOMLValueString(path string, value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case int:
		return strconv.Itoa(typed), nil
	default:
		return "", fmt.Errorf("parse %s: value must be a string or integer", path)
	}
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
