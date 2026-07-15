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
	for _, name := range []string{"cloud-sim-oidc-subject.yml", "cloud-sim-provision.yml", "cloud-sim-analyze.yml", "cloud-sim-cleanup.yml"} {
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

func TestCloudSimulationWorkflowPrivilegeSeparation(t *testing.T) {
	root := repositoryRoot(t)
	provision := readWorkflowText(t, root, "cloud-sim-provision.yml")
	analysis := readWorkflowText(t, root, "cloud-sim-analyze.yml")
	cleanup := readWorkflowText(t, root, "cloud-sim-cleanup.yml")
	oidcSubject := readWorkflowText(t, root, "cloud-sim-oidc-subject.yml")

	assertWorkflowText(t, provision, "build:\n", "provision:\n", "id-token: write", "environment: cloud-sim-provision")
	for _, required := range []string{
		`transition "$RUN_ID" ready`, `transition "$RUN_ID" running --active-until`,
		`transition "$RUN_ID" analysis_grace`, `destroy "$RUN_ID"`, `./scripts/cloud-sim/analyze.sh $RUN_ID`,
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
