//go:build integration

package scripts_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMThreeNodeLocalE2EBuildUsesSelectedWorktreeRevision(t *testing.T) {
	sourceRoot := repoRoot(t)
	root, revision := nestedBuildWorktree(t, sourceRoot)
	outputBin := filepath.Join(t.TempDir(), "wukongim")
	// Keep this assertion independent from link artifacts produced by earlier
	// repository checks so it always exercises this checkout's VCS context.
	buildCache := t.TempDir()

	dryRunCommand := exec.Command("bash", "scripts/start-wukongim-three-nodes.sh",
		"--dry-run",
		"--build-tags", "e2e",
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
		envWithout("GOCACHE", "GOWORK"),
		"GOCACHE="+buildCache,
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

// nestedBuildWorktree creates a controlled linked-worktree topology whose
// primary checkout intentionally points at an unrelated synthetic commit. Go 1.25
// does not recognize a linked worktree's .git file as a VCS root, so placing
// the selected worktree below an ordinary repository root makes the launcher
// binding observable without depending on the outer test executor's layout.
// See https://go.dev/issue/58218.
func nestedBuildWorktree(t *testing.T, sourceRoot string) (string, string) {
	t.Helper()
	fixtureRoot := filepath.Join(t.TempDir(), "repo")
	runGit(t, sourceRoot, "init", "--quiet", fixtureRoot)
	runGit(t, fixtureRoot,
		"-c", "user.name=WuKongIM Test",
		"-c", "user.email=test@wukongim.invalid",
		"commit", "--quiet", "--allow-empty", "-m", "primary fixture",
	)
	runGit(t, fixtureRoot, "fetch", "--quiet", "--update-shallow", "--no-tags", sourceRoot, "HEAD")

	selectedRoot := filepath.Join(fixtureRoot, ".worktrees", "selected")
	runGit(t, fixtureRoot, "worktree", "add", "--quiet", "--detach", selectedRoot, "FETCH_HEAD")
	revision := strings.TrimSpace(runGit(t, selectedRoot, "rev-parse", "HEAD"))
	return selectedRoot, revision
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
