package scripts_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGofailBuildScriptDryRunUsesSafeDefaults(t *testing.T) {
	root := repoRoot(t)
	outPath := filepath.Join(t.TempDir(), "wukongim-gofail")
	workDir := filepath.Join(t.TempDir(), "source")

	cmd := exec.Command("bash", "scripts/build-gofail-binary.sh", "--dry-run", "--out", outPath, "--work-dir", workDir)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)

	for _, want := range []string{
		"gofail_version=go.etcd.io/gofail@v0.2.0",
		"failpoint_packages=pkg/transport",
		"copy_excludes=.git .worktrees",
		"enable_cmd=gofail enable pkg/transport",
		"runtime_dep_cmd=GOWORK=off go get go.etcd.io/gofail/runtime@v0.2.0",
		"build_cmd=GOWORK=off go build -o " + outPath + " ./cmd/wukongim",
		"source_dir=" + workDir,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestGofailBuildScriptAllowsExtraFailpointPackages(t *testing.T) {
	root := repoRoot(t)
	outPath := filepath.Join(t.TempDir(), "wukongim-gofail")

	cmd := exec.Command("bash", "scripts/build-gofail-binary.sh", "--dry-run", "--out", outPath, "--package", "pkg/raftlog")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "failpoint_packages=pkg/transport pkg/raftlog") {
		t.Fatalf("expected package list to include default and extra package:\n%s", output)
	}
}
