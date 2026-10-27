//go:build integration

package scripts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestNativePackageBootstrapWatchdogTimerHandoff controls a legal cancellation
// between the real timer fork and PID publication. It uses no production hook.
func TestNativePackageBootstrapWatchdogTimerHandoff(t *testing.T) {
	sourceRoot := repoRoot(t)
	original, err := os.ReadFile(filepath.Join(sourceRoot, "scripts/validate-native-package-lifecycle-container.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const forkPublication = "    sleep \"$timeout_seconds\" &\n    timer_pid=$!\n"
	if strings.Count(string(original), forkPublication) != 1 {
		t.Fatal("fixture requires one bounded-watchdog timer fork/publication boundary")
	}
	// Builtins preserve $!: cancellation happens after the actual fork while the
	// production timer_pid is still zero. A latched trap can resume publication.
	const pausedPublication = `    sleep "$timeout_seconds" &
    if [[ "$timeout_seconds" == 30 && "$*" == *'systemctl is-system-running'* ]]; then
      handoff_timer_pid=$!
      printf '%s\n' "$handoff_timer_pid" > "$FIXTURE_ROOT/timer-pid"
      printf 'ready\n' > "$FIXTURE_ROOT/owner-ready"
      exec 8<> "$FIXTURE_ROOT/owner-release"
      IFS= read -r -t 2 handoff_unused <&8 || true
      exec 8>&-
    fi
    timer_pid=$!
`
	copySource := []byte(strings.Replace(string(original), forkPublication, pausedPublication, 1))
	runBootstrapWatchdogFixture(t, original, copySource)
}

// TestNativePackageBootstrapWatchdogTimerHardCancel proves cancellation owns
// an internal timer even when TERM is ignored before the actual sleep exec.
func TestNativePackageBootstrapWatchdogTimerHardCancel(t *testing.T) {
	sourceRoot := repoRoot(t)
	original, err := os.ReadFile(filepath.Join(sourceRoot, "scripts/validate-native-package-lifecycle-container.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const forkPublication = "    sleep \"$timeout_seconds\" &\n    timer_pid=$!\n"
	if strings.Count(string(original), forkPublication) != 1 {
		t.Fatal("fixture requires one bounded-watchdog timer fork/publication boundary")
	}
	// The short command cannot finish before this actual timer has installed its
	// signal disposition and the watchdog has retained the child's real PID.
	const uncooperativeTimer = `    if [[ "$timeout_seconds" == 30 && "$*" == *'systemctl is-system-running'* ]]; then
      (
        trap '' TERM
        IFS= read -r handoff_published < "$FIXTURE_ROOT/owner-release"
        printf 'ready\n' > "$FIXTURE_ROOT/owner-ready"
        exec /bin/sleep "$timeout_seconds"
      ) &
      timer_pid=$!
      printf '%s\n' "$timer_pid" > "$FIXTURE_ROOT/timer-pid"
      printf 'published\n' > "$FIXTURE_ROOT/owner-release"
    else
      sleep "$timeout_seconds" &
      timer_pid=$!
    fi
`
	copySource := []byte(strings.Replace(string(original), forkPublication, uncooperativeTimer, 1))
	runBootstrapWatchdogFixture(t, original, copySource)
}

// runBootstrapWatchdogFixture keeps both scenarios on the same real launcher,
// original process deadline, capped Docker boundary and cleanup receipts.
func runBootstrapWatchdogFixture(t *testing.T, original, copySource []byte) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"owner-ready", "owner-release"} {
		if err := syscall.Mkfifo(filepath.Join(root, name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "wukongim-test.rpm"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "launcher.sh")
	if err := os.WriteFile(launcher, copySource, 0700); err != nil {
		t.Fatal(err)
	}
	const fakeDocker = `#!/bin/bash
set -eu
printf '%s\n' "$*" >> "$FIXTURE_ROOT/calls"
case "$1" in
pull|run|rm) exit 0 ;;
inspect) if [[ "$*" == *--format* ]]; then echo false; else echo '{}'; fi; exit 0 ;;
logs)
  echo installer-output-retained
  trap '' PIPE TERM
  head -c 131072 /dev/zero | tr '\0' x || true
  exec sleep 60 ;;
exec)
  if [[ "$*" == *'systemctl is-system-running'* ]]; then
    IFS= read -r ready < "$FIXTURE_ROOT/owner-ready"
    echo initializing
    exit 1
  fi
  if [[ "$*" == *wk-native-bootstrap-stage* ]]; then echo systemd-exec;
  elif [[ "$*" == *'/proc/1/'* ]]; then echo systemd;
  elif [[ "$*" == *list-jobs* ]]; then echo fixture-systemd-job;
  elif [[ "$*" == *journalctl* ]]; then echo fixture-journal;
  else echo fixture-systemd-status; fi
  exit 0 ;;
esac
exit 18
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fakeDocker), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", launcher, "rockylinux:9", "rpm")
	cmd.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C", "TMPDIR=" + root, "WK_NATIVE_PACKAGE_DIST_DIR=" + root, "FIXTURE_ROOT=" + root}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	receipt := map[string]any{"schema": "native-bootstrap-watchdog-handoff/v1", "scenario": t.Name(), "source_sha256": bootstrapHandoffSHA(original), "copy_sha256": bootstrapHandoffSHA(copySource), "context_seconds": 25, "wait_delay_seconds": 1}
	defer func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			deadline := time.Now().Add(time.Second)
			for bootstrapHandoffGroupAlive(cmd.Process.Pid) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			receipt["group_gone_after_cleanup"] = !bootstrapHandoffGroupAlive(cmd.Process.Pid)
			if bootstrapHandoffGroupAlive(cmd.Process.Pid) {
				t.Error("owned fixture process group survived bounded cleanup")
			}
		}
		encoded, marshalErr := json.Marshal(receipt)
		if marshalErr != nil {
			t.Error(marshalErr)
			return
		}
		t.Log(string(encoded))
	}()
	started := time.Now()
	output, runErr := cmd.CombinedOutput()
	receipt["elapsed_ms"] = time.Since(started).Milliseconds()
	receipt["output_bytes"] = len(output)
	receipt["context_expired"] = ctx.Err() != nil
	if ctx.Err() != nil {
		t.Error("timer handoff lost cancellation: original 25s context expired")
	}
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || exit.ExitCode() != 1 {
		t.Errorf("original bootstrap exit1 was not preserved: %v", runErr)
	}
	missing := []string{}
	for _, marker := range []string{"systemd container stopped while booting", "native package lifecycle container logs:", "native package bootstrap stage:", "native package PID 1:", "native package systemd state:", "native package systemd jobs:", "native package system journal:", "wukongim service status:", "wukongim service journal:", "installer-output-retained"} {
		if !strings.Contains(string(output), marker) {
			missing = append(missing, marker)
		}
	}
	receipt["missing_markers"] = missing
	if len(missing) != 0 {
		t.Errorf("bootstrap diagnostics did not finish: missing %v", missing)
	}
	if len(output) > 70000 {
		t.Error("handoff diagnostics exceeded the original output bound")
	}
	markers, markerErr := filepath.Glob(filepath.Join(root, "wk-native-lifecycle-*"))
	if markerErr != nil {
		t.Error(markerErr)
	}
	receipt["remaining_marker_count"] = len(markers)
	if len(markers) != 0 {
		t.Error("watchdog markers survived launcher completion before TempDir cleanup")
	}
	timerBytes, readErr := os.ReadFile(filepath.Join(root, "timer-pid"))
	if readErr != nil {
		t.Errorf("actual handoff timer PID was not retained: %v", readErr)
	} else {
		timerPID, parseErr := strconv.Atoi(strings.TrimSpace(string(timerBytes)))
		if parseErr != nil || timerPID <= 1 {
			t.Errorf("invalid actual handoff timer PID: %v", parseErr)
		} else if cmd.Process != nil {
			timerGroup, timerErr := syscall.Getpgid(timerPID)
			if timerErr != nil && timerErr != syscall.ESRCH {
				t.Errorf("cannot verify actual timer ownership: %v", timerErr)
			}
			alive := timerErr == nil && timerGroup == cmd.Process.Pid
			receipt["timer_pid"] = timerPID
			receipt["timer_owned_before_cleanup"] = alive
			if alive {
				t.Error("actual timer survived launcher completion before cleanup")
			}
		}
	}
	if cmd.Process != nil {
		alive := bootstrapHandoffGroupAlive(cmd.Process.Pid)
		receipt["group_owned_before_cleanup"] = alive
		if alive && ctx.Err() == nil {
			t.Error("launcher returned before its owned process group joined")
		}
	}
	calls, readErr := os.ReadFile(filepath.Join(root, "calls"))
	if readErr != nil {
		t.Error(readErr)
		return
	}
	name := ""
	for _, line := range strings.Split(string(calls), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "inspect" && fields[1] != "--format" {
			name = fields[1]
		}
	}
	removed := name != "" && strings.Count(string(calls), "rm --force --volumes "+name+"\n") == 1
	receipt["exact_container_removed"] = removed
	if !removed {
		t.Error("exact fake container removal was not preserved")
	}
}

func bootstrapHandoffSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func bootstrapHandoffGroupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || err == syscall.EPERM
}
