package scripts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	orchestrator := readFile(t, filepath.Join(root, "scripts", "chat-lifecycle", "rehearsal-orchestrate.sh"))
	ordered := []string{
		"cloud-deployment-bundle.yml",
		"-f quote_only=true",
		"-f quote_only=false",
		"cloud-deployment-activate.yml",
		"systemctl start --no-block '$stage_service'",
		"run-start.json",
		"keep_active=true",
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
	if strings.Count(orchestrator, "cleanup_attempted=false") != 2 {
		t.Fatal("fresh deployment retry does not re-arm EXIT cleanup ownership")
	}
	workflow := string(readWorkflow(t, "chat-lifecycle-rehearsal.yml"))
	if strings.Count(workflow, "      "+"source_sha:") != 1 ||
		!strings.Contains(workflow, "Orchestrate until remote systemd owns the measured run") {
		t.Fatal("rehearsal workflow does not retain the fixed remote-ownership boundary")
	}
}

func TestChatLifecycleFinalizerPublishesEvidenceBeforeExactZeroInventoryCleanup(t *testing.T) {
	workflow := string(readWorkflow(t, "chat-lifecycle-rehearsal-finalize.yml"))
	upload := strings.Index(workflow, "Upload terminal report before any Release")
	release := strings.Index(workflow, "Release exact Lease until zero inventory")
	zero := strings.Index(workflow, "Upload zero-inventory proof")
	if upload < 0 || release <= upload || zero <= release {
		t.Fatalf("finalization order is not report -> release -> zero proof")
	}
	for _, relative := range []string{
		"scripts/chat-lifecycle/rehearsal-orchestrate.sh",
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

	finalizer := string(readWorkflow(t, "chat-lifecycle-formal-finalize.yml"))
	upload := strings.Index(finalizer, "Upload terminal formal evidence before any Release")
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
	} {
		if !strings.Contains(finalize, required) {
			t.Fatalf("finalizer workflow is missing %q", required)
		}
	}
	discovery := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "discover-active-handoffs.sh"))
	for _, required := range []string{"for page in 1 2 3 4 5", "authenticate-handoff-producer.sh", "authenticate-cleanup-artifact.sh"} {
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

func TestChatLifecycleFinalizationMatrixCorrelatesCandidateArtifactSet(t *testing.T) {
	artifacts := []map[string]any{
		{"name": "chat-lifecycle-rehearsal-handoff-r1", "created_at": "2026-08-07T10:00:00Z", "workflow_run": map[string]any{"id": 1}},
		{"name": "chat-lifecycle-rehearsal-handoff-r1", "created_at": "2026-08-07T11:00:00Z", "workflow_run": map[string]any{"id": 2}},
		{"name": "chat-lifecycle-rehearsal-final-r1", "created_at": "2026-08-07T12:00:00Z", "workflow_run": map[string]any{"id": 3}},
		{"name": "chat-lifecycle-rehearsal-handoff-r2", "created_at": "2026-08-07T13:00:00Z", "workflow_run": map[string]any{"id": 4}},
		{"name": "chat-lifecycle-rehearsal-cleanup-r2", "created_at": "2026-08-07T14:00:00Z", "workflow_run": map[string]any{"id": 5}},
		{"name": "chat-lifecycle-rehearsal-handoff-r3", "created_at": "2026-08-07T15:00:00Z", "workflow_run": map[string]any{"id": 6}},
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
			RequestID    string `json:"request_id"`
			HandoffRunID int    `json:"handoff_run_id"`
			FinalExists  bool   `json:"final_exists"`
			FinalRunID   int    `json:"final_run_id"`
			CleanupRunID int    `json:"cleanup_run_id"`
		} `json:"include"`
	}
	if err := json.Unmarshal(output, &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Include) != 3 || matrix.Include[0].RequestID != "r3" || matrix.Include[0].FinalExists ||
		matrix.Include[1].RequestID != "r2" || matrix.Include[1].CleanupRunID != 5 ||
		matrix.Include[2].RequestID != "r1" || matrix.Include[2].HandoffRunID != 2 ||
		!matrix.Include[2].FinalExists || matrix.Include[2].FinalRunID != 3 {
		t.Fatalf("matrix = %+v", matrix.Include)
	}
}

func TestChatLifecycleFormalStartMatrixConsumesOnlyUnspentTransition(t *testing.T) {
	artifacts := []map[string]any{
		{"name": "chat-lifecycle-formal-transition-r1", "created_at": "2026-08-07T10:00:00Z", "workflow_run": map[string]any{"id": 11}},
		{"name": "chat-lifecycle-formal-handoff-r1", "created_at": "2026-08-07T11:00:00Z", "workflow_run": map[string]any{"id": 12}},
		{"name": "chat-lifecycle-formal-transition-r2", "created_at": "2026-08-07T12:00:00Z", "workflow_run": map[string]any{"id": 21}},
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
	if len(matrix.Include) != 1 || matrix.Include[0].RequestID != "r2" || matrix.Include[0].TransitionRunID != 21 {
		t.Fatalf("formal start matrix = %+v", matrix.Include)
	}
	discovery := readFile(t, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "discover-formal-transitions.sh"))
	if !strings.Contains(discovery, "for page in 1 2 3 4 5") || strings.Contains(discovery, "--paginate") ||
		!strings.Contains(discovery, "chat-lifecycle-rehearsal-finalize.yml") {
		t.Fatal("formal transition discovery is not bounded and producer-authenticated")
	}
}
