package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
	benchconfig "github.com/WuKongIM/WuKongIM/internal/bench/config"
	cloudanalysisinfra "github.com/WuKongIM/WuKongIM/internal/infra/cloudanalysis"
	cloudanalysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	defaultAnalysisListenAddr = "0.0.0.0:19092"
	defaultManagerBaseURL     = "http://wk-node1:5301"
	defaultPrometheusBaseURL  = "http://prometheus:9090"
)

var errInvalidAnalysisConfig = errors.New("cmd/wkanalysis: invalid config")

type serveConfig struct {
	listenAddr string
	tlsCert    string
	tlsKey     string
	oidc       *githubOIDCConfig
	gateway    app.CloudAnalysisGatewayConfig
}

func loadServeConfig(getenv func(string) string, now func() time.Time) (serveConfig, error) {
	if getenv == nil {
		return serveConfig{}, errInvalidAnalysisConfig
	}
	if now == nil {
		now = time.Now
	}
	current := now().UTC()
	runID := strings.TrimSpace(getenv("WK_ANALYSIS_RUN_ID"))
	token := getenv("WK_ANALYSIS_MCP_TOKEN")
	oidcConfig, err := loadGitHubOIDCConfig(getenv, runID)
	if runID == "" || (oidcConfig == nil && len(token) < 32) {
		return serveConfig{}, fmt.Errorf("%w: run identity and either GitHub OIDC or a 32-byte MCP token are required", errInvalidAnalysisConfig)
	}
	scenarioPath := strings.TrimSpace(getenv("WK_ANALYSIS_SCENARIO_PATH"))
	if scenarioPath == "" {
		return serveConfig{}, fmt.Errorf("%w: WK_ANALYSIS_SCENARIO_PATH is required", errInvalidAnalysisConfig)
	}
	effectiveScenario, err := benchconfig.LoadScenario(scenarioPath)
	if err != nil {
		return serveConfig{}, fmt.Errorf("%w: load effective scenario: %v", errInvalidAnalysisConfig, err)
	}
	scenarioDigest, err := model.DigestScenario(effectiveScenario)
	if err != nil {
		return serveConfig{}, fmt.Errorf("%w: %v", errInvalidAnalysisConfig, err)
	}
	if expected := strings.TrimSpace(getenv("WK_ANALYSIS_SCENARIO_DIGEST")); expected != "" && expected != scenarioDigest {
		return serveConfig{}, fmt.Errorf("%w: effective scenario digest mismatch", errInvalidAnalysisConfig)
	}
	scenario := cloudanalysis.ScenarioInspection{
		Digest: scenarioDigest, RandomSeed: effectiveScenario.Run.RandomSeed,
		HashSlotCount: 256, Effective: &effectiveScenario,
	}
	if effectiveScenario.Version != "wkbench/v1" || effectiveScenario.Run.RandomSeed == 0 {
		return serveConfig{}, fmt.Errorf("%w: effective scenario requires wkbench/v1 and a non-zero random seed", errInvalidAnalysisConfig)
	}
	fakeInventoryPath := strings.TrimSpace(getenv("WK_ANALYSIS_FAKE_INVENTORY_PATH"))
	locatorPath := strings.TrimSpace(getenv("WK_ANALYSIS_RUN_LOCATOR_PATH"))
	if (fakeInventoryPath == "") != (locatorPath == "") {
		return serveConfig{}, fmt.Errorf("%w: fake inventory and Run Locator must be configured together", errInvalidAnalysisConfig)
	}
	var locator *cloudsim.RunLocator
	if locatorPath != "" {
		decoded, decodeErr := readRunLocator(locatorPath)
		if decodeErr != nil {
			return serveConfig{}, fmt.Errorf("%w: %v", errInvalidAnalysisConfig, decodeErr)
		}
		if decoded.RunID != runID || decoded.ScenarioDigest != scenarioDigest {
			return serveConfig{}, fmt.Errorf("%w: Run Locator identity mismatch", errInvalidAnalysisConfig)
		}
		locator = &decoded
	}
	listenAddr := envDefault(getenv, "WK_ANALYSIS_LISTEN_ADDR", defaultAnalysisListenAddr)
	if _, _, err := net.SplitHostPort(listenAddr); err != nil {
		return serveConfig{}, fmt.Errorf("%w: WK_ANALYSIS_LISTEN_ADDR", errInvalidAnalysisConfig)
	}
	defaultRunExpiry := current.Add(2 * time.Hour)
	if locator != nil {
		defaultRunExpiry = locator.ExpiresAt
	}
	runExpiresAt, err := optionalTime(getenv("WK_ANALYSIS_RUN_EXPIRES_AT"), defaultRunExpiry)
	if err != nil || runExpiresAt.Sub(current) < 30*time.Minute {
		return serveConfig{}, fmt.Errorf("%w: WK_ANALYSIS_RUN_EXPIRES_AT must leave at least 30 minutes", errInvalidAnalysisConfig)
	}
	if locator != nil && !runExpiresAt.Equal(locator.ExpiresAt) {
		return serveConfig{}, fmt.Errorf("%w: Run Lease does not match the Run Locator", errInvalidAnalysisConfig)
	}
	var tokenExpiresAt time.Time
	if oidcConfig == nil {
		defaultTokenExpiry := current.Add(45 * time.Minute)
		if leaseBound := runExpiresAt.Add(-5 * time.Minute); leaseBound.Before(defaultTokenExpiry) {
			defaultTokenExpiry = leaseBound
		}
		tokenExpiresAt, err = optionalTime(getenv("WK_ANALYSIS_TOKEN_EXPIRES_AT"), defaultTokenExpiry)
		if err != nil || !tokenExpiresAt.After(current) || tokenExpiresAt.After(current.Add(45*time.Minute)) || tokenExpiresAt.After(runExpiresAt.Add(-5*time.Minute)) {
			return serveConfig{}, fmt.Errorf("%w: WK_ANALYSIS_TOKEN_EXPIRES_AT exceeds the Analysis Session or Run Lease", errInvalidAnalysisConfig)
		}
	}
	nodeURLs, err := parseNodeURLs(getenv("WK_ANALYSIS_NODE_API_URLS"))
	if err != nil {
		return serveConfig{}, err
	}
	inventoryCount, err := optionalPositiveInt(getenv("WK_ANALYSIS_INVENTORY_COUNT"), 12)
	if err != nil {
		return serveConfig{}, fmt.Errorf("%w: WK_ANALYSIS_INVENTORY_COUNT", errInvalidAnalysisConfig)
	}
	managerAuth := cloudanalysisinfra.ManagerAuth{
		BearerToken: strings.TrimSpace(getenv("WK_ANALYSIS_MANAGER_BEARER_TOKEN")),
		Username:    strings.TrimSpace(getenv("WK_ANALYSIS_MANAGER_USERNAME")),
		Password:    getenv("WK_ANALYSIS_MANAGER_PASSWORD"),
	}
	if managerAuth.BearerToken == "" && (managerAuth.Username == "") != (managerAuth.Password == "") {
		return serveConfig{}, fmt.Errorf("%w: manager username and password must be set together", errInvalidAnalysisConfig)
	}
	rawProvider := strings.TrimSpace(getenv("WK_ANALYSIS_PROVIDER"))
	rawRegion := strings.TrimSpace(getenv("WK_ANALYSIS_REGION"))
	rawSourceSHA := strings.TrimSpace(getenv("WK_ANALYSIS_SOURCE_SHA"))
	provider := envDefault(getenv, "WK_ANALYSIS_PROVIDER", "local-compose")
	region := envDefault(getenv, "WK_ANALYSIS_REGION", "local")
	sourceSHA := rawSourceSHA
	if locator != nil {
		if rawProvider != "" && rawProvider != locator.Provider || rawRegion != "" && rawRegion != locator.Region || rawSourceSHA != "" && rawSourceSHA != locator.SourceSHA {
			return serveConfig{}, fmt.Errorf("%w: configured run identity differs from the Run Locator", errInvalidAnalysisConfig)
		}
		provider, region, sourceSHA = locator.Provider, locator.Region, locator.SourceSHA
	}
	tlsCert := strings.TrimSpace(getenv("WK_ANALYSIS_TLS_CERT_FILE"))
	tlsKey := strings.TrimSpace(getenv("WK_ANALYSIS_TLS_KEY_FILE"))
	if (tlsCert == "") != (tlsKey == "") || (oidcConfig != nil && tlsCert == "") {
		return serveConfig{}, fmt.Errorf("%w: cloud OIDC requires a TLS certificate and key", errInvalidAnalysisConfig)
	}
	return serveConfig{
		listenAddr: listenAddr,
		tlsCert:    tlsCert, tlsKey: tlsKey, oidc: oidcConfig,
		gateway: app.CloudAnalysisGatewayConfig{
			RunID: runID, RunState: cloudanalysis.RunState(envDefault(getenv, "WK_ANALYSIS_RUN_STATE", "running")),
			RunInventoryCount: inventoryCount, Provider: provider,
			Region: region, SourceSHA: sourceSHA, Scenario: scenario, RunLocator: locator,
			RunExpiresAt: runExpiresAt, MCPToken: token, MCPTokenExpiresAt: tokenExpiresAt,
			ManagerBaseURL: envDefault(getenv, "WK_ANALYSIS_MANAGER_BASE_URL", defaultManagerBaseURL), ManagerAuth: managerAuth,
			PrometheusBaseURL: envDefault(getenv, "WK_ANALYSIS_PROMETHEUS_BASE_URL", defaultPrometheusBaseURL),
			NodeAPIBaseURLs:   nodeURLs, MetricQueries: defaultMetricQueries(),
			WorkloadReportDir: effectiveScenario.Run.ReportDir,
			FakeInventoryPath: fakeInventoryPath, Now: now,
		},
	}, nil
}

func readRunLocator(path string) (cloudsim.RunLocator, error) {
	file, err := os.Open(path)
	if err != nil {
		return cloudsim.RunLocator{}, err
	}
	defer file.Close()
	return cloudsim.DecodeRunLocator(file)
}

func parseNodeURLs(raw string) (map[uint64]string, error) {
	if strings.TrimSpace(raw) == "" {
		return map[uint64]string{
			1: "http://wk-node1:5001",
			2: "http://wk-node2:5001",
			3: "http://wk-node3:5001",
		}, nil
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("%w: WK_ANALYSIS_NODE_API_URLS must be a JSON object", errInvalidAnalysisConfig)
	}
	if len(values) != 3 {
		return nil, fmt.Errorf("%w: WK_ANALYSIS_NODE_API_URLS must contain nodes 1, 2, and 3", errInvalidAnalysisConfig)
	}
	result := make(map[uint64]string, len(values))
	for key, value := range values {
		nodeID, err := strconv.ParseUint(key, 10, 64)
		if err != nil || nodeID < 1 || nodeID > 3 || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%w: WK_ANALYSIS_NODE_API_URLS contains an invalid node", errInvalidAnalysisConfig)
		}
		result[nodeID] = value
	}
	for _, nodeID := range []uint64{1, 2, 3} {
		if result[nodeID] == "" {
			return nil, fmt.Errorf("%w: WK_ANALYSIS_NODE_API_URLS must contain nodes 1, 2, and 3", errInvalidAnalysisConfig)
		}
	}
	return result, nil
}

func defaultMetricQueries() map[string]string {
	return map[string]string{
		"targets_up":                                `up`,
		"send_rate":                                 `sum(rate(wukongim_gateway_messages_received_total[1m]))`,
		"deliver_rate":                              `sum(rate(wukongim_gateway_messages_delivered_total[1m]))`,
		"append_ok_rate":                            `sum(rate(wukongim_message_append_total{result="ok"}[1m]))`,
		"append_error_rate":                         `sum(rate(wukongim_message_append_errors_total[1m])) by (path, class)`,
		"gateway_queue_depth":                       `sum(wukongim_gateway_async_send_queue_depth)`,
		"runtime_queue_pressure":                    `max(wukongim_runtime_pool_queue_depth / (wukongim_runtime_pool_queue_capacity > 0))`,
		"storage_commit_queue_depth":                `sum(wukongim_storage_commit_queue_depth) by (store)`,
		"delivery_retry_queue_depth":                `sum(wukongim_delivery_retry_queue_depth)`,
		"process_cpu_rate":                          `sum by (instance) (rate(process_cpu_seconds_total[1m]))`,
		"process_resident_memory":                   `sum by (instance) (process_resident_memory_bytes)`,
		"go_goroutines":                             `sum by (instance) (go_goroutines)`,
		"simulator_cpu_percent":                     `100 * (1 - avg(rate(node_cpu_seconds_total{job="hosts",role="sim",mode="idle"}[1m])))`,
		"simulator_memory_percent":                  `100 * (1 - (node_memory_MemAvailable_bytes{job="hosts",role="sim"} / clamp_min(node_memory_MemTotal_bytes{job="hosts",role="sim"}, 1)))`,
		"simulator_tcp_inuse":                       `node_sockstat_TCP_inuse{job="hosts",role="sim"}`,
		"simulator_tcp_time_wait":                   `node_sockstat_TCP_tw{job="hosts",role="sim"}`,
		"simulator_network_bytes":                   `sum by (instance) (rate(node_network_receive_bytes_total{job="hosts",role="sim",device!~"lo"}[1m]) + rate(node_network_transmit_bytes_total{job="hosts",role="sim",device!~"lo"}[1m]))`,
		"simulator_disk_used_percent":               `100 * (1 - (node_filesystem_avail_bytes{job="hosts",role="sim",mountpoint="/var/lib/wukongim-cloud"} / clamp_min(node_filesystem_size_bytes{job="hosts",role="sim",mountpoint="/var/lib/wukongim-cloud"}, 1)))`,
		"node_memory_percent":                       `100 * (1 - (node_memory_MemAvailable_bytes{job="hosts",role=~"node-[123]"} / clamp_min(node_memory_MemTotal_bytes{job="hosts",role=~"node-[123]"}, 1)))`,
		"node_oom_kills":                            `node_vmstat_oom_kill{job="hosts",role=~"node-[123]"}`,
		"node_service_cgroup_available":             `wukongim_service_cgroup_available{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_current_bytes":         `wukongim_service_cgroup_memory_current_bytes{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_peak_bytes":            `wukongim_service_cgroup_memory_peak_bytes{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_peak_native_available": `wukongim_service_cgroup_memory_peak_native_available{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_limit_bytes":           `wukongim_service_cgroup_memory_limit_bytes{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_limit_unlimited":       `wukongim_service_cgroup_memory_limit_unlimited{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_events_oom":            `wukongim_service_cgroup_memory_events_total{job="hosts",role=~"node-[123]",event="oom"}`,
		"node_service_memory_events_oom_kill":       `wukongim_service_cgroup_memory_events_total{job="hosts",role=~"node-[123]",event="oom_kill"}`,
		"node_service_memory_swap_current_bytes":    `wukongim_service_cgroup_memory_swap_current_bytes{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_swap_limit_bytes":      `wukongim_service_cgroup_memory_swap_limit_bytes{job="hosts",role=~"node-[123]"}`,
		"node_service_memory_swap_limit_unlimited":  `wukongim_service_cgroup_memory_swap_limit_unlimited{job="hosts",role=~"node-[123]"}`,
		"process_start_time_seconds":                `process_start_time_seconds{job="wukongim"}`,
		"gateway_active_connections":                `sum by (instance, node_name) (wukongim_gateway_connections_active{job="wukongim"})`,
		"channel_active_channels":                   `sum by (instance, node_name) (wukongim_channel_active_channels{job="wukongim"})`,
		"conversation_active_cache_rows":            `wukongim_conversation_active_cache_rows{job="wukongim"}`,
		"conversation_active_dirty_rows":            `wukongim_conversation_active_cache_dirty_rows{job="wukongim"}`,
		"conversation_active_oldest_dirty_age":      `wukongim_conversation_active_cache_oldest_dirty_age_seconds{job="wukongim"}`,
		"conversation_active_dirty_mutation_rate":   `sum by (instance, node_name, event) (rate(wukongim_conversation_active_dirty_mutations_total{job="wukongim"}[1m]))`,
		"conversation_active_flush_rows_cumulative": `sum by (instance, node_name, result, stage, reason) (wukongim_conversation_active_flush_rows_total{job="wukongim"})`,
		"conversation_active_flush_stage_p99":       `histogram_quantile(0.99, sum by (instance, node_name, result, stage, le) (rate(wukongim_conversation_active_flush_stage_duration_seconds_bucket{job="wukongim"}[1m])))`,
		"conversation_active_flush_attempt_rate":    `sum by (instance, node_name, result) (rate(wukongim_conversation_active_flush_total{job="wukongim"}[1m]))`,
		"conversation_active_pressure_events":       `sum by (instance, node_name, event) (wukongim_conversation_active_pressure_events_total{job="wukongim"})`,
		"conversation_active_pressure_state":        `wukongim_conversation_active_pressure_draining{job="wukongim"}`,
		"conversation_active_pressure_wakeup_p99":   `histogram_quantile(0.99, sum by (instance, node_name, le) (rate(wukongim_conversation_active_pressure_wakeup_wait_duration_seconds_bucket{job="wukongim"}[1m])))`,
		"node_data_disk_used_bytes":                 `max by (instance, role) (node_filesystem_size_bytes{job="hosts",role=~"node-[123]",mountpoint="/var/lib/wukongim-cloud"} - node_filesystem_avail_bytes{job="hosts",role=~"node-[123]",mountpoint="/var/lib/wukongim-cloud"})`,
	}
}

func optionalTime(raw string, fallback time.Time) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(raw))
}

func optionalPositiveInt(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errInvalidAnalysisConfig
	}
	return value, nil
}

func envDefault(getenv func(string) string, key, fallback string) string {
	if value := strings.TrimSpace(getenv(key)); value != "" {
		return value
	}
	return fallback
}
