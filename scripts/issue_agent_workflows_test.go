package scripts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	codexActionPin = "openai/codex-action@52fe01ec70a42f454c9d2ebd47598f9fd6893d56"
	codexVersion   = "0.146.0"
)

func TestIssueAgentBugFormKeepsConcreteRequiredInputs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(
		repoRoot(t), ".github", "ISSUE_TEMPLATE", "bug.yml",
	))
	require.NoError(t, err)
	var form struct {
		Labels []string `yaml:"labels"`
		Body   []struct {
			Type        string `yaml:"type"`
			ID          string `yaml:"id"`
			Validations struct {
				Required bool `yaml:"required"`
			} `yaml:"validations"`
		} `yaml:"body"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &form))
	require.Contains(t, form.Labels, "bug")
	var required []string
	for _, field := range form.Body {
		if field.Validations.Required {
			require.NotEqual(t, "checkboxes", field.Type)
			required = append(required, field.ID)
		}
	}
	require.Equal(t, []string{
		"environment", "reproduction", "expected_actual",
	}, required)
	require.Contains(t, string(raw), "id: affected_version")
	require.Contains(t, strings.ToLower(string(raw)), "credential")
}

func TestIssueAgentV2IsTheOnlyWorkflow(t *testing.T) {
	root := repoRoot(t)
	for _, removed := range []string{
		"issue-agent-control.yml",
		"issue-agent-reconcile.yml",
		"issue-agent-run.yml",
	} {
		_, err := os.Stat(filepath.Join(root, ".github", "workflows", removed))
		require.ErrorIs(t, err, os.ErrNotExist, removed)
	}
	for _, current := range []string{
		"issue-agent.yml",
		"issue-agent-pr-signal.yml",
		"issue-agent-engineer.yml",
	} {
		raw, err := os.ReadFile(
			filepath.Join(root, ".github", "workflows", current),
		)
		require.NoError(t, err)
		var document any
		require.NoError(t, yaml.Unmarshal(raw, &document), current)
		require.NotContains(t, string(raw), "pull_request_target")
		require.NotContains(t, string(raw), "persist-credentials: true")
	}
}

func TestIssueAgentPRSignalHasNoAuthorityOrCandidateExecution(t *testing.T) {
	signal := readIssueAgentFile(
		t,
		".github/workflows/issue-agent-pr-signal.yml",
	)
	require.Contains(t, signal, "pull_request:")
	require.Contains(t, signal, "pull_request_review:")
	require.Contains(t, signal, "pull_request_review_comment:")
	require.Contains(t, signal, "permissions: {}")
	require.NotContains(t, signal, "secrets.")
	require.NotContains(t, signal, "uses:")
	require.NotContains(t, signal, "actions/checkout")
	require.NotContains(t, signal, "issue-agent-publisher")
	require.NotContains(t, signal, "OPENAI_API_KEY")

	controller := readIssueAgentFile(t, ".github/workflows/issue-agent.yml")
	require.Contains(t, controller, "workflow_run:")
	require.Contains(t, controller,
		`workflows: ["Safety Automation - Issue Agent PR Signal"]`)
	require.Contains(t, controller,
		"github.event.workflow_run.conclusion == 'success'")
	require.Contains(t, controller,
		"startsWith(github.event.workflow_run.head_branch, 'agent/issue-')")
	require.Contains(t, controller,
		"github.event.workflow_run.head_repository.full_name == github.repository")
	require.NotContains(t, controller, "\n  pull_request:")
	require.NotContains(t, controller, "\n  pull_request_review:")
	require.NotContains(t, controller, "\n  pull_request_review_comment:")
}

func TestIssueAgentCodexActionRunsTheWholeEphemeralTask(t *testing.T) {
	raw := readIssueAgentFile(t, ".github/workflows/issue-agent-engineer.yml")
	require.Equal(t, 1, strings.Count(raw, codexActionPin))
	require.Contains(t, raw, "codex-version: "+codexVersion)
	require.Contains(t, raw, `jq -er .task.kind`)
	require.Contains(t, raw, `engineer) cp "$RUNNER_TEMP/engineer.md"`)
	require.Contains(t, raw, `review) cp "$RUNNER_TEMP/review.md"`)
	require.Contains(t, raw, "engineer-result.schema.json")
	require.Contains(t, raw, `["--ephemeral"`)
	require.Contains(t, raw, "sandbox: workspace-write")
	require.Contains(t, raw, "sandbox_workspace_write.network_access=true")
	require.Contains(t, raw, "safety-strategy: drop-sudo")
	require.Contains(t, raw,
		"allow-bot-users: ${{ vars.ISSUE_AGENT_APP_LOGIN }}")
	require.NotContains(t, raw, "allow-bots: true")
	require.Contains(t, raw, "model: openai/gpt-5.6-sol")
	require.Contains(t, raw, "effort: high")
	require.Contains(t, raw, "secrets.OPENAI_API_KEY")
	require.Contains(t, raw,
		"responses-api-endpoint: https://openrouter.ai/api/v1/responses")
	require.NotContains(t, raw, "session resume")
	require.NotContains(t, raw, "codex resume")
}

func TestIssueAgentTaskFreezesExactControlSource(t *testing.T) {
	controller := readIssueAgentFile(t, ".github/workflows/issue-agent.yml")
	require.Contains(t, controller, `--arg control_sha "$(git rev-parse HEAD)"`)
	require.Contains(t, controller, "control_sha: ${{ steps.reconcile.outputs.control_sha }}")
	require.Contains(t, controller, "control_sha: ${{ needs.controller.outputs.control_sha }}")
	require.Contains(t, controller,
		"OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}")
	require.NotContains(t, controller, "secrets: inherit")

	engineer := readIssueAgentFile(t, ".github/workflows/issue-agent-engineer.yml")
	require.Contains(t, engineer, "ref: ${{ inputs.control_sha }}")
	require.NotContains(t, engineer, "ref: main")
	require.Contains(t, engineer, "Check out the exact candidate base")
	require.Contains(t, engineer, "ref: ${{ inputs.base_sha }}")
	require.Contains(t, engineer, "$RUNNER_TEMP/issue-agent-policy.json")
	require.Contains(t, engineer, "$RUNNER_TEMP/issue-agent-prompt.md")
}

func TestIssueAgentReusableCallerGrantsOnlyRequiredReadScopes(t *testing.T) {
	raw := readIssueAgentFile(t, ".github/workflows/issue-agent.yml")
	caller := issueAgentJobText(t, raw, "engineer")
	require.Contains(t, caller, "contents: read")
	require.Contains(t, caller, "issues: read")
	require.Contains(t, caller, "pull-requests: read")
	require.NotContains(t, caller, "write")
}

func TestIssueAgentControllerSerializesFiveMinuteRecovery(t *testing.T) {
	controller := readIssueAgentFile(t, ".github/workflows/issue-agent.yml")
	require.Contains(t, controller, `cron: "*/5 * * * *"`)
	require.Equal(t, 2, strings.Count(controller,
		"group: issue-agent-state-${{ github.repository }}"))
	require.Contains(t, controller, "cancel-in-progress: false")
	require.NotContains(t, controller, "github.event.pull_request.number")
}

func TestIssueAgentJobsSeparateCredentialsAndExecution(t *testing.T) {
	engineerWorkflow := readIssueAgentFile(
		t,
		".github/workflows/issue-agent-engineer.yml",
	)
	for _, job := range []string{
		"recover-task:",
		"context-builder:",
		"engineer:",
		"verifier:",
	} {
		require.Contains(t, engineerWorkflow, "\n  "+job)
	}
	require.NotContains(t, engineerWorkflow, "\n  publisher:")
	require.NotContains(t, engineerWorkflow, "ISSUE_AGENT_APP_PRIVATE_KEY")

	engineer := issueAgentJobText(t, engineerWorkflow, "engineer")
	require.Contains(t, engineer, "persist-credentials: false")
	require.Contains(t, engineer, "OPENAI_API_KEY")
	require.Contains(t, engineer, "/opt/wukongim-issue-agent/baseline")
	require.Contains(t, engineer, "capture-candidate")
	require.NotContains(t, engineer, "ISSUE_AGENT_APP_PRIVATE_KEY")
	require.NotContains(t, engineer, "ISSUE_AGENT_GITHUB_TOKEN")
	require.NotContains(t, engineer, "docker.sock")
	require.NotContains(t, engineer, "git push")
	require.NotContains(t, engineer, "gh api")

	verifier := issueAgentJobText(t, engineerWorkflow, "verifier")
	require.Contains(t, verifier, "verify-candidate")
	require.Contains(t, verifier, "persist-credentials: false")
	require.NotContains(t, verifier, "OPENAI_API_KEY")
	require.NotContains(t, verifier, "ISSUE_AGENT_APP_PRIVATE_KEY")

	controller := readIssueAgentFile(t, ".github/workflows/issue-agent.yml")
	publisher := issueAgentJobText(t, controller, "publisher")
	require.Contains(t, publisher, "environment: issue-agent-publisher")
	require.Contains(t, publisher, "ISSUE_AGENT_APP_PRIVATE_KEY")
	require.Contains(t, publisher, "publish-candidate")
	require.NotContains(t, publisher, "OPENAI_API_KEY")
	require.NotContains(t, publisher, "go test")
	require.NotContains(t, publisher, "verify-candidate")
}

func TestIssueAgentTaskFailuresReachThePublisherFinalizer(t *testing.T) {
	engineer := readIssueAgentFile(t, ".github/workflows/issue-agent-engineer.yml")
	verifier := issueAgentJobText(t, engineer, "verifier")
	require.Contains(t, verifier,
		"if: always() && needs.engineer.result == 'success'")

	controller := readIssueAgentFile(t, ".github/workflows/issue-agent.yml")
	publisher := issueAgentJobText(t, controller, "publisher")
	require.Contains(t, publisher,
		"if: always() && needs.controller.result == 'success' && needs.controller.outputs.dispatch == 'true'")
	require.Contains(t, publisher,
		"- name: Download Context Bundle\n        continue-on-error: true")
	require.Contains(t, publisher,
		"- name: Download candidate and advisory result\n        continue-on-error: true")
	require.Contains(t, publisher,
		"- name: Download trusted evidence\n        continue-on-error: true")
}

func TestIssueAgentArtifactNamesAreFilesystemSafe(t *testing.T) {
	engineer := readIssueAgentFile(t, ".github/workflows/issue-agent-engineer.yml")
	for name, count := range map[string]int{
		"issue-agent-context-${{ inputs.issue_number }}":   2,
		"issue-agent-candidate-${{ inputs.issue_number }}": 2,
		"issue-agent-evidence-${{ inputs.issue_number }}":  1,
	} {
		require.Equal(t, count, strings.Count(engineer, "name: "+name), name)
	}
	require.NotContains(t, engineer,
		"name: issue-agent-context-${{ inputs.issue_number }}-${{ inputs.task_id }}")
	require.NotContains(t, engineer,
		"name: issue-agent-candidate-${{ inputs.issue_number }}-${{ inputs.task_id }}")
	require.NotContains(t, engineer,
		"name: issue-agent-evidence-${{ inputs.issue_number }}-${{ inputs.task_id }}")

	controller := readIssueAgentFile(t, ".github/workflows/issue-agent.yml")
	for _, name := range []string{
		"issue-agent-context-${{ needs.controller.outputs.issue_number }}",
		"issue-agent-candidate-${{ needs.controller.outputs.issue_number }}",
		"issue-agent-evidence-${{ needs.controller.outputs.issue_number }}",
	} {
		require.Equal(t, 1, strings.Count(controller, "name: "+name), name)
	}
}

func TestIssueAgentPublisherUsesOnlyTopLevelEnvironmentSecret(t *testing.T) {
	controller := readIssueAgentFile(t, ".github/workflows/issue-agent.yml")
	engineer := readIssueAgentFile(t, ".github/workflows/issue-agent-engineer.yml")
	require.NotContains(t, engineer, "\n  publisher:")
	require.NotContains(t, engineer, "ISSUE_AGENT_APP_PRIVATE_KEY")

	caller := issueAgentJobText(t, controller, "engineer")
	require.NotContains(t, caller, "ISSUE_AGENT_APP_PRIVATE_KEY")
	publisher := issueAgentJobText(t, controller, "publisher")
	require.Contains(t, publisher, "needs: [controller, engineer]")
	require.Contains(t, publisher,
		"if: always() && needs.controller.result == 'success' && needs.controller.outputs.dispatch == 'true'")
	require.Contains(t, publisher, "environment: issue-agent-publisher")
	require.Contains(t, publisher,
		"ISSUE_AGENT_APP_PRIVATE_KEY: ${{ secrets.ISSUE_AGENT_APP_PRIVATE_KEY }}")
	require.NotContains(t, publisher, "ISSUE_AGENT_PRIVATE_KEY_SECRET")
}

func TestIssueAgentControllerReportsCommittedProjectionWarnings(t *testing.T) {
	raw := readIssueAgentFile(t, ".github/workflows/issue-agent.yml")
	require.Contains(t, raw,
		`jq -r '.warnings[]? | "::warning::Issue Agent \(.projection) projection: \(.reason)"'`)
}

func TestIssueAgentPolicyIsCodexOnlyAndBounded(t *testing.T) {
	raw := readIssueAgentFile(t, ".github/issue-agent/policy.json")
	var policy struct {
		SchemaVersion int    `json:"schema_version"`
		Enabled       bool   `json:"enabled"`
		RolloutMode   string `json:"rollout_mode"`
		Engineer      struct {
			ActionSHA            string `json:"action_sha"`
			CodexVersion         string `json:"codex_version"`
			Model                string `json:"model"`
			Sandbox              string `json:"sandbox"`
			Ephemeral            bool   `json:"ephemeral"`
			NetworkAccess        bool   `json:"network_access"`
			WallTimeSeconds      uint64 `json:"wall_time_seconds"`
			ModifyTestIterations uint32 `json:"modify_test_iterations"`
		} `json:"engineer"`
		Budgets struct {
			TaskStaleAfterSeconds uint64 `json:"task_stale_after_seconds"`
		} `json:"budgets"`
		ProtectedPaths []string `json:"protected_paths"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &policy))
	require.Equal(t, 2, policy.SchemaVersion)
	require.True(t, policy.Enabled)
	require.Equal(t, "active", policy.RolloutMode)
	require.Equal(t, strings.TrimPrefix(codexActionPin, "openai/codex-action@"),
		policy.Engineer.ActionSHA)
	require.Equal(t, codexVersion, policy.Engineer.CodexVersion)
	require.Equal(t, "openai/gpt-5.6-sol", policy.Engineer.Model)
	require.Equal(t, "workspace-write", policy.Engineer.Sandbox)
	require.True(t, policy.Engineer.Ephemeral)
	require.True(t, policy.Engineer.NetworkAccess)
	require.Equal(t, uint64(5400), policy.Engineer.WallTimeSeconds)
	require.Equal(t, uint32(3), policy.Engineer.ModifyTestIterations)
	require.Equal(t, uint64(14400), policy.Budgets.TaskStaleAfterSeconds)
	require.Contains(t, policy.ProtectedPaths, ".github/workflows")
	require.Contains(t, policy.ProtectedPaths, ".github/issue-agent")
	require.Contains(t, policy.ProtectedPaths, "cmd/wkissueagent")

	lower := strings.ToLower(raw)
	for _, legacy := range []string{
		"deepseek",
		"provider",
		"broker",
		"checkpoint",
	} {
		require.NotContains(t, lower, legacy)
	}
}

func TestIssueAgentPromptsMakeAuthorityAndOutcomeExplicit(t *testing.T) {
	for _, name := range []string{"engineer.md", "review.md"} {
		raw := readIssueAgentFile(
			t,
			filepath.Join(".github/issue-agent/prompts", name),
		)
		require.Contains(t, raw, "ISSUE_AGENT_CONTEXT_BUNDLE")
		require.Contains(t, strings.ToLower(raw), "untrusted")
		require.Contains(t, raw, "AGENTS.md")
		require.Contains(t, raw, "FLOW.md")
		require.Contains(t, strings.ToLower(raw), "do not commit")
		require.Contains(t, strings.ToLower(raw), "three modify/test")
		require.NotContains(t, strings.ToLower(raw), "deepseek")
	}
}

func TestIssueAgentHistoricalBuildDoesNotRequireFutureMarker(t *testing.T) {
	root := repoRoot(t)
	for _, removed := range []string{
		".github/issue-agent/check-reproduction-compatibility.sh",
		".github/issue-agent/reproduction-contract",
	} {
		_, err := os.Stat(filepath.Join(root, removed))
		require.ErrorIs(t, err, os.ErrNotExist, removed)
	}
	build := readIssueAgentFile(
		t,
		".github/issue-agent/build-reproduction-binaries.sh",
	)
	require.Contains(t, build, "GOWORK=off go build -trimpath")
	require.NotContains(t, build, "reproduction-contract")
}

func TestIssueAgentLegacyImplementationIsAbsent(t *testing.T) {
	root := repoRoot(t)
	for _, removed := range []string{
		"internal/infra/issueagentmodel",
		"internal/runtime/issueagentworker",
		".github/issue-agent/checkpoint-public-keys.json",
		".github/issue-agent/checkpoint.schema.json",
		".github/issue-agent/result.schema.json",
		".github/issue-agent/task.schema.json",
	} {
		_, err := os.Stat(filepath.Join(root, removed))
		require.ErrorIs(t, err, os.ErrNotExist, removed)
	}
}

func readIssueAgentFile(t *testing.T, relative string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), relative))
	require.NoError(t, err)
	return string(body)
}

func issueAgentJobText(
	t *testing.T,
	workflow string,
	job string,
) string {
	t.Helper()
	start := strings.Index(workflow, "\n  "+job+":")
	require.NotEqual(t, -1, start)
	rest := workflow[start+1:]
	lines := strings.SplitAfter(rest, "\n")
	offset := 0
	for index, line := range lines {
		if index > 0 &&
			strings.HasPrefix(line, "  ") &&
			!strings.HasPrefix(line, "    ") &&
			strings.HasSuffix(strings.TrimSpace(line), ":") {
			return rest[:offset]
		}
		offset += len(line)
	}
	return rest
}
