package scripts_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type actionPin struct {
	sha     string
	release string
}

type ciWorkflow struct {
	Name        string               `yaml:"name"`
	RunName     string               `yaml:"run-name"`
	On          map[string]yaml.Node `yaml:"on"`
	Permissions map[string]string    `yaml:"permissions"`
	Concurrency ciConcurrency        `yaml:"concurrency"`
	Jobs        map[string]ciJob     `yaml:"jobs"`
}

type ciConcurrency struct {
	Group            string `yaml:"group"`
	Queue            string `yaml:"queue"`
	CancelInProgress *bool  `yaml:"cancel-in-progress"`
}

type ciJob struct {
	Name           string            `yaml:"name"`
	If             string            `yaml:"if"`
	RunsOn         string            `yaml:"runs-on"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	Environment    string            `yaml:"environment"`
	Needs          []string          `yaml:"needs"`
	Permissions    map[string]string `yaml:"permissions"`
	Outputs        map[string]string `yaml:"outputs"`
	Concurrency    *ciConcurrency    `yaml:"concurrency"`
	Env            map[string]string `yaml:"env"`
	Defaults       *ciDefaults       `yaml:"defaults"`
	Strategy       *ciStrategy       `yaml:"strategy"`
	Steps          []ciStep          `yaml:"steps"`
	Uses           string            `yaml:"uses"`
}

type ciDefaults struct {
	Run ciRunDefaults `yaml:"run"`
}

type ciRunDefaults struct {
	WorkingDirectory string `yaml:"working-directory"`
}

type ciStrategy struct {
	FailFast *bool    `yaml:"fail-fast"`
	Matrix   ciMatrix `yaml:"matrix"`
}

type ciMatrix struct {
	Include []ciMatrixEntry `yaml:"include"`
}

type ciMatrixEntry struct {
	Name     string `yaml:"name"`
	Packages string `yaml:"packages"`
}

type ciStep struct {
	ID    string            `yaml:"id"`
	Name  string            `yaml:"name"`
	Uses  string            `yaml:"uses"`
	Run   string            `yaml:"run"`
	Shell string            `yaml:"shell"`
	If    string            `yaml:"if"`
	Env   map[string]string `yaml:"env"`
	With  map[string]any    `yaml:"with"`
}

var approvedActionPins = map[string]actionPin{
	"actions/checkout": {
		sha:     "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
		release: "v7.0.0",
	},
	"actions/setup-go": {
		sha:     "924ae3a1cded613372ab5595356fb5720e22ba16",
		release: "v6.5.0",
	},
	"actions/setup-node": {
		sha:     "249970729cb0ef3589644e2896645e5dc5ba9c38",
		release: "v6.5.0",
	},
	"oven-sh/setup-bun": {
		sha:     "0c5077e51419868618aeaa5fe8019c62421857d6",
		release: "v2.2.0",
	},
	"actions/upload-artifact": {
		sha:     "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
		release: "v7.0.1",
	},
	"actions/download-artifact": {
		sha:     "018cc2cf5baa6db3ef3c5f8a56943fffe632ef53",
		release: "v6.0.0",
	},
	"openai/codex-action": {
		sha:     "52fe01ec70a42f454c9d2ebd47598f9fd6893d56",
		release: "v1.11",
	},
}

func checkoutStep() ciStep {
	return ciStep{
		Uses: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
		With: map[string]any{"persist-credentials": false},
	}
}

func setupGoStep() ciStep {
	return ciStep{
		Uses: "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
		With: map[string]any{
			"go-version-file":       "go.mod",
			"cache":                 true,
			"cache-dependency-path": "go.sum",
		},
	}
}

func verifyGoToolchainStep() ciStep {
	return ciStep{Name: "Verify Go toolchain", Run: `test "$(go env GOVERSION)" = "go1.25.11"`}
}

var catalogedWorkflowNames = map[string]string{
	"agent-pr-merge-gate.yml":         "Safety Automation - Agent PR Merge Gate",
	"agent-pr-validation.yml":         "Agent Tool - Validate PR",
	"agent-pr-validation-control.yml": "Safety Automation - Agent PR Validation Control",
	"cloud-sim-analyze.yml":           "Agent Tool - Analyze Cloud Simulation",
	"cloud-sim-cleanup.yml":           "Safety Automation - Reconcile Cloud Simulation Resources",
	"cloud-sim-monitor.yml":           "Safety Automation - Patrol Cloud Simulation Runs",
	"cloud-sim-oidc-subject.yml":      "Agent Tool - Configure Cloud Simulation OIDC Subject",
	"cloud-sim-provision.yml":         "Agent Tool - Provision Cloud Simulation",
	"issue-agent-engineer.yml":        "Agent Tool - Issue Engineer",
	"issue-agent.yml":                 "Safety Automation - GitHub Issue Agent",
}

var autonomousSafetyWorkflows = map[string]struct{}{
	"agent-pr-merge-gate.yml":         {},
	"agent-pr-validation-control.yml": {},
	"cloud-sim-cleanup.yml":           {},
	"cloud-sim-monitor.yml":           {},
	"issue-agent.yml":                 {},
}

var legacyAutomaticTestWorkflows = []string{"ci.yml", "nightly.yml"}

func TestLegacyAutomaticTestWorkflowsAreAbsent(t *testing.T) {
	root := repoRoot(t)
	for _, name := range legacyAutomaticTestWorkflows {
		path := filepath.Join(root, ".github", "workflows", name)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s still exists; tests must be selected through the Agent validation protocol", name)
		} else if !os.IsNotExist(err) {
			t.Errorf("stat %s: %v", name, err)
		}
	}
}

func TestAgentPRValidationWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "agent-pr-validation.yml")
	if err := validateAgentPRValidationWorkflow(raw); err != nil {
		t.Fatal(err)
	}
	t.Run("repository integration files use build tags", assertRepositoryIntegrationTestFilesUseBuildTag)
}

func TestAgentPRValidationControlWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "agent-pr-validation-control.yml")
	if err := validateAgentPRValidationControlWorkflow(raw); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPRValidationMergeGateWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "agent-pr-merge-gate.yml")
	if err := validateAgentPRValidationMergeGateWorkflow(raw); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPRValidationMergeGateBootstrapTreeParsingFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		treeJSON string
		wantGate string
		wantErr  bool
	}{
		{
			name:     "gate absent",
			treeJSON: `{"truncated":false,"tree":[]}`,
			wantGate: "false",
		},
		{
			name: "gate present",
			treeJSON: `{
  "truncated": false,
  "tree": [{"path": ".github/workflows/agent-pr-merge-gate.yml"}]
}`,
			wantGate: "true",
		},
		{
			name:     "tree null",
			treeJSON: `{"truncated":false,"tree":null}`,
			wantErr:  true,
		},
		{
			name:     "tree missing",
			treeJSON: `{"truncated":false}`,
			wantErr:  true,
		},
		{
			name:     "tree truncated",
			treeJSON: `{"truncated":true,"tree":[]}`,
			wantErr:  true,
		},
		{
			name:     "tree entry missing path",
			treeJSON: `{"truncated":false,"tree":[{}]}`,
			wantErr:  true,
		},
		{
			name:     "tree entry null path",
			treeJSON: `{"truncated":false,"tree":[{"path":null}]}`,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			treePath := filepath.Join(t.TempDir(), "base-tree.json")
			if err := os.WriteFile(treePath, []byte(tt.treeJSON), 0o600); err != nil {
				t.Fatalf("write tree fixture: %v", err)
			}
			schema := exec.Command(
				"jq",
				"-e",
				`.truncated == false and
(.tree | type == "array") and
all(.tree[];
  type == "object" and
  (.path | type == "string" and length > 0))`,
				treePath,
			)
			if output, err := schema.CombinedOutput(); err != nil {
				if tt.wantErr {
					return
				}
				t.Fatalf("validate tree schema: %v\n%s", err, output)
			}
			if tt.wantErr {
				t.Fatal("malformed bootstrap tree unexpectedly passed schema validation")
			}
			query := exec.Command(
				"jq",
				"-r",
				`any(.tree[]; .path == ".github/workflows/agent-pr-merge-gate.yml")`,
				treePath,
			)
			output, err := query.CombinedOutput()
			if err != nil {
				t.Fatalf("query gate path: %v\n%s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != tt.wantGate {
				t.Fatalf("base_has_gate = %q, want %q", got, tt.wantGate)
			}
		})
	}
}

func TestAgentPRValidationControlWorkflowRejectsReadOnlyActor(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation-control.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"            admin|maintain|write) ;;",
		"            admin|maintain|write|read) ;;",
	)
	if err := validateAgentPRValidationControlWorkflow([]byte(mutated)); err == nil {
		t.Fatal("control validator accepted a read-only actor")
	}
}

func TestAgentPRValidationControlWorkflowRejectsMissingRequestStatus(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation-control.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		`-f "context=Agent Validation Request / PR #${PR_NUMBER} / Gate #${gate_run_id}"`,
		`-f "context=Unbound Agent Validation Request / PR #${PR_NUMBER} / Gate #${gate_run_id}"`,
	)
	if err := validateAgentPRValidationControlWorkflow([]byte(mutated)); err == nil {
		t.Fatal("control validator accepted dispatch without a commit-bound request status")
	}
}

func TestAgentPRValidationControlWorkflowRejectsNonSummaryInvalidation(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation-control.yml"))
	tests := []struct {
		name        string
		oldFragment string
		newFragment string
	}{
		{
			name: "extra action",
			oldFragment: `      - name: Write invalidation summary
        shell: bash`,
			newFragment: `      - name: Unexpected action
        uses: attacker/example@0123456789abcdef0123456789abcdef01234567
      - name: Write invalidation summary
        shell: bash`,
		},
		{
			name: "alternate status write",
			oldFragment: `      - name: Write invalidation summary
        shell: bash
        run: |
          {
`,
			newFragment: `      - name: Write invalidation summary
        shell: bash
        run: |
          printf '%s' '{"state":"failure","context":"Agent Validation Request / PR #999"}' | gh api --method POST "repos/${GITHUB_REPOSITORY}/commits/${{ github.event.pull_request.head.sha }}/statuses" --input -
          {
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := replaceWorkflowFirst(t, raw, tt.oldFragment, tt.newFragment)
			if err := validateAgentPRValidationControlWorkflow([]byte(mutated)); err == nil {
				t.Fatal("control validator accepted non-summary invalidation behavior")
			}
		})
	}
}

func TestAgentWorkflowCatalogContract(t *testing.T) {
	root := repoRoot(t)
	catalog := readFile(t, filepath.Join(root, ".github", "workflows", "README.md"))
	agents := readFile(t, filepath.Join(root, "AGENTS.md"))
	codeowners := readFile(t, filepath.Join(root, ".github", "CODEOWNERS"))
	cloudRunbook := readFile(t, filepath.Join(root, "docs", "superpowers", "runbooks", "cloud-simulation.md"))

	for file, name := range catalogedWorkflowNames {
		raw := readFile(t, filepath.Join(root, ".github", "workflows", file))
		if !strings.HasPrefix(raw, "name: "+name+"\n") {
			t.Errorf("%s does not use cataloged name %q", file, name)
		}
		if !strings.Contains(catalog, "`"+file+"`") ||
			!strings.Contains(catalog, "`"+name+"`") {
			t.Errorf("workflow catalog does not map %s to %q", file, name)
		}
	}
	for _, removed := range legacyAutomaticTestWorkflows {
		if strings.Contains(catalog, "| `"+removed+"` |") {
			t.Errorf("workflow catalog still lists removed automatic test workflow %s", removed)
		}
	}
	for _, required := range []string{
		"agent-ci/docs-only",
		"agent-ci/go-fast",
		"agent-ci/web",
		"agent-ci/demo",
		"agent-ci/go-race",
		"agent-ci/go-integration",
		"agent-ci/go-e2e",
		"agent-ci/three-node-smoke",
		"agent-ci/run",
		"agent-validation-plan:v1",
		"retry_of_run_id",
		"Agent Validation Gate",
		"Agent Validation Evidence",
		"first_time_contributors",
	} {
		if !strings.Contains(catalog, required) {
			t.Errorf("workflow catalog is missing %q", required)
		}
	}
	normalizedCatalog := strings.Join(strings.Fields(catalog), " ")
	for _, required := range []string{
		"The Agent may perform and approve that review itself",
		"no named GitHub user or Code Owner approval",
		"CODEOWNERS remains review-routing metadata",
	} {
		if !strings.Contains(normalizedCatalog, required) {
			t.Errorf("workflow catalog is missing %q", required)
		}
	}
	if strings.Contains(catalog, "independent Code Owner review") {
		t.Error("workflow catalog still requires an independent Code Owner review")
	}
	for _, workflowPath := range []string{
		".github/workflows/cloud-sim-analyze.yml",
		".github/workflows/cloud-sim-cleanup.yml",
		".github/workflows/cloud-sim-oidc-subject.yml",
		".github/workflows/cloud-sim-provision.yml",
	} {
		if !strings.Contains(cloudRunbook, workflowPath) {
			t.Errorf("Cloud Simulation runbook is missing stable workflow path %q", workflowPath)
		}
	}
	for _, staleName := range []string{
		"Cloud Simulation - Configure OIDC Subject",
		"Cloud Simulation - Provision",
		"Cloud Simulation - Analysis Session",
		"Cloud Simulation - Cleanup",
	} {
		if strings.Contains(cloudRunbook, staleName) {
			t.Errorf("Cloud Simulation runbook still references stale display name %q", staleName)
		}
	}
	if !strings.Contains(agents, ".github/workflows/README.md") {
		t.Error("root AGENTS.md does not route Agents to the Workflow tool catalog")
	}
	for _, protected := range []string{
		"/.github/workflows/ @tangtaoit @No8blackball",
		"/.github/CODEOWNERS @tangtaoit @No8blackball",
		"/scripts/github_workflows_test.go @tangtaoit @No8blackball",
		"/scripts/agent-pr-validation-plan.sh @tangtaoit @No8blackball",
		"/scripts/agent_pr_validation_plan_test.go @tangtaoit @No8blackball",
	} {
		if !strings.Contains(codeowners, protected) {
			t.Errorf("CODEOWNERS is missing %q", protected)
		}
	}
}

func TestAgentWorkflowTriggerContract(t *testing.T) {
	root := repoRoot(t)
	var paths []string
	for _, extension := range []string{"*.yml", "*.yaml"} {
		matches, err := filepath.Glob(filepath.Join(root, ".github", "workflows", extension))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		t.Fatal("workflow inventory is empty")
	}
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		file := filepath.Base(path)
		seen[file] = struct{}{}
		raw := readFile(t, path)
		var workflow struct {
			Name string               `yaml:"name"`
			On   map[string]yaml.Node `yaml:"on"`
		}
		if err := yaml.Unmarshal([]byte(raw), &workflow); err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if err := validateAgentWorkflowTrigger(file, workflow.Name, workflow.On); err != nil {
			t.Error(err)
		}
	}
	for file := range catalogedWorkflowNames {
		if _, ok := seen[file]; !ok {
			t.Errorf("cataloged workflow %s is missing from the workflow inventory", file)
		}
	}
}

func TestAgentWorkflowTriggerContractRejectsAutomaticTestBypasses(t *testing.T) {
	automatic := map[string]yaml.Node{"pull_request": {}}
	tests := []struct {
		name  string
		file  string
		title string
	}{
		{
			name:  "uncataloged yml",
			file:  "ci.yml",
			title: "CI",
		},
		{
			name:  "uncataloged yaml extension",
			file:  "ci.yaml",
			title: "CI",
		},
		{
			name:  "Agent Tool with automatic trigger",
			file:  "agent-pr-validation.yml",
			title: "Agent Tool - Validate PR",
		},
		{
			name:  "unapproved Safety Automation",
			file:  "automatic-tests.yml",
			title: "Safety Automation - Automatic Tests",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateAgentWorkflowTrigger(tt.file, tt.title, automatic); err == nil {
				t.Fatal("trigger contract accepted an automatic test bypass")
			}
		})
	}
}

func validateAgentWorkflowTrigger(
	file string,
	name string,
	triggers map[string]yaml.Node,
) error {
	catalogedName, ok := catalogedWorkflowNames[file]
	if !ok {
		return fmt.Errorf("%s is not in the authoritative workflow catalog", file)
	}
	if name != catalogedName {
		return fmt.Errorf("%s name = %q, want cataloged name %q", file, name, catalogedName)
	}
	switch {
	case strings.HasPrefix(name, "Agent Tool - "):
		if len(triggers) != 1 {
			return fmt.Errorf("%s Agent Tool triggers = %v, want one on-demand trigger", file, triggers)
		}
		for trigger := range triggers {
			if trigger != "workflow_dispatch" &&
				trigger != "repository_dispatch" &&
				trigger != "workflow_call" {
				return fmt.Errorf("%s Agent Tool uses automatic trigger %q", file, trigger)
			}
		}
	case strings.HasPrefix(name, "Safety Automation - "):
		if _, ok := autonomousSafetyWorkflows[file]; !ok {
			return fmt.Errorf("%s is not an approved autonomous safety workflow", file)
		}
	default:
		return fmt.Errorf("%s is neither an Agent Tool nor an approved Safety Automation", file)
	}
	return nil
}

func TestAgentPRValidationWorkflowRejectsWritableTestJob(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: read",
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: write",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a writable PR test job")
	}
}

func TestAgentPRValidationWorkflowRejectsDefaultBranchTestCheckout(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"\n    steps:\n      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0\n        with:\n          ref: ${{ github.event.client_payload.merge_sha }}",
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"\n    steps:\n      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0\n        with:\n          ref: main",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a default-branch checkout for a PR test job")
	}
}

func TestAgentPRValidationWorkflowRejectsWritableDefaultBranchCache(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"\n    steps:\n      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0\n        with:\n          ref: ${{ github.event.client_payload.merge_sha }}\n          persist-credentials: false\n      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0\n        with:\n          go-version-file: go.mod\n          cache: false",
		"  go-quality:\n    name: Agent / Go quality\n    if: needs.plan.outputs.go_fast == 'true'\n    needs: [plan, status-pending]\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"\n    steps:\n      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0\n        with:\n          ref: ${{ github.event.client_payload.merge_sha }}\n          persist-credentials: false\n      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0\n        with:\n          go-version-file: go.mod\n          cache: true",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a writable default-branch Go cache")
	}
}

func TestAgentPRValidationWorkflowRejectsUnconditionalTestJob(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"  go-integration:\n    name: Agent / Go integration\n    if: needs.plan.outputs.go_integration == 'true'",
		"  go-integration:\n    name: Agent / Go integration\n    # if: needs.plan.outputs.go_integration == 'true'",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted an unconditional Agent test job")
	}
}

func TestAgentPRValidationWorkflowRejectsWritablePlanJob(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"      statuses: read",
		"      statuses: write",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a plan job that can write statuses")
	}
}

func TestAgentPRValidationWorkflowRejectsDeploymentEnvironment(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"    timeout-minutes: 10\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"",
		"    timeout-minutes: 10\n    environment: production\n    permissions:\n      contents: read\n    env:\n      GOWORK: \"off\"",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a deployment environment on a PR test job")
	}
}

func TestAgentPRValidationWorkflowRejectsSecretReference(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		"          GH_TOKEN: ${{ github.token }}",
		"          GH_TOKEN: ${{ secrets.PR_VALIDATION_TOKEN }}",
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a secret reference in the PR validation workflow")
	}
}

func TestAgentPRValidationWorkflowRejectsUnboundControlRun(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		`.path == ".github/workflows/agent-pr-validation-control.yml"`,
		`.path == ".github/workflows/ci.yml"`,
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a request that was not bound to the control workflow")
	}
}

func TestAgentPRValidationWorkflowRejectsReusableRequestStatus(t *testing.T) {
	raw := string(readWorkflow(t, "agent-pr-validation.yml"))
	mutated := replaceWorkflowFirst(
		t,
		raw,
		`-f "context=Agent Validation Request / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}"`,
		`-f "context=Reusable Agent Validation Request / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}"`,
	)
	if err := validateAgentPRValidationWorkflow([]byte(mutated)); err == nil {
		t.Fatal("validator accepted a gate that does not consume the one-shot request")
	}
}

func replaceWorkflowFirst(t *testing.T, workflow, old, replacement string) string {
	t.Helper()
	if !strings.Contains(workflow, old) {
		t.Fatalf("workflow mutation source is missing: %q", old)
	}
	return strings.Replace(workflow, old, replacement, 1)
}

func validateAgentPRValidationWorkflow(raw []byte) error {
	document, workflow, err := decodeWorkflow(raw)
	if err != nil {
		return err
	}
	if strings.Contains(string(raw), "secrets.") || strings.Contains(string(raw), "secrets[") {
		return fmt.Errorf("Agent validation workflow must not reference Secrets")
	}
	if strings.Contains(string(raw), "context=Agent Validation Gate") ||
		strings.Contains(string(raw), "context='Agent Validation Gate'") {
		return fmt.Errorf("Agent validation worker must publish evidence, not the PR merge-gate context")
	}
	if err := validateAllUses(document, raw); err != nil {
		return err
	}
	if workflow.Name != "Agent Tool - Validate PR" {
		return fmt.Errorf("workflow name = %q, want Agent Tool - Validate PR", workflow.Name)
	}
	if workflow.RunName != "Agent PR #${{ github.event.client_payload.pr_number }} validation head ${{ github.event.client_payload.head_sha }} merge ${{ github.event.client_payload.merge_sha }} gate ${{ github.event.client_payload.gate_run_id }} request ${{ github.event.client_payload.request_run_id }}" {
		return fmt.Errorf("workflow run-name does not identify the requested PR, head, test-merge, gate generation, and request")
	}
	if err := validateRepositoryDispatchTrigger(workflow.On); err != nil {
		return err
	}
	if len(workflow.Permissions) != 0 {
		return fmt.Errorf("Agent validation root permissions = %#v, want none", workflow.Permissions)
	}
	wantConcurrency := ciConcurrency{
		Group:            "agent-pr-validation-${{ github.event.client_payload.pr_number }}",
		CancelInProgress: boolPointer(true),
	}
	if !reflect.DeepEqual(workflow.Concurrency, wantConcurrency) {
		return fmt.Errorf("Agent validation concurrency = %#v, want %#v", workflow.Concurrency, wantConcurrency)
	}
	jobNames := []string{
		"plan",
		"status-pending",
		"go-quality",
		"go-unit",
		"web",
		"demo",
		"go-race",
		"go-integration",
		"go-e2e",
		"moving-main",
		"three-node-smoke",
		"gate",
	}
	root := document.Content[0]
	if err := validateMappingKeys(
		root,
		[]string{"name", "run-name", "on", "permissions", "concurrency", "jobs"},
		"Agent validation workflow root",
	); err != nil {
		return err
	}
	jobs, ok := mappingValue(root, "jobs")
	if !ok {
		return fmt.Errorf("Agent validation workflow jobs are missing")
	}
	if err := validateMappingKeys(jobs, jobNames, "Agent validation workflow jobs"); err != nil {
		return err
	}
	for _, name := range jobNames {
		if workflow.Jobs[name].Environment != "" {
			return fmt.Errorf("Agent validation job %q must not use a deployment environment", name)
		}
	}
	plan := workflow.Jobs["plan"]
	if !reflect.DeepEqual(plan.Permissions, map[string]string{
		"actions":       "read",
		"contents":      "read",
		"issues":        "read",
		"pull-requests": "read",
		"statuses":      "read",
	}) {
		return fmt.Errorf("Agent validation plan permissions = %#v", plan.Permissions)
	}
	wantPlanOutputs := map[string]string{
		"docs_only":        "${{ steps.plan.outputs.docs_only }}",
		"go_fast":          "${{ steps.plan.outputs.go_fast }}",
		"web":              "${{ steps.plan.outputs.web }}",
		"demo":             "${{ steps.plan.outputs.demo }}",
		"go_race":          "${{ steps.plan.outputs.go_race }}",
		"go_integration":   "${{ steps.plan.outputs.go_integration }}",
		"go_e2e":           "${{ steps.plan.outputs.go_e2e }}",
		"three_node_smoke": "${{ steps.plan.outputs.three_node_smoke }}",
		"plan_comment_id":  "${{ steps.plan.outputs.plan_comment_id }}",
		"retry_of_run_id":  "${{ steps.plan.outputs.retry_of_run_id }}",
		"issue_agent_pr":   "${{ steps.plan.outputs.issue_agent_pr }}",
		"issue_number":     "${{ steps.plan.outputs.issue_number }}",
		"current_main_sha": "${{ steps.plan.outputs.current_main_sha }}",
	}
	if !reflect.DeepEqual(plan.Outputs, wantPlanOutputs) {
		return fmt.Errorf("Agent validation plan outputs = %#v, want %#v", plan.Outputs, wantPlanOutputs)
	}
	var planCheckout *ciStep
	for index := range plan.Steps {
		if strings.HasPrefix(plan.Steps[index].Uses, "actions/checkout@") {
			if planCheckout != nil {
				return fmt.Errorf("Agent validation plan contains multiple checkout steps")
			}
			planCheckout = &plan.Steps[index]
		}
	}
	if planCheckout == nil {
		return fmt.Errorf("Agent validation plan has no default-branch checkout")
	}
	wantPlanCheckout := map[string]any{
		"ref":                 "${{ github.event.repository.default_branch }}",
		"persist-credentials": false,
	}
	if !reflect.DeepEqual(planCheckout.With, wantPlanCheckout) {
		return fmt.Errorf("Agent validation plan checkout = %#v, want %#v", planCheckout.With, wantPlanCheckout)
	}
	var planScript strings.Builder
	for _, step := range plan.Steps {
		planScript.WriteString(step.Run)
		planScript.WriteByte('\n')
	}
	for _, required := range []string{
		`actions/runs/${REQUEST_RUN_ID}`,
		`.path == ".github/workflows/agent-pr-validation-control.yml"`,
		`.event == "pull_request_target"`,
		`.display_title == $title`,
		`validation labeled head ${EXPECTED_HEAD_SHA}`,
		`.actor.login == $actor`,
		`actions/runs/${GATE_RUN_ID}`,
		`.path == ".github/workflows/agent-pr-merge-gate.yml"`,
		`.conclusion == "failure"`,
		`Agent Validation Request / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`select(.context == $context)`,
		`endswith($suffix)`,
		`test "$current_merge_sha" = "$EXPECTED_MERGE_SHA"`,
		`"$TRIGGER_ACTOR" "$EXPECTED_HEAD_SHA" "$EXPECTED_MERGE_SHA"`,
		`"$GATE_RUN_ID"`,
		`^agent/issue-([1-9][0-9]{0,9})$`,
		`echo "issue_agent_pr=true"`,
		`echo "issue_agent_pr=false"`,
		`echo "issue_number="`,
		`current_main_sha`,
	} {
		if !strings.Contains(planScript.String(), required) {
			return fmt.Errorf("Agent validation plan is missing request binding %q", required)
		}
	}
	if strings.Contains(planScript.String(), "Agent validation PR head is not an Issue branch") {
		return fmt.Errorf("ordinary Agent PRs must not be rejected by Issue-only plan classification")
	}
	pending := workflow.Jobs["status-pending"]
	if !reflect.DeepEqual(pending.Needs, []string{"plan"}) {
		return fmt.Errorf("Agent validation pending-status needs = %#v, want plan", pending.Needs)
	}
	if !reflect.DeepEqual(pending.Permissions, map[string]string{
		"pull-requests": "read",
		"statuses":      "write",
	}) {
		return fmt.Errorf("Agent validation pending-status permissions = %#v", pending.Permissions)
	}
	testConditions := map[string]string{
		"go-quality":       "needs.plan.outputs.go_fast == 'true'",
		"go-unit":          "needs.plan.outputs.go_fast == 'true'",
		"web":              "needs.plan.outputs.web == 'true'",
		"demo":             "needs.plan.outputs.demo == 'true'",
		"go-race":          "needs.plan.outputs.go_race == 'true'",
		"go-integration":   "needs.plan.outputs.go_integration == 'true'",
		"go-e2e":           "needs.plan.outputs.go_e2e == 'true'",
		"three-node-smoke": "needs.plan.outputs.three_node_smoke == 'true'",
	}
	for name, condition := range testConditions {
		job := workflow.Jobs[name]
		if job.If != condition {
			return fmt.Errorf("Agent validation test job %q condition = %q, want %q", name, job.If, condition)
		}
		if !reflect.DeepEqual(job.Needs, []string{"plan", "status-pending"}) {
			return fmt.Errorf("Agent validation test job %q needs = %#v", name, job.Needs)
		}
		if !reflect.DeepEqual(job.Permissions, map[string]string{"contents": "read"}) {
			return fmt.Errorf("Agent validation test job %q permissions = %#v, want contents: read", name, job.Permissions)
		}
		var checkout *ciStep
		for index := range job.Steps {
			if strings.HasPrefix(job.Steps[index].Uses, "actions/checkout@") {
				if checkout != nil {
					return fmt.Errorf("Agent validation test job %q contains multiple checkout steps", name)
				}
				checkout = &job.Steps[index]
			}
		}
		if checkout == nil {
			return fmt.Errorf("Agent validation test job %q has no checkout step", name)
		}
		wantCheckout := map[string]any{
			"ref":                 "${{ github.event.client_payload.merge_sha }}",
			"persist-credentials": false,
		}
		if !reflect.DeepEqual(checkout.With, wantCheckout) {
			return fmt.Errorf("Agent validation test job %q checkout = %#v, want %#v", name, checkout.With, wantCheckout)
		}
		if name != "web" && name != "demo" {
			var setupGo *ciStep
			for index := range job.Steps {
				if strings.HasPrefix(job.Steps[index].Uses, "actions/setup-go@") {
					setupGo = &job.Steps[index]
					break
				}
			}
			if setupGo == nil {
				return fmt.Errorf("Agent validation Go job %q has no setup-go step", name)
			}
			wantSetupGo := map[string]any{
				"go-version-file": "go.mod",
				"cache":           false,
			}
			if !reflect.DeepEqual(setupGo.With, wantSetupGo) {
				return fmt.Errorf("Agent validation Go job %q setup-go = %#v, want %#v", name, setupGo.With, wantSetupGo)
			}
		}
	}
	goIntegration := workflow.Jobs["go-integration"]
	if goIntegration.TimeoutMinutes != 40 {
		return fmt.Errorf(
			"Agent validation Go integration timeout = %d, want 40",
			goIntegration.TimeoutMinutes,
		)
	}
	integrationSteps := make(map[string]string)
	for _, step := range goIntegration.Steps {
		integrationSteps[step.Name] = step.Run
	}
	for name, required := range map[string]string{
		"Run integration packages": "go test -tags=integration ./internal/... ./pkg/... -count=1 -timeout=20m -p=1",
		"Run scripts integration":  "timeout --signal=TERM --kill-after=30s 10m go test -tags=integration ./scripts/... -count=1 -timeout=9m -parallel=2",
	} {
		run, ok := integrationSteps[name]
		if !ok {
			return fmt.Errorf("Agent validation Go integration job is missing step %q", name)
		}
		if !strings.Contains(run, required) {
			return fmt.Errorf(
				"Agent validation Go integration step %q is missing command %q",
				name,
				required,
			)
		}
	}
	movingMain := workflow.Jobs["moving-main"]
	if movingMain.If != "needs.plan.outputs.go_e2e == 'true' && needs.plan.outputs.issue_agent_pr == 'true'" ||
		!reflect.DeepEqual(movingMain.Needs, []string{"plan", "status-pending"}) ||
		!reflect.DeepEqual(movingMain.Permissions, map[string]string{"contents": "read"}) {
		return fmt.Errorf("Agent moving-main job is not bound to the frozen E2E plan")
	}
	var movingMainScript strings.Builder
	checkouts := 0
	for _, step := range movingMain.Steps {
		movingMainScript.WriteString(step.Run)
		movingMainScript.WriteByte('\n')
		if strings.HasPrefix(step.Uses, "actions/checkout@") {
			checkouts++
		}
	}
	if checkouts != 2 {
		return fmt.Errorf("Agent moving-main job checkout count = %d, want 2", checkouts)
	}
	for _, required := range []string{
		`"./test/e2e/issue_agent/issue_${ISSUE_NUMBER}"`,
		"timeout --signal=TERM --kill-after=30s 50m",
		"-count=3",
		"-timeout=45m -p=1",
		"binary_sha256",
		"main_passed",
		"main_sha",
	} {
		if !strings.Contains(movingMainScript.String(), required) {
			return fmt.Errorf("Agent moving-main evidence is missing %q", required)
		}
	}
	retentionOK := false
	for _, step := range movingMain.Steps {
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") &&
			fmt.Sprint(step.With["retention-days"]) == "90" {
			retentionOK = true
		}
	}
	if !retentionOK {
		return fmt.Errorf("Agent moving-main Artifact retention is not 90 days")
	}
	checkoutPaths := make([]string, 0, 2)
	for _, step := range movingMain.Steps {
		if strings.HasPrefix(step.Uses, "actions/checkout@") {
			pathValue, ok := step.With["path"].(string)
			if !ok {
				return fmt.Errorf("Agent moving-main checkout has no path")
			}
			checkoutPaths = append(checkoutPaths, pathValue)
		}
	}
	if !reflect.DeepEqual(checkoutPaths, []string{"scenario", "current-main"}) {
		return fmt.Errorf("Agent moving-main checkout paths = %#v", checkoutPaths)
	}
	goE2EBuild := workflow.Jobs["go-e2e"]
	var buildE2EBinary *ciStep
	for index := range goE2EBuild.Steps {
		if goE2EBuild.Steps[index].Name == "Build e2e binary once" {
			buildE2EBinary = &goE2EBuild.Steps[index]
			break
		}
	}
	if buildE2EBinary == nil {
		return fmt.Errorf("Agent validation Go e2e job has no shared binary build step")
	}
	if len(buildE2EBinary.Env) != 0 {
		return fmt.Errorf(
			"Agent validation Go e2e binary build env = %#v, want none",
			buildE2EBinary.Env,
		)
	}
	if !strings.Contains(
		buildE2EBinary.Run,
		`go build -tags=e2e`,
	) {
		return fmt.Errorf("Agent validation Go e2e job does not build the tagged shared binary")
	}
	gate, ok := workflow.Jobs["gate"]
	if !ok {
		return fmt.Errorf("Agent validation workflow gate job is missing")
	}
	if gate.Name != "Publish Agent validation evidence" {
		return fmt.Errorf("Agent validation evidence publisher name = %q", gate.Name)
	}
	if gate.If != "always()" {
		return fmt.Errorf("Agent validation gate must run with always()")
	}
	if len(gate.Steps) == 0 ||
		gate.Steps[0].Env["ISSUE_AGENT_PR"] != "${{ needs.plan.outputs.issue_agent_pr }}" ||
		gate.Steps[0].Env["ISSUE_NUMBER"] != "${{ needs.plan.outputs.issue_number }}" {
		return fmt.Errorf("Agent validation gate does not receive the typed Issue PR classification")
	}
	wantGateNeeds := jobNames[:len(jobNames)-1]
	if !reflect.DeepEqual(gate.Needs, wantGateNeeds) {
		return fmt.Errorf("Agent validation gate needs = %#v, want %#v", gate.Needs, wantGateNeeds)
	}
	if !reflect.DeepEqual(gate.Permissions, map[string]string{
		"actions":       "write",
		"pull-requests": "read",
		"statuses":      "write",
	}) {
		return fmt.Errorf("Agent validation gate permissions are not fail-closed")
	}
	for _, name := range []string{"status-pending", "gate"} {
		for _, step := range workflow.Jobs[name].Steps {
			if strings.HasPrefix(step.Uses, "actions/checkout@") {
				return fmt.Errorf("Agent validation status job %q must not checkout code", name)
			}
		}
	}
	var gateScript strings.Builder
	for _, step := range gate.Steps {
		gateScript.WriteString(step.Run)
		gateScript.WriteByte('\n')
	}
	for _, required := range []string{
		`context=Agent Validation Request / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`target_url="$REQUEST_RUN_URL"`,
		`context=Agent Validation Evidence / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`publish_handoff_error`,
		`state=error`,
		`"$current_head" != "$HEAD_SHA" || "$current_merge" != "$MERGE_SHA"`,
		`latest_gate_run_id`,
		`"$latest_gate_run_id" != "$GATE_RUN_ID"`,
		`should_rerun_gate=false`,
		`actions/runs/${GATE_RUN_ID}/rerun`,
		`moving_main_selected=false`,
		`moving_main_selected=true`,
		`check_result "$moving_main_selected" "$MOVING_MAIN_RESULT"`,
		`if [[ "$ISSUE_AGENT_PR" == true ]]; then`,
		`context=Agent Moving Main / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`description=main=${MOVING_MAIN_SHA};binary=${MOVING_MAIN_BINARY_SHA256};runs=3`,
	} {
		if !strings.Contains(gateScript.String(), required) {
			return fmt.Errorf("Agent validation gate is missing request consumption %q", required)
		}
	}
	return nil
}

func validateAgentPRValidationControlWorkflow(raw []byte) error {
	document, workflow, err := decodeWorkflow(raw)
	if err != nil {
		return err
	}
	if strings.Contains(string(raw), "secrets.") || strings.Contains(string(raw), "secrets[") {
		return fmt.Errorf("control workflow must not reference Secrets")
	}
	if workflow.Name != "Safety Automation - Agent PR Validation Control" {
		return fmt.Errorf("control workflow name = %q", workflow.Name)
	}
	if workflow.RunName != "Agent PR #${{ github.event.pull_request.number }} validation ${{ github.event.action }} head ${{ github.event.pull_request.head.sha }}" {
		return fmt.Errorf("control workflow run-name does not identify the PR event and head")
	}
	if strings.Contains(string(raw), "github.event.pull_request.merge_commit_sha") || strings.Contains(string(raw), "EVENT_MERGE_SHA") {
		return fmt.Errorf("control workflow must resolve the test-merge SHA from the trusted PR API response")
	}
	if err := validateAgentControlTriggers(workflow.On); err != nil {
		return err
	}
	if len(workflow.Permissions) != 0 {
		return fmt.Errorf("control workflow root permissions = %#v, want none", workflow.Permissions)
	}
	root := document.Content[0]
	if err := validateMappingKeys(
		root,
		[]string{"name", "run-name", "on", "permissions", "jobs"},
		"Agent validation control workflow root",
	); err != nil {
		return err
	}
	jobs, ok := mappingValue(root, "jobs")
	if !ok {
		return fmt.Errorf("Agent validation control jobs are missing")
	}
	if err := validateMappingKeys(jobs, []string{"request", "invalidate"}, "Agent validation control jobs"); err != nil {
		return err
	}
	request := workflow.Jobs["request"]
	if request.Environment != "" {
		return fmt.Errorf("Agent validation request must not use a deployment environment")
	}
	if request.If != "github.event.action == 'labeled' && github.event.label.name == 'agent-ci/run'" {
		return fmt.Errorf("Agent validation request condition is not bound to agent-ci/run")
	}
	if !reflect.DeepEqual(request.Permissions, map[string]string{
		"actions":       "read",
		"contents":      "write",
		"pull-requests": "read",
		"statuses":      "write",
	}) {
		return fmt.Errorf("Agent validation request permissions = %#v", request.Permissions)
	}
	var requestScript strings.Builder
	for _, step := range request.Steps {
		requestScript.WriteString(step.Run)
		requestScript.WriteByte('\n')
	}
	for _, required := range []string{
		`test "$current_head" = "$HEAD_SHA"`,
		`.merge_commit_sha`,
		`actions/workflows/agent-pr-merge-gate.yml/runs`,
		`.conclusion == "failure"`,
		`collaborators/${TRIGGER_ACTOR}/permission`,
		"admin|maintain|write) ;;",
		`context=Agent Validation Request / PR #${PR_NUMBER} / Gate #${gate_run_id}`,
		`target_url="$REQUEST_RUN_URL"`,
		"--arg event_type agent-pr-validation",
		"--arg merge_sha \"$merge_sha\"",
		"--arg gate_run_id \"$gate_run_id\"",
		"--arg request_run_id \"$REQUEST_RUN_ID\"",
		`repos/${GITHUB_REPOSITORY}/dispatches`,
	} {
		if !strings.Contains(requestScript.String(), required) {
			return fmt.Errorf("Agent validation request is missing trusted dispatch contract %q", required)
		}
	}
	invalidate := workflow.Jobs["invalidate"]
	if invalidate.Environment != "" {
		return fmt.Errorf("Agent validation invalidation must not use a deployment environment")
	}
	if invalidate.If != "github.event.action == 'edited' || github.event.action == 'opened' || github.event.action == 'reopened' || github.event.action == 'synchronize'" {
		return fmt.Errorf("Agent validation invalidation condition does not cover edited, opened, reopened, and synchronize")
	}
	wantConcurrency := &ciConcurrency{
		Group:            "agent-pr-validation-${{ github.event.pull_request.number }}",
		CancelInProgress: boolPointer(true),
	}
	if !reflect.DeepEqual(invalidate.Concurrency, wantConcurrency) {
		return fmt.Errorf("Agent validation invalidation concurrency = %#v, want %#v", invalidate.Concurrency, wantConcurrency)
	}
	if len(invalidate.Permissions) != 0 {
		return fmt.Errorf("Agent validation invalidation permissions = %#v, want none", invalidate.Permissions)
	}
	invalidateNode, ok := mappingValue(jobs, "invalidate")
	if !ok {
		return fmt.Errorf("Agent validation invalidation job is missing")
	}
	if err := validateMappingKeys(
		invalidateNode,
		[]string{"name", "if", "runs-on", "timeout-minutes", "concurrency", "steps"},
		"Agent validation invalidation job",
	); err != nil {
		return err
	}
	wantInvalidateStep := ciStep{
		Name:  "Write invalidation summary",
		Shell: "bash",
		Run: "{\n" +
			"  echo '## Agent validation invalidated'\n" +
			"  echo\n" +
			"  echo \"- PR: \\`#${{ github.event.pull_request.number }}\\`\"\n" +
			"  echo \"- New head SHA: \\`${{ github.event.pull_request.head.sha }}\\`\"\n" +
			"  echo '- A fresh Agent validation plan is required.'\n" +
			"} >>\"$GITHUB_STEP_SUMMARY\"\n",
	}
	if len(invalidate.Steps) != 1 || !reflect.DeepEqual(invalidate.Steps[0], wantInvalidateStep) {
		return fmt.Errorf("Agent validation invalidation must contain exactly one summary-only step")
	}
	invalidateSteps, ok := mappingValue(invalidateNode, "steps")
	if !ok || invalidateSteps.Kind != yaml.SequenceNode || len(invalidateSteps.Content) != 1 {
		return fmt.Errorf("Agent validation invalidation steps must contain exactly one entry")
	}
	if err := validateMappingKeys(
		invalidateSteps.Content[0],
		[]string{"name", "shell", "run"},
		"Agent validation invalidation summary step",
	); err != nil {
		return err
	}
	if strings.Contains(string(raw), "actions/checkout") ||
		strings.Contains(string(raw), "github.event.pull_request.head.repo") {
		return fmt.Errorf("control workflow must never checkout pull request code")
	}
	return nil
}

func validateAgentPRValidationMergeGateWorkflow(raw []byte) error {
	document, workflow, err := decodeWorkflow(raw)
	if err != nil {
		return err
	}
	if strings.Contains(string(raw), "secrets.") ||
		strings.Contains(string(raw), "secrets[") ||
		strings.Contains(string(raw), "actions/checkout") {
		return fmt.Errorf("Agent PR merge gate must not use Secrets or checkout code")
	}
	if workflow.Name != "Safety Automation - Agent PR Merge Gate" {
		return fmt.Errorf("Agent PR merge gate workflow name = %q", workflow.Name)
	}
	if workflow.RunName != "Agent PR #${{ github.event.pull_request.number }} merge gate ${{ github.event.action }} head ${{ github.event.pull_request.head.sha }} merge ${{ github.sha }}" {
		return fmt.Errorf("Agent PR merge gate run-name is not PR, head, and test-merge bound")
	}
	if !strings.Contains(string(raw), "MERGE_SHA: ${{ github.sha }}") {
		return fmt.Errorf("Agent PR merge gate does not bind MERGE_SHA to github.sha")
	}
	if !strings.Contains(string(raw), "BASE_SHA: ${{ github.event.pull_request.base.sha }}") {
		return fmt.Errorf("Agent PR merge gate does not bind BASE_SHA to the PR base")
	}
	if err := validateAgentMergeGateTriggers(workflow.On); err != nil {
		return err
	}
	if len(workflow.Permissions) != 0 {
		return fmt.Errorf("Agent PR merge gate root permissions = %#v, want none", workflow.Permissions)
	}
	wantConcurrency := ciConcurrency{
		Group:            "agent-pr-merge-gate-${{ github.event.pull_request.number }}-${{ github.run_id }}",
		CancelInProgress: boolPointer(true),
	}
	if !reflect.DeepEqual(workflow.Concurrency, wantConcurrency) {
		return fmt.Errorf("Agent PR merge gate concurrency = %#v, want %#v", workflow.Concurrency, wantConcurrency)
	}
	root := document.Content[0]
	if err := validateMappingKeys(
		root,
		[]string{"name", "run-name", "on", "permissions", "concurrency", "jobs"},
		"Agent PR merge gate workflow root",
	); err != nil {
		return err
	}
	jobs, ok := mappingValue(root, "jobs")
	if !ok {
		return fmt.Errorf("Agent PR merge gate jobs are missing")
	}
	if err := validateMappingKeys(jobs, []string{"gate"}, "Agent PR merge gate jobs"); err != nil {
		return err
	}
	gate := workflow.Jobs["gate"]
	if gate.Name != "Agent Validation Gate" ||
		gate.If != "" ||
		gate.RunsOn != "ubuntu-24.04" ||
		gate.TimeoutMinutes != 3 ||
		gate.Environment != "" {
		return fmt.Errorf("Agent PR merge gate job does not match the stable fail-closed contract")
	}
	if !reflect.DeepEqual(gate.Permissions, map[string]string{
		"actions":       "read",
		"contents":      "read",
		"pull-requests": "read",
		"statuses":      "read",
	}) {
		return fmt.Errorf("Agent PR merge gate permissions = %#v, want read-only evidence access", gate.Permissions)
	}
	if len(gate.Steps) != 1 || gate.Steps[0].Uses != "" {
		return fmt.Errorf("Agent PR merge gate must contain one script-only verification step")
	}
	script := gate.Steps[0].Run
	for _, required := range []string{
		`"$RUN_ATTEMPT" -eq 1`,
		`git/commits/${BASE_SHA}`,
		`.truncated == false`,
		`.tree | type == "array"`,
		`all(.tree[];`,
		`.path | type == "string" and length > 0`,
		`base_has_gate`,
		`test "$base_has_gate" = true`,
		`.github/workflows/agent-pr-merge-gate.yml`,
		`Bootstrap PR: the merge-gate workflow is not yet on the base branch`,
		`[[ "$MERGE_SHA" =~ ^[0-9a-f]{40}$ ]]`,
		`[[ "$GATE_RUN_ID" =~ ^[1-9][0-9]{0,19}$ ]]`,
		`test "$current_head" = "$HEAD_SHA"`,
		`test "$current_merge" = "$MERGE_SHA"`,
		`Agent Validation Request / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`Agent Validation Evidence / PR #${PR_NUMBER} / Gate #${GATE_RUN_ID}`,
		`.created_at >= $event_updated_at`,
		`.path == ".github/workflows/agent-pr-validation-control.yml"`,
		`.event == "pull_request_target"`,
		`Agent PR #${PR_NUMBER} validation labeled head ${HEAD_SHA}`,
		`Agent PR #${PR_NUMBER} validation labeled head ${HEAD_SHA} merge ${MERGE_SHA}`,
		`.display_title == $title or`,
		`.display_title == $legacy_title`,
		`.path == ".github/workflows/agent-pr-validation.yml"`,
		`.event == "repository_dispatch"`,
		`validation head ${HEAD_SHA} merge ${MERGE_SHA} gate ${GATE_RUN_ID} request ${request_run_id}`,
		`for attempt in {1..12}`,
		`test "$evidence_complete" = true`,
		`.status == "completed"`,
		`.conclusion == "success"`,
		`.state == "success"`,
	} {
		if !strings.Contains(script, required) {
			return fmt.Errorf("Agent PR merge gate is missing binding %q", required)
		}
	}
	if strings.Count(script, "verify_latest_gate_generation") < 3 {
		return fmt.Errorf("Agent PR merge gate must verify the latest generation before and after evidence checks")
	}
	return nil
}

func validateExpectedWorkflow(
	raw []byte,
	wantName string,
	jobNames []string,
	expectedJobs map[string]ciJob,
) error {
	document, workflow, err := decodeWorkflow(raw)
	if err != nil {
		return err
	}
	if err := validateAllUses(document, raw); err != nil {
		return err
	}
	if err := validateWorkflowStructure(
		document,
		jobNames,
		expectedJobs,
	); err != nil {
		return err
	}
	if workflow.Name != wantName {
		return fmt.Errorf("workflow name = %q, want %s", workflow.Name, wantName)
	}
	if err := validateManualOnlyTriggers(workflow.On); err != nil {
		return err
	}
	wantPermissions := map[string]string{"contents": "read"}
	if !reflect.DeepEqual(workflow.Permissions, wantPermissions) {
		return fmt.Errorf("permissions = %#v, want exactly %#v", workflow.Permissions, wantPermissions)
	}
	wantConcurrency := ciConcurrency{
		Group:            "${{ github.workflow }}-${{ github.ref }}",
		CancelInProgress: boolPointer(false),
	}
	if !reflect.DeepEqual(workflow.Concurrency, wantConcurrency) {
		return fmt.Errorf("concurrency = %#v, want %#v", workflow.Concurrency, wantConcurrency)
	}
	if len(workflow.Jobs) != len(expectedJobs) {
		return fmt.Errorf("workflow jobs = %d, want exactly %d", len(workflow.Jobs), len(expectedJobs))
	}
	for name, want := range expectedJobs {
		got, ok := workflow.Jobs[name]
		if !ok {
			return fmt.Errorf("workflow missing required job %q", name)
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("job %q does not match the required fail-closed contract", name)
		}
	}
	return nil
}

func validateWorkflowStructure(document *yaml.Node, jobNames []string, expectedJobs map[string]ciJob) error {
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return fmt.Errorf("workflow YAML must contain one mapping document")
	}
	root := document.Content[0]
	if err := validateMappingKeys(
		root,
		[]string{"name", "on", "permissions", "concurrency", "jobs"},
		"workflow root",
	); err != nil {
		return err
	}
	permissions, ok := mappingValue(root, "permissions")
	if !ok {
		return fmt.Errorf("workflow permissions are missing")
	}
	if err := validateMappingKeys(permissions, []string{"contents"}, "workflow permissions"); err != nil {
		return err
	}
	concurrency, ok := mappingValue(root, "concurrency")
	if !ok {
		return fmt.Errorf("workflow concurrency is missing")
	}
	if err := validateMappingKeys(
		concurrency,
		[]string{"group", "cancel-in-progress"},
		"workflow concurrency",
	); err != nil {
		return err
	}
	jobs, ok := mappingValue(root, "jobs")
	if !ok || jobs.Kind != yaml.MappingNode {
		return fmt.Errorf("workflow jobs must be a mapping")
	}
	if err := validateMappingKeys(jobs, jobNames, "workflow jobs"); err != nil {
		return err
	}
	for _, name := range jobNames {
		wantJob := expectedJobs[name]
		job, ok := mappingValue(jobs, name)
		if !ok {
			return fmt.Errorf("workflow missing required job %q", name)
		}
		if err := validateMappingKeys(job, expectedJobKeys(wantJob), fmt.Sprintf("job %q", name)); err != nil {
			return err
		}
		steps, ok := mappingValue(job, "steps")
		if !ok || steps.Kind != yaml.SequenceNode {
			return fmt.Errorf("job %q steps must be a sequence", name)
		}
		if len(steps.Content) != len(wantJob.Steps) {
			return fmt.Errorf("job %q steps = %d, want exactly %d", name, len(steps.Content), len(wantJob.Steps))
		}
		for index, wantStep := range wantJob.Steps {
			context := fmt.Sprintf("job %q step %d", name, index+1)
			if err := validateMappingKeys(steps.Content[index], expectedStepKeys(wantStep), context); err != nil {
				return err
			}
		}
	}
	return nil
}

func expectedJobKeys(job ciJob) []string {
	keys := []string{"name", "runs-on", "timeout-minutes", "steps"}
	if job.If != "" {
		keys = append(keys, "if")
	}
	if job.Environment != "" {
		keys = append(keys, "environment")
	}
	if job.Needs != nil {
		keys = append(keys, "needs")
	}
	if job.Permissions != nil {
		keys = append(keys, "permissions")
	}
	if job.Outputs != nil {
		keys = append(keys, "outputs")
	}
	if job.Concurrency != nil {
		keys = append(keys, "concurrency")
	}
	if job.Env != nil {
		keys = append(keys, "env")
	}
	if job.Defaults != nil {
		keys = append(keys, "defaults")
	}
	if job.Strategy != nil {
		keys = append(keys, "strategy")
	}
	if job.Uses != "" {
		keys = append(keys, "uses")
	}
	return keys
}

func expectedStepKeys(step ciStep) []string {
	var keys []string
	if step.ID != "" {
		keys = append(keys, "id")
	}
	if step.Name != "" {
		keys = append(keys, "name")
	}
	if step.Uses != "" {
		keys = append(keys, "uses")
	}
	if step.Run != "" {
		keys = append(keys, "run")
	}
	if step.Shell != "" {
		keys = append(keys, "shell")
	}
	if step.If != "" {
		keys = append(keys, "if")
	}
	if step.Env != nil {
		keys = append(keys, "env")
	}
	if step.With != nil {
		keys = append(keys, "with")
	}
	return keys
}

func validateMappingKeys(node *yaml.Node, expected []string, context string) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must be a mapping", context)
	}
	if len(node.Content) != len(expected)*2 {
		return fmt.Errorf("%s has %d keys, want exactly %d", context, len(node.Content)/2, len(expected))
	}
	actual := make(map[string]struct{}, len(expected))
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := node.Content[index]
		if key.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s contains a non-scalar key", context)
		}
		if _, duplicate := actual[key.Value]; duplicate {
			return fmt.Errorf("%s contains duplicate key %q", context, key.Value)
		}
		actual[key.Value] = struct{}{}
	}
	for _, key := range expected {
		if _, ok := actual[key]; !ok {
			return fmt.Errorf("%s is missing required key %q", context, key)
		}
	}
	return nil
}

func mappingValue(mapping *yaml.Node, name string) (*yaml.Node, bool) {
	if mapping.Kind != yaml.MappingNode {
		return nil, false
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		if key.Kind == yaml.ScalarNode && key.Value == name {
			return mapping.Content[index+1], true
		}
	}
	return nil, false
}

func decodeWorkflow(raw []byte) (*yaml.Node, ciWorkflow, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return nil, ciWorkflow{}, fmt.Errorf("workflow YAML is empty")
		}
		return nil, ciWorkflow{}, fmt.Errorf("parse workflow YAML: %w", err)
	}
	if len(document.Content) == 0 {
		return nil, ciWorkflow{}, fmt.Errorf("workflow YAML is empty")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, ciWorkflow{}, fmt.Errorf("workflow YAML must contain exactly one document")
		}
		return nil, ciWorkflow{}, fmt.Errorf("parse trailing workflow YAML: %w", err)
	}

	typedDecoder := yaml.NewDecoder(bytes.NewReader(raw))
	typedDecoder.KnownFields(true)
	var workflow ciWorkflow
	if err := typedDecoder.Decode(&workflow); err != nil {
		return nil, ciWorkflow{}, fmt.Errorf("decode workflow hierarchy: %w", err)
	}
	return &document, workflow, nil
}

func validateRepositoryDispatchTrigger(triggers map[string]yaml.Node) error {
	if len(triggers) != 1 {
		return fmt.Errorf("Agent validation trigger keys = %d, want exactly repository_dispatch", len(triggers))
	}
	trigger, ok := triggers["repository_dispatch"]
	if !ok {
		return fmt.Errorf("Agent validation workflow trigger repository_dispatch is missing")
	}
	if err := validateMappingKeys(&trigger, []string{"types"}, "Agent validation repository_dispatch trigger"); err != nil {
		return err
	}
	types, ok := mappingValue(&trigger, "types")
	if !ok || types.Kind != yaml.SequenceNode || len(types.Content) != 1 ||
		types.Content[0].Value != "agent-pr-validation" {
		return fmt.Errorf("Agent validation repository_dispatch types must be exactly [agent-pr-validation]")
	}
	return nil
}

func validateAgentControlTriggers(triggers map[string]yaml.Node) error {
	if len(triggers) != 1 {
		return fmt.Errorf("Agent validation control trigger keys = %d, want exactly pull_request_target", len(triggers))
	}
	trigger, ok := triggers["pull_request_target"]
	if !ok {
		return fmt.Errorf("Agent validation control trigger pull_request_target is missing")
	}
	if err := validateMappingKeys(&trigger, []string{"types"}, "Agent validation pull_request_target trigger"); err != nil {
		return err
	}
	types, ok := mappingValue(&trigger, "types")
	if !ok || types.Kind != yaml.SequenceNode || len(types.Content) != 5 ||
		types.Content[0].Value != "edited" ||
		types.Content[1].Value != "labeled" ||
		types.Content[2].Value != "opened" ||
		types.Content[3].Value != "reopened" ||
		types.Content[4].Value != "synchronize" {
		return fmt.Errorf("Agent validation pull_request_target types must be exactly [edited, labeled, opened, reopened, synchronize]")
	}
	return nil
}

func validateAgentMergeGateTriggers(triggers map[string]yaml.Node) error {
	if len(triggers) != 1 {
		return fmt.Errorf("Agent PR merge gate trigger keys = %d, want exactly pull_request", len(triggers))
	}
	trigger, ok := triggers["pull_request"]
	if !ok {
		return fmt.Errorf("Agent PR merge gate pull_request trigger is missing")
	}
	if err := validateMappingKeys(&trigger, []string{"types"}, "Agent PR merge gate pull_request trigger"); err != nil {
		return err
	}
	types, ok := mappingValue(&trigger, "types")
	if !ok || types.Kind != yaml.SequenceNode || len(types.Content) != 4 ||
		types.Content[0].Value != "edited" ||
		types.Content[1].Value != "opened" ||
		types.Content[2].Value != "reopened" ||
		types.Content[3].Value != "synchronize" {
		return fmt.Errorf("Agent PR merge gate pull_request types must be exactly [edited, opened, reopened, synchronize]")
	}
	return nil
}

func validateManualOnlyTriggers(triggers map[string]yaml.Node) error {
	if len(triggers) != 1 {
		return fmt.Errorf("workflow trigger keys = %d, want exactly workflow_dispatch", len(triggers))
	}
	workflowDispatch, ok := triggers["workflow_dispatch"]
	if !ok {
		return fmt.Errorf("workflow trigger %q is missing", "workflow_dispatch")
	}
	if !isEmptyTrigger(workflowDispatch) {
		return fmt.Errorf("workflow trigger %q must not contain inputs or options", "workflow_dispatch")
	}
	return nil
}

func isEmptyTrigger(trigger yaml.Node) bool {
	return trigger.Kind == 0 || trigger.Tag == "!!null" ||
		(trigger.Kind == yaml.MappingNode && len(trigger.Content) == 0)
}

func validateAllUses(document *yaml.Node, raw []byte) error {
	uses := collectUses(document)
	if len(uses) == 0 {
		return fmt.Errorf("workflow contains no action references")
	}
	lines := strings.Split(string(raw), "\n")
	for _, node := range uses {
		if node.Kind != yaml.ScalarNode || node.Value == "" {
			return fmt.Errorf("action reference must be a non-empty scalar")
		}
		action, ref, ok := strings.Cut(node.Value, "@")
		if !ok || action == "" || ref == "" {
			return fmt.Errorf("action reference %q lacks a complete owner/action@ref", node.Value)
		}
		pin, approved := approvedActionPins[action]
		if !approved {
			return fmt.Errorf("unreviewed action %q", action)
		}
		if ref != pin.sha {
			return fmt.Errorf("action %s ref = %q, want immutable %s", action, ref, pin.sha)
		}
		if node.Line < 1 || node.Line > len(lines) || !strings.Contains(lines[node.Line-1], "# "+pin.release) {
			return fmt.Errorf("action %s must retain release comment %s on its uses line", action, pin.release)
		}
	}
	return nil
}

func collectUses(document *yaml.Node) []*yaml.Node {
	var uses []*yaml.Node
	seen := make(map[*yaml.Node]bool)
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node == nil || seen[node] {
			return
		}
		seen[node] = true
		switch node.Kind {
		case yaml.MappingNode:
			for index := 0; index+1 < len(node.Content); index += 2 {
				key, value := node.Content[index], node.Content[index+1]
				if key.Kind == yaml.ScalarNode && key.Value == "uses" {
					uses = append(uses, value)
				}
				walk(value)
			}
		case yaml.AliasNode:
			walk(node.Alias)
		default:
			for _, child := range node.Content {
				walk(child)
			}
		}
	}
	walk(document)
	return uses
}

func boolPointer(value bool) *bool {
	return &value
}

func readWorkflow(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", ".github", "workflows", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workflow %s: %v", path, err)
	}
	return raw
}
