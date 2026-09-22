//go:build integration && linux

package transportperf

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func python(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, "python3", args...)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	return cmd
}

func records(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if rows[len(rows)-1]["kind"] != "end" {
		t.Fatal("missing terminal record")
	}
	return rows
}

func TestSamplerOwnedChildAndExclusiveEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	cmd := python(t, "host_sample.py", "--output", path, "--duration", "5", "--", "python3", "-c", "import time; time.sleep(2.2)")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sampler: %v %s", err, out)
	}
	rows := records(t, path)
	end := rows[len(rows)-1]
	if end["outcome"] != "child_exited" || end["exit_code"] != float64(0) || end["samples"].(float64) < 3 {
		t.Fatal(end)
	}
	for i, row := range rows[1 : len(rows)-1] {
		if row["index"] != float64(i) || row["begin_ns"].(float64) > row["end_ns"].(float64) {
			t.Fatal(row)
		}
	}
	initial, _ := os.ReadFile(path)
	cmd = python(t, "host_sample.py", "--output", path, "--duration", "1", "--", "python3", "-c", "raise Exception('must not launch')")
	if out, err := cmd.CombinedOutput(); err == nil || strings.Contains(string(out), "must not launch") {
		t.Fatalf("overwrite: %v %s", err, out)
	}
	final, _ := os.ReadFile(path)
	if string(initial) != string(final) {
		t.Fatal("existing evidence changed")
	}
}

func TestSamplerDeadlineKillsOnlyOwnedChild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	cmd := python(t, "host_sample.py", "--output", path, "--duration", "1", "--", "python3", "-c", "import time; time.sleep(30)")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("deadline silently passed: %s", out)
	}
	rows := records(t, path)
	end := rows[len(rows)-1]
	if end["outcome"] != "duration_limit" || end["exit_code"] != float64(1) {
		t.Fatal(end)
	}
	pid := int(rows[0]["identity"].(map[string]any)["PID"].(float64))
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("child still exists: %v", err)
	}
}

func TestSamplerAttachDeadlineDoesNotSignalTarget(t *testing.T) {
	target := python(t, "-c", "import time; time.sleep(30)")
	if err := target.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Process.Kill(); _ = target.Wait() }()
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	cmd := python(t, "host_sample.py", "--output", path, "--duration", "1", "--pid", strconv.Itoa(target.Process.Pid))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("attach: %v %s", err, out)
	}
	end := records(t, path)
	if end[len(end)-1]["outcome"] != "duration_limit" {
		t.Fatal(end)
	}
	if err := target.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("attached target killed: %v", err)
	}
}

func TestSamplerSignalRetainsTerminalAndCleansOwnedChild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	cmd := python(t, "host_sample.py", "--output", path, "--duration", "10", "--", "python3", "-c", "import time; time.sleep(30)")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), `"kind":"sample"`) {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("sampler not ready")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("interruption silently passed")
	}
	rows := records(t, path)
	if rows[len(rows)-1]["outcome"] != "interrupted" {
		t.Fatal(rows[len(rows)-1])
	}
	pid := int(rows[0]["identity"].(map[string]any)["PID"].(float64))
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("child still exists: %v", err)
	}
}

func TestSamplerCapFailureCleansOwnedChild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	cmd := python(t, "-c", "import host_sample; host_sample.THREAD_CAP=0; raise SystemExit(host_sample.main())",
		"--output", path, "--duration", "5", "--", "python3", "-c", "import time; time.sleep(30)")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("cap silently passed: %s", out)
	}
	rows := records(t, path)
	end := rows[len(rows)-1]
	if end["detail"] != "thread_cap" || end["exit_code"] != float64(1) {
		t.Fatal(end)
	}
	pid := int(rows[0]["identity"].(map[string]any)["PID"].(float64))
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("child still exists: %v", err)
	}
}
