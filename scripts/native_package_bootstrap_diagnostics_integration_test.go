//go:build integration

package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Exercise the real launcher and its cleanup with a controlled Docker boundary.
// This proves diagnostic ownership and bounds, not that a distro boots systemd.
func TestNativePackageBootstrapFailureDiagnostics(t *testing.T) {
	for _, mode := range []string{"installer", "systemd", "probe-failure", "probe-timeout", "large-output", "cap-and-timeout"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "wukongim-test.rpm"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			fake := `#!/bin/bash
set -eu
printf '%s\n' "$*" >>"$FIXTURE_ROOT/calls"
case "$1" in
pull) exit 0 ;;
run) printf '%s\n' "${!#}" >"$FIXTURE_ROOT/bootstrap"; exit 0 ;;
inspect) if [[ "$*" == *--format* ]]; then echo false; else echo '{}'; fi; exit 0 ;;
rm) exit 0 ;;
logs)
  echo 'installer-output-retained'
  if [[ "$FIXTURE_MODE" == cap-and-timeout ]]; then
    trap '' PIPE TERM
    head -c 131072 /dev/zero | tr '\0' x || true
    exec sleep 60
  fi
  if [[ "$FIXTURE_MODE" == large-output ]]; then
    head -c 131072 /dev/zero | tr '\0' x
    echo 'diagnostic-overflow-tail'
  fi
  exit 0 ;;
exec)
  if [[ "$*" == *'systemctl is-system-running'* ]]; then echo initializing; exit 1; fi
  if [[ "$FIXTURE_MODE" == probe-failure ]]; then echo 'fixture-diagnostic-error' >&2; exit 17; fi
  if [[ "$*" == *list-jobs* && "$FIXTURE_MODE" == probe-timeout ]]; then exec sleep 60; fi
  if [[ "$*" == *wk-native-bootstrap-stage* ]]; then
    if [[ "$FIXTURE_MODE" == installer ]]; then echo package-install; else echo systemd-exec; fi
  elif [[ "$*" == *'/proc/1/'* ]]; then
    if [[ "$FIXTURE_MODE" == installer ]]; then echo sh; else echo systemd; fi
  elif [[ "$*" == *list-jobs* ]]; then echo 'fixture-systemd-job';
  elif [[ "$*" == *journalctl* ]]; then echo 'fixture-journal';
  else echo 'fixture-systemd-status'; fi
  exit 0 ;;
esac
exit 18
`
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot(t), "scripts/validate-native-package-lifecycle-container.sh"), "rockylinux:9", "rpm")
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "WK_NATIVE_PACKAGE_DIST_DIR="+root, "FIXTURE_ROOT="+root, "FIXTURE_MODE="+mode)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.WaitDelay = time.Second
			cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
			output, err := cmd.CombinedOutput()
			if cmd.Process != nil {
				defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			if ctx.Err() != nil {
				t.Fatalf("diagnostics exceeded bound: %v\n%s", ctx.Err(), output)
			}
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("original bootstrap failure lost: %v\n%s", err, output)
			}
			text := string(output)
			for _, marker := range []string{"systemd container stopped while booting", "native package bootstrap stage:", "native package PID 1:", "native package systemd state:", "native package systemd jobs:", "native package system journal:", "installer-output-retained"} {
				if !strings.Contains(text, marker) {
					t.Errorf("missing %q in %d output bytes", marker, len(output))
				}
			}
			if mode == "probe-timeout" && !strings.Contains(text, "command timed out after 5s") {
				t.Error("blocking diagnostic was not bounded")
			}
			if mode == "large-output" && (len(output) > 70000 || strings.Contains(text, "diagnostic-overflow-tail")) {
				t.Error("diagnostic output was not capped")
			}
			if mode == "cap-and-timeout" && len(output) > 70000 {
				t.Error("blocked diagnostic output was not capped")
			}
			calls, err := os.ReadFile(filepath.Join(root, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			var name string
			for _, line := range strings.Split(string(calls), "\n") {
				fields := strings.Fields(line)
				if len(fields) < 2 {
					continue
				}
				if fields[0] == "inspect" && fields[1] != "--format" {
					name = fields[1]
				}
			}
			if name == "" || !strings.Contains(string(calls), "rm --force --volumes "+name+"\n") {
				t.Fatal("exact owned container was not removed")
			}
			for _, line := range strings.Split(string(calls), "\n") {
				if strings.HasPrefix(line, "exec ") && !strings.HasPrefix(line, "exec "+name+" ") {
					t.Fatalf("diagnostic escaped owned container: %s", line)
				}
			}
		})
	}
}
