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
	If             string                            `yaml:"if"`
	TimeoutMinutes int                               `yaml:"timeout-minutes"`
	Steps          []threeNodeRegressionWorkflowStep `yaml:"steps"`
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

	pr, ok := workflow.Jobs["pr-regression"]
	require.True(t, ok)
	require.Equal(t, "github.event_name == 'pull_request'", pr.If)
	require.LessOrEqual(t, pr.TimeoutMinutes, 35)
	prRun := workflowRunCommands(pr.Steps)
	for _, required := range []string{
		"GOWORK=off go test ./internal/bench/chatlifecycle ./internal/bench/workload ./internal/bench/worker ./pkg/bench/model ./pkg/client ./pkg/gateway/... -count=1",
		"GOWORK=off go test -race ./internal/bench/workload ./internal/bench/worker ./pkg/client ./pkg/gateway/transport/gnet -count=1",
		"BenchmarkThreeNodeMixedSendPath500QPS",
		"BenchmarkThreeNodeChannelAppend500QPS",
		"BenchmarkRealTCPSendackWithSynchronousRecvackPaced500QPS",
		"append-p99-ms",
		"all-p99-ms",
		"send-p99-ms",
		"benchmark metric %s exceeded 400ms",
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
