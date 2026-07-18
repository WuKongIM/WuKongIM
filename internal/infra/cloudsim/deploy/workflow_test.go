package deploy

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
		`status "$run_id"`,
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
	if strings.Contains(monitor, " create ") || strings.Contains(monitor, "wkbench run") {
		t.Fatal("monitor workflow can start a simulation")
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
		"run-name: Cloud Simulation Analysis", "operation:", "request_id:", "client_ipv4:", "client_public_key:",
		`select(test("^[0-9a-f]{40}$"))`, `select(test("^sha256:[0-9a-f]{64}$"))`,
		`open-analysis "$RUN_ID"`, `--source "${CLIENT_IPV4}/32"`, "openssl pkeyutl -encrypt",
		"rsa_padding_mode:oaep", "rsa_oaep_md:sha256", "encrypted-token.bin",
		"cloud-sim-analysis-session-${{ inputs.request_id }}", "retention-days: 1",
	} {
		if !strings.Contains(analysis, required) {
			t.Fatalf("analysis session workflow missing %q", required)
		}
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
