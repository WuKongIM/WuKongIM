//go:build e2e

package send_ban

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/require"
)

// permissionFixedV2RetainConfigs binds the actual process TOML and complete
// canonical object. Only exact fixture addresses and paths may be replaced.
func permissionFixedV2RetainConfigs(t *testing.T, cluster *suite.StartedCluster, reportPath string, report map[string]any) {
	t.Helper()
	dir := reportPath + ".config"
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0755))
	require.NoError(t, os.Mkdir(dir, 0755), "preserve existing config evidence")
	configs := report["node_configs"].(map[uint64]any)
	coordinates := map[uint64]map[string]string{}
	for _, node := range cluster.Nodes {
		coordinates[node.Spec.ID] = map[string]string{
			"data_dir": node.Spec.DataDir, "log_dir": node.Spec.LogDir,
			"cluster_addr": node.Spec.ClusterAddr, "api_addr": node.Spec.APIAddr,
			"manager_addr": node.Spec.ManagerAddr, "gateway_addr": node.Spec.GatewayAddr,
			"plugin_socket": node.Spec.ConfigOverrides["WK_PLUGIN_SOCKET_PATH"],
		}
	}
	report["config_coordinates"] = coordinates
	for _, node := range cluster.Nodes {
		id := node.Spec.ID
		config := configs[id].(map[string]any)
		raw, err := os.ReadFile(node.Spec.ConfigPath)
		require.NoError(t, err)
		require.Equal(t, config["sha256"], fmt.Sprintf("%x", sha256.Sum256(raw)), "actual config changed before retention")
		rawPath := filepath.Join(dir, fmt.Sprintf("node-%d.toml", id))
		require.NoError(t, permissionFixedV2WriteNew(rawPath, raw))
		config["retained_config_path"], config["retained_config_bytes"] = rawPath, len(raw)
		config["retained_config_sha256"] = fmt.Sprintf("%x", sha256.Sum256(raw))
		var parsed map[string]any
		require.NoError(t, toml.Unmarshal(raw, &parsed))
		gateway, ok := parsed["gateway"].(map[string]any)
		require.True(t, ok, "complete gateway config required")
		clusterConfig, ok := parsed["cluster"].(map[string]any)
		require.True(t, ok, "complete cluster config required")
		require.Equal(t, 1, permissionFixedV2ArrayLength(gateway["listeners"]), "unexpected listener must not be normalized")
		require.Equal(t, 3, permissionFixedV2ArrayLength(clusterConfig["nodes"]), "unexpected peer must not be normalized")
		var rules []map[string]any
		replace := func(pointer, from, to string) {
			require.NotEmpty(t, from, "exact expected fixture coordinate required")
			require.NoError(t, permissionFixedV2Replace(parsed, pointer, from, to))
			rules = append(rules, map[string]any{"path": pointer, "from": from, "to": to, "matches": 1})
		}
		coord := coordinates[id]
		for _, field := range []struct{ pointer, key string }{
			{"/node/data_dir", "data_dir"}, {"/log/dir", "log_dir"},
			{"/cluster/listen_addr", "cluster_addr"}, {"/api/listen_addr", "api_addr"},
			{"/manager/listen_addr", "manager_addr"}, {"/plugin/socket_path", "plugin_socket"},
			{"/gateway/listeners/0/address", "gateway_addr"},
		} {
			replace(field.pointer, coord[field.key], fmt.Sprintf("@node-%d-%s@", id, field.key))
		}
		for index, peer := range cluster.Nodes {
			replace(fmt.Sprintf("/cluster/nodes/%d/addr", index), coordinates[peer.Spec.ID]["cluster_addr"], fmt.Sprintf("@node-%d-cluster_addr@", peer.Spec.ID))
		}
		canonical, err := json.MarshalIndent(parsed, "", "  ")
		require.NoError(t, err)
		canonical = append(canonical, '\n')
		canonicalPath := filepath.Join(dir, fmt.Sprintf("node-%d.canonical.json", id))
		require.NoError(t, permissionFixedV2WriteNew(canonicalPath, canonical))
		config["canonical_config_path"], config["canonical_config_bytes"] = canonicalPath, len(canonical)
		config["canonical_config_sha256"] = fmt.Sprintf("%x", sha256.Sum256(canonical))
		config["normalization_rules"] = rules
		var productKeys []string
		runtimeEnv := map[string]string{}
		for _, item := range node.Process.Cmd.Env {
			key, value, found := strings.Cut(item, "=")
			if !found {
				continue
			}
			if strings.HasPrefix(key, "WK_") {
				productKeys = append(productKeys, key)
			}
			if key == "GOMAXPROCS" || key == "GOGC" || key == "GODEBUG" || key == "GOMEMLIMIT" {
				runtimeEnv[key] = value
			}
		}
		config["runtime_environment"] = runtimeEnv
		config["unexpected_product_environment_keys"] = productKeys
		require.Empty(t, productKeys, "ambient product overrides invalidate config attribution")
		require.Equal(t, "4", runtimeEnv["GOMAXPROCS"])
		require.Equal(t, "100", runtimeEnv["GOGC"])
		require.Empty(t, runtimeEnv["GODEBUG"])
		require.Empty(t, runtimeEnv["GOMEMLIMIT"])
	}
}

func permissionFixedV2ArrayLength(value any) int {
	switch array := value.(type) {
	case []any:
		return len(array)
	case []map[string]any:
		return len(array)
	default:
		return -1
	}
}

// permissionFixedV2Replace never broadens a normalization rule to unknown keys.
func permissionFixedV2Replace(parsed map[string]any, pointer, from, to string) error {
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	var current any = parsed
	for _, part := range parts[:len(parts)-1] {
		switch value := current.(type) {
		case map[string]any:
			child, present := value[part]
			if !present {
				return fmt.Errorf("normalization path missing: %s", pointer)
			}
			current = child
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(value) {
				return fmt.Errorf("normalization array index invalid: %s", pointer)
			}
			current = value[index]
		case []map[string]any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(value) {
				return fmt.Errorf("normalization table index invalid: %s", pointer)
			}
			current = value[index]
		default:
			return fmt.Errorf("normalization parent type invalid: %s", pointer)
		}
	}
	parent, ok := current.(map[string]any)
	if !ok || parent[parts[len(parts)-1]] != from {
		return fmt.Errorf("normalization exact coordinate mismatch: %s", pointer)
	}
	parent[parts[len(parts)-1]] = to
	return nil
}

// permissionFixedV2WriteNew preserves previous artifacts and reports short writes.
func permissionFixedV2WriteNew(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	n, writeErr := file.Write(body)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if n != len(body) {
		return fmt.Errorf("short artifact write: %d of %d bytes", n, len(body))
	}
	return closeErr
}
