package scripts_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestIssueAgentBugFormHasFourRequiredSemanticInputs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(
		repoRoot(t), ".github", "ISSUE_TEMPLATE", "bug.yml",
	))
	require.NoError(t, err)
	var form struct {
		Body []struct {
			Type       string `yaml:"type"`
			ID         string `yaml:"id"`
			Attributes struct {
				Label       string `yaml:"label"`
				Description string `yaml:"description"`
				Value       string `yaml:"value"`
			} `yaml:"attributes"`
			Validations struct {
				Required bool `yaml:"required"`
			} `yaml:"validations"`
		} `yaml:"body"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &form))
	var required []string
	for _, field := range form.Body {
		if field.Validations.Required {
			require.NotEqual(t, "checkboxes", field.Type)
			required = append(required, field.ID)
		}
	}
	require.Equal(t, []string{
		"affected_version", "environment", "reproduction", "expected_actual",
	}, required)
	require.Contains(t, strings.ToLower(string(raw)), "credential")
	require.Contains(t, strings.ToLower(string(raw)), "private")
}

func TestIssueAgentWorkflowSecurityContracts(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"issue-agent-control.yml",
		"issue-agent-reconcile.yml",
		"issue-agent-run.yml",
	} {
		raw := readWorkflow(t, name)
		document, workflow, err := decodeWorkflow(raw)
		require.NoError(t, err, name)
		require.NotNil(t, document)
		require.Empty(t, workflow.Permissions, name)
		require.NotEmpty(t, workflow.Jobs, name)
		require.NotContains(t, string(raw), "pull_request_target")
		require.NotContains(t, string(raw), "persist-credentials: true")
		for jobName, job := range workflow.Jobs {
			require.Greater(t, job.TimeoutMinutes, 0, "%s/%s", name, jobName)
			jobText := fmt.Sprintf("%#v", job)
			switch {
			case name == "issue-agent-control.yml" &&
				(jobName == "intake-publisher" || jobName == "state-publisher"):
				require.Equal(t, "issue-agent-publisher", job.Environment)
				require.NotNil(t, job.Concurrency)
				require.Equal(t,
					"issue-agent-publisher-${{ github.repository }}-${{ needs.planner.outputs.issue_number }}",
					job.Concurrency.Group,
				)
				require.Equal(t, "max", job.Concurrency.Queue)
				require.NotNil(t, job.Concurrency.CancelInProgress)
				require.False(t, *job.Concurrency.CancelInProgress)
				require.Contains(t, jobText, "ISSUE_AGENT_APP_PRIVATE_KEY")
				if jobName == "state-publisher" {
					require.Equal(t, map[string]string{
						"actions": "read", "contents": "read",
					}, job.Permissions)
					require.Contains(t, jobText, "ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY")
				} else {
					require.Equal(t,
						map[string]string{"contents": "read"},
						job.Permissions,
					)
					require.NotContains(t, jobText, "ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY")
				}
				require.NotContains(t, jobText, "CODEX_API_KEY")
				require.NotContains(t, jobText, "OPENROUTER_API_KEY")
				require.NotContains(t, jobText, "DEEPSEEK_API_KEY")
			case name == "issue-agent-reconcile.yml" && jobName == "dispatcher":
				require.Equal(t, "issue-agent-publisher", job.Environment)
				require.Contains(t, jobText, "ISSUE_AGENT_APP_PRIVATE_KEY")
				require.NotContains(t, jobText, "ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY")
				require.NotContains(t, jobText, "CODEX_API_KEY")
				require.NotContains(t, jobText, "OPENROUTER_API_KEY")
				require.NotContains(t, jobText, "DEEPSEEK_API_KEY")
			case name == "issue-agent-run.yml" && jobName == "publisher":
				require.Equal(t, "issue-agent-publisher", job.Environment)
				require.NotNil(t, job.Concurrency)
				require.Equal(t,
					"issue-agent-publisher-${{ github.repository }}-${{ inputs.issue_number }}",
					job.Concurrency.Group,
				)
				require.Equal(t, "max", job.Concurrency.Queue)
				require.NotNil(t, job.Concurrency.CancelInProgress)
				require.False(t, *job.Concurrency.CancelInProgress)
				require.Contains(t, jobText, "ISSUE_AGENT_APP_PRIVATE_KEY")
				require.Contains(t, jobText, "ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY")
				require.NotContains(t, jobText, "CODEX_API_KEY")
				require.NotContains(t, jobText, "OPENROUTER_API_KEY")
				require.NotContains(t, jobText, "DEEPSEEK_API_KEY")
			case name == "issue-agent-run.yml" && jobName == "codex-worker":
				require.Equal(t, "issue-agent-codex", job.Environment)
				require.Contains(t, jobText, "OPENROUTER_API_KEY")
				require.NotContains(t, jobText, "CODEX_API_KEY")
				require.NotContains(t, jobText, "DEEPSEEK_API_KEY")
				require.NotContains(t, jobText, "ISSUE_AGENT_APP_PRIVATE_KEY")
				require.NotContains(t, jobText, "ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY")
			case name == "issue-agent-run.yml" && jobName == "deepseek-worker":
				require.Equal(t, "issue-agent-deepseek", job.Environment)
				require.Contains(t, jobText, "DEEPSEEK_API_KEY")
				require.NotContains(t, jobText, "CODEX_API_KEY")
				require.NotContains(t, jobText, "OPENROUTER_API_KEY")
				require.NotContains(t, jobText, "ISSUE_AGENT_APP_PRIVATE_KEY")
				require.NotContains(t, jobText, "ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY")
			default:
				require.Empty(t, job.Environment, "%s/%s", name, jobName)
				require.NotContains(t, jobText, "ISSUE_AGENT_APP_PRIVATE_KEY")
				require.NotContains(t, jobText, "ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY")
				require.NotContains(t, jobText, "CODEX_API_KEY")
				require.NotContains(t, jobText, "OPENROUTER_API_KEY")
				require.NotContains(t, jobText, "DEEPSEEK_API_KEY")
			}
			for _, step := range job.Steps {
				if step.Uses != "" {
					require.NoError(t, validatePinnedIssueAgentAction(step.Uses))
				}
				if name == "issue-agent-control.yml" &&
					jobName == "state-publisher" {
					require.NotContains(t, step.Run, "${{",
						"large Publisher scripts must receive expressions through env")
				}
				require.NotContains(t, step.Run, "github.event.issue.body")
				require.NotContains(t, step.Run, "github.event.comment.body")
				require.NotContains(t, step.Run, "github.event.pull_request.title")
			}
		}
		if name == "issue-agent-run.yml" {
			require.Equal(t,
				"issue-agent-${{ inputs.issue_number }}",
				workflow.Concurrency.Group,
			)
		} else {
			require.Equal(t,
				"issue-agent-scheduler-${{ github.repository }}",
				workflow.Concurrency.Group,
			)
		}
		require.Equal(t, "max", workflow.Concurrency.Queue)
		require.NotNil(t, workflow.Concurrency.CancelInProgress)
		require.False(t, *workflow.Concurrency.CancelInProgress)
	}
}

func TestIssueAgentWorkflowRunUsesSeparateReadOnlyCheckouts(t *testing.T) {
	t.Parallel()

	raw := string(readWorkflow(t, "issue-agent-run.yml"))
	require.Contains(t, raw, "path: control")
	require.Contains(t, raw, "path: workspace")
	require.Contains(t, raw, "persist-credentials: false")
	require.Contains(t, raw, "group: issue-agent-${{ inputs.issue_number }}")
	require.Contains(t, raw, "queue: max")
	require.Contains(t, raw, "cancel-in-progress: false")
	require.NotContains(t, raw, "permissions:\n      contents: write")
	require.Contains(t, raw, "environment: issue-agent-publisher")
	require.Contains(t, raw, "module_cache")
	require.Contains(t, raw, ".enabled == true")
	require.Contains(t, raw, "remediation_issue_allowlist")
	require.Contains(t, raw, "docker pull \"$sandbox_image\"")
	require.Contains(t, raw, "prompt_phase=address-review")
	require.Contains(t, raw, "pull-requests: read")
}

func TestIssueAgentReproductionBuildSupportsHistoricalRootEntrypoint(t *testing.T) {
	t.Parallel()

	helperPath := filepath.Join(
		repoRoot(t), ".github", "issue-agent", "build-reproduction-binary.sh",
	)
	helperRaw, err := os.ReadFile(helperPath)
	require.NoError(t, err)
	helper := string(helperRaw)
	require.Contains(t, helper, `if [[ -d "$source_dir/cmd/wukongim" ]]; then`)
	require.Contains(t, helper, `entrypoint="./cmd/wukongim"`)
	require.Contains(t, helper, `elif [[ -f "$source_dir/main.go" ]]; then`)
	require.Contains(t, helper, `entrypoint="."`)
	require.Contains(t, helper, `No supported WuKongIM entrypoint in $source_dir`)
	helperInfo, err := os.Stat(helperPath)
	require.NoError(t, err)
	require.NotZero(t, helperInfo.Mode().Perm()&0o111)

	raw := readWorkflow(t, "issue-agent-run.yml")
	_, workflow, err := decodeWorkflow(raw)
	require.NoError(t, err)

	for _, jobName := range []string{"codex-worker", "deepseek-worker"} {
		job, ok := workflow.Jobs[jobName]
		require.True(t, ok, jobName)
		_, step := findIssueAgentStep(t, job, "Build exact reproduction binaries")
		require.Contains(t, step.Run,
			`control/.github/issue-agent/build-reproduction-binary.sh \
  affected-source "$RUNNER_TEMP/affected-wukongim"`)
		require.Contains(t, step.Run,
			`control/.github/issue-agent/build-reproduction-binary.sh \
  workspace "$RUNNER_TEMP/diagnosis-wukongim"`)
	}
}

func TestIssueAgentCodexWorkerUsesOfficialBootstrap(t *testing.T) {
	t.Parallel()

	raw := readWorkflow(t, "issue-agent-run.yml")
	_, workflow, err := decodeWorkflow(raw)
	require.NoError(t, err)
	job, ok := workflow.Jobs["codex-worker"]
	require.True(t, ok)

	pullIndex, _ := findIssueAgentStep(
		t, job, "Pull the digest-pinned sandbox without provider credentials",
	)
	verifyIndex, verify := findIssueAgentStep(
		t, job, "Verify Codex bootstrap home is absent",
	)
	bootstrapIndex, bootstrap := findIssueAgentStep(
		t, job, "Bootstrap the pinned Codex CLI and Responses proxy",
	)
	workerIndex, worker := findIssueAgentStep(
		t, job, "Run the bounded Codex Worker",
	)
	require.NoError(t, validateCodexWorkerBoundary(job))
	require.NoError(t, validateCodexBootstrapStep(bootstrap))
	require.Less(t, pullIndex, verifyIndex)
	require.Equal(t, verifyIndex+1, bootstrapIndex)
	require.Equal(t, bootstrapIndex+1, workerIndex)
	require.Contains(t, verify.Run,
		`[[ -e "$bootstrap_home" || -L "$bootstrap_home" ]]`)
	require.Equal(t, 1,
		strings.Count(string(raw), "secrets.OPENROUTER_API_KEY"))
	require.NotContains(t, string(raw), "secrets.CODEX_API_KEY")
	require.NotContains(t, worker.Env, "ISSUE_AGENT_CODEX_API_KEY")
	require.NotContains(t, worker.Env, "CODEX_API_KEY")
	require.NotContains(t, worker.Env, "OPENROUTER_API_KEY")
	require.Equal(t,
		"${{ runner.temp }}/issue-agent-codex-bootstrap",
		worker.Env["ISSUE_AGENT_CODEX_BOOTSTRAP_HOME"],
	)
	require.NotContains(t, worker.Run, "CODEX_API_KEY")
	require.NotContains(t, worker.Run, "ISSUE_AGENT_CODEX_API_KEY")
	require.NotContains(t, worker.Run, "OPENROUTER_API_KEY")
}

func TestIssueAgentCodexBootstrapContractRejectsMutations(t *testing.T) {
	t.Parallel()

	mutations := map[string]func(*ciStep){
		"moving tag": func(step *ciStep) {
			step.Uses = "openai/codex-action@v1"
		},
		"unsafe strategy": func(step *ciStep) {
			step.With["safety-strategy"] = "unsafe"
		},
		"broad bots": func(step *ciStep) {
			step.With["allow-bots"] = true
		},
		"prompt": func(step *ciStep) {
			step.With["prompt"] = "inspect the repository"
		},
		"missing OpenRouter endpoint": func(step *ciStep) {
			delete(step.With, "responses-api-endpoint")
		},
		"alternate Responses endpoint": func(step *ciStep) {
			step.With["responses-api-endpoint"] =
				"https://example.com/v1/responses"
		},
		"legacy API key": func(step *ciStep) {
			step.With["openai-api-key"] = "${{ secrets.CODEX_API_KEY }}"
		},
		"extra bot": func(step *ciStep) {
			step.With["allow-bot-users"] =
				"wukongim-issue-agent,github-actions"
		},
	}
	require.NoError(t, validateCodexBootstrapStep(canonicalCodexBootstrapStep()))
	for name, mutate := range mutations {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			step := canonicalCodexBootstrapStep()
			mutate(&step)
			require.Error(t, validateCodexBootstrapStep(step))
		})
	}
}

func TestIssueAgentCodexWorkerBoundaryRejectsOrderAndKeyMutations(t *testing.T) {
	t.Parallel()

	t.Run("bootstrap before image pull", func(t *testing.T) {
		job := canonicalCodexWorkerBoundary()
		job.Steps[0], job.Steps[2] = job.Steps[2], job.Steps[0]
		require.Error(t, validateCodexWorkerBoundary(job))
	})
	t.Run("forward API key", func(t *testing.T) {
		job := canonicalCodexWorkerBoundary()
		job.Steps[3].Env["OPENROUTER_API_KEY"] =
			"${{ secrets.OPENROUTER_API_KEY }}"
		require.Error(t, validateCodexWorkerBoundary(job))
	})
}

func TestIssueAgentWorkflowPolicyUsesReproductionRollout(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(
		repoRoot(t), ".github", "issue-agent", "policy.json",
	))
	require.NoError(t, err)
	var policy struct {
		RolloutMode string `json:"rollout_mode"`
	}
	require.NoError(t, json.Unmarshal(raw, &policy))
	require.Equal(t, "reproduction", policy.RolloutMode)
}

func TestIssueAgentCodexProviderPolicyUsesOpenRouterCredential(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(
		repoRoot(t), ".github", "issue-agent", "policy.json",
	))
	require.NoError(t, err)
	var policy struct {
		Providers []struct {
			Provider           string `json:"provider"`
			Endpoint           string `json:"endpoint"`
			ModelVariable      string `json:"model_variable"`
			CredentialVariable string `json:"credential_variable"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(raw, &policy))
	require.Contains(t, policy.Providers, struct {
		Provider           string `json:"provider"`
		Endpoint           string `json:"endpoint"`
		ModelVariable      string `json:"model_variable"`
		CredentialVariable string `json:"credential_variable"`
	}{
		Provider:           "codex",
		ModelVariable:      "ISSUE_AGENT_CODEX_MODEL",
		CredentialVariable: "OPENROUTER_API_KEY",
	})
}

func TestIssueAgentControlVerifiesProtectedControllerRevision(t *testing.T) {
	t.Parallel()

	raw := string(readWorkflow(t, "issue-agent-control.yml"))
	require.Contains(t, raw, `grep -F "vcs.revision=$revision"`)
	require.NotContains(t, raw, `grep -F "vcs.revision\t$revision"`)
}

func TestIssueAgentControlIntakeRolloutAdmitsOnlyIntakeAndAuthorization(
	t *testing.T,
) {
	t.Parallel()

	raw := string(readWorkflow(t, "issue-agent-control.yml"))
	require.Contains(t, raw, `if [[ "$rollout" = intake ]]; then
            case "$operation" in
              intake|authorize) ;;
              *) operation=report_only ;;
            esac
          fi`)
	require.Contains(t, raw, `needs.planner.outputs.rollout != 'intake' ||
       needs.planner.outputs.operation == 'authorize'`)
}

func TestIssueAgentControlUsesTrustedGitHubEventName(
	t *testing.T,
) {
	t.Parallel()

	raw := string(readWorkflow(t, "issue-agent-control.yml"))
	require.Contains(t, raw, `event_name="$GITHUB_EVENT_NAME"`)
	require.NotContains(t, raw, `jq -r '.event_name`)
	require.NotContains(t, raw, `if [[ -z "$event_name" ]]`)
}

func TestIssueAgentControlRoutesTypedLifecycleFailuresAndMaintainerCommands(t *testing.T) {
	t.Parallel()

	raw := string(readWorkflow(t, "issue-agent-control.yml"))
	require.Contains(t, raw, ".plan.operation")
	require.Contains(t, raw,
		"startsWith(github.event.pull_request.head.ref, 'agent/issue-')")
	require.Contains(t, raw, `case "$LIFECYCLE_OPERATION:$STATE"`)
	require.Contains(t, raw, `"$conclusion" = failure`)
	require.Contains(t, raw, "publish-command")
	require.Contains(t, raw, "publish-merge")
	require.Contains(t, raw, "observe_merge")
	require.Contains(t, raw, "record_merge:ready_for_review")
	require.Contains(t, raw, "record_branch_drift:*")
	require.Contains(t, raw, "publish-branch-drift")
	require.Contains(t, raw, "record_work_drift:*")
	require.Contains(t, raw, "publish-work-drift")
	require.Contains(t, raw, "publish_worker_result:reproducing")
	require.Contains(t, raw, "Download exact recoverable Worker Artifact")
	require.Contains(t, raw, "needs.planner.outputs.artifact_run_id")
	require.Contains(t, raw, "github-token: ${{ github.token }}")
	require.Contains(t, raw, "git -C target merge-tree --write-tree")
	require.Contains(t, raw,
		`--name-status -z "$mechanical_main_sha" "$mechanical_merge_tree_sha"`,
	)
	require.Contains(t, raw, "mechanical_main_sha")
	require.Contains(t, raw, "mechanical_merge_tree_sha")
	require.Contains(t, raw, "mechanical_change_set")
	require.Contains(t, raw, `--rawfile content_base64 "$content_base64_file"`)
	require.NotContains(t, raw,
		`--arg content_base64 "$(base64 -w0 "$content_file")"`,
	)
	require.Contains(t, raw, "fetch-depth: 0")
	require.Contains(t, raw, "publish-projection-repair")
	require.NotContains(t, raw, "/update-branch")
	require.Contains(t, raw,
		"needs.planner.outputs.lifecycle_operation == 'dispatch_worker'",
	)
	require.Contains(t, raw,
		"needs.planner.outputs.lifecycle_operation == 'request_validation'",
	)
	require.NotContains(t, raw,
		"needs.planner.outputs.lifecycle_operation != 'alert_audit_failure'",
	)
	require.Contains(t, raw,
		"needs.planner.outputs.command_requires_target == 'true'",
	)
	for _, command := range []string{
		"revise", "cancel", "address-review", "adopt-head", "backport",
		"recover-chain",
	} {
		require.Contains(t, raw, command)
	}
	require.Contains(t, raw, "repair_operation")
}

func validatePinnedIssueAgentAction(value string) error {
	parts := strings.Split(value, "@")
	if len(parts) != 2 || len(parts[1]) != 40 {
		return fmt.Errorf("Action %q is not pinned by full SHA", value)
	}
	pin, ok := approvedActionPins[parts[0]]
	if !ok || pin.sha != parts[1] {
		return fmt.Errorf("Action %q is not an approved pin", value)
	}
	return nil
}

func canonicalCodexBootstrapStep() ciStep {
	return ciStep{
		Name: "Bootstrap the pinned Codex CLI and Responses proxy",
		Uses: "openai/codex-action@" +
			"52fe01ec70a42f454c9d2ebd47598f9fd6893d56",
		With: map[string]any{
			"openai-api-key":         "${{ secrets.OPENROUTER_API_KEY }}",
			"responses-api-endpoint": "https://openrouter.ai/api/v1/responses",
			"codex-version":          "0.145.0",
			"codex-home":             "${{ runner.temp }}/issue-agent-codex-bootstrap",
			"safety-strategy":        "drop-sudo",
			"allow-bot-users":        "wukongim-issue-agent",
		},
	}
}

func validateCodexBootstrapStep(step ciStep) error {
	expected := canonicalCodexBootstrapStep()
	if step.Name != expected.Name || step.Uses != expected.Uses ||
		!reflect.DeepEqual(step.With, expected.With) ||
		step.Run != "" || step.Shell != "" || step.If != "" ||
		len(step.Env) != 0 {
		return fmt.Errorf("Codex Action bootstrap step is not exact")
	}
	return nil
}

func canonicalCodexWorkerBoundary() ciJob {
	return ciJob{Steps: []ciStep{
		{Name: "Pull the digest-pinned sandbox without provider credentials"},
		{
			Name:  "Verify Codex bootstrap home is absent",
			Shell: "bash",
			Run: `if [[ -e "$bootstrap_home" || -L "$bootstrap_home" ]]; then
  exit 1
fi`,
		},
		canonicalCodexBootstrapStep(),
		{
			Name: "Run the bounded Codex Worker",
			Env: map[string]string{
				"ISSUE_AGENT_CODEX_BOOTSTRAP_HOME": "${{ runner.temp }}/issue-agent-codex-bootstrap",
			},
		},
	}}
}

func validateCodexWorkerBoundary(job ciJob) error {
	pullIndex, _, pullOK := lookupIssueAgentStep(
		job, "Pull the digest-pinned sandbox without provider credentials",
	)
	verifyIndex, verify, verifyOK := lookupIssueAgentStep(
		job, "Verify Codex bootstrap home is absent",
	)
	bootstrapIndex, _, bootstrapOK := lookupIssueAgentStep(
		job, "Bootstrap the pinned Codex CLI and Responses proxy",
	)
	workerIndex, worker, workerOK := lookupIssueAgentStep(
		job, "Run the bounded Codex Worker",
	)
	if !pullOK || !verifyOK || !bootstrapOK || !workerOK ||
		pullIndex >= verifyIndex || verifyIndex+1 != bootstrapIndex ||
		bootstrapIndex+1 != workerIndex ||
		!strings.Contains(
			verify.Run, `[[ -e "$bootstrap_home" || -L "$bootstrap_home" ]]`,
		) ||
		worker.Env["ISSUE_AGENT_CODEX_BOOTSTRAP_HOME"] !=
			"${{ runner.temp }}/issue-agent-codex-bootstrap" ||
		worker.Env["CODEX_API_KEY"] != "" ||
		worker.Env["OPENROUTER_API_KEY"] != "" ||
		worker.Env["ISSUE_AGENT_CODEX_API_KEY"] != "" ||
		strings.Contains(worker.Run, "CODEX_API_KEY") ||
		strings.Contains(worker.Run, "OPENROUTER_API_KEY") {
		return fmt.Errorf("Codex Worker boundary is not exact")
	}
	return nil
}

func findIssueAgentStep(t *testing.T, job ciJob, name string) (int, ciStep) {
	t.Helper()
	index, step, ok := lookupIssueAgentStep(job, name)
	if ok {
		return index, step
	}
	t.Fatalf("step %q is absent", name)
	return -1, ciStep{}
}

func lookupIssueAgentStep(job ciJob, name string) (int, ciStep, bool) {
	for index, step := range job.Steps {
		if step.Name == name {
			return index, step, true
		}
	}
	return -1, ciStep{}, false
}
