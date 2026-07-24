package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	benchconfig "github.com/WuKongIM/WuKongIM/internal/bench/config"
	cloudanalysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
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

func TestDefaultMetricQueriesExcludeZeroCapacityRuntimeQueues(t *testing.T) {
	got := defaultMetricQueries()["runtime_queue_pressure"]
	want := `max(wukongim_runtime_pool_queue_depth / (wukongim_runtime_pool_queue_capacity > 0))`
	if got != want {
		t.Fatalf("runtime_queue_pressure = %q, want %q", got, want)
	}
}

func TestDefaultMetricQueriesIncludePerPoolRuntimeQueuePressure(t *testing.T) {
	got := defaultMetricQueries()["runtime_queue_pressure_by_pool"]
	want := `max by (instance, node_id, node_name, component, pool, queue, priority) (wukongim_runtime_pool_queue_depth{job="wukongim"} / (wukongim_runtime_pool_queue_capacity{job="wukongim"} > 0))`
	if got != want {
		t.Fatalf("runtime_queue_pressure_by_pool = %q, want %q", got, want)
	}
}

func TestDefaultMetricQueriesIncludeBoundedChannelRPCStageEvidence(t *testing.T) {
	queries := defaultMetricQueries()
	want := map[string]string{
		"channel_rpc_worker_admission_cumulative": `sum by (instance, node_id, node_name, pool, result) (wukongim_runtime_pool_admission_total{job="wukongim",component="channel",pool="channelv2-rpc",queue="worker"})`,
		"channel_rpc_worker_wait_p99":             `histogram_quantile(0.99, sum by (instance, node_id, node_name, pool, result, le) (rate(wukongim_runtime_pool_wait_duration_seconds_bucket{job="wukongim",component="channel",pool="channelv2-rpc",queue="worker"}[1m])))`,
		"channel_rpc_worker_task_p99":             `histogram_quantile(0.99, sum by (instance, node_id, node_name, pool, task, result, le) (rate(wukongim_runtime_pool_task_duration_seconds_bucket{job="wukongim",component="channel",pool="channelv2-rpc"}[1m])))`,
		"channel_rpc_worker_batch_items_p99":      `histogram_quantile(0.99, sum by (instance, node_id, node_name, kind, result, le) (rate(wukongim_channelv2_worker_batch_items_bucket{job="wukongim",kind=~"rpc_pull|rpc_pull_hint"}[1m])))`,
		"channel_rpc_follower_stage_p99":          `histogram_quantile(0.99, sum by (instance, node_id, node_name, stage, result, le) (rate(wukongim_channelv2_replication_stage_duration_seconds_bucket{job="wukongim",stage=~"follower_pull_hint_to_submit|follower_pull_rpc"}[1m])))`,
		"channel_rpc_pull_batch_items_p99":        `histogram_quantile(0.99, sum by (instance, node_id, node_name, result, le) (rate(wukongim_channelv2_pull_batch_items_bucket{job="wukongim"}[1m])))`,
		"channel_rpc_pull_batch_stage_p99":        `histogram_quantile(0.99, sum by (instance, node_id, node_name, stage, result, le) (rate(wukongim_channelv2_pull_batch_duration_seconds_bucket{job="wukongim"}[1m])))`,
		"channel_rpc_pull_hint_receive_rate":      `sum by (instance, node_id, node_name, reason, stage, result, error) (rate(wukongim_channelv2_pull_hint_receive_total{job="wukongim"}[1m]))`,
	}
	for id, query := range want {
		if queries[id] != query {
			t.Fatalf("metric query %q = %q, want %q", id, queries[id], query)
		}
		if strings.Contains(query, "channel_key") || strings.Contains(query, "slot_id") || strings.Contains(query, "uid") {
			t.Fatalf("metric query %q exposes a high-cardinality dimension: %s", id, query)
		}
	}
}

func TestDefaultMetricQueriesIncludeRecipientDeliveryAndPostCommitEvidence(t *testing.T) {
	queries := defaultMetricQueries()
	want := map[string]string{
		"delivery_recipient_worker_queue_depth":                   `max by (instance, node_name) (wukongim_delivery_recipient_worker_queue_depth{job="wukongim"})`,
		"delivery_recipient_worker_queue_capacity":                `max by (instance, node_name) (wukongim_delivery_recipient_worker_queue_capacity{job="wukongim"})`,
		"delivery_recipient_worker_inflight":                      `max by (instance, node_name) (wukongim_delivery_recipient_worker_inflight{job="wukongim"})`,
		"delivery_recipient_worker_capacity":                      `max by (instance, node_name) (wukongim_delivery_recipient_worker_capacity{job="wukongim"})`,
		"delivery_recipient_worker_admission_cumulative":          `sum by (instance, node_name, result) (wukongim_delivery_recipient_worker_admission_total{job="wukongim"})`,
		"delivery_recipient_worker_admission_wait_p99":            `histogram_quantile(0.99, sum by (instance, node_name, result, le) (rate(wukongim_delivery_recipient_worker_admission_wait_seconds_bucket{job="wukongim"}[1m])))`,
		"delivery_recipient_worker_process_cumulative":            `sum by (instance, node_name, result) (wukongim_delivery_recipient_worker_process_total{job="wukongim"})`,
		"delivery_recipient_worker_process_p99":                   `histogram_quantile(0.99, sum by (instance, node_name, result, le) (rate(wukongim_delivery_recipient_worker_process_duration_seconds_bucket{job="wukongim"}[1m])))`,
		"delivery_recipient_worker_process_recipients_cumulative": `sum by (instance, node_name, result) (wukongim_delivery_recipient_worker_process_recipients_sum{job="wukongim"})`,
		"channelappend_post_commit_handoff_depth":                 `max by (instance, node_name) (wukongim_channelappend_post_commit_handoff_depth{job="wukongim"})`,
		"channelappend_post_commit_handoff_capacity":              `max by (instance, node_name) (wukongim_channelappend_post_commit_handoff_capacity{job="wukongim"})`,
		"channelappend_post_commit_retry_queue_depth":             `max by (instance, node_name) (wukongim_channelappend_post_commit_retry_queue_depth{job="wukongim"})`,
		"channelappend_post_commit_retry_contended":               `max by (instance, node_name) (wukongim_channelappend_post_commit_retry_contended{job="wukongim"})`,
	}
	for id, query := range want {
		if queries[id] != query {
			t.Fatalf("metric query %q = %q, want %q", id, queries[id], query)
		}
	}
}

func TestDefaultMetricQueriesIncludeRecipientPipelineStageEvidence(t *testing.T) {
	queries := defaultMetricQueries()
	want := map[string]string{
		cloudanalysis.MetricQueryDeliveryRecipientAuthorityResolveRate:        `sum by (instance, node_name, result) (rate(wukongim_delivery_recipient_authority_resolve_total{job="wukongim"}[1m]))`,
		cloudanalysis.MetricQueryDeliveryRecipientAuthorityResolveItemsRate:   `sum by (instance, node_name, result) (rate(wukongim_delivery_recipient_authority_resolve_items_total{job="wukongim"}[1m]))`,
		cloudanalysis.MetricQueryDeliveryRecipientAuthorityResolveTargetsRate: `sum by (instance, node_name, result) (rate(wukongim_delivery_recipient_authority_resolve_targets_total{job="wukongim"}[1m]))`,
		cloudanalysis.MetricQueryDeliveryRecipientAuthorityResolveP99:         `histogram_quantile(0.99, sum by (instance, node_name, result, le) (rate(wukongim_delivery_recipient_authority_resolve_duration_seconds_bucket{job="wukongim"}[1m])))`,
		cloudanalysis.MetricQueryPresenceEndpointLookupRate:                   `sum by (instance, node_name, path, outcome, stale_retry) (rate(wukongim_presence_endpoint_lookup_total{job="wukongim"}[1m]))`,
		cloudanalysis.MetricQueryPresenceEndpointLookupItemsRate:              `sum by (instance, node_name, path, outcome, stale_retry) (rate(wukongim_presence_endpoint_lookup_items_total{job="wukongim"}[1m]))`,
		cloudanalysis.MetricQueryPresenceEndpointLookupGroupsRate:             `sum by (instance, node_name, path, outcome, stale_retry) (rate(wukongim_presence_endpoint_lookup_groups_total{job="wukongim"}[1m]))`,
		cloudanalysis.MetricQueryPresenceEndpointLookupP99:                    `histogram_quantile(0.99, sum by (instance, node_name, path, outcome, stale_retry, le) (rate(wukongim_presence_endpoint_lookup_duration_seconds_bucket{job="wukongim"}[1m])))`,
		cloudanalysis.MetricQueryDeliveryAckBatchCumulative:                   `sum by (instance, node_name, phase, outcome) (wukongim_delivery_ack_batch_total{job="wukongim"})`,
		cloudanalysis.MetricQueryDeliveryAckBatchItemsCumulative:              `sum by (instance, node_name, phase, outcome) (wukongim_delivery_ack_batch_items_total{job="wukongim"})`,
		cloudanalysis.MetricQueryDeliveryAckBatchShardsCumulative:             `sum by (instance, node_name, phase, outcome) (wukongim_delivery_ack_batch_shards_total{job="wukongim"})`,
		cloudanalysis.MetricQueryDeliveryAckBatchRejectedCumulative:           `sum by (instance, node_name, phase, outcome) (wukongim_delivery_ack_batch_rejected_total{job="wukongim"})`,
		cloudanalysis.MetricQueryDeliveryAckBatchRollbackCumulative:           `sum by (instance, node_name, phase, outcome) (wukongim_delivery_ack_batch_rollback_total{job="wukongim"})`,
		cloudanalysis.MetricQueryDeliveryAckBatchP99:                          `histogram_quantile(0.99, sum by (instance, node_name, phase, outcome, le) (rate(wukongim_delivery_ack_batch_duration_seconds_bucket{job="wukongim"}[1m])))`,
	}
	for id, query := range want {
		if queries[id] != query {
			t.Fatalf("metric query %q = %q, want %q", id, queries[id], query)
		}
	}

	for id, query := range want {
		if strings.Contains(query, "uid") || strings.Contains(query, "node_id") || strings.Contains(query, "route") || strings.Contains(query, "slot") {
			t.Fatalf("metric query %q exposes a high-cardinality dimension: %s", id, query)
		}
	}
}

func TestDefaultMetricQueriesIncludeConversationActiveConservationEvidence(t *testing.T) {
	queries := defaultMetricQueries()
	wantIDs := []string{
		"conversation_active_cache_rows",
		"conversation_active_dirty_rows",
		"conversation_active_dirty_queue_rows",
		"conversation_active_dirty_age_buckets",
		"conversation_active_oldest_dirty_age",
		"conversation_active_dirty_mutation_rate",
		"conversation_active_cache_lock_p99",
		"conversation_active_flush_rows_cumulative",
		"conversation_active_flush_stage_p99",
		"conversation_active_flush_attempt_rate",
		"conversation_active_pressure_events",
		"conversation_active_pressure_state",
		"conversation_active_pressure_wakeup_p99",
	}
	for _, id := range wantIDs {
		if queries[id] == "" {
			t.Fatalf("metric query %q is missing from the Analysis allowlist", id)
		}
	}
	if got := queries["conversation_active_flush_rows_cumulative"]; got != `sum by (instance, node_name, result, stage, reason) (wukongim_conversation_active_flush_rows_total{job="wukongim"})` {
		t.Fatalf("conversation flush conservation query = %q", got)
	}
}

func TestDefaultMetricQueriesIncludeConversationPersistDrilldownEvidence(t *testing.T) {
	queries := defaultMetricQueries()
	wantIDs := []string{
		"storage_commit_queue_depth",
		"storage_commit_request_p99",
		"storage_commit_batch_stage_p99",
		"slot_proposal_rate",
		"slot_proposal_apply_p99",
		"slot_apply_gap",
		"slot_background_proposal_admission_rate",
		"slot_runtime_queue_pressure",
	}
	for _, id := range wantIDs {
		if queries[id] == "" {
			t.Fatalf("metric query %q is missing from the Analysis allowlist", id)
		}
	}
	if got := queries["storage_commit_queue_depth"]; got != `sum by (instance, node_name, store) (wukongim_storage_commit_queue_depth{job="wukongim"})` {
		t.Fatalf("storage commit queue query = %q, want node-preserving dimensions", got)
	}
	if got := queries["conversation_active_cache_lock_p99"]; got != `histogram_quantile(0.99, sum by (instance, node_name, result, phase, le) (rate(wukongim_conversation_active_cache_lock_duration_seconds_bucket{job="wukongim"}[1m])))` {
		t.Fatalf("conversation cache lock query = %q, want result and phase dimensions", got)
	}
	if got := queries["slot_proposal_apply_p99"]; got != `histogram_quantile(0.99, sum by (instance, node_name, le) (rate(wukongim_slot_apply_duration_seconds_bucket{job="wukongim"}[1m])))` {
		t.Fatalf("slot proposal apply query = %q, want per-node aggregation without slot_id", got)
	}
}

func TestDefaultMetricQueriesIncludePreferredLeaderReconcileEvidence(t *testing.T) {
	queries := defaultMetricQueries()
	want := map[string]string{
		"slot_preferred_leader_reconcile_rate":  `sum by (instance, node_name, decision) (rate(wukongim_slot_preferred_leader_reconcile_total{job="wukongim"}[1m]))`,
		"slot_preferred_leader_strict_wait_p99": `histogram_quantile(0.99, sum by (le, instance, node_name, decision) (rate(wukongim_slot_preferred_leader_strict_wait_duration_seconds_bucket{job="wukongim"}[5m])))`,
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
