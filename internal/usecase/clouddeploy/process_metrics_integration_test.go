//go:build integration

package clouddeploy_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

func TestProcessCollectorPublishesOnlyCompleteObservations(t *testing.T) {
	root := prepareTestPayload(t)
	if _, err := clouddeploy.Seal(openDirectory(t, root), testSourceSHA, testControlSHA); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(root, "scripts", "collect-process-metrics.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	proc := filepath.Join(dir, "proc")
	if err := os.MkdirAll(filepath.Join(proc, "123", "fd"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(name, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "collect")
	// Only redirect the Linux kernel filesystem so the bundle can be tested on macOS.
	write(script, strings.ReplaceAll(string(payload), "/proc/", proc+"/"), 0o700)
	write(filepath.Join(dir, "systemctl"), `#!/bin/sh
for unit do :; done
if [ "$unit" = wukongim.service ]; then
  fixture="$PROCESS_FIXTURE"
  if [ "$fixture" = daemon ] && [ ! -f "$RECOVERED" ]; then fixture=bus_failure; fi
  if [ "$fixture" = bus_failure ]; then exit 1; fi
  state=active pid=123
  case "$fixture" in
    incomplete) state=active pid=0 ;;
    stopped) state=failed pid=0 ;;
    malformed) state=failed pid=invalid ;;
  esac
  load=loaded
else
  load=not-found state=inactive pid=0
fi
case " $* " in
  *" --value "*) printf '%s\n' "$pid" ;;
  *) printf 'MainPID=%s\nLoadState=%s\nActiveState=%s\n' "$pid" "$load" "$state" ;;
esac
`, 0o700)
	write(filepath.Join(dir, "date"), "#!/bin/sh\nprintf '%s\\n' \"$COLLECTOR_FIXTURE_TIME\"\n", 0o700)
	stat := filepath.Join(proc, "123", "stat")
	liveStat := "123 (wukongim) S 1 1 1 0 0 0 0 0 0 0 11 12 0 0 0 0 2 0 1 0 10\n"
	write(stat, liveStat, 0o600)

	output := filepath.Join(dir, "processes.prom")
	recovered := filepath.Join(dir, "recovered")
	previous := filepath.Join(dir, "previous.prom")
	write(filepath.Join(dir, "sleep"), `#!/bin/sh
if [ -f "$RECOVERED" ]; then exit 23; fi
cmp -s "$PREVIOUS" "$WK_PROCESS_METRICS_OUTPUT" || exit 24
touch "$RECOVERED"
`, 0o700)
	run := func(t *testing.T, state string, seconds int) ([]byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		args := []string{script}
		if state != "daemon" {
			args = append(args, "--once")
		}
		cmd := exec.CommandContext(ctx, "bash", args...)
		cmd.Env = append(os.Environ(),
			"PATH="+dir+":"+os.Getenv("PATH"), "WK_PROCESS_METRICS_OUTPUT="+output,
			"RECOVERED="+recovered, "PREVIOUS="+previous,
			"PROCESS_FIXTURE="+state, fmt.Sprintf("COLLECTOR_FIXTURE_TIME=%d", seconds))
		log, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("collector exceeded deadline: %s", log)
		}
		return log, err
	}
	read := func(t *testing.T) []byte {
		t.Helper()
		got, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	const up = "wukongim_process_up{unit=\"wukongim.service\"} "
	const timestamp = "wukongim_process_collector_last_success_unixtime_seconds "
	if log, err := run(t, "live", 1000); err != nil {
		t.Fatalf("collect healthy process: %v, %s", err, log)
	}
	good := read(t)
	for _, line := range []string{up + "1\n", timestamp + "1000\n", "wukongim_process_up{unit=\"prometheus.service\"} 0\n"} {
		if !bytes.Contains(good, []byte(line)) {
			t.Fatalf("healthy snapshot missing %q", line)
		}
	}

	for index, state := range []string{"bus_failure", "incomplete", "malformed", "missing_proc", "truncated_proc"} {
		t.Run(state, func(t *testing.T) {
			switch state {
			case "missing_proc":
				if err := os.Remove(stat); err != nil {
					t.Fatal(err)
				}
			case "truncated_proc":
				write(stat, "123 (wukongim) S 1\n", 0o600)
			}
			log, err := run(t, state, 1015+index*15)
			if got := read(t); !bytes.Equal(got, good) {
				t.Fatalf("uncertain observation replaced last complete snapshot (reported down=%v): %s",
					bytes.Contains(got, []byte(up+"0\n")), log)
			}
			if _, ok := err.(*exec.ExitError); !ok {
				t.Fatalf("uncertain --once collection returned %v, want nonzero exit", err)
			}
		})
	}
	write(stat, liveStat, 0o600)
	t.Run("exit_during_collection", func(t *testing.T) {
		// Opening the FIFO for I/O metrics proves the collector already read stat.
		io := filepath.Join(proc, "123", "io")
		if log, err := exec.Command("mkfifo", io).CombinedOutput(); err != nil {
			t.Fatalf("mkfifo: %v, %s", err, log)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c",
			`exec 3>"$1"; rm "$2"; printf 'read_bytes: 0\nwrite_bytes: 0\n' >&3`, "withdraw-stat", io, stat)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		joined := false
		t.Cleanup(func() {
			if !joined {
				cancel()
				_ = cmd.Wait()
			}
		})
		log, err := run(t, "live", 1090)
		joinErr := cmd.Wait()
		joined = true
		if joinErr != nil {
			t.Fatalf("join procfs fixture: %v", joinErr)
		}
		if err == nil || !bytes.Equal(good, read(t)) {
			t.Fatalf("exit during the read published a new snapshot: %v, %s", err, log)
		}
		if err := os.Remove(io); err != nil {
			t.Fatal(err)
		}
	})
	write(stat, liveStat, 0o600)
	t.Run("daemon_recovers", func(t *testing.T) {
		if log, err := run(t, "live", 1090); err != nil {
			t.Fatalf("seed complete snapshot: %v, %s", err, log)
		}
		write(previous, string(read(t)), 0o600)
		log, err := run(t, "daemon", 1105)
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 23 || !bytes.Contains(read(t), []byte(timestamp+"1105\n")) {
			t.Fatalf("collector did not preserve the prior cut and recover in the same process: %v, %s", err, log)
		}
	})
	if log, err := run(t, "stopped", 1120); err != nil {
		t.Fatalf("collect confirmed exit: %v, %s", err, log)
	}
	down := read(t)
	if !bytes.Contains(down, []byte(up+"0\n")) || !bytes.Contains(down, []byte(timestamp+"1120\n")) ||
		bytes.Contains(down, []byte("wukongim_process_cpu_jiffies_total{unit=\"wukongim.service\"}")) {
		t.Fatal("confirmed exit must publish a fresh down sample without process usage")
	}
}
