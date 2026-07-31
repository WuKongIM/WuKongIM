package scripts_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

var workflowCatalog = map[string]string{
	"cloud-sim-analyze.yml":                "Agent Tool - Analyze Cloud Simulation",
	"cloud-sim-cleanup.yml":                "Safety Automation - Reconcile Cloud Simulation Resources",
	"cloud-sim-monitor.yml":                "Safety Automation - Patrol Cloud Simulation Runs",
	"cloud-sim-oidc-subject.yml":           "Agent Tool - Configure Cloud Simulation OIDC Subject",
	"cloud-sim-provision.yml":              "Agent Tool - Provision Cloud Simulation",
	"issue-agent-engineer.yml":             "Agent Tool - Issue Engineer",
	"issue-agent-pr-signal.yml":            "Safety Automation - Issue Agent PR Signal",
	"issue-agent.yml":                      "Safety Automation - GitHub Issue Agent",
	"review-agent-pr-signal.yml":           "Safety Automation - Review Agent PR Signal",
	"review-agent-issue-signal.yml":        "Safety Automation - Review Agent Issue Signal",
	"review-agent-run.yml":                 "Agent Tool - Review Pull Request",
	"review-agent.yml":                     "Safety Automation - Review Agent Controller",
}

var externalActionPattern = regexp.MustCompile(
	`(?m)^\s*uses:\s*([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@([^\s#]+)`,
)
var fullCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func TestGitHubWorkflowCatalogIsComplete(t *testing.T) {
	root := repoRoot(t)
	catalogBody := readFile(
		t,
		filepath.Join(root, ".github", "workflows", "README.md"),
	)
	paths, err := filepath.Glob(
		filepath.Join(root, ".github", "workflows", "*.yml"),
	)
	require.NoError(t, err)
	require.Len(t, paths, len(workflowCatalog))
	for _, path := range paths {
		name := filepath.Base(path)
		want, ok := workflowCatalog[name]
		require.True(t, ok, "uncataloged workflow %s", name)
		raw := readFile(t, path)
		require.True(t, strings.HasPrefix(raw, "name: "+want+"\n"), name)
		require.Contains(t, catalogBody, "`"+name+"`")
		require.Contains(t, catalogBody, "`"+want+"`")
	}
}

func TestGitHubWorkflowExternalActionsUseFullCommitPins(t *testing.T) {
	root := repoRoot(t)
	paths, err := filepath.Glob(
		filepath.Join(root, ".github", "workflows", "*.yml"),
	)
	require.NoError(t, err)
	for _, path := range paths {
		raw := readFile(t, path)
		for _, match := range externalActionPattern.FindAllStringSubmatch(
			raw,
			-1,
		) {
			require.Truef(
				t,
				fullCommitPattern.MatchString(match[2]),
				"%s action %s must use a full commit pin, got %q",
				filepath.Base(path),
				match[1],
				match[2],
			)
		}
	}
}

func TestLegacyAutomaticTestWorkflowsAreAbsent(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"ci.yml", "nightly.yml"} {
		_, err := os.Stat(
			filepath.Join(root, ".github", "workflows", name),
		)
		require.ErrorIs(t, err, os.ErrNotExist, name)
	}
}

func TestOnlyCatalogedSafetyWorkflowsUseAutomaticTriggers(t *testing.T) {
	automatic := map[string]struct{}{
		"issues": {}, "issue_comment": {}, "pull_request": {},
		"pull_request_target": {}, "pull_request_review": {},
		"pull_request_review_comment": {}, "schedule": {}, "workflow_run": {},
	}
	for file, name := range workflowCatalog {
		var workflow struct {
			On map[string]yaml.Node `yaml:"on"`
		}
		require.NoError(t, yaml.Unmarshal(readWorkflow(t, file), &workflow))
		for trigger := range workflow.On {
			if _, ok := automatic[trigger]; ok {
				require.Truef(
					t,
					strings.HasPrefix(name, "Safety Automation - "),
					"%s uses automatic trigger %s",
					file,
					trigger,
				)
			}
		}
	}
}

func readWorkflow(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join(repoRoot(t), ".github", "workflows", name)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}
