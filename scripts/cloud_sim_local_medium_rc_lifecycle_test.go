//go:build !windows

package scripts_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLocalMediumRCBuildSmokeLifecycle(t *testing.T) {
	t.Run("injects overlays and keeps both revisions clean", func(t *testing.T) {
		fixture := newLocalMediumRCBuildFixture(t, false)
		output := filepath.Join(filepath.Dir(fixture.root), "artifacts-success", "nested", "smoke")
		command := fixture.command(output)
		if combined, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build-smoke failed: %v\n%s", err, combined)
		}
		if _, err := os.Stat(filepath.Join(output, "build-smoke-manifest.json")); err != nil {
			t.Fatalf("build-smoke manifest: %v", err)
		}
		if _, err := os.Lstat(output + ".failed"); !os.IsNotExist(err) {
			t.Fatalf("unexpected failed evidence path: %v", err)
		}
		fixture.requireOnlyPrimaryWorktree(t)
		fixture.requireClean(t)
	})

	t.Run("rejects dangling placeholder symlink and retains failure", func(t *testing.T) {
		fixture := newLocalMediumRCBuildFixture(t, true)
		output := filepath.Join(filepath.Dir(fixture.root), "artifacts-symlink", "missing-parent", "smoke")
		command := fixture.command(output)
		combined, err := command.CombinedOutput()
		if err == nil {
			t.Fatalf("build-smoke unexpectedly accepted dangling placeholder symlink:\n%s", combined)
		}
		if !strings.Contains(string(combined), "common overlay target already exists") {
			t.Fatalf("unexpected dangling-symlink failure:\n%s", combined)
		}
		if _, err := os.Lstat(fixture.outsideTarget); !os.IsNotExist(err) {
			t.Fatalf("placeholder followed dangling symlink outside worktree: %v", err)
		}
		if info, err := os.Stat(output + ".failed"); err != nil || !info.IsDir() {
			t.Fatalf("failed evidence was not retained in nested output parent: info=%v err=%v", info, err)
		}
		fixture.requireOnlyPrimaryWorktree(t)
		fixture.requireClean(t)
	})

	t.Run("term cleans compile process group and isolated clones", func(t *testing.T) {
		fixture := newLocalMediumRCBuildFixture(t, false)
		if err := os.WriteFile(filepath.Join(fixture.toolRoot, "sleep"), []byte("1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(filepath.Dir(fixture.root), "artifacts-signal", "signal", "smoke")
		command := fixture.command(output)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		var combined bytes.Buffer
		command.Stdout = &combined
		command.Stderr = &combined
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		started := filepath.Join(fixture.toolRoot, "compile-started")
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(started); err == nil {
				break
			}
			if time.Now().After(deadline) {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				_ = command.Wait()
				t.Fatalf("fake compile did not start:\n%s", combined.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
		fakeGoPID := readLocalMediumRCPID(t, filepath.Join(fixture.toolRoot, "fake-go.pid"))
		sleepPID := readLocalMediumRCPID(t, filepath.Join(fixture.toolRoot, "sleep.pid"))
		concurrent := fixture.command(output)
		concurrentOutput, concurrentErr := concurrent.CombinedOutput()
		if concurrentErr == nil || !strings.Contains(string(concurrentOutput), "output already exists") {
			t.Fatalf("concurrent build-smoke did not fail on the reserved output: err=%v\n%s", concurrentErr, concurrentOutput)
		}
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		waited := make(chan error, 1)
		go func() { waited <- command.Wait() }()
		select {
		case err := <-waited:
			if err == nil {
				t.Fatalf("TERM build-smoke unexpectedly succeeded:\n%s", combined.String())
			}
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			t.Fatal("TERM build-smoke did not exit within 10 seconds")
		}
		if info, err := os.Stat(output + ".failed"); err != nil || !info.IsDir() {
			t.Fatalf("interrupted evidence was not retained: info=%v err=%v\n%s", info, err, combined.String())
		}
		requireLocalMediumRCProcessGone(t, fakeGoPID)
		requireLocalMediumRCProcessGone(t, sleepPID)
		fixture.requireOnlyPrimaryWorktree(t)
		fixture.requireClean(t)
	})

	t.Run("term cleans clone process group and partial clone", func(t *testing.T) {
		fixture := newLocalMediumRCBuildFixture(t, false)
		if err := os.WriteFile(filepath.Join(fixture.toolRoot, "block-clone"), []byte("1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(filepath.Dir(fixture.root), "artifacts-clone-signal", "signal", "smoke")
		command := fixture.command(output)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		var combined bytes.Buffer
		command.Stdout = &combined
		command.Stderr = &combined
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		started := filepath.Join(fixture.toolRoot, "clone-started")
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(started); err == nil {
				break
			}
			if time.Now().After(deadline) {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				_ = command.Wait()
				t.Fatalf("fake clone did not start:\n%s", combined.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
		fakeGitPID := readLocalMediumRCPID(t, filepath.Join(fixture.toolRoot, "fake-git.pid"))
		cloneSleepPID := readLocalMediumRCPID(t, filepath.Join(fixture.toolRoot, "clone-sleep.pid"))
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		waited := make(chan error, 1)
		go func() { waited <- command.Wait() }()
		select {
		case err := <-waited:
			if err == nil {
				t.Fatalf("TERM during clone unexpectedly succeeded:\n%s", combined.String())
			}
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			t.Fatal("TERM during clone did not exit within 10 seconds")
		}
		if info, err := os.Stat(output + ".failed"); err != nil || !info.IsDir() {
			t.Fatalf("clone interruption evidence was not retained: info=%v err=%v\n%s", info, err, combined.String())
		}
		requireLocalMediumRCProcessGone(t, fakeGitPID)
		requireLocalMediumRCProcessGone(t, cloneSleepPID)
		fixture.requireOnlyPrimaryWorktree(t)
		fixture.requireClean(t)
	})
}

type localMediumRCBuildFixture struct {
	root          string
	baseline      string
	candidate     string
	buildScript   string
	goTool        string
	toolRoot      string
	scratchRoot   string
	outsideTarget string
}

func newLocalMediumRCBuildFixture(t *testing.T, danglingPlaceholder bool) localMediumRCBuildFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runLocalMediumRCGit(t, root, "init", "-q", "-b", "main")
	runLocalMediumRCGit(t, root, "config", "user.email", "local-medium-rc@example.invalid")
	runLocalMediumRCGit(t, root, "config", "user.name", "Local Medium RC")
	writeLocalMediumRCFile(t, filepath.Join(root, "README.md"), []byte("baseline\n"), 0o644)
	writeLocalMediumRCFile(t, filepath.Join(root, "internal", "app", "app.go"), []byte("package app\n"), 0o644)
	runLocalMediumRCGit(t, root, "add", "README.md", "internal/app/app.go")
	runLocalMediumRCGit(t, root, "commit", "-q", "-m", "baseline")
	baseline := strings.TrimSpace(runLocalMediumRCGit(t, root, "rev-parse", "HEAD"))

	sourceDir := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "local-medium-rc")
	destinationDir := filepath.Join(root, "scripts", "cloud-sim", "local-medium-rc")
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"build-smoke.sh",
		"workload_test.go.overlay",
		"adapter_baseline_test.go.overlay",
		"adapter_candidate_test.go.overlay",
		"validate-build-smoke-manifest.jq",
	} {
		content, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		writeLocalMediumRCFile(t, filepath.Join(destinationDir, name), content, mode)
	}
	lock := fmt.Sprintf("{\n  \"schema\": \"wukongim/local-medium-rc-baseline-lock/v1\",\n  \"baseline_source_sha\": %q,\n  \"source_run_identity\": \"gh-1-1\"\n}\n", baseline)
	writeLocalMediumRCFile(t, filepath.Join(destinationDir, "baseline-lock.json"), []byte(lock), 0o644)

	outsideTarget := filepath.Join(filepath.Dir(root), "outside-placeholder-target")
	if danglingPlaceholder {
		target := filepath.Join(root, "internal", "app", "zz_local_medium_rc_workload_test.go")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideTarget, target); err != nil {
			t.Fatal(err)
		}
	}
	runLocalMediumRCGit(t, root, "add", "-A")
	runLocalMediumRCGit(t, root, "commit", "-q", "-m", "candidate")
	candidate := strings.TrimSpace(runLocalMediumRCGit(t, root, "rev-parse", "HEAD"))

	toolRoot := filepath.Join(filepath.Dir(root), "fake-go")
	writeLocalMediumRCFakeToolchain(t, toolRoot)
	scratchRoot := filepath.Join(filepath.Dir(root), "scratch")
	if err := os.MkdirAll(scratchRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	return localMediumRCBuildFixture{
		root:          root,
		baseline:      baseline,
		candidate:     candidate,
		buildScript:   filepath.Join(destinationDir, "build-smoke.sh"),
		goTool:        filepath.Join(toolRoot, "bin", "go"),
		toolRoot:      toolRoot,
		scratchRoot:   scratchRoot,
		outsideTarget: outsideTarget,
	}
}

func (f localMediumRCBuildFixture) command(output string) *exec.Cmd {
	command := exec.Command("/bin/bash", f.buildScript, f.baseline, f.candidate, output)
	command.Dir = f.root
	command.Env = append(os.Environ(), "WK_LOCAL_RC_GO="+f.goTool, "TMPDIR="+f.scratchRoot, "PATH="+filepath.Join(f.toolRoot, "bin")+":"+os.Getenv("PATH"))
	return command
}

func (f localMediumRCBuildFixture) requireOnlyPrimaryWorktree(t *testing.T) {
	t.Helper()
	listing := runLocalMediumRCGit(t, f.root, "worktree", "list", "--porcelain")
	count := 0
	for _, line := range strings.Split(listing, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("worktree count = %d, want 1\n%s", count, listing)
	}
	entries, err := os.ReadDir(f.scratchRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary source clones were retained: %v", entries)
	}
}

func (f localMediumRCBuildFixture) requireClean(t *testing.T) {
	t.Helper()
	if status := runLocalMediumRCGit(t, f.root, "status", "--porcelain=v1", "--untracked-files=normal"); strings.TrimSpace(status) != "" {
		t.Fatalf("fixture repository is dirty:\n%s", status)
	}
}

func writeLocalMediumRCFakeToolchain(t *testing.T, root string) {
	t.Helper()
	goTool := `#!/bin/bash
set -euo pipefail
tool_root="$(cd "$(dirname "$0")/.." && pwd -P)"
case "${1:-}" in
  env)
    case "${2:-}" in
      GOROOT) printf '%s\n' "$tool_root" ;;
      GOHOSTOS) printf 'linux\n' ;;
      GOHOSTARCH) printf 'arm64\n' ;;
      *) exit 2 ;;
    esac
    ;;
  version)
    if [[ "${2:-}" == "-m" ]]; then
      binary="$3"
      revision="$(<"$binary.revision")"
      printf '%s: go1.25.11\n' "$binary"
      printf 'build\tvcs.revision=%s\n' "$revision"
      printf 'build\tvcs.modified=false\n'
      printf 'build\t-trimpath=true\n'
    else
      printf 'go version go1.25.11 linux/arm64\n'
    fi
    ;;
  test)
    output=""
    overlay=""
    buildvcs=""
    while (($#)); do
      if [[ "$1" == "-o" ]]; then
        output="$2"
        shift 2
        continue
      fi
      if [[ "$1" == "-overlay" ]]; then
        overlay="$2"
        shift 2
        continue
      fi
      if [[ "$1" == "-buildvcs=true" ]]; then
        buildvcs=true
      fi
      shift
    done
    [[ -n "$output" ]] || { printf 'missing output argument\n' >&2; exit 2; }
    [[ -f "$overlay" && ! -L "$overlay" ]] || { printf 'invalid overlay: %s\n' "$overlay" >&2; exit 2; }
    [[ "$buildvcs" == true ]] || { printf 'buildvcs was not required\n' >&2; exit 2; }
    [[ "${GIT_CONFIG_COUNT:-}" == 1 && "${GIT_CONFIG_KEY_0:-}" == status.showUntrackedFiles && "${GIT_CONFIG_VALUE_0:-}" == no ]] || { printf 'untracked-only placeholder VCS policy absent\n' >&2; exit 2; }
    common_target="$PWD/internal/app/zz_local_medium_rc_workload_test.go"
    adapter_target="$PWD/internal/app/zz_local_medium_rc_adapter_test.go"
    [[ -f "$common_target" && ! -L "$common_target" && ! -s "$common_target" ]] || { printf 'invalid common placeholder: %s\n' "$common_target" >&2; exit 2; }
    [[ -f "$adapter_target" && ! -L "$adapter_target" && ! -s "$adapter_target" ]] || { printf 'invalid adapter placeholder: %s\n' "$adapter_target" >&2; exit 2; }
    /usr/bin/grep -F "\"$common_target\"" "$overlay" >/dev/null || { printf 'common target absent from overlay\n' >&2; exit 2; }
    /usr/bin/grep -F "\"$adapter_target\"" "$overlay" >/dev/null || { printf 'adapter target absent from overlay\n' >&2; exit 2; }
    /usr/bin/grep -F 'workload_test.go.overlay' "$overlay" >/dev/null || { printf 'common source absent from overlay\n' >&2; exit 2; }
    case "$output" in
      *baseline*) adapter_name=adapter_baseline_test.go.overlay ;;
      *candidate*) adapter_name=adapter_candidate_test.go.overlay ;;
      *) exit 2 ;;
    esac
    /usr/bin/grep -F "$adapter_name" "$overlay" >/dev/null || { printf 'adapter source absent from overlay: %s\n' "$adapter_name" >&2; exit 2; }
    if [[ -f "$tool_root/sleep" ]]; then
      printf '%s\n' "$$" >"$tool_root/fake-go.pid"
      /bin/sleep 60 &
      sleep_pid="$!"
      printf '%s\n' "$sleep_pid" >"$tool_root/sleep.pid"
      : >"$tool_root/compile-started"
      wait "$sleep_pid"
    else
      : >"$tool_root/compile-started"
    fi
    /bin/cp "$tool_root/fake-test-binary" "$output"
    /bin/chmod 755 "$output"
    /usr/bin/git -C "$PWD" rev-parse HEAD >"$output.revision"
    ;;
  *) exit 2 ;;
esac
`
	fakeBinary := `#!/bin/bash
set -euo pipefail
case "$(basename "$0")" in
  *baseline*) variant=baseline ;;
  *candidate*) variant=candidate ;;
  *) exit 2 ;;
esac
printf '=== RUN   TestLocalMediumRCWorkloadEquivalence\n'
printf '    fixture: WKRC-EQUIVALENCE variant=%s physical_hash_slots=256 logical_slots=10 recipients=512 target_groups=221 online_routes=55 preferred_leader_mismatches=256 digest=1\n' "$variant"
printf '%s\n' '--- PASS: TestLocalMediumRCWorkloadEquivalence (0.00s)'
printf 'PASS\n'
`
	gitTool := `#!/bin/bash
set -euo pipefail
tool_root="$(cd "$(dirname "$0")/.." && pwd -P)"
if [[ "${1:-}" == clone && -f "$tool_root/block-clone" ]]; then
  printf '%s\n' "$$" >"$tool_root/fake-git.pid"
  /bin/sleep 60 &
  sleep_pid="$!"
  printf '%s\n' "$sleep_pid" >"$tool_root/clone-sleep.pid"
  : >"$tool_root/clone-started"
  wait "$sleep_pid"
fi
exec /usr/bin/git "$@"
`
	writeLocalMediumRCFile(t, filepath.Join(root, "bin", "go"), []byte(goTool), 0o755)
	writeLocalMediumRCFile(t, filepath.Join(root, "bin", "git"), []byte(gitTool), 0o755)
	writeLocalMediumRCFile(t, filepath.Join(root, "fake-test-binary"), []byte(fakeBinary), 0o755)
	for _, name := range []string{"compile", "link", "asm"} {
		writeLocalMediumRCFile(t, filepath.Join(root, "pkg", "tool", "linux_arm64", name), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	}
}

func writeLocalMediumRCFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

func runLocalMediumRCGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("/usr/bin/git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func readLocalMediumRCPID(t *testing.T, path string) int {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func requireLocalMediumRCProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if err == syscall.ESRCH {
			return
		}
		if err != nil {
			t.Fatalf("probe process %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d survived build-smoke cleanup", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
