//go:build integration

package scripts_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMThreeNodeLocalE2EBuildUsesSelectedWorktreeRevision(t *testing.T) {
	root := repoRoot(t)
	revisionCommand := exec.Command("git", "rev-parse", "HEAD")
	revisionCommand.Dir = root
	revisionOutput, err := revisionCommand.Output()
	if err != nil {
		t.Fatalf("resolve worktree revision: %v", err)
	}
	revision := strings.TrimSpace(string(revisionOutput))
	outputBin := filepath.Join(t.TempDir(), "wukongim")

	dryRunCommand := exec.Command("bash", "scripts/start-wukongim-three-nodes.sh",
		"--dry-run",
		"--build-tags", "e2e",
		"--backup-e2e-revision", revision,
		"--bin", outputBin,
	)
	dryRunCommand.Dir = root
	dryRunOutput, err := dryRunCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve local e2e build command: %v\n%s", err, dryRunOutput)
	}

	var buildCommand string
	for _, line := range strings.Split(string(dryRunOutput), "\n") {
		if strings.HasPrefix(line, "build_cmd=") {
			buildCommand = strings.TrimPrefix(line, "build_cmd=")
			break
		}
	}
	if buildCommand == "" {
		t.Fatalf("dry-run output missing build command:\n%s", dryRunOutput)
	}

	build := exec.Command("bash", "-c", buildCommand)
	build.Dir = root
	build.Env = append(
		envWithout("GOWORK"),
		"GOWORK="+filepath.Join(t.TempDir(), "missing-go.work"),
	)
	buildOutput, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build local e2e binary: %v\n%s", err, buildOutput)
	}

	versionCommand := exec.Command("go", "version", "-m", outputBin)
	versionOutput, err := versionCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect local e2e binary: %v\n%s", err, versionOutput)
	}
	want := "vcs.revision=" + revision
	if !strings.Contains(string(versionOutput), want) {
		t.Fatalf("binary metadata missing %q:\n%s", want, versionOutput)
	}
}
