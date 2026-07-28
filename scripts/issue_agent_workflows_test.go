package scripts_test

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestIssueAgentWorkflowShadowContracts(t *testing.T) {
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
		require.NotContains(t, string(raw), "ISSUE_AGENT_APP_PRIVATE_KEY")
		require.NotContains(t, string(raw), "ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY")
		require.NotContains(t, string(raw), "DEEPSEEK_API_KEY")
		require.NotContains(t, string(raw), "CODEX_API_KEY")
		require.NotContains(t, string(raw), "persist-credentials: true")
		for jobName, job := range workflow.Jobs {
			require.Greater(t, job.TimeoutMinutes, 0, "%s/%s", name, jobName)
			for _, step := range job.Steps {
				if step.Uses != "" {
					require.NoError(t, validatePinnedIssueAgentAction(step.Uses))
				}
				require.NotContains(t, step.Run, "github.event.issue.body")
				require.NotContains(t, step.Run, "github.event.comment.body")
				require.NotContains(t, step.Run, "github.event.pull_request.title")
			}
		}
		if name != "issue-agent-run.yml" {
			require.Equal(t,
				"issue-agent-scheduler-${{ github.repository }}",
				workflow.Concurrency.Group,
			)
			require.NotNil(t, workflow.Concurrency.CancelInProgress)
			require.False(t, *workflow.Concurrency.CancelInProgress)
		}
	}
}

func TestIssueAgentWorkflowRunUsesSeparateReadOnlyCheckouts(t *testing.T) {
	t.Parallel()

	raw := string(readWorkflow(t, "issue-agent-run.yml"))
	require.Contains(t, raw, "path: control")
	require.Contains(t, raw, "path: workspace")
	require.Contains(t, raw, "persist-credentials: false")
	require.Contains(t, raw, "group: issue-agent-${{ inputs.issue_number }}")
	require.Contains(t, raw, "cancel-in-progress: false")
	require.NotContains(t, raw, "permissions:\n      contents: write")
	require.NotContains(t, raw, "environment:")
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
