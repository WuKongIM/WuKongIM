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
	if runID == "" || len(token) < 32 {
		return serveConfig{}, fmt.Errorf("%w: WK_ANALYSIS_RUN_ID and a 32-byte WK_ANALYSIS_MCP_TOKEN are required", errInvalidAnalysisConfig)
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
	defaultTokenExpiry := current.Add(45 * time.Minute)
	if leaseBound := runExpiresAt.Add(-5 * time.Minute); leaseBound.Before(defaultTokenExpiry) {
		defaultTokenExpiry = leaseBound
	}
	tokenExpiresAt, err := optionalTime(getenv("WK_ANALYSIS_TOKEN_EXPIRES_AT"), defaultTokenExpiry)
	if err != nil || !tokenExpiresAt.After(current) || tokenExpiresAt.After(current.Add(45*time.Minute)) || tokenExpiresAt.After(runExpiresAt.Add(-5*time.Minute)) {
		return serveConfig{}, fmt.Errorf("%w: WK_ANALYSIS_TOKEN_EXPIRES_AT exceeds the Analysis Session or Run Lease", errInvalidAnalysisConfig)
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
	return serveConfig{
		listenAddr: listenAddr,
		gateway: app.CloudAnalysisGatewayConfig{
			RunID: runID, RunState: cloudanalysis.RunState(envDefault(getenv, "WK_ANALYSIS_RUN_STATE", "running")),
			RunInventoryCount: inventoryCount, Provider: provider,
			Region: region, SourceSHA: sourceSHA, Scenario: scenario, RunLocator: locator,
			RunExpiresAt: runExpiresAt, MCPToken: token, MCPTokenExpiresAt: tokenExpiresAt,
			ManagerBaseURL: envDefault(getenv, "WK_ANALYSIS_MANAGER_BASE_URL", defaultManagerBaseURL), ManagerAuth: managerAuth,
			PrometheusBaseURL: envDefault(getenv, "WK_ANALYSIS_PROMETHEUS_BASE_URL", defaultPrometheusBaseURL),
			NodeAPIBaseURLs:   nodeURLs, MetricQueries: defaultMetricQueries(),
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
		"targets_up":                 `up`,
		"send_rate":                  `sum(rate(wukongim_gateway_messages_received_total[1m]))`,
		"deliver_rate":               `sum(rate(wukongim_gateway_messages_delivered_total[1m]))`,
		"append_ok_rate":             `sum(rate(wukongim_message_append_total{result="ok"}[1m]))`,
		"append_error_rate":          `sum(rate(wukongim_message_append_errors_total[1m])) by (path, class)`,
		"gateway_queue_depth":        `sum(wukongim_gateway_async_send_queue_depth)`,
		"runtime_queue_pressure":     `max(wukongim_runtime_pool_queue_depth / clamp_min(wukongim_runtime_pool_queue_capacity, 1))`,
		"storage_commit_queue_depth": `sum(wukongim_storage_commit_queue_depth) by (store)`,
		"delivery_retry_queue_depth": `sum(wukongim_delivery_retry_queue_depth)`,
		"process_cpu_rate":           `sum by (instance) (rate(process_cpu_seconds_total[1m]))`,
		"process_resident_memory":    `sum by (instance) (process_resident_memory_bytes)`,
		"go_goroutines":              `sum by (instance) (go_goroutines)`,
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
