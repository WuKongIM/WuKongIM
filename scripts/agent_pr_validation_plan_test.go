package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	agentValidationRequestRunID = "9001"
	agentValidationMergeSHA     = "ffffffffffffffffffffffffffffffffffffffff"
	agentValidationGateRunID    = "7001"
)

func agentValidationRequestStatus(prNumber int) string {
	return `{
  "state": "pending",
  "context": "Agent Validation Request / PR #` + strconv.Itoa(prNumber) + ` / Gate #` + agentValidationGateRunID + `",
  "target_url": "https://github.com/WuKongIM/WuKongIM/actions/runs/9001"
}`
}

func TestAgentPRValidationPlanAcceptsGoFastForGoChange(t *testing.T) {
	headSHA := strings.Repeat("a", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 42,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [
    {"name": "agent-ci/go-fast"},
    {"name": "agent-ci/run"}
  ]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 101,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"medium\",\"selected_suites\":[\"go-fast\"],\"reason\":\"Go runtime change\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": "internal/app/app.go"}
]`)
	statusesPath := writeAgentValidationFixture(
		t,
		dir,
		"statuses.json",
		agentValidationStatuses(t, 42, `[]`),
	)
	outputPath := filepath.Join(dir, "github-output")
	planPath := filepath.Join(dir, "validated-plan.json")

	newCommand := func() *exec.Cmd {
		return agentValidationPlanCommand(
			t,
			prPath,
			commentsPath,
			filesPath,
			statusesPath,
			"tangtaoit",
			headSHA,
			outputPath,
			planPath,
		)
	}
	command := newCommand()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("validate Agent PR plan: %v\n%s", err, output)
	}

	gotOutput, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read GitHub output: %v", err)
	}
	wantLines := []string{
		"docs_only=false",
		"go_fast=true",
		"web=false",
		"demo=false",
		"go_race=false",
		"go_integration=false",
		"go_e2e=false",
		"three_node_smoke=false",
		"plan_comment_id=101",
		"retry_of_run_id=",
	}
	for _, line := range wantLines {
		if !strings.Contains(string(gotOutput), line+"\n") {
			t.Errorf("GitHub output missing %q:\n%s", line, gotOutput)
		}
	}

	validatedPlan, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read validated plan: %v", err)
	}
	if !strings.Contains(string(validatedPlan), `"reason": "Go runtime change"`) {
		t.Fatalf("validated plan does not preserve the reason:\n%s", validatedPlan)
	}
	if err := os.WriteFile(statusesPath, []byte("[]"), 0o600); err != nil {
		t.Fatalf("remove request authorization status: %v", err)
	}
	if output, err := newCommand().CombinedOutput(); err == nil {
		t.Fatalf("validation unexpectedly accepted a direct dispatch without request authorization:\n%s", output)
	}
	t.Run("production scripts require integration", testAgentPRValidationPlanRequiresGoIntegrationForProductionScript)
}

func testAgentPRValidationPlanRequiresGoIntegrationForProductionScript(t *testing.T) {
	headSHA := strings.Repeat("c", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 57,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [
    {"name": "agent-ci/go-fast"},
    {"name": "agent-ci/run"}
  ]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 157,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"medium\",\"selected_suites\":[\"go-fast\"],\"reason\":\"Production shell script change\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": "scripts/cloud-sim/analyze.sh"}
]`)
	statusesPath := writeAgentValidationFixture(
		t,
		dir,
		"statuses.json",
		agentValidationStatuses(t, 57, `[]`),
	)
	outputPath := filepath.Join(dir, "github-output")
	planPath := filepath.Join(dir, "validated-plan.json")

	command := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		outputPath,
		planPath,
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("validation accepted a production script without go-integration:\n%s", output)
	}

	writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 57,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [
    {"name": "agent-ci/go-fast"},
    {"name": "agent-ci/go-integration"},
    {"name": "agent-ci/run"}
  ]
}`)
	writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 158,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"medium\",\"selected_suites\":[\"go-fast\",\"go-integration\"],\"reason\":\"Production shell script change\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	command = agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		outputPath,
		planPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("validation rejected production script with go-integration: %v\n%s", err, output)
	}
	gotOutput, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read GitHub output: %v", err)
	}
	for _, want := range []string{"go_fast=true\n", "go_integration=true\n"} {
		if !strings.Contains(string(gotOutput), want) {
			t.Fatalf("GitHub output missing %q:\n%s", want, gotOutput)
		}
	}

	writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": "scripts/example_integration_test.go"}
]`)
	writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 57,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [
    {"name": "agent-ci/go-fast"},
    {"name": "agent-ci/run"}
  ]
}`)
	writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 159,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"medium\",\"selected_suites\":[\"go-fast\"],\"reason\":\"Scripts integration test change\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	if output, err := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		outputPath,
		planPath,
	).CombinedOutput(); err == nil {
		t.Fatalf("validation accepted a scripts integration test without go-integration:\n%s", output)
	}

	writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": "scripts/channel-metrics-summary.awk"}
]`)
	if output, err := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		outputPath,
		planPath,
	).CombinedOutput(); err != nil {
		t.Fatalf("validation required go-integration solely for an AWK contract: %v\n%s", err, output)
	}
}

func TestAgentPRValidationPlanRejectsDocsOnlyForWorkflowChange(t *testing.T) {
	headSHA := strings.Repeat("b", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 43,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [
    {"name": "agent-ci/docs-only"},
    {"name": "agent-ci/run"}
  ]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 102,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"low\",\"selected_suites\":[\"docs-only\"],\"reason\":\"Documentation change\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": ".github/workflows/README.md"}
]`)
	statusesPath := writeAgentValidationFixture(
		t,
		dir,
		"statuses.json",
		agentValidationStatuses(t, 43, `[]`),
	)

	command := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		filepath.Join(dir, "github-output"),
		filepath.Join(dir, "validated-plan.json"),
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("docs-only validation unexpectedly accepted a Workflow change:\n%s", output)
	}
}

func TestAgentPRValidationPlanStartsFreshGenerationForSameHead(t *testing.T) {
	headSHA := strings.Repeat("1", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 51,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [{"name": "agent-ci/go-fast"}]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 112,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"medium\",\"selected_suites\":[\"go-fast\"],\"reason\":\"PR metadata changed and requires a fresh validation generation\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": "internal/app/app.go"}
]`)
	statusesPath := writeAgentValidationFixture(
		t,
		dir,
		"statuses.json",
		agentValidationStatuses(t, 51, `[
  {
    "state": "success",
    "context": "Agent Validation Evidence / PR #51 / Gate #6001",
    "target_url": "https://github.com/WuKongIM/WuKongIM/actions/runs/555"
  }
]`),
	)

	command := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		filepath.Join(dir, "github-output"),
		filepath.Join(dir, "validated-plan.json"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fresh validation generation was blocked by old evidence: %v\n%s", err, output)
	}
}

func TestAgentPRValidationPlanRejectsDocsOnlyForProductionFileRenamedToDocs(t *testing.T) {
	headSHA := strings.Repeat("d", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 50,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [
    {"name": "agent-ci/docs-only"},
    {"name": "agent-ci/run"}
  ]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 110,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"low\",\"selected_suites\":[\"docs-only\"],\"reason\":\"Production file was moved into documentation\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {
    "filename": "docs/client.md",
    "previous_filename": "pkg/client/client.go",
    "status": "renamed"
  }
]`)
	statusesPath := writeAgentValidationFixture(
		t,
		dir,
		"statuses.json",
		agentValidationStatuses(t, 50, `[]`),
	)

	command := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		filepath.Join(dir, "github-output"),
		filepath.Join(dir, "validated-plan.json"),
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("docs-only validation accepted a production path hidden by rename:\n%s", output)
	}

	prPath = writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 50,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [
    {"name": "agent-ci/go-fast"},
    {"name": "agent-ci/run"}
  ]
}`)
	commentsPath = writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 111,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"medium\",\"selected_suites\":[\"go-fast\"],\"reason\":\"Production source path requires the Go fast suite\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	command = agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		filepath.Join(dir, "github-output"),
		filepath.Join(dir, "validated-plan.json"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go-fast validation rejected a valid production rename inventory: %v\n%s", err, output)
	}
}

func TestAgentPRValidationPlanRequiresRetryEvidenceAfterFailure(t *testing.T) {
	headSHA := strings.Repeat("c", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 44,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [{"name": "agent-ci/go-fast"}]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 103,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"medium\",\"selected_suites\":[\"go-fast\"],\"reason\":\"Retry without evidence\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": "pkg/cluster/server.go"}
]`)
	statusesPath := writeAgentValidationFixture(t, dir, "statuses.json", agentValidationStatuses(t, 44, `[
  {
    "state": "failure",
    "context": "Agent Validation Evidence / PR #44 / Gate #7001",
    "target_url": "https://github.com/WuKongIM/WuKongIM/actions/runs/555"
  }
]`))

	command := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		filepath.Join(dir, "github-output"),
		filepath.Join(dir, "validated-plan.json"),
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("validation unexpectedly accepted a retry without evidence:\n%s", output)
	}
}

func TestAgentPRValidationPlanAcceptsSingleEvidenceBoundRetry(t *testing.T) {
	headSHA := strings.Repeat("f", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 47,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [{"name": "agent-ci/go-fast"}]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 106,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"medium\",\"selected_suites\":[\"go-fast\"],\"reason\":\"retry-evidence:runner: Runner was interrupted before tests started\",\"retry_of_run_id\":555}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": "pkg/cluster/server.go"}
]`)
	statusesPath := writeAgentValidationFixture(t, dir, "statuses.json", agentValidationStatuses(t, 47, `[
  {
    "state": "failure",
    "context": "Agent Validation Evidence / PR #47 / Gate #7001",
    "target_url": "https://github.com/WuKongIM/WuKongIM/actions/runs/555"
  },
  {
    "state": "pending",
    "context": "Agent Validation Evidence / PR #47 / Gate #7001",
    "target_url": "https://github.com/WuKongIM/WuKongIM/actions/runs/555"
  }
]`))
	outputPath := filepath.Join(dir, "github-output")

	newCommand := func() *exec.Cmd {
		return agentValidationPlanCommand(
			t,
			prPath,
			commentsPath,
			filesPath,
			statusesPath,
			"tangtaoit",
			headSHA,
			outputPath,
			filepath.Join(dir, "validated-plan.json"),
		)
	}
	command := newCommand()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("validate evidence-bound retry: %v\n%s", err, output)
	}
	gotOutput, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read GitHub output: %v", err)
	}
	if !strings.Contains(string(gotOutput), "retry_of_run_id=555\n") {
		t.Fatalf("retry run ID missing from GitHub output:\n%s", gotOutput)
	}

	wrongMergeCommand := newCommand()
	attemptRunsPath := filepath.Join(dir, "attempt-runs.json")
	attemptRuns, err := os.ReadFile(attemptRunsPath)
	if err != nil {
		t.Fatalf("read retry run metadata: %v", err)
	}
	wrongMergeRuns := strings.Replace(
		string(attemptRuns),
		agentValidationMergeSHA,
		strings.Repeat("0", 40),
		1,
	)
	if err := os.WriteFile(attemptRunsPath, []byte(wrongMergeRuns), 0o600); err != nil {
		t.Fatalf("write wrong-merge retry metadata: %v", err)
	}
	if output, err := wrongMergeCommand.CombinedOutput(); err == nil {
		t.Fatalf("validation unexpectedly accepted retry evidence for another test-merge SHA:\n%s", output)
	}

	comments, err := os.ReadFile(commentsPath)
	if err != nil {
		t.Fatalf("read retry comment fixture: %v", err)
	}
	unsupportedNarrative := strings.Replace(
		string(comments),
		"retry-evidence:runner: ",
		"",
		1,
	)
	if err := os.WriteFile(commentsPath, []byte(unsupportedNarrative), 0o600); err != nil {
		t.Fatalf("write unsupported retry comment fixture: %v", err)
	}
	if output, err := newCommand().CombinedOutput(); err == nil {
		t.Fatalf("validation unexpectedly accepted an unstructured retry narrative:\n%s", output)
	}
	if err := os.WriteFile(commentsPath, comments, 0o600); err != nil {
		t.Fatalf("restore structured retry comment fixture: %v", err)
	}
	cancelledStatuses := agentValidationStatuses(t, 47, `[
  {
    "state": "pending",
    "context": "Agent Validation Evidence / PR #47 / Gate #7001",
    "target_url": "https://github.com/WuKongIM/WuKongIM/actions/runs/555"
  }
]`)
	if err := os.WriteFile(statusesPath, []byte(cancelledStatuses), 0o600); err != nil {
		t.Fatalf("write cancelled retry status fixture: %v", err)
	}
	if output, err := newCommand().CombinedOutput(); err != nil {
		t.Fatalf("validation rejected a terminally cancelled evidence-bound retry: %v\n%s", err, output)
	}

	failedStatuses := agentValidationStatuses(t, 47, `[
  {
    "state": "failure",
    "context": "Agent Validation Evidence / PR #47 / Gate #7001",
    "target_url": "https://github.com/WuKongIM/WuKongIM/actions/runs/555"
  }
]`)
	if err := os.WriteFile(statusesPath, []byte(failedStatuses), 0o600); err != nil {
		t.Fatalf("write failed retry status fixture: %v", err)
	}
	cancelledRunCommand := newCommand()
	attemptRuns, err = os.ReadFile(attemptRunsPath)
	if err != nil {
		t.Fatalf("read failed retry run metadata: %v", err)
	}
	cancelledRun := strings.Replace(
		string(attemptRuns),
		`"conclusion":"failure"`,
		`"conclusion":"cancelled"`,
		1,
	)
	if err := os.WriteFile(attemptRunsPath, []byte(cancelledRun), 0o600); err != nil {
		t.Fatalf("write cancelled retry run metadata: %v", err)
	}
	if output, err := cancelledRunCommand.CombinedOutput(); err != nil {
		t.Fatalf("validation rejected failed evidence from a cancelled run: %v\n%s", err, output)
	}
}

func TestAgentPRValidationPlanRejectsThirdAttemptAfterSuccessfulRetry(t *testing.T) {
	headSHA := strings.Repeat("d", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 45,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [{"name": "agent-ci/go-fast"}]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 104,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"medium\",\"selected_suites\":[\"go-fast\"],\"reason\":\"retry-evidence:known-flake: Prior retry also failed and must be terminal\",\"retry_of_run_id\":555}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": "pkg/cluster/server.go"}
]`)
	statusesPath := writeAgentValidationFixture(t, dir, "statuses.json", agentValidationStatuses(t, 45, `[
  {
    "state": "success",
    "context": "Agent Validation Evidence / PR #45 / Gate #7001",
    "target_url": "https://github.com/WuKongIM/WuKongIM/actions/runs/556"
  },
  {
    "state": "failure",
    "context": "Agent Validation Evidence / PR #45 / Gate #7001",
    "target_url": "https://github.com/WuKongIM/WuKongIM/actions/runs/555"
  }
]`))

	command := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		filepath.Join(dir, "github-output"),
		filepath.Join(dir, "validated-plan.json"),
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("validation unexpectedly accepted a third attempt after a retry:\n%s", output)
	}
}

func TestAgentPRValidationPlanRequiresGoFastForCodeOwnersChange(t *testing.T) {
	headSHA := strings.Repeat("e", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 46,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [{"name": "agent-ci/go-race"}]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 105,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"high\",\"selected_suites\":[\"go-race\"],\"reason\":\"CODEOWNERS must exercise workflow contracts\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": ".github/CODEOWNERS"}
]`)
	statusesPath := writeAgentValidationFixture(
		t,
		dir,
		"statuses.json",
		agentValidationStatuses(t, 46, `[]`),
	)

	command := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		filepath.Join(dir, "github-output"),
		filepath.Join(dir, "validated-plan.json"),
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("validation unexpectedly accepted CODEOWNERS without go-fast:\n%s", output)
	}
}

func TestAgentPRValidationPlanRequiresGoFastForRootDockerConfig(t *testing.T) {
	headSHA := strings.Repeat("2", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 49,
  "changed_files": 1,
  "head": {"sha": "`+headSHA+`"},
  "labels": [{"name": "agent-ci/go-race"}]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 108,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"medium\",\"selected_suites\":[\"go-race\"],\"reason\":\"Root Docker configuration changed\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": "Dockerfile"}
]`)
	statusesPath := writeAgentValidationFixture(
		t,
		dir,
		"statuses.json",
		agentValidationStatuses(t, 49, `[]`),
	)

	command := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		filepath.Join(dir, "github-output"),
		filepath.Join(dir, "validated-plan.json"),
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("validation unexpectedly accepted root Docker config without go-fast:\n%s", output)
	}
}

func TestAgentPRValidationPlanRejectsIncompleteChangedFileInventory(t *testing.T) {
	headSHA := strings.Repeat("1", 40)
	dir := t.TempDir()
	prPath := writeAgentValidationFixture(t, dir, "pr.json", `{
  "number": 48,
  "changed_files": 2,
  "head": {"sha": "`+headSHA+`"},
  "labels": [{"name": "agent-ci/docs-only"}]
}`)
	commentsPath := writeAgentValidationFixture(t, dir, "comments.json", `[
  {
    "id": 107,
    "user": {"login": "tangtaoit"},
    "body": "<!-- agent-validation-plan:v1\n{\"schema_version\":1,\"head_sha\":\"`+headSHA+`\",\"risk\":\"low\",\"selected_suites\":[\"docs-only\"],\"reason\":\"The incomplete inventory must fail closed\",\"retry_of_run_id\":null}\n-->\n\n## Agent validation plan"
  }
]`)
	filesPath := writeAgentValidationFixture(t, dir, "files.json", `[
  {"filename": "docs/development/CI.md"}
]`)
	statusesPath := writeAgentValidationFixture(
		t,
		dir,
		"statuses.json",
		agentValidationStatuses(t, 48, `[]`),
	)

	command := agentValidationPlanCommand(
		t,
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		"tangtaoit",
		headSHA,
		filepath.Join(dir, "github-output"),
		filepath.Join(dir, "validated-plan.json"),
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("validation unexpectedly accepted an incomplete changed-file inventory:\n%s", output)
	}
}

func writeAgentValidationFixture(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func agentValidationPlanCommand(
	t *testing.T,
	prPath string,
	commentsPath string,
	filesPath string,
	statusesPath string,
	actor string,
	headSHA string,
	outputPath string,
	planPath string,
) *exec.Cmd {
	t.Helper()
	statuses, err := os.ReadFile(statusesPath)
	if err != nil {
		t.Fatalf("read Agent validation statuses: %v", err)
	}
	var decoded []struct {
		State     string `json:"state"`
		Context   string `json:"context"`
		TargetURL string `json:"target_url"`
	}
	if err := json.Unmarshal(statuses, &decoded); err != nil {
		t.Fatalf("parse Agent validation statuses: %v", err)
	}
	prContents, err := os.ReadFile(prPath)
	if err != nil {
		t.Fatalf("read Agent validation PR fixture: %v", err)
	}
	var pr struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(prContents, &pr); err != nil {
		t.Fatalf("parse Agent validation PR fixture: %v", err)
	}
	evidenceContext := "Agent Validation Evidence / PR #" + strconv.Itoa(pr.Number) +
		" / Gate #" + agentValidationGateRunID
	seen := make(map[int64]struct{})
	runs := make([]map[string]any, 0, len(decoded))
	for _, status := range decoded {
		if status.Context != evidenceContext {
			continue
		}
		const marker = "/actions/runs/"
		index := strings.LastIndex(status.TargetURL, marker)
		if index < 0 {
			continue
		}
		runID, err := strconv.ParseInt(status.TargetURL[index+len(marker):], 10, 64)
		if err != nil {
			t.Fatalf("parse Agent validation run ID: %v", err)
		}
		if _, ok := seen[runID]; ok {
			continue
		}
		seen[runID] = struct{}{}
		conclusion := status.State
		switch status.State {
		case "error":
			conclusion = "failure"
		case "pending":
			conclusion = "cancelled"
		}
		runs = append(runs, map[string]any{
			"id":         runID,
			"status":     "completed",
			"conclusion": conclusion,
			"path":       ".github/workflows/agent-pr-validation.yml",
			"event":      "repository_dispatch",
			"display_title": "Agent PR #" + strconv.Itoa(pr.Number) +
				" validation head " + headSHA + " merge " + agentValidationMergeSHA +
				" gate " + agentValidationGateRunID + " request " +
				agentValidationRequestRunID,
		})
	}
	attemptRuns, err := json.Marshal(runs)
	if err != nil {
		t.Fatalf("encode Agent validation attempt runs: %v", err)
	}
	attemptRunsPath := writeAgentValidationFixture(
		t,
		filepath.Dir(statusesPath),
		"attempt-runs.json",
		string(attemptRuns),
	)
	return exec.Command(
		"bash",
		filepath.Join(repoRoot(t), "scripts", "agent-pr-validation-plan.sh"),
		prPath,
		commentsPath,
		filesPath,
		statusesPath,
		attemptRunsPath,
		actor,
		headSHA,
		agentValidationMergeSHA,
		agentValidationGateRunID,
		outputPath,
		planPath,
	)
}

func agentValidationStatuses(t *testing.T, prNumber int, statuses string) string {
	t.Helper()
	var existing []json.RawMessage
	if err := json.Unmarshal([]byte(statuses), &existing); err != nil {
		t.Fatalf("parse Agent validation status fixture: %v", err)
	}
	all := append(
		[]json.RawMessage{json.RawMessage(agentValidationRequestStatus(prNumber))},
		existing...,
	)
	encoded, err := json.Marshal(all)
	if err != nil {
		t.Fatalf("encode Agent validation status fixture: %v", err)
	}
	return string(encoded)
}
