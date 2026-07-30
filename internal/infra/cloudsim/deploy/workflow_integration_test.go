//go:build integration

package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudSimulationMonitorDiscoveryShellBehavior(t *testing.T) {
	root := repositoryRoot(t)
	script := workflowStepRun(t, root, "cloud-sim-monitor.yml", "Discover running provider inventory")
	testCases := []struct {
		name           string
		artifactPages  string
		inventory      string
		inventoryExit  string
		legacyConfig   string
		wantCandidates int
		wantError      string
		wantInventory  int
	}{
		{
			name:          "empty inventory ignores retained released locators",
			artifactPages: monitorProviderArtifactPages(1),
			inventory:     monitorInventorySnapshot(nil),
			wantInventory: 1,
		},
		{
			name:          "artifact budget boundary is admitted",
			artifactPages: monitorProviderArtifactPages(512),
			inventory:     monitorInventorySnapshot(nil),
			wantInventory: 1,
		},
		{
			name:           "running inventory yields exact candidate",
			artifactPages:  monitorProviderArtifactPages(1),
			inventory:      monitorInventorySnapshot([]string{"gh-running-1"}),
			wantCandidates: 1,
			wantInventory:  1,
		},
		{
			name:          "provider artifact budget fails closed before cloud query",
			artifactPages: monitorProviderArtifactPages(513),
			inventory:     monitorInventorySnapshot(nil),
			wantError:     `"error":"provider_config_artifact_budget_exceeded"`,
		},
		{
			name:          "provider binding budget fails without processing a subset",
			artifactPages: monitorProviderArtifactBindingPages(5),
			inventory:     monitorInventorySnapshot(nil),
			wantError:     `"error":"provider_binding_budget_exceeded"`,
		},
		{
			name:          "legacy binding cannot bypass resolved binding budget",
			artifactPages: monitorProviderArtifactBindingPages(4),
			inventory:     monitorInventorySnapshot(nil),
			legacyConfig:  `{"region":"cn-legacy","account_id_hash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
			wantError:     `"error":"provider_binding_budget_exceeded"`,
		},
		{
			name:          "legacy duplicate binding is deduplicated before inventory",
			artifactPages: monitorProviderArtifactBindingPages(4),
			inventory:     monitorInventorySnapshot(nil),
			legacyConfig:  `{"region":"cn-region-1","account_id_hash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			wantInventory: 4,
		},
		{
			name:          "inventory authority failure fails closed",
			artifactPages: monitorProviderArtifactPages(1),
			inventory:     monitorInventorySnapshot(nil),
			inventoryExit: "19",
			wantError:     `"error":"inventory_unavailable"`,
			wantInventory: 1,
		},
		{
			name:           "running candidate budget boundary is admitted",
			artifactPages:  monitorProviderArtifactPages(1),
			inventory:      monitorInventorySnapshot([]string{"gh-01-1", "gh-02-1", "gh-03-1", "gh-04-1"}),
			wantCandidates: 4,
			wantInventory:  1,
		},
		{
			name:          "running candidate budget fails without truncation",
			artifactPages: monitorProviderArtifactPages(1),
			inventory:     monitorInventorySnapshot([]string{"gh-01-1", "gh-02-1", "gh-03-1", "gh-04-1", "gh-05-1"}),
			wantError:     `"error":"running_candidate_budget_exceeded"`,
			wantInventory: 1,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			workDir := t.TempDir()
			binDir := filepath.Join(workDir, "bin")
			inventoryLog := filepath.Join(workDir, "inventory-calls.log")
			writeMonitorDiscoveryFakes(t, workDir, binDir)
			command := exec.Command("/bin/bash", "-c", script)
			command.Dir = workDir
			command.Env = append(os.Environ(),
				"RUN_ID=",
				"GITHUB_REPOSITORY=example/repository",
				"LEGACY_PROVIDER_CONFIG_JSON="+testCase.legacyConfig,
				"MAX_PROVIDER_CONFIG_ARTIFACTS=512",
				"MAX_PROVIDER_BINDINGS=4",
				"MAX_RUNNING_CANDIDATES=4",
				"PROVIDER_COMMAND_TIMEOUT_SECONDS=60",
				"MONITOR_PROVIDER_ARTIFACT_PAGES="+testCase.artifactPages,
				"MONITOR_INVENTORY_JSON="+testCase.inventory,
				"MONITOR_INVENTORY_EXIT="+testCase.inventoryExit,
				"MONITOR_INVENTORY_CALL_LOG="+inventoryLog,
				"PATH="+binDir+":"+os.Getenv("PATH"),
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("discover monitor candidates: %v\n%s", err, output)
			}
			candidates, err := os.ReadFile(filepath.Join(workDir, "inventory-candidates.tsv"))
			if err != nil {
				t.Fatal(err)
			}
			if got := len(strings.Fields(string(candidates))) / 2; got != testCase.wantCandidates {
				t.Fatalf("candidate rows = %q (%d), want %d", candidates, got, testCase.wantCandidates)
			}
			errorsJSON, err := os.ReadFile(filepath.Join(workDir, "discovery-errors.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if testCase.wantError != "" && !strings.Contains(string(errorsJSON), testCase.wantError) {
				t.Fatalf("discovery errors = %q, want %q", errorsJSON, testCase.wantError)
			}
			inventoryCalls := 0
			if inventoryData, inventoryErr := os.ReadFile(inventoryLog); inventoryErr == nil {
				trimmed := strings.TrimSpace(string(inventoryData))
				if trimmed != "" {
					inventoryCalls = len(strings.Split(trimmed, "\n"))
				}
			} else if !os.IsNotExist(inventoryErr) {
				t.Fatal(inventoryErr)
			}
			if inventoryCalls != testCase.wantInventory {
				t.Fatalf("inventory calls = %d, want %d", inventoryCalls, testCase.wantInventory)
			}
		})
	}
}

func TestCloudSimulationMonitorPatrolShellBehavior(t *testing.T) {
	root := repositoryRoot(t)
	script := workflowStepRun(t, root, "cloud-sim-monitor.yml", "Patrol public liveness and Prometheus evidence")
	accountHash := "sha256:" + strings.Repeat("a", 64)
	testCases := []struct {
		name             string
		scenario         string
		locator          string
		wantPatrolFailed string
		wantPublicCalls  bool
		wantResult       string
	}{
		{name: "released skips public patrol", scenario: "released", wantPatrolFailed: "0"},
		{name: "non-running live skips public patrol", scenario: "stopped", wantPatrolFailed: "0"},
		{name: "running live patrols public evidence", scenario: "running", wantPatrolFailed: "0", wantPublicCalls: true, wantResult: `"verdict":"healthy"`},
		{name: "missing simulator address is structured", scenario: "sim-missing", wantPatrolFailed: "1", wantResult: `"error":"simulator_public_address_unavailable"`},
		{name: "targets HTTP failure is structured", scenario: "targets-failure", wantPatrolFailed: "1", wantPublicCalls: true, wantResult: `"error":"prometheus_targets_unavailable"`},
		{name: "targets JSON failure is structured", scenario: "targets-invalid", wantPatrolFailed: "1", wantPublicCalls: true, wantResult: `"error":"prometheus_targets_unavailable"`},
		{name: "sustained query failure is structured", scenario: "sustained-failure", wantPatrolFailed: "1", wantPublicCalls: true, wantResult: `"error":"prometheus_sustained_targets_unavailable"`},
		{name: "missing metric is structured", scenario: "metric-missing", wantPatrolFailed: "1", wantPublicCalls: true, wantResult: `"error":"prometheus_cpu_unavailable"`},
		{name: "cross-page duplicate locator fails closed", scenario: "duplicate", wantPatrolFailed: "1", wantResult: `"error":"locator_unavailable"`},
		{name: "invalid locator fails closed", scenario: "released", locator: `{"run_id":"gh-test-1","region":"INVALID","account_id_hash":"` + accountHash + `"}`, wantPatrolFailed: "1", wantResult: `"error":"locator_invalid"`},
		{name: "preflight failure fails closed", scenario: "preflight-failure", wantPatrolFailed: "1", wantResult: `"error":"preflight_unavailable"`},
		{name: "invalid preflight fails closed", scenario: "preflight-invalid", wantPatrolFailed: "1", wantResult: `"error":"preflight_invalid"`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			workDir := t.TempDir()
			binDir := filepath.Join(workDir, "bin")
			callLog := filepath.Join(workDir, "public-calls.log")
			outputPath := filepath.Join(workDir, "github-output")
			locator := testCase.locator
			if locator == "" {
				locator = `{"run_id":"gh-test-1","region":"cn-hangzhou","account_id_hash":"` + accountHash + `","expires_at":"2099-01-01T00:00:00Z"}`
			}
			writeMonitorWorkflowFakes(t, workDir, binDir)
			if err := os.WriteFile(filepath.Join(workDir, "provider.json"), []byte(`{"region":"cn-hangzhou","account_id_hash":"`+accountHash+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workDir, "inventory-candidates.tsv"), []byte("gh-test-1\tprovider.json\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workDir, "discovery-errors.jsonl"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/bash", "-c", script)
			command.Dir = workDir
			command.Env = append(os.Environ(),
				"MONITOR_SCENARIO="+testCase.scenario,
				"MONITOR_LOCATOR_JSON="+locator,
				"MONITOR_PUBLIC_CALL_LOG="+callLog,
				"GITHUB_OUTPUT="+outputPath,
				"GITHUB_REPOSITORY=example/repository",
				"PROVIDER_COMMAND_TIMEOUT_SECONDS=60",
				"PATH="+binDir+":"+os.Getenv("PATH"),
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("execute monitor patrol: %v\n%s", err, output)
			}
			githubOutput, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(githubOutput), "patrol_failed="+testCase.wantPatrolFailed) {
				t.Fatalf("GitHub output = %q, want patrol_failed=%s", githubOutput, testCase.wantPatrolFailed)
			}
			_, publicCallErr := os.Stat(callLog)
			if testCase.wantPublicCalls && publicCallErr != nil {
				t.Fatalf("public patrol was not called: %v", publicCallErr)
			}
			if !testCase.wantPublicCalls && !os.IsNotExist(publicCallErr) {
				t.Fatalf("public patrol was called unexpectedly: %v", publicCallErr)
			}
			results, err := os.ReadFile(filepath.Join(workDir, "monitor-results.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if testCase.wantResult != "" && !strings.Contains(string(results), testCase.wantResult) {
				t.Fatalf("monitor results = %q, want %q", results, testCase.wantResult)
			}
		})
	}
}
