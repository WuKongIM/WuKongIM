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
	"binary-release-publish.yml":               "Safety Automation - Publish WuKongIM Binaries",
	"chat-lifecycle-rehearsal.yml":             "Agent Tool - Start Chat Lifecycle Rehearsal",
	"chat-lifecycle-rehearsal-finalize.yml":    "Safety Automation - Finalize Chat Lifecycle Rehearsals",
	"chat-lifecycle-formal.yml":                "Safety Automation - Start Fresh Formal Chat Lifecycle",
	"chat-lifecycle-formal-finalize.yml":       "Safety Automation - Finalize Formal Chat Lifecycle Runs",
	"chat-lifecycle-stop.yml":                  "Agent Tool - Stop Chat Lifecycle Request",
	"cloud-deployment-activate.yml":            "Agent Tool - Activate Cloud Deployment",
	"cloud-deployment-bundle.yml":              "Agent Tool - Build Cloud Deployment Bundle",
	"cloud-lease-analyze.yml":                  "Agent Tool - Analyze Chat Lifecycle Cloud Lease",
	"cloud-lease-observe.yml":                  "Agent Tool - Inspect Cloud Lease",
	"cloud-lease-oidc-setup.yml":               "Agent Tool - Configure Cloud Lease OIDC Roles",
	"cloud-lease-provision.yml":                "Agent Tool - Provision Cloud Lease",
	"cloud-lease-release.yml":                  "Safety Automation - Release Cloud Leases",
	"cloud-sim-analyze.yml":                    "Agent Tool - Analyze Cloud Simulation",
	"cloud-sim-cleanup.yml":                    "Safety Automation - Reconcile Cloud Simulation Resources",
	"cloud-sim-monitor.yml":                    "Safety Automation - Patrol Cloud Simulation Runs",
	"cloud-sim-oidc-subject.yml":               "Agent Tool - Configure Cloud Simulation OIDC Subject",
	"cloud-sim-provision.yml":                  "Agent Tool - Provision Cloud Simulation",
	"docker-image-publish.yml":                 "Safety Automation - Publish Docker Images",
	"easysdk-release-acceptance.yml":           "Safety Automation - EasySDK Released Package Acceptance",
	"issue-agent-engineer.yml":                 "Agent Tool - Issue Engineer",
	"issue-agent-pr-signal.yml":                "Safety Automation - Issue Agent PR Signal",
	"issue-agent.yml":                          "Safety Automation - GitHub Issue Agent",
	"manager-browser-smoke.yml":                "Safety Automation - Manager Browser Smoke",
	"native-package-preview.yml":               "Safety Automation - Validate Native Package Preview",
	"review-agent-pr-signal.yml":               "Safety Automation - Review Agent PR Signal",
	"review-agent-run.yml":                     "Agent Tool - Review Pull Request",
	"review-agent.yml":                         "Safety Automation - Review Agent Controller",
	"three-node-chat-lifecycle-regression.yml": "Safety Automation - Three-Node Chat Lifecycle Regression",
}

func TestCloudLeaseProvisionRejectsGitHubOwnedRepairPlans(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "cloud-lease-provision.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`plan_stage="$(jq -er '.tags.stage // ""' "$RUNNER_TEMP/plan.json")"`,
		`[[ "$plan_stage" != repair ]]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generic Provision workflow still permits GitHub-owned repair acquisition; missing %q", want)
		}
	}
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

func TestEasySDKAndroidEmulatorScriptHasIndependentCommands(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string `yaml:"uses"`
				With struct {
					Script string `yaml:"script"`
				} `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(
		t,
		yaml.Unmarshal(readWorkflow(t, "easysdk-release-acceptance.yml"), &workflow),
	)

	found := false
	for _, step := range workflow.Jobs["android-release"].Steps {
		if !strings.HasPrefix(step.Uses, "reactivecircus/android-emulator-runner@") {
			continue
		}
		found = true
		for lineNumber, line := range strings.Split(step.With.Script, "\n") {
			require.Falsef(
				t,
				strings.HasSuffix(strings.TrimSpace(line), `\`),
				"android-emulator-runner executes script line %d independently; move multiline commands into a helper script",
				lineNumber+1,
			)
		}
	}
	require.True(t, found, "EasySDK Android emulator step not found")
}

func TestEasySDKFlutterReleaseSmokeIsBounded(t *testing.T) {
	root := repoRoot(t)
	workflow := string(readWorkflow(t, "easysdk-release-acceptance.yml"))
	helperPath := filepath.Join(
		root,
		"test",
		"easysdk-release",
		"flutter",
		"run-release-smoke.sh",
	)

	require.Contains(
		t,
		workflow,
		`"${GITHUB_WORKSPACE}/test/easysdk-release/flutter/run-release-smoke.sh"`,
		"the Flutter release smoke must use its bounded runner",
	)
	require.NotContains(
		t,
		workflow,
		"flutter test integration_test/release_smoke_test.dart",
		"the workflow must not bypass the bounded Flutter runner",
	)

	helper := readFile(t, helperPath)
	require.Contains(
		t,
		helper,
		`FLUTTER_RELEASE_SMOKE_TIMEOUT_SECONDS:-480`,
		"the timeout must cover observed healthy macOS runner variance",
	)
	require.Contains(t, helper, "FLUTTER_RELEASE_SMOKE_TIMEOUT")
	require.NotContains(
		t,
		helper,
		"FLUTTER_RELEASE_SMOKE_RETRY",
		"a retry must use a fresh runner instead of reusing degraded runtime state",
	)

	testBody := readFile(
		t,
		filepath.Join(filepath.Dir(helperPath), "release_smoke_test.dart"),
	)
	require.Contains(
		t,
		testBody,
		"await sdk.connect().timeout(",
		"the SDK connection must have its own Dart-level deadline",
	)
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
