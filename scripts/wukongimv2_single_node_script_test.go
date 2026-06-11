package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMV2SingleNodeScriptBuildsStartsAndStopsNode(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outputBin := filepath.Join(t.TempDir(), "wukongimv2")
	logDir := filepath.Join(t.TempDir(), "logs")
	dataDir := filepath.Join(t.TempDir(), "data")
	writeFakeGoWukongIMV2Starter(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeWukongIMV2ReadyCurlAfterNodeStart(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/start-wukongimv2-single-node.sh",
		"--exit-after-ready",
		"--ready-timeout", "5",
		"--poll", "0",
		"--bin", outputBin,
		"--log-dir", logDir,
		"--data-dir", dataDir,
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "node ready") {
		t.Fatalf("script output missing ready marker:\n%s", output)
	}

	goCalls := readFile(t, filepath.Join(callsDir, "go.calls"))
	if !strings.Contains(goCalls, "build -o "+outputBin+" ./cmd/wukongimv2") {
		t.Fatalf("expected build command, got:\n%s", goCalls)
	}

	nodeCalls := readFile(t, filepath.Join(callsDir, "wukongimv2.calls"))
	wantConfig := "-config " + filepath.Join(root, "scripts/wukongimv2/wukongimv2.conf")
	if !strings.Contains(nodeCalls, wantConfig) {
		t.Fatalf("expected node command %q, got:\n%s", wantConfig, nodeCalls)
	}

	curlCalls := readFile(t, filepath.Join(callsDir, "curl.calls"))
	if !strings.Contains(curlCalls, "http://127.0.0.1:5001/readyz") {
		t.Fatalf("expected ready probe, got:\n%s", curlCalls)
	}
	if _, err := os.Stat(filepath.Join(logDir, "node1.log")); err != nil {
		t.Fatalf("expected log file: %v", err)
	}
}

func writeFakeWukongIMV2ReadyCurlAfterNodeStart(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/curl.calls"
for _ in $(seq 1 50); do
  if [[ -f "$calls_dir/wukongimv2.calls" ]]; then
    echo "ok"
    exit 0
  fi
  sleep 0.02
done
echo "node did not start" >&2
exit 7
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestWukongIMV2SingleNodeScriptDryRunPrintsCommand(t *testing.T) {
	root := repoRoot(t)
	outputBin := filepath.Join(t.TempDir(), "wukongimv2")
	logDir := filepath.Join(t.TempDir(), "logs")
	dataDir := filepath.Join(t.TempDir(), "data")

	cmd := exec.Command("bash", "scripts/start-wukongimv2-single-node.sh",
		"--dry-run",
		"--bin", outputBin,
		"--log-dir", logDir,
		"--data-dir", dataDir,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"build_cmd=go build -o " + outputBin + " ./cmd/wukongimv2",
		"config=" + filepath.Join(root, "scripts/wukongimv2/wukongimv2.conf"),
		"ready=http://127.0.0.1:5001/readyz",
		"log=" + filepath.Join(logDir, "node1.log"),
		"data_dir=" + dataDir,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, ".conf.example") {
		t.Fatalf("dry-run output should not use example configs:\n%s", text)
	}
}

func TestWukongIMV2SingleNodeScriptDefaultsUseIsolatedDataDir(t *testing.T) {
	root := repoRoot(t)
	singleDataDir := filepath.Join(root, "data/wukongimv2-single-node-data")
	threeNode1DataDir := filepath.Join(root, "data/wukongimv2-node-1")

	cmd := exec.Command("bash", "scripts/start-wukongimv2-single-node.sh", "--dry-run")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "data_dir="+singleDataDir) {
		t.Fatalf("dry-run output should default to isolated data dir %q:\n%s", singleDataDir, text)
	}
	if strings.Contains(text, "data_dir="+threeNode1DataDir) {
		t.Fatalf("dry-run output should not reuse three-node node1 data dir:\n%s", text)
	}

	config := readFile(t, filepath.Join(root, "scripts/wukongimv2/wukongimv2.conf"))
	if !strings.Contains(config, "WK_NODE_DATA_DIR=./data/wukongimv2-single-node-data") {
		t.Fatalf("single-node config should use isolated data dir:\n%s", config)
	}
	if strings.Contains(config, "WK_NODE_DATA_DIR=./data/wukongimv2-node-1") {
		t.Fatalf("single-node config should not reuse three-node node1 data dir:\n%s", config)
	}
}
