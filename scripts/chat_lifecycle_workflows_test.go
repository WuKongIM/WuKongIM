package scripts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestChatLifecycleRehearsalExposesExactlyFourOperatorInputs(t *testing.T) {
	var workflow struct {
		On struct {
			Dispatch struct {
				Inputs map[string]struct {
					Required bool   `yaml:"required"`
					Type     string `yaml:"type"`
				} `yaml:"inputs"`
			} `yaml:"workflow_dispatch"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal(readWorkflow(t, "chat-lifecycle-rehearsal.yml"), &workflow); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		Required bool   `yaml:"required"`
		Type     string `yaml:"type"`
	}{
		"source_sha":              {Required: true, Type: "string"},
		"operator":                {Required: true, Type: "string"},
		"codex_diagnostic_pubkey": {Required: true, Type: "string"},
		"request_id":              {Required: true, Type: "string"},
	}
	if !reflect.DeepEqual(workflow.On.Dispatch.Inputs, want) {
		t.Fatalf("operator inputs = %#v, want %#v", workflow.On.Dispatch.Inputs, want)
	}
}

func TestChatLifecycleRehearsalFixesBuildQuoteAcquireDeployAndRemoteOwnershipOrder(t *testing.T) {
	root := repoRoot(t)
	orchestrator := readFile(t, filepath.Join(root, "scripts", "chat-lifecycle", "stage-orchestrate.sh"))
	ordered := []string{
		"cloud-deployment-bundle.yml",
		"-f quote_only=true",
		"-f quote_only=false",
		"  while true; do\n    run_deployment_action",
		"systemctl start --no-block '$stage_service'",
		"run-start.json",
		"keep_active=true",
		"classify-stage-service-state.sh",
	}
	previous := -1
	for _, fragment := range ordered {
		start := previous + 1
		relative := strings.Index(orchestrator[start:], fragment)
		if relative < 0 {
			t.Fatalf("fragment %q is missing or out of order", fragment)
		}
		index := start + relative
		previous = index
	}
	for _, forbidden := range []string{"docker", "sleep 7200", "sleep 2h"} {
		if strings.Contains(strings.ToLower(orchestrator), forbidden) {
			t.Fatalf("orchestrator unexpectedly contains %q", forbidden)
		}
	}
	for _, required := range []string{
		"deployment_repair_pending",
		"Chat-Lifecycle-Repair: $WK_CHAT_REQUEST_ID",
		"wait_for_deployment_repair_revision",
		"attempted-deployment-control-shas",
		"repair_reserve_seconds",
		"for attempt in 1; do",
		"(sudo systemctl reset-failed '$stage_service' || true)",
		"capture-stage-journal-cursor.sh",
		"pre-clock journal cursor unavailable; exact Lease was released",
		"read_pre_clock_terminal_code",
		`[[ "$state" == terminal ]]`,
		"--after-cursor='$journal_cursor'",
		"classify-pre-clock-summary.sh",
		"stage terminated before run-start with coordinator_code=",
	} {
		if !strings.Contains(orchestrator, required) {
			t.Fatalf("same-Lease deployment repair is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"for attempt in 1 2",
		"complete_failed_attempt",
		"second acquisition/deployment/readiness attempt failed",
		"--excluded-zone",
		"capture-stage-journal-cursor.sh \\\n      \"$WK_CLOUD_SSH_CONFIG\" wukong-load || true",
	} {
		if strings.Contains(orchestrator, forbidden) {
			t.Fatalf("orchestrator still contains fresh-Lease deployment retry %q", forbidden)
		}
	}
	if strings.Count(orchestrator, `-f quote_only=false`) != 1 {
		t.Fatal("orchestrator may acquire more than one Lease per stage")
	}
	workflow := string(readWorkflow(t, "chat-lifecycle-rehearsal.yml"))
	if strings.Count(workflow, "      "+"source_sha:") != 1 ||
		!strings.Contains(workflow, "Orchestrate until remote systemd owns the measured run") {
		t.Fatal("rehearsal workflow does not retain the fixed remote-ownership boundary")
	}
}

func TestChatLifecyclePreClockSummaryClassification(t *testing.T) {
	classifier := filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "classify-pre-clock-summary.sh")
	tests := []struct {
		name    string
		summary string
		want    string
		ok      bool
	}{
		{
			name:    "setup is terminal",
			summary: "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=setup preflight_code= report=unavailable",
			want:    "setup\n",
			ok:      true,
		},
		{
			name:    "current observer detail remains terminal",
			summary: "chat-lifecycle outcome=product_failure cause=worker_product_failure coordinator_code=observer worker_runtime_code= observer_code=cluster_health preflight_code= report=unavailable",
			want:    "observer\n",
			ok:      true,
		},
		{
			name:    "current known worker runtime detail remains terminal",
			summary: "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=runtime worker_runtime_code=engine_cpu_saturated observer_code= preflight_code= report=unavailable",
			want:    "runtime\n",
			ok:      true,
		},
		{
			name:    "current grant delivery detail remains terminal",
			summary: "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=grant grant_failure_code=delivery worker_runtime_code= observer_code= preflight_code= report=unavailable",
			want:    "grant\n",
			ok:      true,
		},
		{
			name:    "observer detail remains terminal",
			summary: "chat-lifecycle outcome=product_failure cause=worker_product_failure coordinator_code=observer observer_code=cluster_health preflight_code= report=unavailable",
			want:    "observer\n",
			ok:      true,
		},
		{
			name:    "legacy observer summary remains terminal",
			summary: "chat-lifecycle outcome=product_failure cause=worker_product_failure coordinator_code=observer preflight_code= report=unavailable",
			want:    "observer\n",
			ok:      true,
		},
		{
			name:    "unknown worker runtime detail fails closed",
			summary: "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=runtime worker_runtime_code=future_reason observer_code= preflight_code= report=unavailable",
		},
		{
			name:    "unknown grant detail fails closed",
			summary: "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=grant grant_failure_code=future_reason worker_runtime_code= observer_code= preflight_code= report=unavailable",
		},
		{
			name:    "unknown observer detail fails closed",
			summary: "chat-lifecycle outcome=product_failure cause=worker_product_failure coordinator_code=observer observer_code=future_reason preflight_code= report=unavailable",
		},
		{
			name:    "empty observer detail fails closed",
			summary: "chat-lifecycle outcome=product_failure cause=worker_product_failure coordinator_code=observer observer_code= preflight_code= report=unavailable",
		},
		{
			name:    "non-observer rejects observer detail",
			summary: "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=setup observer_code=service_health preflight_code= report=unavailable",
		},
		{
			name:    "preflight remains repairable",
			summary: "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=preflight preflight_code=process_evidence report=unavailable",
		},
		{
			name:    "unknown code fails closed",
			summary: "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=future_code preflight_code= report=unavailable",
		},
		{
			name:    "extra line is rejected",
			summary: "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=setup preflight_code= report=unavailable\nsecret=value",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash", classifier, test.summary)
			output, err := command.CombinedOutput()
			if test.ok {
				if err != nil || string(output) != test.want {
					t.Fatalf("classification = %q, %v; want %q, success", output, err, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("classification unexpectedly succeeded: %q", output)
			}
		})
	}
}

func TestChatLifecycleStageServiceStateKeepsQueuedInactiveStartAlive(t *testing.T) {
	root := repoRoot(t)
	classifier := filepath.Join(root, "scripts", "chat-lifecycle", "classify-stage-service-state.sh")
	command := exec.Command("bash", classifier)
	command.Stdin = strings.NewReader("ActiveState=inactive\nJob=8841\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("classify queued inactive stage: %v\n%s", err, output)
	}
	if string(output) != "pending\n" {
		t.Fatalf("classification = %q, want pending", output)
	}

	orchestrator := readFile(t, filepath.Join(root, "scripts", "chat-lifecycle", "stage-orchestrate.sh"))
	if !strings.Contains(orchestrator, "classify-stage-service-state.sh") {
		t.Fatal("stage orchestrator does not use the queued-start classifier")
	}
}

func TestChatLifecycleStageServiceStateRejectsTerminalInactiveUnit(t *testing.T) {
	classifier := filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "classify-stage-service-state.sh")
	command := exec.Command("bash", classifier)
	command.Stdin = strings.NewReader("ActiveState=inactive\nJob=0\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("classify terminal inactive stage: %v\n%s", err, output)
	}
	if string(output) != "terminal\n" {
		t.Fatalf("classification = %q, want terminal", output)
	}
}

func TestChatLifecycleCapturesJournalCursorBeforeFirstUnitStart(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	fakeSSH := filepath.Join(binDir, "ssh")
	if err := os.WriteFile(fakeSSH, []byte(`#!/usr/bin/env bash
set -euo pipefail
if [[ " $* " == *" -u "* ]]; then
  printf '%s\n' '-- No entries --'
  exit 0
fi
printf '%s\n' '-- cursor: s=0123456789abcdef;i=2;b=abcdef0123456789;m=3;t=4;x=5'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	fakeTimeout := filepath.Join(binDir, "timeout")
	if err := os.WriteFile(fakeTimeout, []byte("#!/usr/bin/env bash\nshift\nexec \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sshConfig := filepath.Join(t.TempDir(), "ssh-config")
	if err := os.WriteFile(sshConfig, []byte("Host wukong-load\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "capture-stage-journal-cursor.sh"), sshConfig, "wukong-load")
	command.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("capture journal cursor: %v\n%s", err, output)
	}
	const want = "s=0123456789abcdef;i=2;b=abcdef0123456789;m=3;t=4;x=5\n"
	if string(output) != want {
		t.Fatalf("cursor = %q, want %q", output, want)
	}
}

func TestChatLifecycleEncryptedAccessHandoffNeverPublishesPlaintextCredentials(t *testing.T) {
	root := repoRoot(t)
	deployment := string(readWorkflow(t, "cloud-deployment-activate.yml"))
	for _, required := range []string{
		"codex_diagnostic_pubkey:",
		"wkchatlifecycle\" seal-access",
		"encrypted-access.json",
		"rm -f access-credentials.json",
	} {
		if !strings.Contains(deployment, required) {
			t.Fatalf("Deployment Action access handoff is missing %q", required)
		}
	}
	if strings.Contains(deployment, "access-credentials.json\n            deployment-plan.json") {
		t.Fatal("Deployment Action uploads plaintext access credentials")
	}
	orchestrator := readFile(t, filepath.Join(root, "scripts", "chat-lifecycle", "stage-orchestrate.sh"))
	for _, required := range []string{
		`-f codex_diagnostic_pubkey="$WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY"`,
		"encrypted-access.json",
		`cp "${encrypted_access[0]}" "$WK_CHAT_OUTPUT_DIR/encrypted-access.json"`,
	} {
		if !strings.Contains(orchestrator, required) {
			t.Fatalf("stage orchestration access handoff is missing %q", required)
		}
	}
}

func TestChatLifecycleAnalysisUsesExactLeaseHandoffInsteadOfCloudSimulationLocator(t *testing.T) {
	root := repoRoot(t)
	workflowPath := filepath.Join(root, ".github", "workflows", "cloud-lease-analyze.yml")
	workflow := readFile(t, workflowPath)
	for _, required := range []string{
		"chat-lifecycle-${STAGE}-handoff-${CHAT_REQUEST_ID}",
		"chat-lifecycle-${STAGE}-cleanup-${CHAT_REQUEST_ID}",
		"analysis-endpoint.json",
		"release-selector.json",
		"zero-inventory.json",
		"wkcloudlease\" inspect",
		"wkcloudlease\" grant_access",
		"wkcloudlease\" revoke_access",
		"wukongim/chat-lifecycle-analysis-preflight/v1",
		"wukongim/chat-lifecycle-analysis-session/v1",
		`state:"released"`,
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("Cloud Lease Analysis workflow is missing %q", required)
		}
	}
	if strings.Contains(workflow, "cloud-sim-locator-") || strings.Contains(workflow, "wkcloudsim") {
		t.Fatal("Cloud Lease Analysis workflow still depends on the legacy Cloud Simulation identity dialect")
	}

	deployment := string(readWorkflow(t, "cloud-deployment-activate.yml"))
	for _, required := range []string{
		"WK_ANALYSIS_GITHUB_OIDC_ENABLED=true",
		"WK_ANALYSIS_GITHUB_OIDC_AUDIENCE=wukongim-cloud-lease:$lease_id",
		".github/workflows/cloud-lease-analyze.yml@refs/heads/main",
		"WK_ANALYSIS_GITHUB_ENVIRONMENT=cloud-lease-provision",
		"analysis-endpoint.json",
	} {
		if !strings.Contains(deployment, required) {
			t.Fatalf("Deployment Action Analysis handoff is missing %q", required)
		}
	}

	orchestrator := readFile(t, filepath.Join(root, "scripts", "chat-lifecycle", "stage-orchestrate.sh"))
	for _, required := range []string{
		"analysis-endpoint.json",
		`cp "${analysis_endpoints[0]}" "$WK_CHAT_OUTPUT_DIR/analysis-endpoint.json"`,
	} {
		if !strings.Contains(orchestrator, required) {
			t.Fatalf("stage orchestration Analysis handoff is missing %q", required)
		}
	}

	launcher := readFile(t, filepath.Join(root, "scripts", "chat-lifecycle", "analyze.sh"))
	for _, required := range []string{
		"--chat-request-id",
		"../cloud-sim/analyze.sh",
	} {
		if !strings.Contains(launcher, required) {
			t.Fatalf("chat-lifecycle Analysis launcher is missing %q", required)
		}
	}
}

func TestChatLifecycleAnalysisSelectsCanonicalHandoffFiles(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(readWorkflow(t, "cloud-lease-analyze.yml"), &workflow); err != nil {
		t.Fatal(err)
	}
	var run string
	for _, step := range workflow.Jobs["resolve"].Steps {
		if step.Name == "Resolve and authenticate one exact retained handoff" {
			run = step.Run
			break
		}
	}
	if run == "" {
		t.Fatal("Cloud Lease Analysis workflow is missing the handoff resolver")
	}
	start := strings.Index(run, "for name in handoff.json")
	if start < 0 {
		t.Fatal("Cloud Lease Analysis workflow is missing the canonical handoff selection loop")
	}
	end := strings.Index(run[start:], "\ndone\n")
	if end < 0 {
		t.Fatal("Cloud Lease Analysis handoff selection loop has no terminator")
	}
	selector := run[start : start+end+len("\ndone")]

	work := t.TempDir()
	for _, directory := range []string{"handoff/attempt-1", "analysis-input"} {
		if err := os.MkdirAll(filepath.Join(work, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"handoff.json", "receipt.json", "deployment-plan.json", "release-selector.json", "analysis-endpoint.json"} {
		if err := os.WriteFile(filepath.Join(work, "handoff", name), []byte("canonical-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, "handoff", "attempt-1", name), []byte("audit-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mapfileCompat := `mapfile() {
  [[ "$1" == -t ]]
  local variable="$2" line quoted
  eval "$variable=()"
  while IFS= read -r line; do
    printf -v quoted '%q' "$line"
    eval "$variable+=( $quoted )"
  done
}
`
	command := exec.Command("bash", "-c", "set -euo pipefail\n"+mapfileCompat+selector)
	command.Dir = work
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("select canonical handoff files: %v\n%s", err, output)
	}
	for _, name := range []string{"handoff.json", "receipt.json", "deployment-plan.json", "release-selector.json", "analysis-endpoint.json"} {
		got, err := os.ReadFile(filepath.Join(work, "analysis-input", name))
		if err != nil {
			t.Fatal(err)
		}
		if want := "canonical-" + name; string(got) != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestChatLifecycleUsesOneEncryptedDeploymentIdentityPerLease(t *testing.T) {
	root := repoRoot(t)
	orchestrator := readFile(t, filepath.Join(root, "scripts", "chat-lifecycle", "stage-orchestrate.sh"))
	for _, required := range []string{
		"ssh-keygen -q -t ed25519",
		"seal-deployment-identity",
		"encrypted-deployment-identity.json",
		`-f encrypted_deployment_identity_json="$(jq -c . "$attempt_dir/encrypted-deployment-identity.json")"`,
	} {
		if !strings.Contains(orchestrator, required) {
			t.Fatalf("stage orchestration per-Lease identity is missing %q", required)
		}
	}
	sealIdentity := strings.Index(orchestrator, `"$WK_CHAT_TOOL" seal-deployment-identity`)
	paidAcquire := strings.Index(orchestrator, `-f paid_authorization=create-paid-cloud-lease`)
	if sealIdentity < 0 || paidAcquire < 0 || sealIdentity >= paidAcquire {
		t.Fatal("stage orchestration does not validate and seal the deployment identity before paid Acquire")
	}
	if strings.Contains(orchestrator, "WK_CHAT_DEPLOYMENT_KEY:?required") ||
		strings.Contains(orchestrator, "WK_CHAT_DEPLOYMENT_PUBKEY:?required") {
		t.Fatal("stage orchestration still requires one standing deployment identity")
	}

	for _, workflowName := range []string{
		"chat-lifecycle-rehearsal.yml",
		"chat-lifecycle-formal.yml",
		"cloud-deployment-activate.yml",
		"chat-lifecycle-rehearsal-finalize.yml",
		"chat-lifecycle-formal-finalize.yml",
	} {
		workflow := string(readWorkflow(t, workflowName))
		if strings.Contains(workflow, "CLOUD_DEPLOYMENT_SSH_PRIVATE_KEY") {
			t.Fatalf("%s still uses the standing deployment SSH private key", workflowName)
		}
	}
	deployment := string(readWorkflow(t, "cloud-deployment-activate.yml"))
	for _, required := range []string{
		"encrypted_deployment_identity_json:",
		"WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY",
		"open-deployment-identity",
	} {
		if !strings.Contains(deployment, required) {
			t.Fatalf("Deployment Action encrypted identity contract is missing %q", required)
		}
	}
	for _, workflowName := range []string{"chat-lifecycle-rehearsal-finalize.yml", "chat-lifecycle-formal-finalize.yml"} {
		workflow := string(readWorkflow(t, workflowName))
		for _, required := range []string{"encrypted-deployment-identity.json", "WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY", "open-deployment-identity"} {
			if !strings.Contains(workflow, required) {
				t.Fatalf("%s encrypted finalizer identity is missing %q", workflowName, required)
			}
		}
	}
	for _, workflowName := range []string{"chat-lifecycle-rehearsal-finalize.yml", "chat-lifecycle-formal-finalize.yml"} {
		workflow := string(readWorkflow(t, workflowName))
		for _, required := range []string{
			"/ 1800", "WK_CHAT_ISSUE_DEDUPE_KEY", "scripts/chat-lifecycle/accrued-cost.sh", "aggregate_conservative_micros",
		} {
			if !strings.Contains(workflow, required) {
				t.Fatalf("%s 30-minute Issue monitor is missing %q", workflowName, required)
			}
		}
	}
	formalFinalizer := string(readWorkflow(t, "chat-lifecycle-formal-finalize.yml"))
	if !strings.Contains(formalFinalizer, "WK_CHAT_ISSUE_CLOSE=true") ||
		!strings.Contains(formalFinalizer, "steps.complete_upload.outcome") {
		t.Fatal("formal finalizer does not close the Issue only after the combined final Artifact")
	}
}

func TestChatLifecycleStopActionBlocksFormalProcurementAndRequestsBoundedOperatorStop(t *testing.T) {
	stop := string(readWorkflow(t, "chat-lifecycle-stop.yml"))
	for _, required := range []string{
		"operator-stop-chat-lifecycle",
		"chat-lifecycle-operator-stop-${{ inputs.request_id }}",
		"chat-lifecycle-rehearsal-finalize.yml",
		"chat-lifecycle-formal-finalize.yml",
	} {
		if !strings.Contains(stop, required) {
			t.Fatalf("stop workflow is missing %q", required)
		}
	}
	if strings.Contains(stop, "gh run cancel") {
		t.Fatal("Stop Action hard-cancels the cleanup owner while a dispatched Acquire or Deployment may still mutate")
	}
	orchestrator := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "stage-orchestrate.sh"))
	for _, required := range []string{
		"operator-stop-requested.sh", `gh run cancel "$DISPATCH_RUN_ID"`,
		"operator stop canceled the in-flight stage after exact zero-inventory cleanup",
		"paid Lease was released after cleanup",
	} {
		if !strings.Contains(orchestrator, required) {
			t.Fatalf("coordinated pre-handoff stop is missing %q", required)
		}
	}
	detector := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "operator-stop-requested.sh"))
	if !strings.Contains(detector, "authenticate-operator-stop-producer.sh") {
		t.Fatal("operator-stop detector does not use the shared protected-producer gate")
	}
	handoffWrite := strings.Index(orchestrator, `>"$WK_CHAT_OUTPUT_DIR/handoff.json"`)
	ownershipTransfer := -1
	if handoffWrite >= 0 {
		if relative := strings.Index(orchestrator[handoffWrite:], "keep_active=true"); relative >= 0 {
			ownershipTransfer = handoffWrite + relative
		}
	}
	if handoffWrite < 0 || ownershipTransfer <= handoffWrite ||
		!strings.Contains(orchestrator[handoffWrite:ownershipTransfer], "check_operator_stop") {
		t.Fatal("stage orchestration does not recheck durable stop intent immediately before handoff ownership transfer")
	}
	for _, workflowName := range []string{"chat-lifecycle-rehearsal-finalize.yml", "chat-lifecycle-formal-finalize.yml"} {
		workflow := string(readWorkflow(t, workflowName))
		for _, required := range []string{
			"operation:", "stop_authorization:", "operator-stop-chat-lifecycle", "WK_CHAT_OPERATOR_STOP",
			"Authenticate durable request-scoped stop marker", "operator-stop-requested.sh \"$REQUEST_ID\"",
			"Resolve durable operator-stop intent for this handoff", "operator-stop-requested.sh \"$WK_CHAT_REQUEST_ID\"",
		} {
			if !strings.Contains(workflow, required) {
				t.Fatalf("%s stop contract is missing %q", workflowName, required)
			}
		}
	}
	rehearsalFinalizer := string(readWorkflow(t, "chat-lifecycle-rehearsal-finalize.yml"))
	if !strings.Contains(rehearsalFinalizer, `authenticate-handoff-producer.sh "${{ matrix.handoff_run_id }}"`) {
		t.Fatal("rehearsal finalizer cannot consume a cleanup continuation produced by an earlier finalizer pass")
	}
	for _, relative := range []string{"rehearsal-finalize.sh", "formal-finalize.sh"} {
		body := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", relative))
		for _, required := range []string{"WK_CHAT_OPERATOR_STOP", "systemctl kill --kill-who=main --signal=SIGTERM", "operator_stop_deadline", "600"} {
			if !strings.Contains(body, required) {
				t.Fatalf("%s operator stop is missing %q", relative, required)
			}
		}
	}
}

func TestChatLifecycleWorkflowArmsAndDisarmsTheRehearsalFinalizerOnlyAtSafeLifecycleEdges(t *testing.T) {
	root := repoRoot(t)
	start := string(readWorkflow(t, "chat-lifecycle-rehearsal.yml"))
	arm := strings.Index(start, "Enable rehearsal finalizer safety schedule before paid orchestration")
	orchestrate := strings.Index(start, "Orchestrate until remote systemd owns the measured run")
	if arm < 0 || orchestrate <= arm ||
		!strings.Contains(start[arm:orchestrate], "scripts/chat-lifecycle/rehearsal-finalizer-state.sh enable") {
		t.Fatal("rehearsal workflow does not fail closed while arming the finalizer before paid orchestration")
	}

	finalizer := string(readWorkflow(t, "chat-lifecycle-rehearsal-finalize.yml"))
	for _, required := range []string{
		"name: Disable idle rehearsal finalizer schedule",
		"needs: [discover, finalize]",
		"if: always()",
		"scripts/chat-lifecycle/rehearsal-finalizer-state.sh disable-if-idle",
	} {
		if !strings.Contains(finalizer, required) {
			t.Fatalf("rehearsal finalizer idle disarm contract is missing %q", required)
		}
	}
	stop := string(readWorkflow(t, "chat-lifecycle-stop.yml"))
	stopArm := strings.Index(stop, "scripts/chat-lifecycle/rehearsal-finalizer-state.sh enable")
	stopDispatch := strings.Index(stop, `gh workflow run "$workflow"`)
	if stopArm < 0 || stopDispatch <= stopArm {
		t.Fatal("operator stop does not arm the rehearsal finalizer before exact dispatch")
	}

	directory := t.TempDir()
	calls := filepath.Join(directory, "calls")
	fakeGH := filepath.Join(directory, "gh")
	fake := `#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  *'/actions/artifacts?'*) printf '%s\n' '{"artifacts":[]}' ;;
  *'chat-lifecycle-rehearsal.yml/runs?'*)
    if [[ "${FAKE_ACTIVE_PRODUCER:-false}" == true && "$*" == *'status=in_progress'* ]]; then
      printf '%s\n' '{"total_count":1,"workflow_runs":[{"repository":{"full_name":"WuKongIM/WuKongIM"},"head_repository":{"full_name":"WuKongIM/WuKongIM"},"event":"workflow_dispatch","head_branch":"main","status":"in_progress"}]}'
    else
      printf '%s\n' '{"total_count":0,"workflow_runs":[]}'
    fi
    ;;
  *'chat-lifecycle-rehearsal-finalize.yml/enable'*) printf '%s\n' enable >>"$FAKE_CALLS" ;;
  *'chat-lifecycle-rehearsal-finalize.yml/disable'*) printf '%s\n' disable >>"$FAKE_CALLS" ;;
  *) echo "unexpected gh call: $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(fakeGH, []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "chat-lifecycle", "rehearsal-finalizer-state.sh")
	run := func(operation string, active bool) string {
		t.Helper()
		if err := os.WriteFile(calls, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("bash", script, operation)
		command.Dir = root
		command.Env = append(os.Environ(),
			"PATH="+directory+string(os.PathListSeparator)+os.Getenv("PATH"),
			"GH_TOKEN=test-token", "GITHUB_REPOSITORY=WuKongIM/WuKongIM",
			"FAKE_CALLS="+calls, "FAKE_ACTIVE_PRODUCER="+strconv.FormatBool(active))
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s finalizer state: %v\n%s", operation, err, output)
		}
		return string(output)
	}

	run("enable", false)
	if got := strings.TrimSpace(readFile(t, calls)); got != "enable" {
		t.Fatalf("enable calls = %q", got)
	}
	activeOutput := run("disable-if-idle", true)
	if got := strings.TrimSpace(readFile(t, calls)); got != "" ||
		!strings.Contains(activeOutput, "producer may still publish a handoff") {
		t.Fatalf("active producer disarm calls = %q, output = %q", got, activeOutput)
	}
	run("disable-if-idle", false)
	if got := strings.TrimSpace(readFile(t, calls)); got != "disable" {
		t.Fatalf("idle disarm calls = %q", got)
	}

	state := readFile(t, script)
	for _, required := range []string{
		"discover-active-handoffs.sh", "queued in_progress waiting requested pending",
		"head_repository.full_name == $repository", ".total_count <= 100", "api_attempts=4",
	} {
		if !strings.Contains(state, required) {
			t.Fatalf("rehearsal finalizer state gate is missing %q", required)
		}
	}
}

func TestOperatorStopDiscoveryAuthenticatesProducerAndFailsClosedAtInventoryBound(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(directory, "page.json")
	runPath := filepath.Join(directory, "run.json")
	fakeGH := `#!/bin/sh
case "$*" in
  *'/actions/artifacts'*) cat "$FAKE_ARTIFACT_PAGE" ;;
  *'/actions/runs/22'*) cat "$FAKE_STOP_RUN" ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGH), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path string, value any) {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(pagePath, map[string]any{"artifacts": []map[string]any{{
		"id": 44, "name": "chat-lifecycle-operator-stop-request-1", "expired": false,
		"created_at": "2030-01-01T00:00:00Z", "workflow_run": map[string]any{"id": 22},
	}}})
	baseRun := map[string]any{
		"repository": map[string]any{"full_name": "WuKongIM/WuKongIM"}, "head_repository": map[string]any{"full_name": "WuKongIM/WuKongIM"},
		"event": "workflow_dispatch", "head_branch": "main", "status": "completed", "conclusion": "success",
		"path": ".github/workflows/chat-lifecycle-stop.yml",
	}
	write(runPath, baseRun)
	run := func() ([]byte, error) {
		command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "operator-stop-requested.sh"), "request-1")
		command.Dir = root
		command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"GH_TOKEN=test", "GITHUB_REPOSITORY=WuKongIM/WuKongIM", "FAKE_ARTIFACT_PAGE="+pagePath, "FAKE_STOP_RUN="+runPath)
		return command.CombinedOutput()
	}
	output, err := run()
	if err != nil {
		t.Fatalf("authenticate operator stop: %v\n%s", err, output)
	}
	var observation struct {
		Schema     string `json:"schema"`
		RequestID  string `json:"request_id"`
		RunID      int    `json:"run_id"`
		ArtifactID int    `json:"artifact_id"`
	}
	if err := json.Unmarshal(output, &observation); err != nil {
		t.Fatal(err)
	}
	if observation.Schema != "wukongim.chat_lifecycle.operator_stop_observation/v1" || observation.RequestID != "request-1" || observation.RunID != 22 || observation.ArtifactID != 44 {
		t.Fatalf("operator stop observation = %#v", observation)
	}
	inProgress := mapsClone(baseRun)
	inProgress["status"] = "in_progress"
	inProgress["conclusion"] = nil
	write(runPath, inProgress)
	if output, err := run(); err != nil {
		t.Fatalf("in-progress protected stop did not become authoritative at Artifact upload: %v\n%s", err, output)
	}

	untrusted := mapsClone(baseRun)
	untrusted["path"] = ".github/workflows/untrusted.yml"
	write(runPath, untrusted)
	if output, err := run(); err == nil || len(output) != 0 {
		t.Fatalf("untrusted operator stop = %v, output %q", err, output)
	}

	fullPage := make([]map[string]any, 100)
	for index := range fullPage {
		fullPage[index] = map[string]any{"id": index + 1, "name": "chat-lifecycle-operator-stop-request-1", "expired": false,
			"created_at": "2030-01-01T00:00:00Z", "workflow_run": map[string]any{"id": 22}}
	}
	write(pagePath, map[string]any{"artifacts": fullPage})
	if output, err := run(); err == nil || !strings.Contains(string(output), "operator-stop discovery exceeded") {
		t.Fatalf("bounded operator-stop discovery = %v\n%s", err, output)
	}
}

func TestHandoffProducerAuthenticatesRehearsalFinalizerCleanupContinuation(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	runPath := filepath.Join(directory, "run.json")
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\ncat \"$FAKE_HANDOFF_RUN\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeRun := func(path, event string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"repository":      map[string]any{"full_name": "WuKongIM/WuKongIM"},
			"head_repository": map[string]any{"full_name": "WuKongIM/WuKongIM"},
			"event":           event, "head_branch": "main", "status": "completed", "conclusion": "failure", "path": path,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(runPath, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(name string) ([]byte, error) {
		output := filepath.Join(directory, name+".json")
		command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "authenticate-handoff-producer.sh"), "22", output)
		command.Dir = root
		command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"GH_TOKEN=test", "GITHUB_REPOSITORY=WuKongIM/WuKongIM", "WK_CHAT_STAGE=rehearsal", "FAKE_HANDOFF_RUN="+runPath)
		return command.CombinedOutput()
	}
	writeRun(".github/workflows/chat-lifecycle-rehearsal-finalize.yml", "schedule")
	if output, err := run("finalizer"); err != nil {
		t.Fatalf("authenticate finalizer continuation: %v\n%s", err, output)
	}
	writeRun(".github/workflows/untrusted.yml", "schedule")
	if output, err := run("untrusted"); err == nil {
		t.Fatalf("untrusted handoff producer accepted: %s", output)
	}
}

func TestChatLifecycleFinalizerPublishesEvidenceBeforeExactZeroInventoryCleanup(t *testing.T) {
	workflow := string(readWorkflow(t, "chat-lifecycle-rehearsal-finalize.yml"))
	upload := strings.Index(workflow, "Upload bounded terminal or diagnosis evidence before any Release")
	release := strings.Index(workflow, "Release exact Lease until zero inventory")
	zero := strings.Index(workflow, "Upload zero-inventory proof")
	if upload < 0 || release <= upload || zero <= release {
		t.Fatalf("finalization order is not report -> release -> zero proof")
	}
	for _, required := range []string{
		"id: release", "continue-on-error: true", "Classify rehearsal Release continuation",
		"cleanup-pending.json", "Upload rehearsal cleanup-pending continuation",
		"Fail after persisting a rehearsal Release continuation",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("rehearsal Release continuation is missing %q", required)
		}
	}
	for _, relative := range []string{
		"scripts/chat-lifecycle/stage-orchestrate.sh",
		"scripts/chat-lifecycle/release-until-zero.sh",
	} {
		body := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(relative)))
		if !strings.Contains(body, `.result.zero_inventory.selector == $expected[0].selector`) {
			t.Fatalf("%s does not require exact zero inventory", relative)
		}
		if strings.Contains(body, ".result.zero_inventory == true") ||
			!strings.Contains(body, "zero_inventory:($release[0].result.zero_inventory != null)") {
			t.Fatalf("%s treats the typed zero-inventory proof as a boolean", relative)
		}
	}
	authenticator := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "authenticate-cleanup-artifact.sh"))
	if strings.Contains(authenticator, ".result.zero_inventory == true") ||
		!strings.Contains(authenticator, `.result.zero_inventory | type == "object"`) {
		t.Fatal("cleanup authenticator does not validate the typed zero-inventory proof object")
	}
}

func TestCleanupAuthenticatorFallsBackToRetainedCompleteEvidenceAfterHandoffDeletion(t *testing.T) {
	authenticator := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "authenticate-cleanup-artifact.sh"))
	for _, required := range []string{
		`chat-lifecycle-${WK_CHAT_STAGE}-complete-${request_id}`,
		`$destination/complete/manifest.json`,
		`$destination/complete/terminal/receipt.json`,
		`.result.zero_inventory.selector`,
		`.receipt.account_id_hash`,
	} {
		if !strings.Contains(authenticator, required) {
			t.Fatalf("cleanup authenticator cannot recover after encrypted handoff deletion; missing %q", required)
		}
	}
}

func TestChatLifecycleRuntimeFailureRetainsOneBoundedDiagnosisWindow(t *testing.T) {
	for _, contract := range []struct {
		workflow string
		script   string
	}{
		{workflow: "chat-lifecycle-rehearsal-finalize.yml", script: "rehearsal-finalize.sh"},
		{workflow: "chat-lifecycle-formal-finalize.yml", script: "formal-finalize.sh"},
	} {
		workflow := string(readWorkflow(t, contract.workflow))
		for _, required := range []string{
			"diagnosis-window.json",
			"previous.outputs.diagnosis_pending",
			"steps.collect.outputs.diagnosis_pending == 'true'",
			"Upload bounded terminal or diagnosis evidence before any Release",
		} {
			if !strings.Contains(workflow, required) {
				t.Fatalf("%s diagnosis retention is missing %q", contract.workflow, required)
			}
		}
		script := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", contract.script))
		for _, required := range []string{
			"diagnosis_window_seconds=7200",
			"diagnosis_pending",
			"diagnosis-window.json",
			"systemctl stop wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service",
			"WK_CHAT_PREVIOUS_DIAGNOSIS_WINDOW",
		} {
			if !strings.Contains(script, required) {
				t.Fatalf("%s diagnosis window is missing %q", contract.script, required)
			}
		}
	}
}

func TestChatLifecycleReportUploadRetriesAreBoundedByFailureEvidenceTime(t *testing.T) {
	for _, workflowName := range []string{"chat-lifecycle-rehearsal-finalize.yml", "chat-lifecycle-formal-finalize.yml"} {
		workflow := string(readWorkflow(t, workflowName))
		for _, required := range []string{
			"continue-on-error: true",
			"report_rescue_deadline_epoch",
			"steps.final_upload.outcome == 'success'",
			"steps.final_upload.outcome == 'failure'",
		} {
			if !strings.Contains(workflow, required) {
				t.Fatalf("%s report rescue retry is missing %q", workflowName, required)
			}
		}
	}
}

func TestChatLifecycleInvalidOrExpiredDeploymentIdentityStillReleasesByProvider(t *testing.T) {
	for _, workflowName := range []string{"chat-lifecycle-rehearsal-finalize.yml", "chat-lifecycle-formal-finalize.yml"} {
		workflow := string(readWorkflow(t, workflowName))
		for _, required := range []string{
			"id: identity",
			"continue-on-error: true",
			"steps.identity.outcome == 'failure'",
			"deployment_identity_unavailable",
			"steps.credential_evidence.outputs.ready == 'true'",
		} {
			if !strings.Contains(workflow, required) {
				t.Fatalf("%s unavailable deployment identity cleanup is missing %q", workflowName, required)
			}
		}
	}
}

func TestChatLifecycleCompleteArtifactCombinesTerminalAndCleanupEvidence(t *testing.T) {
	for _, contract := range []struct {
		workflow string
		name     string
	}{
		{workflow: "chat-lifecycle-rehearsal-finalize.yml", name: "chat-lifecycle-rehearsal-complete-"},
		{workflow: "chat-lifecycle-formal-finalize.yml", name: "chat-lifecycle-formal-complete-"},
	} {
		workflow := string(readWorkflow(t, contract.workflow))
		assemble := strings.Index(workflow, "Assemble terminal evidence with exact zero-inventory proof")
		uploadComplete := strings.Index(workflow, contract.name)
		uploadCleanup := strings.Index(workflow, "cleanup-${{ matrix.request_id }}")
		deleteHandoff := strings.Index(workflow, "Delete released encrypted deployment handoff")
		if assemble < 0 || uploadComplete <= assemble || uploadCleanup <= uploadComplete || deleteHandoff <= uploadCleanup {
			t.Fatalf("%s final evidence order is not assemble -> complete -> cleanup -> credential deletion", contract.workflow)
		}
		for _, required := range []string{"zero-inventory.json", "cleanup.json", "finalization.json", "retention-days: 90"} {
			if !strings.Contains(workflow[assemble:], required) {
				t.Fatalf("%s complete Artifact is missing %q", contract.workflow, required)
			}
		}
	}
}

func TestChatLifecycleWorkflowsDurablyAdvanceTheExactTrackingIssue(t *testing.T) {
	root := repoRoot(t)
	commenter := readFile(t, filepath.Join(root, "scripts", "chat-lifecycle", "comment-request-issue.sh"))
	for _, required := range []string{
		`[chat-lifecycle] $WK_CHAT_REQUEST_ID`,
		"chat-lifecycle:${WK_CHAT_REQUEST_ID}:${issue_dedupe_key}",
		"/search/issues",
		"/issues/${issue_number}/comments",
		"WK_CHAT_ISSUE_CLOSE",
		"state=closed",
	} {
		if !strings.Contains(commenter, required) {
			t.Fatalf("request Issue updater is missing %q", required)
		}
	}
	for _, workflowName := range []string{
		"chat-lifecycle-rehearsal.yml",
		"chat-lifecycle-rehearsal-finalize.yml",
		"chat-lifecycle-formal.yml",
		"chat-lifecycle-formal-finalize.yml",
		"chat-lifecycle-stop.yml",
	} {
		workflow := string(readWorkflow(t, workflowName))
		if !strings.Contains(workflow, "issues: write") ||
			!strings.Contains(workflow, "scripts/chat-lifecycle/comment-request-issue.sh") {
			t.Fatalf("%s does not durably update the tracking Issue", workflowName)
		}
	}
}

func TestChatLifecycleIssueUpdaterIsExactAndIdempotent(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	fakeGH := filepath.Join(directory, "gh")
	capture := filepath.Join(directory, "comment.txt")
	closed := filepath.Join(directory, "closed.txt")
	if err := os.WriteFile(fakeGH, []byte(`#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *"/search/issues"* ]]; then
  printf '%s\n' '{"items":[{"number":42,"title":"[chat-lifecycle] request-1"}]}'
elif [[ "$*" == *"/issues/42/comments"* && "$*" == *"--method GET"* ]]; then
  if [[ -s "$FAKE_COMMENT" ]]; then
    jq -n --rawfile body "$FAKE_COMMENT" '[{body:$body}]'
  else
    printf '%s\n' '[]'
  fi
elif [[ "$*" == *"/issues/42/comments"* && "$*" == *"--method POST"* ]]; then
  while (( $# > 0 )); do
    if [[ "$1" == -f && "${2:-}" == body=* ]]; then
      printf '%s' "${2#body=}" >"$FAKE_COMMENT"
      exit 0
    fi
    shift
  done
  exit 2
elif [[ "$*" == *"/issues/42"* && "$*" == *"--method PATCH"* ]]; then
  printf '%s' closed >"$FAKE_CLOSED"
else
  exit 3
fi
`), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(closeIssue bool) {
		t.Helper()
		command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "comment-request-issue.sh"))
		command.Dir = root
		command.Env = append(os.Environ(),
			"PATH="+directory+":"+os.Getenv("PATH"), "GH_TOKEN=test", "GITHUB_REPOSITORY=WuKongIM/WuKongIM",
			"WK_CHAT_REQUEST_ID=request-1", "WK_CHAT_ISSUE_STATE=formal_running", "WK_CHAT_ISSUE_BODY=run=example",
			"WK_CHAT_ISSUE_CLOSE="+strconv.FormatBool(closeIssue),
			"FAKE_COMMENT="+capture, "FAKE_CLOSED="+closed)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("comment request Issue: %v\n%s", err, output)
		}
	}
	run(false)
	first := readFile(t, capture)
	if !strings.Contains(first, "chat-lifecycle:request-1:formal_running") || !strings.Contains(first, "run=example") ||
		!strings.Contains(first, "observed_at_utc=") || !strings.Contains(first, "observed_at_asia_shanghai=") {
		t.Fatalf("Issue comment = %q", first)
	}
	run(false)
	if second := readFile(t, capture); second != first {
		t.Fatalf("idempotent comment changed: %q -> %q", first, second)
	}
	run(true)
	if got := readFile(t, closed); got != "closed" {
		t.Fatalf("closed marker = %q", got)
	}
}

func TestChatLifecycleFinalArtifactCombinesRehearsalFormalCapacityCostAndCleanup(t *testing.T) {
	formalStarter := string(readWorkflow(t, "chat-lifecycle-formal.yml"))
	for _, required := range []string{
		`$WK_CHAT_OUTPUT_DIR/rehearsal`,
		`cp -a "$WK_CHAT_TRANSITION_DIR/." "$WK_CHAT_OUTPUT_DIR/rehearsal/"`,
	} {
		if !strings.Contains(formalStarter, required) {
			t.Fatalf("formal handoff does not retain rehearsal evidence: missing %q", required)
		}
	}
	formalFinalizer := string(readWorkflow(t, "chat-lifecycle-formal-finalize.yml"))
	for _, required := range []string{
		`$WK_CHAT_COMPLETE_DIR/rehearsal`,
		`$WK_CHAT_COMPLETE_DIR/formal`,
		`rehearsal/transition.json`,
		"rehearsal_committed_micros",
		"formal_quote_micros",
		"aggregate_conservative_micros",
		"formal/qualification.json",
		"formal/capacity/final.json",
		"rehearsal/evidence/manifest.json",
		"formal/evidence/manifest.json",
	} {
		if !strings.Contains(formalFinalizer, required) {
			t.Fatalf("combined final Artifact is missing %q", required)
		}
	}
	for _, collector := range []string{"rehearsal-finalize.sh", "formal-finalize.sh"} {
		content := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", collector))
		for _, required := range []string{"collect-terminal-evidence.sh", "terminal_evidence_incomplete"} {
			if !strings.Contains(content, required) {
				t.Fatalf("%s is missing %q", collector, required)
			}
		}
	}
}

func TestCloudDeploymentRunbookUsesLeaseBoundEncryptedIdentity(t *testing.T) {
	runbook := readFile(t, filepath.Join(repoRoot(t), "docs", "superpowers", "runbooks", "cloud-deployment-activate.md"))
	if strings.Contains(runbook, "CLOUD_DEPLOYMENT_SSH_PRIVATE_KEY") {
		t.Fatal("deployment runbook still documents a standing SSH private key")
	}
	for _, required := range []string{"encrypted-deployment-identity.json", "WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY"} {
		if !strings.Contains(runbook, required) {
			t.Fatalf("deployment runbook is missing %q", required)
		}
	}
}

func TestChatLifecycleFormalTransitionRunsOnFreshLeaseAndReportsBeforeRelease(t *testing.T) {
	rehearsalFinalizer := string(readWorkflow(t, "chat-lifecycle-rehearsal-finalize.yml"))
	release := strings.Index(rehearsalFinalizer, "Release exact Lease until zero inventory")
	transition := strings.Index(rehearsalFinalizer, "Seal fresh-formal transition after rehearsal evidence and zero inventory")
	if release < 0 || transition <= release || !strings.Contains(rehearsalFinalizer, "formal_transition/v1") {
		t.Fatal("formal transition is not sealed after rehearsal zero-inventory cleanup")
	}

	formal := string(readWorkflow(t, "chat-lifecycle-formal.yml"))
	for _, required := range []string{
		"group: chat-lifecycle-paid-${{ github.repository }}",
		"discover-formal-transitions.sh",
		"configs/cloud/chat-lifecycle/formal-v1.json",
		"Refuse procurement while any paid scenario lacks zero proof",
		"WK_CHAT_STAGE: formal",
	} {
		if !strings.Contains(formal, required) {
			t.Fatalf("formal starter is missing %q", required)
		}
	}
	for _, required := range []string{
		"receipt.json final.json rehearsal-result.json",
		"scripts/chat-lifecycle/accrued-cost.sh",
		".resources.capacity.network_transmit_bytes",
	} {
		if !strings.Contains(rehearsalFinalizer, required) {
			t.Fatalf("rehearsal-to-formal cost handoff is missing %q", required)
		}
	}
	for _, required := range []string{
		"quote.json receipt.json final.json rehearsal-result.json",
		"scripts/chat-lifecycle/accrued-cost.sh",
		"prior + rehearsal_cost",
	} {
		if !strings.Contains(formal, required) {
			t.Fatalf("formal accrued-cost authentication is missing %q", required)
		}
	}
	if strings.Contains(formal, "prior + rehearsal_quote") {
		t.Fatal("formal starter authenticates the accrued ledger using the full rehearsal Quote")
	}

	finalizer := string(readWorkflow(t, "chat-lifecycle-formal-finalize.yml"))
	upload := strings.Index(finalizer, "Upload bounded terminal or diagnosis evidence before any Release")
	formalRelease := strings.Index(finalizer, "Release exact formal Lease until zero inventory")
	zero := strings.Index(finalizer, "Upload formal zero-inventory proof")
	if upload < 0 || formalRelease <= upload || zero <= formalRelease ||
		!strings.Contains(finalizer, "scripts/chat-lifecycle/formal-finalize.sh") {
		t.Fatal("formal finalization order is not report -> exact Release -> zero proof")
	}

	collector := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "formal-finalize.sh"))
	for _, required := range []string{
		"wkbench-formal.service",
		"remote_root=/var/lib/wukongim-cloud/reports",
		"$remote_root/formal/final.json",
		"$remote_root/capacity/final.${extension}",
		"validate-formal-chain",
		"9 * 60 * 60",
	} {
		if !strings.Contains(collector, required) {
			t.Fatalf("formal collector is missing %q", required)
		}
	}
}

func TestRehearsalFinalizerSkipsFormalTransitionWhenFailureEvidenceHasNoRehearsalResult(t *testing.T) {
	workflow := string(readWorkflow(t, "chat-lifecycle-rehearsal-finalize.yml"))
	guard := strings.Index(workflow, `if [[ ! -f "$final_dir/rehearsal-result.json" ]]`)
	outcome := strings.Index(workflow, `outcome="$(jq -er .outcome "$final_dir/rehearsal-result.json")"`)
	if guard < 0 || outcome <= guard {
		t.Fatal("rehearsal finalizer does not guard missing failure-only rehearsal-result before reading it")
	}
	if !strings.Contains(workflow[guard:outcome], `echo 'ready=false' >>"$GITHUB_OUTPUT"`) {
		t.Fatal("missing rehearsal-result guard does not explicitly suppress the formal transition")
	}
}

func TestChatLifecycleWorkflowClosesBudgetHandoffAndDiscoverySafetyBoundaries(t *testing.T) {
	start := string(readWorkflow(t, "chat-lifecycle-rehearsal.yml"))
	for _, required := range []string{
		"group: chat-lifecycle-paid-${{ github.repository }}",
		"Refuse a second paid scenario while any prior Lease lacks zero proof",
		"for stage in rehearsal formal",
		"steps.handoff_upload.outcome != 'success'",
		`[[ ! -f "$WK_CHAT_SELECTOR" ]]`,
	} {
		if !strings.Contains(start, required) {
			t.Fatalf("start workflow is missing %q", required)
		}
	}
	if strings.Contains(start, "chat-lifecycle-paid-${{ github.repository }}-${{ inputs.request_id }}") {
		t.Fatal("request-scoped concurrency permits multiple paid scenario runs")
	}

	finalize := string(readWorkflow(t, "chat-lifecycle-rehearsal-finalize.yml"))
	for _, required := range []string{
		"discover-active-handoffs.sh",
		"WK_CHAT_SELECTOR: ${{ env.WK_CHAT_HANDOFF_DIR }}/release-selector.json",
		"mode=cleanup_complete",
	} {
		if !strings.Contains(finalize, required) {
			t.Fatalf("finalizer workflow is missing %q", required)
		}
	}
	formalStart := string(readWorkflow(t, "chat-lifecycle-formal.yml"))
	for _, required := range []string{
		"Detect immediate formal zero-inventory proof",
		"steps.immediate_cleanup.outputs.ready == 'true'",
		"chat-lifecycle-formal-cleanup-${{ matrix.request_id }}",
		"retention-days: 90",
	} {
		if !strings.Contains(formalStart, required) {
			t.Fatalf("formal pre-handoff cleanup evidence is missing %q", required)
		}
	}
	formalFinalize := string(readWorkflow(t, "chat-lifecycle-formal-finalize.yml"))
	if !strings.Contains(formalFinalize, "mode=cleanup_complete") ||
		!strings.Contains(formalFinalize, `steps.handoff.outputs.mode == 'cleanup_complete' || steps.release.outcome != 'skipped'`) {
		t.Fatal("formal finalizer cannot recover already-zero pre-handoff cleanup evidence")
	}
	discovery := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "discover-active-handoffs.sh"))
	for _, required := range []string{"max_pages=50", "artifact_api_attempts=4", "fetch_artifact_page", "inventory_complete=false", "active handoff discovery exceeded", "authenticate-handoff-producer.sh", "authenticate-cleanup-artifact.sh"} {
		if !strings.Contains(discovery, required) {
			t.Fatalf("bounded discovery is missing %q", required)
		}
	}
	if strings.Contains(discovery, "--paginate") {
		t.Fatal("finalizer discovery must keep repository history enumeration bounded")
	}

	provision := string(readWorkflow(t, "cloud-lease-provision.yml"))
	for _, required := range []string{
		"admitted_quote_json:",
		"Paid Acquire requires the exact admitted Quote.",
		"if: inputs.quote_only == true",
	} {
		if !strings.Contains(provision, required) {
			t.Fatalf("provision workflow is missing %q", required)
		}
	}

	release := string(readWorkflow(t, "cloud-lease-release.yml"))
	if !strings.Contains(release, `cron: "3,18,33,48 * * * *"`) ||
		!strings.Contains(release, "timeout-minutes: 40") {
		t.Fatal("generic Cloud Lease cleanup backstop is not a full provider-bounded scheduled pass")
	}
}

func TestChatLifecycleShellProgramsHaveValidBashSyntax(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 8 {
		t.Fatalf("chat-lifecycle shell programs = %d, want at least 8", len(paths))
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0111 == 0 {
			t.Fatalf("%s is not executable", filepath.Base(path))
		}
		command := exec.Command("bash", "-n", path)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("bash -n %s: %v\n%s", filepath.Base(path), err, output)
		}
	}
}

func TestChatLifecycleAccruedCostUsesHeldHoursObservedTrafficAndFullRetentionRisk(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	planPath := filepath.Join(directory, "run-plan.json")
	quotePath := filepath.Join(directory, "quote.json")
	plan := `{"lease_plan":{"host_groups":[{"role":"service","count":3},{"role":"load","count":1}]}}`
	quote := `{"quote":{"line_items":[` +
		`{"kind":"postpaid_host_hour","role":"service","quantity":18,"cost_micros":180},` +
		`{"kind":"postpaid_host_hour","role":"load","quantity":6,"cost_micros":120},` +
		`{"kind":"eip_public_egress_gib","quantity":10,"cost_micros":50},` +
		`{"kind":"eip_retention_policy_risk_hour","quantity":6,"cost_micros":60}]}}`
	if err := os.WriteFile(planPath, []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(quotePath, []byte(quote), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "chat-lifecycle", "accrued-cost.sh")
	for _, test := range []struct {
		name         string
		networkBytes string
		want         string
	}{
		{name: "observed one byte rounds to one GiB", networkBytes: "1", want: "165"},
		{name: "unknown traffic reserves full quoted allowance", networkBytes: "-1", want: "210"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash", script, planPath, quotePath,
				"2030-01-01T00:00:00.123Z", "2030-01-01T01:30:00Z", test.networkBytes)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("accrued cost: %v\n%s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != test.want {
				t.Fatalf("cost = %s, want %s", got, test.want)
			}
		})
	}
}

func TestChatLifecycleDiagnosisWindowRechecksAggregateOperationalBudget(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	planPath := filepath.Join(directory, "run-plan.json")
	quotePath := filepath.Join(directory, "quote.json")
	receiptPath := filepath.Join(directory, "receipt.json")
	reportPath := filepath.Join(directory, "final.json")
	plan := `{"lease_plan":{"budget":{"committed_micros":100,"operational_stop_micros":266},"host_groups":[{"role":"service","count":3},{"role":"load","count":1}]}}`
	quote := `{"quote":{"line_items":[` +
		`{"kind":"postpaid_host_hour","role":"service","quantity":18,"cost_micros":180},` +
		`{"kind":"postpaid_host_hour","role":"load","quantity":6,"cost_micros":120},` +
		`{"kind":"eip_public_egress_gib","quantity":10,"cost_micros":50},` +
		`{"kind":"eip_retention_policy_risk_hour","quantity":6,"cost_micros":60}]}}`
	receipt := `{"receipt":{"created_at":"2030-01-01T00:00:00Z"}}`
	report := `{"resources":{"capacity":{"network_transmit_bytes":1}}}`
	for path, content := range map[string]string{planPath: plan, quotePath: quote, receiptPath: receipt, reportPath: report} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(root, "scripts", "chat-lifecycle", "diagnosis-budget.sh")
	run := func() map[string]any {
		command := exec.Command("bash", script, planPath, quotePath, receiptPath, "2030-01-01T01:30:00Z", reportPath)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("diagnosis budget: %v\n%s", err, output)
		}
		var result map[string]any
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := run(); result["safe"] != true || result["aggregate_cost_micros"] != float64(265) {
		t.Fatalf("safe budget result = %#v", result)
	}
	plan = strings.Replace(plan, `"operational_stop_micros":266`, `"operational_stop_micros":265`, 1)
	if err := os.WriteFile(planPath, []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := run(); result["safe"] != false || result["aggregate_cost_micros"] != float64(265) {
		t.Fatalf("exhausted budget result = %#v", result)
	}
}

func TestChatLifecycleTerminalEvidenceCollectorIsBoundedAndComplete(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	output := filepath.Join(directory, "evidence")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"timeout": "#!/bin/sh\nshift\nexec \"$@\"\n",
		"ssh": `#!/bin/sh
case "$*" in
  *api/v1/targets*) printf '{"status":"success","data":{"activeTargets":[]}}\n' ;;
  *api/v1/query_range*) printf '{"status":"success","data":{"result":[]}}\n' ;;
  *) printf 'bounded terminal evidence\n' ;;
esac
`,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sshConfig := filepath.Join(directory, "ssh-config")
	if err := os.WriteFile(sshConfig, []byte("Host *\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "collect-terminal-evidence.sh"), "rehearsal", output)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_CLOUD_SSH_CONFIG="+sshConfig,
		"WK_CLOUD_SERVICE1_IP=10.0.0.1", "WK_CLOUD_SERVICE2_IP=10.0.0.2", "WK_CLOUD_SERVICE3_IP=10.0.0.3")
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("terminal evidence collector: %v\n%s", err, combined)
	}
	var manifest struct {
		Schema          string `json:"schema"`
		Complete        bool   `json:"complete"`
		CaptureFailures int    `json:"capture_failures"`
		Captures        []struct {
			State string `json:"state"`
			Bytes int64  `json:"bytes"`
		} `json:"captures"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(output, "manifest.json"))), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != "wukongim.chat_lifecycle.terminal_evidence/v1" || !manifest.Complete || manifest.CaptureFailures != 0 || len(manifest.Captures) != 16 {
		t.Fatalf("terminal evidence manifest = %#v", manifest)
	}
	for _, capture := range manifest.Captures {
		if capture.State != "collected" || capture.Bytes <= 0 || capture.Bytes > 4<<20 {
			t.Fatalf("terminal evidence capture = %#v", capture)
		}
	}

	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nprintf 'not-prometheus-json\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	invalidOutput := filepath.Join(directory, "invalid-evidence")
	invalidCommand := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "collect-terminal-evidence.sh"), "formal", invalidOutput)
	invalidCommand.Dir = root
	invalidCommand.Env = command.Env
	if combined, err := invalidCommand.CombinedOutput(); err == nil {
		t.Fatalf("terminal evidence collector accepted malformed Prometheus responses\n%s", combined)
	}
	var invalidManifest struct {
		Complete        bool `json:"complete"`
		CaptureFailures int  `json:"capture_failures"`
		Captures        []struct {
			State string `json:"state"`
		} `json:"captures"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(invalidOutput, "manifest.json"))), &invalidManifest); err != nil {
		t.Fatal(err)
	}
	if invalidManifest.Complete || invalidManifest.CaptureFailures != 2 {
		t.Fatalf("malformed Prometheus manifest = %#v", invalidManifest)
	}
}

func TestChatLifecycleActiveHandoffDiscoveryFailsClosedAtInventoryBound(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	artifacts := make([]map[string]any, 100)
	for index := range artifacts {
		artifacts[index] = map[string]any{"id": index + 1, "name": "unrelated", "expired": false}
	}
	page, err := json.Marshal(map[string]any{"artifacts": artifacts})
	if err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(directory, "page.json")
	if err := os.WriteFile(pagePath, page, 0o600); err != nil {
		t.Fatal(err)
	}
	gh := "#!/bin/sh\ncat \"$FAKE_ARTIFACT_PAGE\"\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "matrix.json")
	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "discover-active-handoffs.sh"), "", output)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_ARTIFACT_PAGE="+pagePath,
		"GH_TOKEN=test-token", "GITHUB_REPOSITORY=WuKongIM/WuKongIM", "WK_CHAT_STAGE=rehearsal")
	combined, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(combined), "active handoff discovery exceeded") {
		t.Fatalf("bounded discovery error = %v\n%s", err, combined)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("bounded discovery wrote output: %v", err)
	}
}

func TestChatLifecycleActiveHandoffDiscoveryRetriesTransientArtifactInventoryFailure(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	attemptsPath := filepath.Join(directory, "attempts")
	if err := os.WriteFile(attemptsPath, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gh := `#!/bin/sh
attempts="$(cat "$FAKE_ATTEMPTS")"
attempts=$((attempts + 1))
printf '%s\n' "$attempts" >"$FAKE_ATTEMPTS"
if [ "$attempts" -lt 3 ]; then
  printf '%s\n' 'tls: failed to verify certificate: x509: certificate is not valid for any names' >&2
  exit 1
fi
printf '%s\n' '{"artifacts":[]}'
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "matrix.json")
	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "discover-active-handoffs.sh"), "", output)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_ATTEMPTS="+attemptsPath,
		"GH_TOKEN=test-token", "GITHUB_REPOSITORY=WuKongIM/WuKongIM", "WK_CHAT_STAGE=rehearsal")
	combined, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("transient artifact inventory failure was not retried: %v\n%s", err, combined)
	}
	if attempts := strings.TrimSpace(readFile(t, attemptsPath)); attempts != "3" {
		t.Fatalf("artifact inventory attempts = %s, want 3", attempts)
	}
	var matrix struct {
		Include []json.RawMessage `json:"include"`
	}
	if err := json.Unmarshal([]byte(readFile(t, output)), &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Include) != 0 {
		t.Fatalf("active handoff matrix = %#v, want empty", matrix.Include)
	}
}

func TestChatLifecycleActiveHandoffDiscoveryFailsClosedAfterBoundedArtifactInventoryRetries(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	attemptsPath := filepath.Join(directory, "attempts")
	if err := os.WriteFile(attemptsPath, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gh := `#!/bin/sh
attempts="$(cat "$FAKE_ATTEMPTS")"
printf '%s\n' "$((attempts + 1))" >"$FAKE_ATTEMPTS"
printf '%s\n' 'tls: failed to verify certificate: x509: certificate is not valid for any names' >&2
exit 1
`
	for name, content := range map[string]string{
		"gh":    gh,
		"sleep": "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(directory, "matrix.json")
	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "discover-active-handoffs.sh"), "", output)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_ATTEMPTS="+attemptsPath,
		"GH_TOKEN=test-token", "GITHUB_REPOSITORY=WuKongIM/WuKongIM", "WK_CHAT_STAGE=rehearsal")
	combined, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(combined), "tls: failed to verify certificate") {
		t.Fatalf("persistent artifact inventory error = %v\n%s", err, combined)
	}
	if attempts := strings.TrimSpace(readFile(t, attemptsPath)); attempts != "4" {
		t.Fatalf("artifact inventory attempts = %s, want 4", attempts)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("persistent artifact inventory failure wrote output: %v", err)
	}
}

func TestChatLifecycleLedgerSelectorsExecuteAgainstTypedEvidence(t *testing.T) {
	root := repoRoot(t)
	tests := []struct {
		name   string
		filter string
		args   []string
		input  string
		want   string
	}{
		{
			name:   "active receipt creation",
			filter: "select-active-receipt-created-at.jq",
			args:   []string{"--arg", "request", "request-1"},
			input:  `{"schema":"wukongim.cloud_lease.receipt/v1","receipt":{"request_id":"request-1","state":"active","created_at":"2030-01-01T00:00:00Z"}}`,
			want:   "2030-01-01T00:00:00Z",
		},
		{
			name:   "formal committed cost",
			filter: "select-formal-transition-committed.jq",
			args:   []string{"--arg", "request", "request-1", "--arg", "source", strings.Repeat("a", 40), "--arg", "bundle", "sha256:" + strings.Repeat("b", 64)},
			input:  `{"schema":"wukongim.chat_lifecycle.formal_transition/v1","from_stage":"rehearsal","outcome":"rehearsal_pass","zero_inventory":true,"request_id":"request-1","source_sha":"` + strings.Repeat("a", 40) + `","bundle_digest":"sha256:` + strings.Repeat("b", 64) + `","committed_micros":123456}`,
			want:   "123456",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments := append([]string{"-er"}, test.args...)
			arguments = append(arguments, "-f", filepath.Join(root, "scripts", "chat-lifecycle", test.filter))
			command := exec.Command("jq", arguments...)
			command.Stdin = strings.NewReader(test.input)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("execute selector: %v\n%s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != test.want {
				t.Fatalf("selected value = %q, want %q", got, test.want)
			}
		})
	}
}

func TestChatLifecycleFinalizationMatrixCorrelatesCandidateArtifactSet(t *testing.T) {
	artifacts := []map[string]any{
		{"id": 101, "name": "chat-lifecycle-rehearsal-handoff-r1", "created_at": "2026-08-07T10:00:00Z", "workflow_run": map[string]any{"id": 1}},
		{"id": 102, "name": "chat-lifecycle-rehearsal-handoff-r1", "created_at": "2026-08-07T11:00:00Z", "workflow_run": map[string]any{"id": 2}},
		{"name": "chat-lifecycle-rehearsal-final-r1", "created_at": "2026-08-07T12:00:00Z", "workflow_run": map[string]any{"id": 3}},
		{"id": 104, "name": "chat-lifecycle-rehearsal-handoff-r2", "created_at": "2026-08-07T13:00:00Z", "workflow_run": map[string]any{"id": 4}},
		{"name": "chat-lifecycle-rehearsal-cleanup-r2", "created_at": "2026-08-07T14:00:00Z", "workflow_run": map[string]any{"id": 5}},
		{"id": 106, "name": "chat-lifecycle-rehearsal-handoff-r3", "created_at": "2026-08-07T15:00:00Z", "workflow_run": map[string]any{"id": 6}},
	}
	input, err := json.Marshal(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	filter := filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "select-finalization-matrix.jq")
	command := exec.Command("jq", "-c",
		"--arg", "prefix", "chat-lifecycle-rehearsal-handoff-",
		"--arg", "final_prefix", "chat-lifecycle-rehearsal-final-",
		"--arg", "cleanup_prefix", "chat-lifecycle-rehearsal-cleanup-",
		"--arg", "requested", "", "-f", filter)
	command.Stdin = bytes.NewReader(input)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Include []struct {
			RequestID         string `json:"request_id"`
			HandoffRunID      int    `json:"handoff_run_id"`
			HandoffArtifactID int    `json:"handoff_artifact_id"`
			FinalExists       bool   `json:"final_exists"`
			FinalRunID        int    `json:"final_run_id"`
			CleanupRunID      int    `json:"cleanup_run_id"`
		} `json:"include"`
	}
	if err := json.Unmarshal(output, &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Include) != 3 || matrix.Include[0].RequestID != "r3" || matrix.Include[0].FinalExists ||
		matrix.Include[1].RequestID != "r2" || matrix.Include[1].CleanupRunID != 5 ||
		matrix.Include[2].RequestID != "r1" || matrix.Include[2].HandoffRunID != 2 || matrix.Include[2].HandoffArtifactID != 102 ||
		!matrix.Include[2].FinalExists || matrix.Include[2].FinalRunID != 3 {
		t.Fatalf("matrix = %+v", matrix.Include)
	}
}

func TestChatLifecycleDeletesEncryptedHandoffOnlyAfterZeroInventoryArtifact(t *testing.T) {
	for _, workflowName := range []string{"chat-lifecycle-rehearsal-finalize.yml", "chat-lifecycle-formal-finalize.yml"} {
		workflow := string(readWorkflow(t, workflowName))
		zero := strings.Index(workflow, "Upload ")
		deleteIdentity := strings.Index(workflow, "Delete released encrypted deployment handoff")
		if zero < 0 || deleteIdentity <= zero || !strings.Contains(workflow[deleteIdentity:], "handoff_artifact_id") ||
			!strings.Contains(workflow[deleteIdentity:], "actions/artifacts/") {
			t.Fatalf("%s does not delete the encrypted handoff after retained zero proof", workflowName)
		}
	}
}

func TestChatLifecycleFormalStartMatrixConsumesOnlyUnspentTransition(t *testing.T) {
	artifacts := []map[string]any{
		{"name": "chat-lifecycle-formal-transition-r1", "created_at": "2026-08-07T10:00:00Z", "workflow_run": map[string]any{"id": 11}},
		{"name": "chat-lifecycle-formal-handoff-r1", "created_at": "2026-08-07T11:00:00Z", "workflow_run": map[string]any{"id": 12}},
		{"name": "chat-lifecycle-formal-transition-r2", "created_at": "2026-08-07T12:00:00Z", "workflow_run": map[string]any{"id": 21}},
		{"name": "chat-lifecycle-operator-stop-r2", "created_at": "2026-08-07T12:01:00Z", "workflow_run": map[string]any{"id": 22}},
		{"name": "chat-lifecycle-formal-transition-r3", "created_at": "2026-08-07T13:00:00Z", "workflow_run": map[string]any{"id": 31}},
		{"name": "chat-lifecycle-formal-cleanup-r3", "created_at": "2026-08-07T14:00:00Z", "workflow_run": map[string]any{"id": 32}},
	}
	input, err := json.Marshal(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	filter := filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "select-formal-start-matrix.jq")
	command := exec.Command("jq", "-c", "--arg", "requested", "", "-f", filter)
	command.Stdin = bytes.NewReader(input)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Include []struct {
			RequestID       string `json:"request_id"`
			TransitionRunID int    `json:"transition_run_id"`
		} `json:"include"`
	}
	if err := json.Unmarshal(output, &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Include) != 0 {
		t.Fatalf("formal start matrix = %+v", matrix.Include)
	}
	discovery := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "discover-formal-transitions.sh"))
	authenticator := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "authenticate-operator-stop-producer.sh"))
	if !strings.Contains(discovery, "max_pages=50") || !strings.Contains(discovery, "inventory_complete=false") ||
		!strings.Contains(discovery, "formal transition discovery exceeded") || strings.Contains(discovery, "--paginate") ||
		!strings.Contains(discovery, "authenticate-operator-stop-producer.sh") ||
		!strings.Contains(discovery, "chat-lifecycle-rehearsal-finalize.yml") ||
		!strings.Contains(authenticator, "chat-lifecycle-stop.yml") {
		t.Fatal("formal transition discovery is not bounded and producer-authenticated")
	}
}

func TestFormalTransitionDiscoveryFailsClosedAtInventoryBound(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	artifacts := make([]map[string]any, 100)
	for index := range artifacts {
		artifacts[index] = map[string]any{"id": index + 1, "name": "unrelated", "expired": false}
	}
	page, err := json.Marshal(map[string]any{"artifacts": artifacts})
	if err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(directory, "page.json")
	if err := os.WriteFile(pagePath, page, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\ncat \"$FAKE_ARTIFACT_PAGE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "matrix.json")
	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "discover-formal-transitions.sh"), "", output)
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_ARTIFACT_PAGE="+pagePath,
		"GH_TOKEN=test-token", "GITHUB_REPOSITORY=WuKongIM/WuKongIM")
	combined, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(combined), "formal transition discovery exceeded") {
		t.Fatalf("bounded transition discovery error = %v\n%s", err, combined)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("bounded transition discovery wrote output: %v", err)
	}
}

func TestFormalTransitionDiscoveryTrustsOnlyAuthenticatedStopProducer(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	artifactsPath := filepath.Join(directory, "artifacts.json")
	transitionRunPath := filepath.Join(directory, "transition-run.json")
	stopRunPath := filepath.Join(directory, "stop-run.json")
	fakeGH := filepath.Join(directory, "gh")
	artifacts := map[string]any{"artifacts": []map[string]any{
		{"name": "chat-lifecycle-formal-transition-r2", "created_at": "2026-08-07T12:00:00Z", "expired": false, "workflow_run": map[string]any{"id": 21}},
		{"name": "chat-lifecycle-operator-stop-r2", "created_at": "2026-08-07T12:01:00Z", "expired": false, "workflow_run": map[string]any{"id": 22}},
	}}
	writeJSON := func(path string, value any) {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(artifactsPath, artifacts)
	baseRun := map[string]any{
		"repository":      map[string]any{"full_name": "WuKongIM/WuKongIM"},
		"head_repository": map[string]any{"full_name": "WuKongIM/WuKongIM"},
		"event":           "workflow_dispatch", "head_branch": "main", "status": "completed", "conclusion": "success",
	}
	transitionRun := mapsClone(baseRun)
	transitionRun["path"] = ".github/workflows/chat-lifecycle-rehearsal-finalize.yml"
	writeJSON(transitionRunPath, transitionRun)
	if err := os.WriteFile(fakeGH, []byte(`#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == api ]]
case "$2" in
  *'/actions/artifacts?'*) cat "$FAKE_ARTIFACTS" ;;
  */actions/runs/21) cat "$FAKE_TRANSITION_RUN" ;;
  */actions/runs/22) cat "$FAKE_STOP_RUN" ;;
  *) exit 2 ;;
esac
`), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name           string
		stopPath       string
		stopStatus     string
		stopConclusion any
		wantRows       int
	}{
		{name: "protected in-progress stop blocks immediately", stopPath: ".github/workflows/chat-lifecycle-stop.yml", stopStatus: "in_progress", wantRows: 0},
		{name: "protected canceled stop remains durable", stopPath: ".github/workflows/chat-lifecycle-stop.yml", stopStatus: "completed", stopConclusion: "cancelled", wantRows: 0},
		{name: "untrusted name collision ignored", stopPath: ".github/workflows/another.yml", stopStatus: "completed", stopConclusion: "success", wantRows: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			stopRun := mapsClone(baseRun)
			stopRun["path"] = test.stopPath
			stopRun["status"] = test.stopStatus
			stopRun["conclusion"] = test.stopConclusion
			writeJSON(stopRunPath, stopRun)
			output := filepath.Join(directory, strings.ReplaceAll(test.name, " ", "-")+".json")
			command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "discover-formal-transitions.sh"), "", output)
			command.Dir = root
			command.Env = append(os.Environ(),
				"PATH="+directory+":"+os.Getenv("PATH"), "GH_TOKEN=test", "GITHUB_REPOSITORY=WuKongIM/WuKongIM",
				"FAKE_ARTIFACTS="+artifactsPath, "FAKE_TRANSITION_RUN="+transitionRunPath, "FAKE_STOP_RUN="+stopRunPath)
			if body, err := command.CombinedOutput(); err != nil {
				t.Fatalf("discover formal transitions: %v\n%s", err, body)
			}
			var matrix struct {
				Include []json.RawMessage `json:"include"`
			}
			if err := json.Unmarshal([]byte(readFile(t, output)), &matrix); err != nil {
				t.Fatal(err)
			}
			if len(matrix.Include) != test.wantRows {
				t.Fatalf("matrix rows = %d, want %d", len(matrix.Include), test.wantRows)
			}
		})
	}
}

func mapsClone(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
