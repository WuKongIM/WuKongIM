package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDynamicNodeReadinessGateDryRunQuickProfile(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	binary := filepath.Join(outDir, "wukongimv2-gofail")

	cmd := exec.Command("bash", "scripts/e2ev2/dynamic-node-readiness-gate.sh",
		"--dry-run",
		"--profile", "quick",
		"--out-dir", outDir,
		"--binary", binary,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"profile=quick",
		"out_dir=" + outDir,
		"gofail_binary=" + binary,
		"summary=" + filepath.Join(outDir, "summary.md"),
		"command_log=" + filepath.Join(outDir, "commands.log"),
		"controllerv2_cmd=GOWORK=off go test ./pkg/controllerv2 -count=1",
		"faults_default_cmd=GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1",
		"build_gofail_cmd=scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out " + binary,
		"stage10a_cmd=WK_E2EV2_BINARY=" + binary + " WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestStage10A|TestGofailDynamicNodeBinaryExposesFailpoints' -count=1 -timeout 15m -p=1",
		"diff_check_cmd=git diff --check",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "stage9d_cmd=") {
		t.Fatalf("quick profile should not include Stage9D command:\n%s", text)
	}
}

func TestDynamicNodeReadinessGateDryRunFullProfileIncludesStage9D(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	binary := filepath.Join(outDir, "wukongimv2-gofail")

	cmd := exec.Command("bash", "scripts/e2ev2/dynamic-node-readiness-gate.sh",
		"--dry-run",
		"--profile", "full",
		"--out-dir", outDir,
		"--binary", binary,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"profile=full",
		"stage9d_cmd=GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestDynamicNodeReadinessGateDryRunNormalizesRelativeOutDirFromAbsoluteScriptPath(t *testing.T) {
	root := repoRoot(t)
	callerDir := t.TempDir()
	relativeOutDir := filepath.Join("data", "manual")
	normalizedOutDir := filepath.Join(root, relativeOutDir)

	cmd := exec.Command("bash", filepath.Join(root, "scripts/e2ev2/dynamic-node-readiness-gate.sh"),
		"--dry-run",
		"--profile", "quick",
		"--out-dir", relativeOutDir,
	)
	cmd.Dir = callerDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"root_dir=" + root,
		"out_dir=" + normalizedOutDir,
		"summary=" + filepath.Join(normalizedOutDir, "summary.md"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestDynamicNodeReadinessGateDryRunNormalizesRelativeBinaryPath(t *testing.T) {
	root := repoRoot(t)
	relativeBinary := filepath.Join("data", "manual", "wukongimv2-gofail")
	normalizedBinary := filepath.Join(root, relativeBinary)

	cmd := exec.Command("bash", "scripts/e2ev2/dynamic-node-readiness-gate.sh",
		"--dry-run",
		"--profile", "quick",
		"--binary", relativeBinary,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"gofail_binary=" + normalizedBinary,
		"stage10a_cmd=WK_E2EV2_BINARY=" + normalizedBinary,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestDynamicNodeReadinessGateWritesEvidenceWhenStepFails(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	binDir := t.TempDir()
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\nprintf 'fake go failure for %s\\n' \"$*\" >&2\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "scripts/e2ev2/dynamic-node-readiness-gate.sh",
		"--profile", "quick",
		"--out-dir", outDir,
		"--binary", filepath.Join(outDir, "wukongimv2-gofail"),
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"WK_DYNAMIC_NODE_GATE_GO_BIN="+fakeGo,
		"WK_DYNAMIC_NODE_GATE_BUILD_GOFAIL_SCRIPT=/bin/true",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("gate should fail when go test fails:\n%s", output)
	}
	text := string(output)
	for _, want := range []string{
		"step controllerv2 failed",
		"--- step log:",
		"fake go failure for test ./pkg/controllerv2 -count=1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure output missing %q:\n%s", want, text)
		}
	}
	stepLogIndex := strings.Index(text, "--- step log:")
	if stepLogIndex < 0 {
		t.Fatalf("failure output missing step log marker:\n%s", text)
	}
	if !strings.Contains(text[stepLogIndex:], "fake go failure for test ./pkg/controllerv2 -count=1") {
		t.Fatalf("failure output did not tail the step log after marker:\n%s", text)
	}
	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	if !strings.Contains(summary, "- controllerv2: FAIL") {
		t.Fatalf("summary missing failure marker:\n%s", summary)
	}
	commands := readFile(t, filepath.Join(outDir, "commands.log"))
	if !strings.Contains(commands, "controllerv2") {
		t.Fatalf("commands log missing step name:\n%s", commands)
	}
	environment := readFile(t, filepath.Join(outDir, "environment.md"))
	for _, want := range []string{
		"profile: quick",
		"root_dir: " + root,
		"go_bin: " + fakeGo,
		"build_gofail_script: /bin/true",
	} {
		if !strings.Contains(environment, want) {
			t.Fatalf("environment metadata missing %q:\n%s", want, environment)
		}
	}
}
