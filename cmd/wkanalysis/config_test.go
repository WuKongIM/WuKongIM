package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	benchconfig "github.com/WuKongIM/WuKongIM/internal/bench/config"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestLoadServeConfigRequiresRunScopedToken(t *testing.T) {
	_, err := loadServeConfig(func(key string) string {
		if key == "WK_ANALYSIS_RUN_ID" {
			return "run-1"
		}
		return ""
	}, func() time.Time { return time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC) })
	if err == nil {
		t.Fatal("loadServeConfig() error = nil, want missing token rejection")
	}
}

func TestLoadServeConfigBindsThreeNodeClusterAndTokenLease(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	env := map[string]string{
		"WK_ANALYSIS_RUN_ID":           "run-1",
		"WK_ANALYSIS_MCP_TOKEN":        "run-token-0123456789-0123456789-ab",
		"WK_ANALYSIS_RUN_EXPIRES_AT":   now.Add(2 * time.Hour).Format(time.RFC3339),
		"WK_ANALYSIS_TOKEN_EXPIRES_AT": now.Add(45 * time.Minute).Format(time.RFC3339),
		"WK_ANALYSIS_MANAGER_USERNAME": "analysis",
		"WK_ANALYSIS_MANAGER_PASSWORD": "capability-token",
		"WK_ANALYSIS_NODE_API_URLS":    `{"1":"http://node1:5001","2":"http://node2:5001","3":"http://node3:5001"}`,
		"WK_ANALYSIS_SCENARIO_PATH":    testScenarioPath(t),
	}
	cfg, err := loadServeConfig(func(key string) string { return env[key] }, func() time.Time { return now })
	if err != nil {
		t.Fatalf("loadServeConfig() error = %v", err)
	}
	if len(cfg.gateway.NodeAPIBaseURLs) != 3 || cfg.gateway.NodeAPIBaseURLs[2] != "http://node2:5001" {
		t.Fatalf("node URLs = %#v", cfg.gateway.NodeAPIBaseURLs)
	}
	if cfg.gateway.MCPTokenExpiresAt != now.Add(45*time.Minute) || cfg.gateway.RunExpiresAt != now.Add(2*time.Hour) {
		t.Fatalf("token=%s run=%s", cfg.gateway.MCPTokenExpiresAt, cfg.gateway.RunExpiresAt)
	}
	if cfg.gateway.MetricQueries["send_rate"] == "" || cfg.gateway.MetricQueries["runtime_queue_pressure"] == "" ||
		cfg.gateway.MetricQueries["simulator_cpu_percent"] == "" || cfg.gateway.MetricQueries["simulator_memory_percent"] == "" ||
		cfg.gateway.MetricQueries["node_data_disk_used_bytes"] == "" {
		t.Fatalf("metric query IDs = %#v", cfg.gateway.MetricQueries)
	}
}

func TestDefaultMetricQueriesIncludeNodeProcessLossAndMemoryEvidence(t *testing.T) {
	queries := defaultMetricQueries()
	want := map[string]string{
		"node_memory_percent":                       `100 * (1 - (node_memory_MemAvailable_bytes{job="hosts",role=~"node-[123]"} / clamp_min(node_memory_MemTotal_bytes{job="hosts",role=~"node-[123]"}, 1)))`,
		"node_oom_kills":                            `node_vmstat_oom_kill{job="hosts",role=~"node-[123]"}`,
		"node_service_cgroup_available":             `wukongim_service_cgroup_available{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_peak_bytes":            `wukongim_service_cgroup_memory_peak_bytes{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_peak_native_available": `wukongim_service_cgroup_memory_peak_native_available{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_limit_bytes":           `wukongim_service_cgroup_memory_limit_bytes{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_limit_unlimited":       `wukongim_service_cgroup_memory_limit_unlimited{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_events_oom":            `wukongim_service_cgroup_memory_events_total{job="hosts",role=~"node-[123]",event="oom"}`,
		"node_service_memory_events_oom_kill":       `wukongim_service_cgroup_memory_events_total{job="hosts",role=~"node-[123]",event="oom_kill"}`,
		"process_start_time_seconds":                `process_start_time_seconds{job="wukongim"}`,
		"gateway_active_connections":                `sum by (instance, node_name) (wukongim_gateway_connections_active{job="wukongim"})`,
		"channel_active_channels":                   `sum by (instance, node_name) (wukongim_channel_active_channels{job="wukongim"})`,
	}
	for id, query := range want {
		if queries[id] != query {
			t.Fatalf("metric query %q = %q, want %q", id, queries[id], query)
		}
	}
}

func TestLoadServeConfigRejectsTokenPastLeaseGuard(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	env := map[string]string{
		"WK_ANALYSIS_RUN_ID":           "run-1",
		"WK_ANALYSIS_MCP_TOKEN":        "run-token-0123456789-0123456789-ab",
		"WK_ANALYSIS_RUN_EXPIRES_AT":   now.Add(40 * time.Minute).Format(time.RFC3339),
		"WK_ANALYSIS_TOKEN_EXPIRES_AT": now.Add(39 * time.Minute).Format(time.RFC3339),
		"WK_ANALYSIS_SCENARIO_PATH":    testScenarioPath(t),
	}
	_, err := loadServeConfig(func(key string) string { return env[key] }, func() time.Time { return now })
	if err == nil {
		t.Fatal("loadServeConfig() error = nil, want lease guard rejection")
	}
}

func TestLoadServeConfigRequiresLocatorWithFakeInventory(t *testing.T) {
	env := map[string]string{
		"WK_ANALYSIS_RUN_ID":              "run-1",
		"WK_ANALYSIS_MCP_TOKEN":           "run-token-0123456789-0123456789-ab",
		"WK_ANALYSIS_SCENARIO_PATH":       testScenarioPath(t),
		"WK_ANALYSIS_FAKE_INVENTORY_PATH": filepath.Join(t.TempDir(), "inventory.json"),
	}
	_, err := loadServeConfig(func(key string) string { return env[key] }, func() time.Time {
		return time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	})
	if err == nil {
		t.Fatal("loadServeConfig() error = nil, want missing Run Locator rejection")
	}
}

func TestLoadServeConfigBindsFakeInventoryToRunLocatorAndScenario(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	scenarioPath := testScenarioPath(t)
	effective, err := benchconfig.LoadScenario(scenarioPath)
	if err != nil {
		t.Fatalf("LoadScenario() error = %v", err)
	}
	digest, err := model.DigestScenario(effective)
	if err != nil {
		t.Fatalf("DigestScenario() error = %v", err)
	}
	locatorPath := filepath.Join(t.TempDir(), "locator.json")
	locatorFile, err := os.Create(locatorPath)
	if err != nil {
		t.Fatal(err)
	}
	locator := cloudsim.RunLocator{
		Schema: cloudsim.RunLocatorSchemaV1, RunID: "run-1", Provider: "fake", Region: "local",
		AccountIDHash: "sha256:account", Repository: "WuKongIM/WuKongIM", SourceSHA: "abc123",
		ScenarioDigest: digest, CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour), ProvisionWorkflowRunID: 42,
	}
	if err := cloudsim.EncodeRunLocator(locatorFile, locator); err != nil {
		locatorFile.Close()
		t.Fatalf("EncodeRunLocator() error = %v", err)
	}
	if err := locatorFile.Close(); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"WK_ANALYSIS_RUN_ID":              "run-1",
		"WK_ANALYSIS_MCP_TOKEN":           "run-token-0123456789-0123456789-ab",
		"WK_ANALYSIS_SCENARIO_PATH":       scenarioPath,
		"WK_ANALYSIS_FAKE_INVENTORY_PATH": filepath.Join(t.TempDir(), "inventory.json"),
		"WK_ANALYSIS_RUN_LOCATOR_PATH":    locatorPath,
	}
	cfg, err := loadServeConfig(func(key string) string { return env[key] }, func() time.Time { return now })
	if err != nil {
		t.Fatalf("loadServeConfig() error = %v", err)
	}
	if cfg.gateway.RunLocator == nil || cfg.gateway.RunLocator.RunID != "run-1" || cfg.gateway.Provider != "fake" || cfg.gateway.SourceSHA != "abc123" {
		t.Fatalf("gateway identity = %#v", cfg.gateway)
	}
}

func testScenarioPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	data := []byte("version: wkbench/v1\nrun:\n  id: test-scenario\n  random_seed: 42\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write scenario: %v", err)
	}
	return path
}
