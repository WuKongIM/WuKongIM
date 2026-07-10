package scripts_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
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
	On          map[string]yaml.Node `yaml:"on"`
	Permissions map[string]string    `yaml:"permissions"`
	Concurrency ciConcurrency        `yaml:"concurrency"`
	Jobs        map[string]ciJob     `yaml:"jobs"`
}

type ciConcurrency struct {
	Group            string `yaml:"group"`
	CancelInProgress bool   `yaml:"cancel-in-progress"`
}

type ciJob struct {
	Name           string            `yaml:"name"`
	RunsOn         string            `yaml:"runs-on"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
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
	Name  string            `yaml:"name"`
	Uses  string            `yaml:"uses"`
	Run   string            `yaml:"run"`
	Shell string            `yaml:"shell"`
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
	"oven-sh/setup-bun": {
		sha:     "0c5077e51419868618aeaa5fe8019c62421857d6",
		release: "v2.2.0",
	},
	"actions/upload-artifact": {
		sha:     "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
		release: "v7.0.1",
	},
}

const trackedGoFormattingCommand = `mapfile -t go_files < <(git ls-files '*.go')
test "${#go_files[@]}" -gt 0
unformatted="$(gofmt -l "${go_files[@]}")"
if [[ -n "$unformatted" ]]; then
  echo "The following tracked Go files need gofmt:"
  echo "$unformatted"
  exit 1
fi
`

var expectedCIJobs = map[string]ciJob{
	"go-quality": {
		Name:           "Go quality",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 10,
		Env:            map[string]string{"GOWORK": "off"},
		Steps: []ciStep{
			{
				Uses: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
				With: map[string]any{"persist-credentials": false},
			},
			{
				Uses: "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
				With: map[string]any{
					"go-version-file":       "go.mod",
					"cache":                 true,
					"cache-dependency-path": "go.sum",
				},
			},
			{Name: "Verify Go toolchain", Run: `test "$(go env GOVERSION)" = "go1.25.11"`},
			{Name: "Check tracked Go formatting", Shell: "bash", Run: trackedGoFormattingCommand},
			{Name: "Check module metadata", Run: "go mod tidy -diff"},
			{Name: "Vet explicit roots", Run: "go vet ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/..."},
		},
	},
	"go-unit": {
		Name:           "Go unit (${{ matrix.name }})",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 15,
		Env:            map[string]string{"GOWORK": "off"},
		Strategy: &ciStrategy{
			FailFast: boolPointer(false),
			Matrix: ciMatrix{Include: []ciMatrixEntry{
				{Name: "cmd", Packages: "./cmd/..."},
				{Name: "internal", Packages: "./internal/..."},
				{Name: "pkg", Packages: "./pkg/..."},
				{Name: "scripts-docker", Packages: "./scripts/... ./docker/..."},
			}},
		},
		Steps: []ciStep{
			{
				Uses: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
				With: map[string]any{"persist-credentials": false},
			},
			{
				Uses: "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
				With: map[string]any{
					"go-version-file":       "go.mod",
					"cache":                 true,
					"cache-dependency-path": "go.sum",
				},
			},
			{Name: "Verify Go toolchain", Run: `test "$(go env GOVERSION)" = "go1.25.11"`},
			{
				Name:  "Run unit group",
				Shell: "bash",
				Env:   map[string]string{"PACKAGES": "${{ matrix.packages }}"},
				Run:   "go test $PACKAGES -count=1 -timeout=14m",
			},
		},
	},
	"web": {
		Name:           "Web",
		RunsOn:         "ubuntu-24.04",
		TimeoutMinutes: 10,
		Defaults:       &ciDefaults{Run: ciRunDefaults{WorkingDirectory: "web"}},
		Steps: []ciStep{
			{
				Uses: "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
				With: map[string]any{"persist-credentials": false},
			},
			{
				Uses: "oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6",
				With: map[string]any{"bun-version": "1.3.11"},
			},
			{Name: "Verify Bun version", Run: `test "$(bun --version)" = "1.3.11"`},
			{Name: "Install dependencies", Run: "bun install --frozen-lockfile"},
			{Name: "Lint against baseline", Run: "bun run lint"},
			{Name: "Test", Run: "bun run test"},
			{Name: "Type check", Run: "bunx tsc -b"},
			{Name: "Build", Run: "bun run build"},
			{Name: "Check deterministic tracked build output", Run: "git diff --exit-code -- dist/index.html"},
		},
	},
}

func TestCIWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "ci.yml")
	if err := validateCIWorkflow(raw); err != nil {
		t.Fatal(err)
	}
}

func TestCIWorkflowContractRejectsMutations(t *testing.T) {
	raw := string(readWorkflow(t, "ci.yml"))
	tests := []struct {
		name      string
		mutate    func(*testing.T, string) string
		wantError string
	}{
		{
			name: "named action step using floating ref",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0",
					"      - name: Unreviewed action\n        uses: owner/action@main",
				)
			},
			wantError: "unreviewed action",
		},
		{
			name: "job level reusable workflow using floating ref",
			mutate: func(_ *testing.T, workflow string) string {
				return workflow + "\n  reusable:\n    uses: owner/workflow@main\n"
			},
			wantError: "unreviewed action",
		},
		{
			name: "commented triggers do not replace on hierarchy",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"on:\n  pull_request:\n  push:\n    branches: [main]\n  workflow_dispatch:",
					"on: {}\n# pull_request:\n# push:\n#   branches: [main]\n# workflow_dispatch:",
				)
			},
		},
		{
			name: "commented read permission does not mask write all",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"permissions:\n  contents: read",
					"permissions: write-all\n# permissions:\n#   contents: read",
				)
			},
		},
		{
			name: "commented timeout does not mask wrong job timeout",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"    timeout-minutes: 10",
					"    timeout-minutes: 11 # timeout-minutes: 10",
				)
			},
		},
		{
			name: "unit command cannot add benchmark",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"        run: go test $PACKAGES -count=1 -timeout=14m",
					"        run: go test $PACKAGES -bench=. -count=1 -timeout=14m # go test $PACKAGES -count=1 -timeout=14m",
				)
			},
		},
		{
			name: "unit command cannot add race after package operand",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"        run: go test $PACKAGES -count=1 -timeout=14m",
					"        run: go test $PACKAGES -race -count=1 -timeout=14m # go test $PACKAGES -count=1 -timeout=14m",
				)
			},
		},
		{
			name: "job cannot declare null continue on error",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"    timeout-minutes: 10\n    env:\n      GOWORK: \"off\"",
					"    timeout-minutes: 10\n    continue-on-error: null\n    env:\n      GOWORK: \"off\"",
				)
			},
		},
		{
			name: "step cannot declare null continue on error",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"      - name: Verify Go toolchain\n        run: test \"$(go env GOVERSION)\" = \"go1.25.11\"",
					"      - name: Verify Go toolchain\n        continue-on-error: null\n        run: test \"$(go env GOVERSION)\" = \"go1.25.11\"",
				)
			},
		},
		{
			name: "web job cannot declare null strategy",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"  web:\n    name: Web\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    defaults:",
					"  web:\n    name: Web\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    strategy: null\n    defaults:",
				)
			},
		},
		{
			name: "run step cannot declare null with",
			mutate: func(t *testing.T, workflow string) string {
				return replaceWorkflowFirst(t, workflow,
					"      - name: Verify Bun version\n        run: test \"$(bun --version)\" = \"1.3.11\"",
					"      - name: Verify Bun version\n        with: null\n        run: test \"$(bun --version)\" = \"1.3.11\"",
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := []byte(tt.mutate(t, raw))
			err := validateCIWorkflow(mutated)
			if err == nil {
				t.Fatal("validator accepted mutated workflow")
			}
			if tt.wantError != "" && !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("validation error %q does not contain %q", err, tt.wantError)
			}
		})
	}
}

func replaceWorkflowFirst(t *testing.T, workflow, old, replacement string) string {
	t.Helper()
	if !strings.Contains(workflow, old) {
		t.Fatalf("workflow mutation source is missing: %q", old)
	}
	return strings.Replace(workflow, old, replacement, 1)
}

func validateCIWorkflow(raw []byte) error {
	document, workflow, err := decodeCIWorkflow(raw)
	if err != nil {
		return err
	}
	if err := validateAllUses(document, raw); err != nil {
		return err
	}
	if err := validateCIJobAndStepKeys(document); err != nil {
		return err
	}
	if workflow.Name != "CI" {
		return fmt.Errorf("workflow name = %q, want CI", workflow.Name)
	}
	if err := validateCITriggers(workflow.On); err != nil {
		return err
	}
	wantPermissions := map[string]string{"contents": "read"}
	if !reflect.DeepEqual(workflow.Permissions, wantPermissions) {
		return fmt.Errorf("permissions = %#v, want exactly %#v", workflow.Permissions, wantPermissions)
	}
	wantConcurrency := ciConcurrency{
		Group:            "${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}",
		CancelInProgress: true,
	}
	if workflow.Concurrency != wantConcurrency {
		return fmt.Errorf("concurrency = %#v, want %#v", workflow.Concurrency, wantConcurrency)
	}
	if len(workflow.Jobs) != len(expectedCIJobs) {
		return fmt.Errorf("workflow jobs = %d, want exactly %d", len(workflow.Jobs), len(expectedCIJobs))
	}
	for name, want := range expectedCIJobs {
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

func validateCIJobAndStepKeys(document *yaml.Node) error {
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return fmt.Errorf("workflow YAML must contain one mapping document")
	}
	root := document.Content[0]
	jobs, ok := mappingValue(root, "jobs")
	if !ok || jobs.Kind != yaml.MappingNode {
		return fmt.Errorf("workflow jobs must be a mapping")
	}
	for _, name := range []string{"go-quality", "go-unit", "web"} {
		wantJob := expectedCIJobs[name]
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

func decodeCIWorkflow(raw []byte) (*yaml.Node, ciWorkflow, error) {
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

func validateCITriggers(triggers map[string]yaml.Node) error {
	for _, name := range []string{"pull_request", "push", "workflow_dispatch"} {
		if _, ok := triggers[name]; !ok {
			return fmt.Errorf("workflow trigger %q is missing", name)
		}
	}
	if len(triggers) != 3 {
		return fmt.Errorf("workflow trigger keys = %d, want exactly pull_request, push, and workflow_dispatch", len(triggers))
	}
	for _, name := range []string{"pull_request", "workflow_dispatch"} {
		trigger := triggers[name]
		if !isEmptyTrigger(trigger) {
			return fmt.Errorf("workflow trigger %q must not contain filters or options", name)
		}
	}

	push := triggers["push"]
	if push.Kind != yaml.MappingNode || len(push.Content) != 2 {
		return fmt.Errorf("push trigger must contain only branches: [main]")
	}
	if key := push.Content[0]; key.Kind != yaml.ScalarNode || key.Value != "branches" {
		return fmt.Errorf("push trigger must contain only branches: [main]")
	}
	branches := push.Content[1]
	if branches.Kind != yaml.SequenceNode || len(branches.Content) != 1 || branches.Content[0].Value != "main" {
		return fmt.Errorf("push branches must be exactly [main]")
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
