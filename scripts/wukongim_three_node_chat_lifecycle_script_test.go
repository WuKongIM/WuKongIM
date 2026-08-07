package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleShakeoutScriptStaticContract(t *testing.T) {
	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-shakeout.sh")
	script := readFile(t, scriptPath)

	for _, want := range []string{
		"#!/usr/bin/env bash", "set -euo pipefail", "umask 077", "--run-dir", "--dry-run", "--stop-after",
		"go build", "./cmd/wukongim", "./cmd/wkbench", "WK_CLUSTER_INITIAL_SLOT_COUNT=12",
		"WK_CLUSTER_HASH_SLOT_COUNT=256", "WK_CLUSTER_SLOT_REPLICA_N=3",
		"WK_CLUSTER_CHANNEL_REPLICA_N=3", "worker --mode chat-lifecycle", "host-metrics",
		"WK_PLUGIN_SOCKET_PATH",
		"soak chat-lifecycle", "request_coordinator_stop", "handle_signal", "GRACEFUL_STOP_DEADLINE",
		"kill -TERM", "kill -KILL", "pids", "final.json",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("shakeout script missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(script), "docker") {
		t.Fatal("shakeout script must not reference container tooling")
	}
	if output, err := exec.Command("bash", "-n", scriptPath).CombinedOutput(); err != nil {
		t.Fatalf("bash syntax failed: %v\n%s", err, output)
	}
}

func TestChatLifecycleShakeoutScriptDryRunIsReadOnlyAndRejectsBroadRunDirs(t *testing.T) {
	root := repoRoot(t)
	runDir := filepath.Join(t.TempDir(), "shakeout")
	canonicalParent, err := filepath.EvalSymlinks(filepath.Dir(runDir))
	if err != nil {
		t.Fatal(err)
	}
	canonicalRunDir := filepath.Join(canonicalParent, filepath.Base(runDir))
	cmd := exec.Command("bash", "scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh",
		"--dry-run", "--run-dir", runDir, "--base-port", "24000")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"run_dir=" + canonicalRunDir, "logical_slot_groups=12", "hash_slots=256", "replicas=3/3",
		"service_1=http://127.0.0.1:24001", "worker_3=http://127.0.0.1:24053",
		"host_metrics_2=http://127.0.0.1:24062", "host_metrics_load=http://127.0.0.1:24060",
		"coordinator_config=" + filepath.Join(canonicalRunDir, "chat-lifecycle.yaml"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
	if _, statErr := os.Stat(runDir); !os.IsNotExist(statErr) {
		t.Fatalf("dry run created %s", runDir)
	}

	for _, broad := range []string{"/", root} {
		cmd = exec.Command("bash", "scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh", "--dry-run", "--run-dir", broad)
		cmd.Dir = root
		if rejected, rejectErr := cmd.CombinedOutput(); rejectErr == nil || !strings.Contains(string(rejected), "unsafe --run-dir") {
			t.Fatalf("broad run dir %q not rejected: %v\n%s", broad, rejectErr, rejected)
		}
	}
}
