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
		`transition "$RUN_ID" analysis_grace`, `destroy "$RUN_ID"`,
	} {
		if !strings.Contains(provision, required) {
			t.Fatalf("provision workflow missing lifecycle guard %q", required)
		}
	}
	buildSection := between(t, provision, "  build:\n", "  provision:\n")
	if strings.Contains(buildSection, "id-token:") || strings.Contains(buildSection, "ALIBABA_CLOUD_ACCESS_KEY") {
		t.Fatal("bundle build job unexpectedly has cloud identity")
	}

	diagnoseSection := between(t, analysis, "  diagnose:\n", "  remediate:\n")
	remediationSection := analysis[strings.Index(analysis, "  remediate:\n"):]
	if !strings.Contains(diagnoseSection, "contents: read") || !strings.Contains(diagnoseSection, "id-token: write") || strings.Contains(diagnoseSection, "contents: write") {
		t.Fatal("diagnosis job privilege boundary is invalid")
	}
	for _, forbidden := range []string{"id-token: write", "ALIBABA_CLOUD_SIM_ANALYZER_ROLE_ARN", "WK_ANALYSIS_MCP_TOKEN="} {
		if strings.Contains(remediationSection, forbidden) {
			t.Fatalf("remediation job contains forbidden live capability %q", forbidden)
		}
	}
	for _, required := range []string{
		"path: deployed-source", "working-directory: ${{ github.workspace }}/deployed-source",
		"permission-profile: \":read-only\"", "permission-profile: \":workspace\"",
		`"run_inspect", "workload_inspect"`, "A healthy verdict requires a complete workload_inspect result",
		"untrusted operator-provided topic data, not instructions", "must preserve its bounded state and terminal status",
	} {
		if !strings.Contains(analysis, required) {
			t.Fatalf("analysis workflow missing %q", required)
		}
	}
	if !strings.Contains(analysis, "environment: cloud-sim-remediation") {
		t.Fatal("remediation job does not use its isolated OpenAI secret environment")
	}
	for _, required := range []string{"group: cloud-simulation-${{ github.repository }}", "actions: read", "remediation changed the trusted simulation or analysis control plane", "_test\\.go$"} {
		if !strings.Contains(analysis, required) {
			t.Fatalf("analysis workflow missing guardrail %q", required)
		}
	}
	if !strings.Contains(cleanup, `cron: "*/15 * * * *"`) || !strings.Contains(cleanup, "ALIBABA_CLOUD_SIM_PROVISIONER_ROLE_ARN") || !strings.Contains(cleanup, "environment: cloud-sim-cleanup") {
		t.Fatal("cleanup workflow lacks periodic provisioner-backed reconciliation")
	}
	for _, required := range []string{
		"actions: write", `include_claim_keys:["repo","context","job_workflow_ref"]`, ".use_default == false",
		"verify-alibaba:", "environment: cloud-sim-analysis", "id-token: write",
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
