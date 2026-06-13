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
	embedDir := filepath.Join(t.TempDir(), "prometheus-embed")
	writeFakeGoWukongIMV2Starter(t, filepath.Join(binDir, "go"), callsDir)
	writeFakePrometheusGitClone(t, filepath.Join(binDir, "git"), callsDir)
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
	cmd.Env = append(envWithout("WK_PROMETHEUS_ENABLE", "WK_PROMETHEUS_BINARY_PATH", "WK_PROMETHEUS_EMBED_DIR"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_PROMETHEUS_EMBED_DIR="+embedDir,
	)
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
	nodeEnv := readFile(t, filepath.Join(callsDir, "wukongimv2.env"))
	if !strings.Contains(nodeEnv, "WK_PROMETHEUS_ENABLE=true") {
		t.Fatalf("expected single-node script to enable prometheus by default, got:\n%s", nodeEnv)
	}
	if !strings.Contains(nodeEnv, "WK_PROMETHEUS_BINARY_PATH=") || strings.Contains(nodeEnv, "WK_PROMETHEUS_BINARY_PATH=<unset>") {
		t.Fatalf("expected script to clear binary path so wukongimv2 uses embedded prometheus, got:\n%s", nodeEnv)
	}
	goCalls = readFile(t, filepath.Join(callsDir, "go.calls"))
	if strings.Contains(goCalls, "install github.com/prometheus/prometheus/cmd/prometheus@") {
		t.Fatalf("script must not use go install for prometheus modules with replace directives, got:\n%s", goCalls)
	}
	if !strings.Contains(goCalls, "build -o ") || !strings.Contains(goCalls, " ./cmd/prometheus") {
		t.Fatalf("expected script to build embedded prometheus from a checked-out source tree, got:\n%s", goCalls)
	}
	if _, err := os.Stat(filepath.Join(embedDir, "prometheus-testos-testarch")); err != nil {
		t.Fatalf("expected embedded prometheus binary: %v", err)
	}

	curlCalls := readFile(t, filepath.Join(callsDir, "curl.calls"))
	if !strings.Contains(curlCalls, "http://127.0.0.1:5001/readyz") {
		t.Fatalf("expected ready probe, got:\n%s", curlCalls)
	}
	if _, err := os.Stat(filepath.Join(logDir, "node1.log")); err != nil {
		t.Fatalf("expected log file: %v", err)
	}
}

func TestWukongIMV2SingleNodeScriptAllowsPrometheusDisableOverride(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outputBin := filepath.Join(t.TempDir(), "wukongimv2")
	logDir := filepath.Join(t.TempDir(), "logs")
	dataDir := filepath.Join(t.TempDir(), "data")
	embedDir := filepath.Join(t.TempDir(), "prometheus-embed")
	writeFakeGoWukongIMV2Starter(t, filepath.Join(binDir, "go"), callsDir)
	writeFakePrometheusGitClone(t, filepath.Join(binDir, "git"), callsDir)
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
	cmd.Env = append(envWithout("WK_PROMETHEUS_ENABLE", "WK_PROMETHEUS_BINARY_PATH", "WK_PROMETHEUS_EMBED_DIR"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_PROMETHEUS_ENABLE=false",
		"WK_PROMETHEUS_EMBED_DIR="+embedDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	nodeEnv := readFile(t, filepath.Join(callsDir, "wukongimv2.env"))
	if !strings.Contains(nodeEnv, "WK_PROMETHEUS_ENABLE=false") {
		t.Fatalf("expected explicit prometheus disable override, got:\n%s", nodeEnv)
	}
	goCalls := readFile(t, filepath.Join(callsDir, "go.calls"))
	if strings.Contains(goCalls, "./cmd/prometheus") {
		t.Fatalf("disabled prometheus should not build embedded prometheus, got:\n%s", goCalls)
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

func writeFakePrometheusGitClone(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "$*" >> "` + callsDir + `/git.calls"
if [[ "$1" != "clone" ]]; then
  echo "unexpected git args: $*" >&2
  exit 2
fi
dest="${@: -1}"
mkdir -p "$dest/cmd/prometheus"
cat > "$dest/go.mod" <<'MOD'
module github.com/prometheus/prometheus

go 1.25
MOD
cat > "$dest/cmd/prometheus/main.go" <<'GO'
package main

func main() {}
GO
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func envWithout(keys ...string) []string {
	omit := map[string]struct{}{}
	for _, key := range keys {
		omit[key] = struct{}{}
	}
	out := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := omit[key]; ok {
			continue
		}
		out = append(out, entry)
	}
	return out
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
	cmd.Env = envWithout("WK_PROMETHEUS_ENABLE", "WK_PROMETHEUS_BINARY_PATH")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"build_cmd=go build -o " + outputBin + " ./cmd/wukongimv2",
		"config=" + filepath.Join(root, "scripts/wukongimv2/wukongimv2.conf"),
		"ready=http://127.0.0.1:5001/readyz",
		"prometheus_enable=true",
		"prometheus_binary_path=<embedded>",
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
	cmd.Env = envWithout("WK_PROMETHEUS_ENABLE", "WK_PROMETHEUS_BINARY_PATH")
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
