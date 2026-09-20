package scripts_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

type threeNodeRegressionWorkflowStep struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	If   string `yaml:"if"`
	Run  string `yaml:"run"`
	With struct {
		Name           string `yaml:"name"`
		Path           string `yaml:"path"`
		IfNoFilesFound string `yaml:"if-no-files-found"`
		RetentionDays  int    `yaml:"retention-days"`
	} `yaml:"with"`
}

type threeNodeRegressionWorkflowJob struct {
	If             string `yaml:"if"`
	TimeoutMinutes int    `yaml:"timeout-minutes"`
	Needs          any    `yaml:"needs"`
	Strategy       struct {
		FailFast bool `yaml:"fail-fast"`
		Matrix   struct {
			Seam []string `yaml:"seam"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []threeNodeRegressionWorkflowStep `yaml:"steps"`
}

func TestThreeNodeChatLifecycleRegressionSeparatesPRSmokeFromNightlyQualification(t *testing.T) {
	raw := readWorkflow(t, "three-node-chat-lifecycle-regression.yml")
	var workflow struct {
		Name        string                                    `yaml:"name"`
		On          map[string]yaml.Node                      `yaml:"on"`
		Permissions map[string]string                         `yaml:"permissions"`
		Jobs        map[string]threeNodeRegressionWorkflowJob `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &workflow))
	require.NotContains(t, string(raw), "EVIDENCE_ROOT: ${{ runner.temp }}")
	require.Equal(t, "Safety Automation - Three-Node Chat Lifecycle Regression", workflow.Name)
	for _, trigger := range []string{"pull_request", "schedule", "workflow_dispatch"} {
		_, ok := workflow.On[trigger]
		require.Truef(t, ok, "missing %s trigger", trigger)
	}
	require.Equal(t, map[string]string{"contents": "read"}, workflow.Permissions)

	pr, ok := workflow.Jobs["pr-correctness"]
	require.True(t, ok)
	require.Equal(t, "github.event_name == 'pull_request'", pr.If)
	require.LessOrEqual(t, pr.TimeoutMinutes, 35)
	prRun := workflowRunCommands(pr.Steps)
	aggregate := workflow.Jobs["pr-regression"]
	require.Contains(t, aggregate.If, "always()")
	require.Equal(t, []any{"pr-unit", "pr-correctness", "pr-performance", "pr-baseline"}, aggregate.Needs)
	require.Contains(t, workflowRunCommands(aggregate.Steps), `"$PERFORMANCE_RESULT" == success`)
	require.Nil(t, pr.Needs, "correctness must run independently of performance")
	require.NotContains(t, prRun, "-bench")
	require.NotContains(t, prRun, "-race")
	unit := workflow.Jobs["pr-unit"]
	require.Nil(t, unit.Needs)
	require.Contains(t, workflowRunCommands(unit.Steps), "go test -race")
	performance := workflow.Jobs["pr-performance"]
	require.Equal(t, []any{"pr-baseline"}, performance.Needs)
	require.Contains(t, performance.If, "always()")
	require.Contains(t, performance.If, "needs.pr-baseline.outputs.reuse != 'true'")
	require.Contains(t, performance.If, "github.event_name == 'schedule'")
	require.Contains(t, performance.If, "inputs.qualify_candidate")
	require.Contains(t, workflowRunCommands(aggregate.Steps), `"$BASELINE_RESULT" == success`)
	require.Contains(t, workflowRunCommands(aggregate.Steps), `"$BASELINE_REUSED" == true`)
	baseline := workflow.Jobs["pr-baseline"]
	require.Equal(t, "github.event_name == 'pull_request'", baseline.If)
	require.LessOrEqual(t, baseline.TimeoutMinutes, 3)
	require.Contains(t, workflowRunCommands(baseline.Steps), "scripts/ci-500qps-baseline.py")
	require.False(t, performance.Strategy.FailFast)
	require.ElementsMatch(t, []string{"channel-append", "mixed-send", "tcp-sendack"}, performance.Strategy.Matrix.Seam)
	require.Contains(t, workflowRunCommands(performance.Steps), "scripts/run-500qps-seam.sh")
	require.NotContains(t, string(raw), "continue-on-error")
	for _, required := range []string{
		"--send-rate 500",
		"--measure-seconds 90",
		"--warmup-seconds 60",
		"--drain-timeout 90",
		"--hot-sendack-p99-ms 1000",
	} {
		require.Contains(t, prRun, required)
	}
	require.NotContains(t, prRun, "run-wukongim-three-node-chat-lifecycle-local-baseline.sh")
	require.NotContains(t, prRun, "BenchmarkThreeNodeMixedSendPath1000QPS")
	require.NotContains(t, prRun, "BenchmarkThreeNodeChannelAppend1000QPS")
	require.NotContains(t, prRun, "BenchmarkRealTCPSendackWithSynchronousRecvackPaced1000QPS")
	require.NotContains(t, prRun, "--send-rate 1000")
	require.NotContains(t, prRun, "--measure-seconds 600")
	assertInitializesEvidenceRootFromRunnerTemp(t, pr.Steps)
	assertRejectsTrackedTreeMutationAfter(t, pr.Steps, "Run bounded three-node 500 QPS correctness smoke")
	assertRegressionArtifactStep(t, pr.Steps, 7)

	nightly, ok := workflow.Jobs["nightly-qualification"]
	require.True(t, ok)
	require.Contains(t, nightly.If, "github.event_name == 'schedule'")
	require.Contains(t, nightly.If, "github.ref == 'refs/heads/main'")
	require.Contains(t, nightly.If, "!inputs.diagnose_send")
	require.Contains(t, nightly.If, "inputs.qualify_candidate")
	require.Contains(t, workflowRunCommands(nightly.Steps), "git merge-base --is-ancestor")
	require.LessOrEqual(t, nightly.TimeoutMinutes, 45)
	nightlyRun := workflowRunCommands(nightly.Steps)
	require.Contains(t, nightlyRun, "MINIMUM_FREE_PERCENT=15")
	for _, required := range []string{
		"run-wukongim-three-node-chat-lifecycle-shakeout.sh",
		"--send-rate 500",
		"--measure-seconds 600",
		"--warmup-seconds 60",
		"--drain-timeout 90",
		"--hot-sendack-p99-ms 400",
	} {
		require.Contains(t, nightlyRun, required)
	}
	require.NotContains(t, nightlyRun, "run-wukongim-three-node-chat-lifecycle-local-baseline.sh")
	require.NotContains(t, nightlyRun, "--send-rate 1000")
	require.NotContains(t, nightlyRun, "--no-start")
	require.NotContains(t, nightlyRun, "--no-worker")
	assertInitializesEvidenceRootFromRunnerTemp(t, nightly.Steps)
	assertRejectsTrackedTreeMutationAfter(t, nightly.Steps, "Run direct ten-minute 500 QPS qualification")
	assertRegressionArtifactStep(t, nightly.Steps, 14)
}

func TestMixedSendDiagnosisIsManualBoundedAndSeparateFromGates(t *testing.T) {
	raw := readWorkflow(t, "three-node-chat-lifecycle-regression.yml")
	var workflow struct {
		Jobs map[string]threeNodeRegressionWorkflowJob `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &workflow))
	job := workflow.Jobs["send-diagnosis"]
	require.Equal(t, "github.event_name == 'workflow_dispatch' && inputs.diagnose_send", job.If)
	require.LessOrEqual(t, job.TimeoutMinutes, 20)
	require.Contains(t, workflowRunCommands(job.Steps), "bash scripts/diagnose-mixed-send-linux.sh")
	require.Contains(t, workflowRunCommands(job.Steps), "git merge-base --is-ancestor")
	require.NotContains(t, workflowRunCommands(workflow.Jobs["pr-correctness"].Steps), "diagnose-mixed-send-linux.sh")
	require.NotContains(t, workflowRunCommands(workflow.Jobs["nightly-qualification"].Steps), "diagnose-mixed-send-linux.sh")
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			require.Equal(t, "always()", step.If)
			require.Equal(t, "error", step.With.IfNoFilesFound)
			require.Equal(t, 90, step.With.RetentionDays)
		}
	}
}

func workflowRunCommands(steps []threeNodeRegressionWorkflowStep) string {
	var commands strings.Builder
	for _, step := range steps {
		commands.WriteString(step.Run)
		commands.WriteByte('\n')
	}
	return commands.String()
}

func assertRegressionArtifactStep(t *testing.T, steps []threeNodeRegressionWorkflowStep, retentionDays int) {
	t.Helper()
	for _, step := range steps {
		if !strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			continue
		}
		require.Equal(t, "always()", step.If)
		require.NotEmpty(t, step.With.Name)
		require.NotEmpty(t, step.With.Path)
		require.Equal(t, "warn", step.With.IfNoFilesFound)
		require.Equal(t, retentionDays, step.With.RetentionDays)
		return
	}
	t.Fatal("missing upload-artifact step")
}

func assertRejectsTrackedTreeMutationAfter(t *testing.T, steps []threeNodeRegressionWorkflowStep, predecessor string) {
	t.Helper()
	predecessorIndex := -1
	mutationCheckIndex := -1
	for index, step := range steps {
		if step.Name == predecessor {
			predecessorIndex = index
		}
		if step.Name != "Reject tracked-tree mutation" {
			continue
		}
		mutationCheckIndex = index
		require.Equal(t, "always()", step.If)
		require.Contains(t, step.Run, "git diff --exit-code HEAD --")
	}
	require.NotEqual(t, -1, predecessorIndex)
	require.Greater(t, mutationCheckIndex, predecessorIndex)
}

func assertInitializesEvidenceRootFromRunnerTemp(t *testing.T, steps []threeNodeRegressionWorkflowStep) {
	t.Helper()
	for _, step := range steps {
		if !strings.Contains(step.Run, `echo "EVIDENCE_ROOT=$evidence_root" >>"$GITHUB_ENV"`) {
			continue
		}
		require.Contains(t, step.Run, `evidence_root="$RUNNER_TEMP/`)
		return
	}
	t.Fatal("missing RUNNER_TEMP evidence-root initialization")
}
