//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDynamicNodeReadinessGateWritesEvidenceWhenStepFails(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	outDir := t.TempDir()
	binDir := t.TempDir()
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\nprintf 'fake go failure for %s\\n' \"$*\" >&2\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "scripts/e2e/dynamic-node-readiness-gate.sh",
		"--profile", "quick",
		"--out-dir", outDir,
		"--binary", filepath.Join(outDir, "wukongim-gofail"),
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
		"step controller failed",
		"--- step log:",
		"fake go failure for test ./pkg/controller -count=1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure output missing %q:\n%s", want, text)
		}
	}
	stepLogIndex := strings.Index(text, "--- step log:")
	if stepLogIndex < 0 {
		t.Fatalf("failure output missing step log marker:\n%s", text)
	}
	if !strings.Contains(text[stepLogIndex:], "fake go failure for test ./pkg/controller -count=1") {
		t.Fatalf("failure output did not tail the step log after marker:\n%s", text)
	}
	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	if !strings.Contains(summary, "- controller: FAIL") {
		t.Fatalf("summary missing failure marker:\n%s", summary)
	}
	commands := readFile(t, filepath.Join(outDir, "commands.log"))
	if !strings.Contains(commands, "controller") {
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
