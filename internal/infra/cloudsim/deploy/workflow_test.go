package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var immutableAction = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+@[0-9a-f]{40}$`)

func TestCloudSimulationWorkflowsParseAndPinEveryAction(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{"cloud-sim-oidc-subject.yml", "cloud-sim-provision.yml", "cloud-sim-analyze.yml", "cloud-sim-monitor.yml", "cloud-sim-cleanup.yml"} {
		path := filepath.Join(root, ".github", "workflows", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		uses := collectYAMLScalars(&document, "uses")
		if len(uses) == 0 && name != "cloud-sim-oidc-subject.yml" {
			t.Fatalf("%s contains no actions", name)
		}
		for _, action := range uses {
			if !immutableAction.MatchString(action) {
				t.Errorf("%s action %q is not pinned to a commit", name, action)
			}
		}
	}
}

func TestCloudSimulationMonitorPatrolsRunningRunsWithoutStartingThem(t *testing.T) {
	monitor := readWorkflowText(t, repositoryRoot(t), "cloud-sim-monitor.yml")
	for _, required := range []string{
		`cron: "*/30 * * * *"`,
		"run_id:",
		"environment: cloud-sim-analysis",
		`MAX_PROVIDER_CONFIG_ARTIFACTS: "512"`,
		`MAX_PROVIDER_BINDINGS: "4"`,
		`MAX_RUNNING_CANDIDATES: "4"`,
		`PROVIDER_COMMAND_TIMEOUT_SECONDS: "60"`,
		`inventory >"$snapshot"`,
		`discovery-errors.jsonl`,
		`record_discovery_error provider_config_artifact_budget_exceeded`,
		`record_discovery_error provider_binding_budget_exceeded`,
		`record_discovery_error running_candidate_budget_exceeded`,
		`artifact_name="cloud-sim-locator-${run_id}"`,
		`gh api --paginate --slurp`,
		`[.[] | .artifacts[]? | select(.expired == false)] | length`,
		`error:"locator_unavailable"`,
		`error:"locator_invalid"`,
		`error:"provider_config_unavailable"`,
		`preflight --locator "$locator_path"`,
		`error:"preflight_unavailable"`,
		`error:"preflight_invalid"`,
		`if [[ "$preflight_state" == "released" ]]; then`,
		`run_json="$(jq -cer '.run' "$preflight_path")"`,
		`/cloud-view/status`,
		`/prometheus/api/v1/targets?state=active`,
		`min_over_time(up[30m])`,
		`"$state" != "running"`,
		"monitor-results.jsonl",
	} {
		if !strings.Contains(monitor, required) {
			t.Fatalf("monitor workflow missing patrol contract %q", required)
		}
	}
	for _, forbidden := range []string{`status "$run_id"`, `error:"status_unavailable"`, "tail -n 25", " create ", "wkbench run"} {
		if strings.Contains(monitor, forbidden) {
			t.Fatalf("monitor workflow contains forbidden lifecycle contract %q", forbidden)
		}
	}
	inventoryIndex := strings.Index(monitor, `inventory >"$snapshot"`)
	locatorIndex := strings.Index(monitor, `artifact_name="cloud-sim-locator-${run_id}"`)
	preflightIndex := strings.Index(monitor, `preflight --locator "$locator_path"`)
	releasedIndex := strings.Index(monitor, `if [[ "$preflight_state" == "released" ]]; then`)
	runIndex := strings.Index(monitor, `run_json="$(jq -cer '.run' "$preflight_path")"`)
	nonRunningIndex := strings.Index(monitor, `if [[ "$state" != "running" ]]; then`)
	patrolIndex := strings.Index(monitor, `/cloud-view/status`)
	if inventoryIndex < 0 || locatorIndex <= inventoryIndex || preflightIndex <= locatorIndex || releasedIndex <= preflightIndex || runIndex <= releasedIndex ||
		nonRunningIndex <= runIndex || patrolIndex <= nonRunningIndex {
		t.Fatal("monitor workflow does not narrow provider inventory and bind locator preflight before public patrol")
	}
	releasedBlock := workflowShellIfBlock(t, monitor, `if [[ "$preflight_state" == "released" ]]; then`)
	if !strings.Contains(releasedBlock, "\n              continue") || strings.Contains(releasedBlock, "patrol_failed=1") {
		t.Fatal("monitor workflow does not treat provider-confirmed released as a normal skip")
	}
	nonRunningBlock := workflowShellIfBlock(t, monitor, `if [[ "$state" != "running" ]]; then`)
	if !strings.Contains(nonRunningBlock, "\n              continue") || strings.Contains(nonRunningBlock, "/cloud-view/") {
		t.Fatal("monitor workflow does not skip non-running live runs before public patrol")
	}
	for _, errorMarker := range []string{
		`error:"locator_unavailable"`, `error:"locator_invalid"`, `error:"provider_config_unavailable"`,
		`error:"preflight_unavailable"`, `error:"preflight_invalid"`,
	} {
		assertWorkflowFailureIsFailClosed(t, monitor, errorMarker)
	}
}

func TestCloudSimulationMonitorCommandBudgetsFitJobTimeout(t *testing.T) {
	monitor := readWorkflowText(t, repositoryRoot(t), "cloud-sim-monitor.yml")
	jobMinutes := workflowIntValue(t, monitor, `timeout-minutes:\s*([0-9]+)`)
	providerSeconds := workflowIntValue(t, monitor, `PROVIDER_COMMAND_TIMEOUT_SECONDS:\s*"([0-9]+)"`)
	maxBindings := workflowIntValue(t, monitor, `MAX_PROVIDER_BINDINGS:\s*"([0-9]+)"`)
	maxCandidates := workflowIntValue(t, monitor, `MAX_RUNNING_CANDIDATES:\s*"([0-9]+)"`)
	curlMatches := regexp.MustCompile(`--max-time\s+([0-9]+)`).FindAllStringSubmatch(monitor, -1)
	if len(curlMatches) == 0 {
		t.Fatal("monitor workflow has no bounded public HTTP timeout")
	}
	curlSeconds, err := strconv.Atoi(curlMatches[0][1])
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range curlMatches[1:] {
		if match[1] != curlMatches[0][1] {
			t.Fatalf("monitor public HTTP timeouts are inconsistent: %v", curlMatches)
		}
	}
	const startupAndLocalReserveSeconds = 10 * 60
	const publicCallsPerCandidate = 7
	const boundedCommandsPerCandidate = 3
	boundedSeconds := providerSeconds*(1+2*maxBindings+boundedCommandsPerCandidate*maxCandidates) +
		curlSeconds*publicCallsPerCandidate*maxCandidates + startupAndLocalReserveSeconds
	if boundedSeconds > jobMinutes*60 {
		t.Fatalf("monitor command budget %ds plus reserve exceeds job timeout %ds", boundedSeconds, jobMinutes*60)
	}
}

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

func TestCloudSimulationWorkflowPrivilegeSeparation(t *testing.T) {
	root := repositoryRoot(t)
	provision := readWorkflowText(t, root, "cloud-sim-provision.yml")
	analysis := readWorkflowText(t, root, "cloud-sim-analyze.yml")
	cleanup := readWorkflowText(t, root, "cloud-sim-cleanup.yml")
	oidcSubject := readWorkflowText(t, root, "cloud-sim-oidc-subject.yml")

	assertWorkflowText(t, provision, "build:\n", "provision:\n", "id-token: write", "environment: cloud-sim-provision")
	for _, required := range []string{
		`transition "$RUN_ID" ready`, `transition "$RUN_ID" running --active-until`,
		`transition "$RUN_ID" analysis_grace`, `destroy "$RUN_ID"`, `./scripts/cloud-sim/finalize.sh $RUN_ID`,
	} {
		if !strings.Contains(provision, required) {
			t.Fatalf("provision workflow missing lifecycle guard %q", required)
		}
	}
	buildSection := between(t, provision, "  build:\n", "  provision:\n")
	if strings.Contains(buildSection, "id-token:") || strings.Contains(buildSection, "ALIBABA_CLOUD_ACCESS_KEY") {
		t.Fatal("bundle build job unexpectedly has cloud identity")
	}

	prepareSection := between(t, analysis, "  prepare:\n", "  close:\n")
	closeSection := analysis[strings.Index(analysis, "  close:\n"):]
	if !strings.Contains(prepareSection, "contents: read") || !strings.Contains(prepareSection, "id-token: write") || strings.Contains(prepareSection, "contents: write") {
		t.Fatal("analysis session preparation privilege boundary is invalid")
	}
	for _, required := range []string{
		"run-name: Cloud Simulation Analysis", "operation:", "- inspect", "request_id:", "client_ipv4:", "client_public_key:",
		`select(test("^[0-9a-f]{40}$"))`, `select(test("^sha256:[0-9a-f]{64}$"))`,
		"wukongim/cloud-simulation-analysis-preflight/v1", `.resources == [] and (has("run") | not)`,
		`provider="$(jq -cer '{state:.state,resources:.resources}' preflight.json)"`,
		`open-analysis "$RUN_ID"`, `--source "${CLIENT_IPV4}/32"`, "openssl pkeyutl -encrypt",
		"rsa_padding_mode:oaep", "rsa_oaep_md:sha256", "encrypted-token.bin",
		"cloud-sim-analysis-session-${{ inputs.request_id }}", "retention-days: 1",
	} {
		if !strings.Contains(analysis, required) {
			t.Fatalf("analysis session workflow missing %q", required)
		}
	}
	clientValidation := strings.Index(prepareSection, "- name: Validate live client material")
	preflight := strings.Index(prepareSection, "- name: Bind locator to current provider inventory")
	if preflight < 0 || clientValidation <= preflight ||
		!strings.Contains(prepareSection, "if: inputs.operation == 'prepare' && steps.preflight.outputs.state == 'live'") ||
		!strings.Contains(prepareSection, "continue-on-error: true") {
		t.Fatal("analysis broker does not defer nullable client-material validation until provider-confirmed live state")
	}
	runnerClose := strings.Index(prepareSection, `close-analysis "$RUN_ID"`)
	clientOpen := strings.Index(prepareSection, `--source "${CLIENT_IPV4}/32"`)
	liveDescriptor := strings.Index(prepareSection, ">session/session.json")
	if runnerClose < 0 || clientOpen < 0 || runnerClose > clientOpen {
		t.Fatal("analysis broker does not revoke the runner exchange rule before opening local-client ingress")
	}
	if liveDescriptor < clientOpen || !strings.Contains(prepareSection, "rm -f session/encrypted-token.bin session/pinned-ca.pem") {
		t.Fatal("analysis broker can publish a live or token-bearing handoff before local-client ingress succeeds")
	}
	if !strings.Contains(prepareSection, "id: handoff\n        if: steps.ingress.outputs.opened == 'true'\n        continue-on-error: true") {
		t.Fatal("analysis broker cannot materialize an insufficient-evidence handoff after exchange failure")
	}
	if !strings.Contains(prepareSection, "id: ingress\n        if: inputs.operation == 'prepare' && steps.client.outputs.valid == 'true'\n        continue-on-error: true") {
		t.Fatal("analysis broker cannot materialize an insufficient-evidence session after bounded ingress failure")
	}
	if !strings.Contains(prepareSection, `echo "attempted=true" >>"$GITHUB_OUTPUT"`) ||
		!strings.Contains(prepareSection, "steps.ingress.outputs.attempted == 'true' && steps.handoff.outputs.client_window != 'true'") {
		t.Fatal("analysis broker cannot revoke a possibly-created exchange rule after a timed-out ingress command")
	}
	for _, forbidden := range []string{"OPENAI_API_KEY", "openai/codex-action", "environment: cloud-sim-remediation", "contents: write", "WK_ANALYSIS_MCP_TOKEN="} {
		if strings.Contains(analysis, forbidden) {
			t.Fatalf("analysis broker contains forbidden credential or repository-write capability %q", forbidden)
		}
	}
	for _, required := range []string{"id-token: write", "environment: cloud-sim-analysis", `close-analysis "$RUN_ID"`} {
		if !strings.Contains(closeSection, required) {
			t.Fatalf("analysis close job missing %q", required)
		}
	}
	if !strings.Contains(closeSection, `[[ "$RUN_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]]`) {
		t.Fatal("analysis close job does not validate the Run Identity")
	}
	for _, required := range []string{"group: cloud-simulation-${{ github.repository }}", "actions: read"} {
		if !strings.Contains(analysis, required) {
			t.Fatalf("analysis session workflow missing guardrail %q", required)
		}
	}
	if !strings.Contains(cleanup, `cron: "*/15 * * * *"`) || !strings.Contains(cleanup, "ALIBABA_CLOUD_SIM_PROVISIONER_ROLE_ARN") || !strings.Contains(cleanup, "environment: cloud-sim-cleanup") {
		t.Fatal("cleanup workflow lacks periodic provisioner-backed reconciliation")
	}
	for _, required := range []string{
		"actions: write", `include_claim_keys:["repo","context","job_workflow_ref"]`, ".use_default == false",
		"verify-alibaba:", "environment: cloud-sim-analysis", "id-token: write",
		"if: vars.ALIBABA_CLOUD_SIM_ANALYZER_ROLE_ARN != '' && vars.ALIBABA_CLOUD_SIM_OIDC_PROVIDER_ARN != '' && vars.ALIBABA_CLOUD_SIM_OIDC_AUDIENCE != ''",
		"run-name: Cloud Simulation OIDC Verification", "verification_id:",
		"aliyun/configure-aliyun-credentials-action@1e5248c8d5d93a8781ac344a68e19a43341e79e6",
		"ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABA_CLOUD_SECURITY_TOKEN",
	} {
		if !strings.Contains(oidcSubject, required) {
			t.Fatalf("OIDC subject workflow missing %q", required)
		}
	}
}

func TestCloudSimulationAnalysisLiveClientValidationRejectsMissingMaterial(t *testing.T) {
	root := repositoryRoot(t)
	script := workflowStepRun(t, root, "cloud-sim-analyze.yml", "Validate live client material")
	outputPath := filepath.Join(t.TempDir(), "github-output")
	command := exec.Command("/bin/bash", "-c", script)
	command.Env = append(os.Environ(),
		"CLIENT_IPV4=",
		"CLIENT_PUBLIC_KEY=",
		"GITHUB_OUTPUT="+outputPath,
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("live client validation accepted missing material:\n%s", output)
	}
	if output, err := os.ReadFile(outputPath); err == nil && strings.Contains(string(output), "valid=true") {
		t.Fatalf("live client validation published success for missing material: %s", output)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestCloudSimulationWorkflowPublishesFinalizationSchedule(t *testing.T) {
	root := repositoryRoot(t)
	provision := readWorkflowText(t, root, "cloud-sim-provision.yml")
	cleanup := readWorkflowText(t, root, "cloud-sim-cleanup.yml")

	for _, required := range []string{
		"options: [30m, 2h, 24h, 48h, 168h]",
		"options: [2h, 6h]",
		"duration_seconds=\"$(case '${{ inputs.duration }}' in 30m) echo 1800;;",
		"168h) echo 604800",
		"grace_seconds=\"$(case '${{ inputs.analysis_grace }}' in 2h) echo 7200;;",
		"provisioning_budget_seconds=3000",
		"warmup_budget_seconds=1200",
		"expires_epoch=\"$(( $(date -u +%s) + provisioning_budget_seconds + warmup_budget_seconds + duration_seconds + grace_seconds ))\"",
		`schema:"wukongim/cloud-simulation-finalize/v1"`,
		`--arg active_until "$active_until"`,
		`--arg analysis_ready_at "$analysis_ready_at"`,
		`analysis_ready_delay_seconds="$(case "$EFFECTIVE_SCENARIO" in cloud-small) echo 180;; cloud-medium) echo 300;; cloud-large) echo 600;; esac)"`,
		`analysis_ready_at="$(date -u -d "@$((active_until_epoch + analysis_ready_delay_seconds))" +%Y-%m-%dT%H:%M:%SZ)"`,
		`--arg expires_at '${{ steps.prepare.outputs.expires_at }}'`,
		"name: cloud-sim-finalize-${{ needs.build.outputs.run_id }}",
		"path: finalize.json",
		"running)\n                ;;",
		"./scripts/cloud-sim/finalize.sh $RUN_ID",
	} {
		if !strings.Contains(provision, required) {
			t.Fatalf("provision workflow missing finalization contract %q", required)
		}
	}
	if got := strings.Count(provision, "default: 2h"); got != 1 {
		t.Fatalf("2-hour workflow defaults = %d, want analysis grace only", got)
	}
	if got := strings.Count(provision, "default: 48h"); got != 1 {
		t.Fatalf("48-hour workflow defaults = %d, want active duration only", got)
	}

	for _, required := range []string{
		"run-name: Cloud Simulation Cleanup",
		"request_id:",
		`REQUEST_ID: ${{ inputs.request_id || '' }}`,
		`[[ "$REQUEST_ID" =~ ^finalize-[0-9a-f]{16}$ ]]`,
	} {
		if !strings.Contains(cleanup, required) {
			t.Fatalf("cleanup workflow missing finalization correlation %q", required)
		}
	}
}

func TestCloudSimulationWorkflowMapsReviewedScaleAliasesAndConfirmsWeekCost(t *testing.T) {
	provision := readWorkflowText(t, repositoryRoot(t), "cloud-sim-provision.yml")
	for _, required := range []string{
		"options: [cloud-small, cloud-medium, cloud-large, cloud-standard, cloud-stress]",
		"options: [small, medium, large, standard, stress]",
		"confirm_large_168h_cost:",
		`effective_scenario=cloud-medium`,
		`effective_scenario=cloud-large`,
		`medium|standard) echo standard`,
		`large|stress) echo stress`,
		`::warning::cloud-standard is deprecated; using cloud-medium`,
		`::warning::cloud-stress is deprecated; using cloud-large`,
		`Large 168h simulation requires confirm_large_168h_cost=true`,
	} {
		if !strings.Contains(provision, required) {
			t.Fatalf("provision workflow missing reviewed scale contract %q", required)
		}
	}
}

func TestCloudSimulationWorkflowRequiresEmpiricalStorageCalibrationForStandardRuns(t *testing.T) {
	provision := readWorkflowText(t, repositoryRoot(t), "cloud-sim-provision.yml")
	for _, required := range []string{
		"storage_calibration_run_id:",
		"storage_bytes_per_message:",
		"48h|168h)",
		"Standard stability runs require a completed 30m storage calibration",
		"calibrated_data_disk_gib",
		`.data_disk_size_gib = $size`,
		`* 1.5 / 0.7 / 1073741824`,
	} {
		if !strings.Contains(provision, required) {
			t.Fatalf("provision workflow missing storage calibration guard %q", required)
		}
	}
}

func TestCloudSimulationWorkflowGatesOptionalPublicObservation(t *testing.T) {
	provision := readWorkflowText(t, repositoryRoot(t), "cloud-sim-provision.yml")
	for _, required := range []string{
		"public_observation:", "default: true", "wkcloudview:./cmd/wkcloudview",
		`if [[ "$PUBLIC_OBSERVATION" == "true" ]]`, `public_observation:$public_observation`,
		`{username:"admin",password:"a1234567",permissions:[`, `{resource:"*",actions:["*"]}`,
		"--public-observation", `open-public-view "$RUN_ID"`, "--source 0.0.0.0/0",
		`wkcloudview doctor --base-url "http://${SIM_PUBLIC}:19443"`, `--gate-token "$CLOUD_VIEW_GATE_TOKEN"`, "--expected-targets 7",
		"WK_CLOUD_VIEW_GATE_PROBE_TOKEN", "WK_CLOUD_RUN_ID",
		`close-public-view "$RUN_ID"`, "Manager: http://${SIM_PUBLIC}:19443/",
		"Demo: http://${SIM_PUBLIC}:19443/demo/", "Prometheus: http://${SIM_PUBLIC}:19443/prometheus/",
		"WebSocket: ws://${SIM_PUBLIC}:19443/", "admin / a1234567",
	} {
		if !strings.Contains(provision, required) {
			t.Fatalf("provision workflow missing public observation contract %q", required)
		}
	}
	bootstrap := string(mustReadFile(t, filepath.Join(repositoryRoot(t), "scripts", "cloud-sim", "collect-bootstrap-gate.sh")))
	for _, required := range []string{"WK_CLOUD_PUBLIC_OBSERVATION", "wkcloudview", "cloud_view_self_check", "wukongim-cgroup-metrics"} {
		if !strings.Contains(bootstrap, required) {
			t.Fatalf("Bootstrap Gate missing optional Cloud View contract %q", required)
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func collectYAMLScalars(node *yaml.Node, key string) []string {
	values := make([]string, 0)
	if node.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(node.Content); index += 2 {
			if node.Content[index].Value == key && node.Content[index+1].Kind == yaml.ScalarNode {
				values = append(values, node.Content[index+1].Value)
			}
			values = append(values, collectYAMLScalars(node.Content[index+1], key)...)
		}
		return values
	}
	for _, child := range node.Content {
		values = append(values, collectYAMLScalars(child, key)...)
	}
	return values
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

func readWorkflowText(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func between(t *testing.T, value, start, end string) string {
	t.Helper()
	startIndex := strings.Index(value, start)
	endIndex := strings.Index(value, end)
	if startIndex < 0 || endIndex <= startIndex {
		t.Fatalf("cannot find workflow section %q .. %q", start, end)
	}
	return value[startIndex:endIndex]
}

func assertWorkflowText(t *testing.T, value string, required ...string) {
	t.Helper()
	for _, item := range required {
		if !strings.Contains(value, item) {
			t.Fatalf("workflow missing %q", item)
		}
	}
}

func workflowShellIfBlock(t *testing.T, workflow, guard string) string {
	t.Helper()
	start := strings.Index(workflow, guard)
	if start < 0 {
		t.Fatalf("workflow missing shell guard %q", guard)
	}
	tail := workflow[start:]
	end := strings.Index(tail, "\n            fi")
	if end < 0 {
		t.Fatalf("workflow shell guard %q has no closing fi", guard)
	}
	return tail[:end]
}

func assertWorkflowFailureIsFailClosed(t *testing.T, workflow, errorMarker string) {
	t.Helper()
	found := false
	for offset := 0; offset < len(workflow); {
		relativeStart := strings.Index(workflow[offset:], errorMarker)
		if relativeStart < 0 {
			break
		}
		found = true
		start := offset + relativeStart
		tail := workflow[start:]
		end := strings.Index(tail, "\n            fi")
		if end < 0 {
			t.Fatalf("workflow failure %q has no closing fi", errorMarker)
		}
		block := tail[:end]
		if !strings.Contains(block, "\n              patrol_failed=1") ||
			!strings.Contains(block, "\n              continue") {
			t.Fatalf("workflow failure %q does not fail closed in its branch", errorMarker)
		}
		offset = start + len(errorMarker)
	}
	if !found {
		t.Fatalf("workflow missing failure marker %q", errorMarker)
	}
}

func workflowStepRun(t *testing.T, root, workflowName, stepName string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", workflowName))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if step.Name == stepName {
				if step.Run == "" {
					t.Fatalf("workflow step %q has no shell body", stepName)
				}
				return step.Run
			}
		}
	}
	t.Fatalf("workflow step %q not found", stepName)
	return ""
}

func workflowIntValue(t *testing.T, workflow, pattern string) int {
	t.Helper()
	match := regexp.MustCompile(pattern).FindStringSubmatch(workflow)
	if len(match) != 2 {
		t.Fatalf("workflow integer pattern %q not found", pattern)
	}
	value, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("parse workflow integer %q: %v", match[1], err)
	}
	return value
}

func writeWorkflowExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func monitorProviderArtifactPages(count int) string {
	var result strings.Builder
	result.WriteString(`[{"artifacts":[`)
	for index := 1; index <= count; index++ {
		if index > 1 {
			result.WriteByte(',')
		}
		fmt.Fprintf(&result,
			`{"id":%d,"name":"cloud-sim-provider-config--gh-%d-1--cn-hangzhou--%s","expired":false,"created_at":"2026-07-23T00:00:00Z"}`,
			index, index, strings.Repeat("a", 64))
	}
	result.WriteString(`]}]`)
	return result.String()
}

func monitorProviderArtifactBindingPages(count int) string {
	var result strings.Builder
	result.WriteString(`[{"artifacts":[`)
	for index := 1; index <= count; index++ {
		if index > 1 {
			result.WriteByte(',')
		}
		fmt.Fprintf(&result,
			`{"id":%d,"name":"cloud-sim-provider-config--gh-%d-1--cn-region-%d--%064x","expired":false,"created_at":"2026-07-23T00:00:00Z"}`,
			index, index, index, index)
	}
	result.WriteString(`]}]`)
	return result.String()
}

func monitorInventorySnapshot(runIDs []string) string {
	var result strings.Builder
	fmt.Fprintf(&result, `{"authority":{"provider":"alibaba","region":"cn-hangzhou","account_id_hash":"sha256:%s"},"runs":[`, strings.Repeat("a", 64))
	for index, runID := range runIDs {
		if index > 0 {
			result.WriteByte(',')
		}
		fmt.Fprintf(&result, `{"id":%q,"provider":"alibaba","region":"cn-hangzhou","account_id_hash":"sha256:%s","repository":"example/repository","state":"running"}`,
			runID, strings.Repeat("a", 64))
	}
	result.WriteString(`]}`)
	return result.String()
}

func writeMonitorDiscoveryFakes(t *testing.T, workDir, binDir string) {
	t.Helper()
	writeWorkflowExecutable(t, filepath.Join(binDir, "gh"), `#!/bin/bash
set -euo pipefail
case "$*" in
  *"/zip"*)
    printf 'fake zip\n'
    ;;
  *"/actions/artifacts"*)
    [[ "$*" == *"--paginate --slurp"* && "$*" == *"per_page=100"* ]]
    printf '%s\n' "$MONITOR_PROVIDER_ARTIFACT_PAGES"
    ;;
  *)
    exit 2
    ;;
esac
`)
	writeWorkflowExecutable(t, filepath.Join(binDir, "unzip"), `#!/bin/bash
set -euo pipefail
destination=""
while (($#)); do
  if [[ "$1" == "-d" ]]; then destination="$2"; shift 2; continue; fi
  shift
done
[[ -n "$destination" ]]
mkdir -p "$destination"
artifact_id="${destination##*-}"
printf '{"region":"cn-region-%s","account_id_hash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}\n' "$artifact_id" >"$destination/provider.json"
`)
	writeWorkflowExecutable(t, filepath.Join(binDir, "timeout"), `#!/bin/bash
set -euo pipefail
shift
exec "$@"
`)
	writeWorkflowExecutable(t, filepath.Join(workDir, "scripts", "cloud-sim", "select-provider-config-artifacts.sh"), `#!/bin/bash
set -euo pipefail
awk -F '\t' '
  {
    count = split($2, parts, "--")
    binding = parts[count - 1] "--" parts[count]
    if (!seen[binding]++) print $1
  }
'
`)
	writeWorkflowExecutable(t, filepath.Join(workDir, "scripts", "cloud-sim", "resolve-exact-provider-config.sh"), `#!/bin/bash
exit 99
`)
	writeWorkflowExecutable(t, filepath.Join(workDir, "wkcloudsim"), `#!/bin/bash
set -euo pipefail
[[ "$*" == *" inventory" ]]
printf '%s\n' "$*" >>"$MONITOR_INVENTORY_CALL_LOG"
if [[ -n "${MONITOR_INVENTORY_EXIT:-}" ]]; then exit "$MONITOR_INVENTORY_EXIT"; fi
printf '%s\n' "$MONITOR_INVENTORY_JSON"
`)
}

func writeMonitorWorkflowFakes(t *testing.T, workDir, binDir string) {
	t.Helper()
	writeWorkflowExecutable(t, filepath.Join(binDir, "gh"), `#!/bin/bash
set -euo pipefail
case "$*" in
  *"/zip"*)
    printf 'fake zip\n'
    ;;
  *"/actions/artifacts"*)
    [[ "$*" == *"--paginate --slurp"* && "$*" == *"per_page=100"* ]]
    if [[ "$MONITOR_SCENARIO" == "duplicate" ]]; then
      printf '%s\n' '[{"artifacts":[{"id":1,"expired":false}]},{"artifacts":[{"id":2,"expired":false}]}]'
    else
      printf '%s\n' '[{"artifacts":[{"id":0,"expired":true}]},{"artifacts":[{"id":1,"expired":false}]}]'
    fi
    ;;
  *)
    exit 2
    ;;
esac
`)
	writeWorkflowExecutable(t, filepath.Join(binDir, "unzip"), `#!/bin/bash
set -euo pipefail
destination=""
while (($#)); do
  if [[ "$1" == "-d" ]]; then destination="$2"; shift 2; continue; fi
  shift
done
[[ -n "$destination" ]]
mkdir -p "$destination"
printf '%s\n' "$MONITOR_LOCATOR_JSON" >"$destination/run-locator.json"
`)
	writeWorkflowExecutable(t, filepath.Join(binDir, "timeout"), `#!/bin/bash
set -euo pipefail
shift
exec "$@"
`)
	writeWorkflowExecutable(t, filepath.Join(workDir, "scripts", "cloud-sim", "resolve-exact-provider-config.sh"), `#!/bin/bash
set -euo pipefail
if [[ "${EXPECTED_REGION:-}" != "cn-hangzhou" ||
      "${EXPECTED_ACCOUNT_ID_HASH:-}" != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ]]; then
  exit 17
fi
if [[ "$MONITOR_SCENARIO" == "provider-failure" ]]; then exit 18; fi
printf '{}\n' >"$2"
`)
	writeWorkflowExecutable(t, filepath.Join(workDir, "wkcloudsim"), `#!/bin/bash
set -euo pipefail
case "$MONITOR_SCENARIO" in
  released|provider-failure)
    printf '%s\n' '{"state":"released"}'
    ;;
  stopped)
    printf '%s\n' '{"state":"live","run":{"state":"stopped","resources":[]}}'
    ;;
  running|targets-failure|targets-invalid|sustained-failure|metric-missing)
    printf '%s\n' '{"state":"live","run":{"state":"running","resources":[{"kind":"public-address","role":"sim","public_address":"127.0.0.1"}]}}'
    ;;
  sim-missing)
    printf '%s\n' '{"state":"live","run":{"state":"running","resources":[]}}'
    ;;
  preflight-failure)
    exit 19
    ;;
  preflight-invalid)
    printf '%s\n' '{"state":"unexpected"}'
    ;;
  *)
    exit 20
    ;;
esac
`)
	writeWorkflowExecutable(t, filepath.Join(binDir, "curl"), `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >>"$MONITOR_PUBLIC_CALL_LOG"
if [[ "$MONITOR_SCENARIO" == "targets-failure" && "$*" == *"/prometheus/api/v1/targets"* ]]; then exit 22; fi
if [[ "$MONITOR_SCENARIO" == "sustained-failure" && "$*" == *"min_over_time(up[30m])"* ]]; then exit 23; fi
case "$*" in
  *"/cloud-view/status"*)
    printf '%s\n' '{"run_id":"gh-test-1","persistence_healthy":true}'
    ;;
  *"/prometheus/api/v1/targets"*)
    if [[ "$MONITOR_SCENARIO" == "targets-invalid" ]]; then
      printf '%s\n' '{}'
    else
      printf '%s\n' '{"data":{"activeTargets":[{"health":"up"},{"health":"up"},{"health":"up"},{"health":"up"},{"health":"up"},{"health":"up"},{"health":"up"}]}}'
    fi
    ;;
  *"min_over_time(up[30m])"*)
    printf '%s\n' '{"data":{"result":[{"value":[1,"1"]}]}}'
    ;;
  *"/prometheus/api/v1/query"*)
    if [[ "$MONITOR_SCENARIO" == "metric-missing" ]]; then
      printf '%s\n' '{"data":{"result":[]}}'
    else
      printf '%s\n' '{"data":{"result":[{"value":[1,"0.1"]}]}}'
    fi
    ;;
  *)
    exit 21
    ;;
esac
`)
}
