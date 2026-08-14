//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMSingleNodeScriptPassesBenchAPITokenOnlyThroughEnvironment(t *testing.T) {
	runTimingSensitiveShellScriptTestExclusively(t)
	root := repoRoot(t)
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "token-received")
	argsFile := filepath.Join(t.TempDir(), "node.args")
	node := filepath.Join(t.TempDir(), "wukongim")
	const canary = "single-node-api-token-must-not-leak"
	nodeScript := `#!/usr/bin/env bash
set -euo pipefail
: "${WK_BENCH_API_TOKEN:?}"
[[ "$WK_BENCH_API_TOKEN" == "` + canary + `" ]]
: > "$WK_TEST_TOKEN_MARKER"
printf '%s\n' "$@" > "$WK_TEST_ARGS_FILE"
trap 'exit 0' TERM INT
while true; do sleep 0.05; done
`
	if err := os.WriteFile(node, []byte(nodeScript), 0o700); err != nil {
		t.Fatal(err)
	}
	curl := filepath.Join(binDir, "curl")
	if err := os.WriteFile(curl, []byte("#!/usr/bin/env bash\nset -euo pipefail\nif [[ ! -f \"$WK_TEST_TOKEN_MARKER\" ]]; then exit 7; fi\nprintf 'ok\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(t.TempDir(), "logs")
	command := exec.Command("bash", "scripts/start-wukongim-single-node.sh",
		"--no-build", "--bin", node, "--exit-after-ready", "--poll", "0",
		"--ready-timeout", "3", "--log-dir", logDir)
	command.Dir = root
	command.Env = append(envWithout("WK_BENCH_API_TOKEN", "WK_TEST_TOKEN_MARKER", "WK_TEST_ARGS_FILE"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_BENCH_API_TOKEN="+canary,
		"WK_TEST_TOKEN_MARKER="+marker,
		"WK_TEST_ARGS_FILE="+argsFile,
		"WK_PROMETHEUS_ENABLE=false",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("single-node startup failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("node did not receive WK_BENCH_API_TOKEN: %v\n%s", err, output)
	}
	args := readFile(t, argsFile)
	log := readFile(t, filepath.Join(logDir, "node1.log"))
	for source, content := range map[string]string{"stdout": string(output), "argv": args, "node log": log} {
		if strings.Contains(content, canary) {
			t.Fatalf("%s leaked API token canary: %s", source, content)
		}
	}
}

func TestWukongIMSingleNodeScriptBuildsStartsAndStopsNode(t *testing.T) {
	runTimingSensitiveShellScriptTestExclusively(t)
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outputBin := filepath.Join(t.TempDir(), "wukongim")
	logDir := filepath.Join(t.TempDir(), "logs")
	dataDir := filepath.Join(t.TempDir(), "data")
	embedDir := filepath.Join(t.TempDir(), "prometheus-embed")
	writeFakeGoWukongIMStarter(t, filepath.Join(binDir, "go"), callsDir)
	writeFakePrometheusGitClone(t, filepath.Join(binDir, "git"), callsDir)
	writeFakeWukongIMReadyCurlAfterNodeStart(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/start-wukongim-single-node.sh",
		"--exit-after-ready",
		"--ready-timeout", "5",
		"--poll", "0",
		"--bin", outputBin,
		"--log-dir", logDir,
		"--data-dir", dataDir,
	)
	cmd.Dir = root
	cmd.Env = append(envWithout("WK_PROMETHEUS_ENABLE", "WK_PROMETHEUS_BINARY_PATH", "WK_PROMETHEUS_EMBED_DIR",
		"GOWORK",
		"WK_NODE_DATA_DIR",
		"WK_WUKONGIM_SINGLE_NODE_CONFIG",
		"WK_WUKONGIM_SINGLE_NODE_BIN",
		"WK_WUKONGIM_SINGLE_NODE_LOG_DIR",
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR",
		"WK_WUKONGIM_SINGLE_NODE_READY_URL",
		"WK_WUKONGIM_SINGLE_NODE_READY_TIMEOUT",
		"WK_WUKONGIM_SINGLE_NODE_POLL_INTERVAL"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GOWORK="+filepath.Join(t.TempDir(), "ancestor-go.work"),
		"WK_PROMETHEUS_EMBED_DIR="+embedDir,
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR="+filepath.Join(t.TempDir(), "script-data"),
		"WK_BENCH_SINGLE_NODE_QPS=250,500,750,1000",
		"WK_BENCH_API_ENABLE=true",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "node ready") {
		t.Fatalf("script output missing ready marker:\n%s", output)
	}

	goCalls := readFile(t, filepath.Join(callsDir, "go.calls"))
	if !strings.Contains(goCalls, "build -o "+outputBin+" ./cmd/wukongim") {
		t.Fatalf("expected build command, got:\n%s", goCalls)
	}
	goEnv := readFile(t, filepath.Join(callsDir, "go.env"))
	wantBuildEnv := "GOWORK=off args=build -o " + outputBin + " ./cmd/wukongim"
	if !strings.Contains(goEnv, wantBuildEnv) {
		t.Fatalf("single-node build must ignore an ancestor go.work file, got:\n%s", goEnv)
	}

	nodeCalls := readFile(t, filepath.Join(callsDir, "wukongim.calls"))
	wantConfig := "-config " + filepath.Join(root, "scripts/wukongim/wukongim.toml")
	if !strings.Contains(nodeCalls, wantConfig) {
		t.Fatalf("expected node command %q, got:\n%s", wantConfig, nodeCalls)
	}
	nodeEnv := readFile(t, filepath.Join(callsDir, "wukongim.env"))
	canonicalDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatalf("resolve product data directory: %v", err)
	}
	if !strings.Contains(nodeEnv, "WK_PROMETHEUS_ENABLE=true") {
		t.Fatalf("expected single-node script to enable prometheus by default, got:\n%s", nodeEnv)
	}
	if !strings.Contains(nodeEnv, "WK_PROMETHEUS_BINARY_PATH=") || strings.Contains(nodeEnv, "WK_PROMETHEUS_BINARY_PATH=<unset>") {
		t.Fatalf("expected script to clear binary path so wukongim uses embedded prometheus, got:\n%s", nodeEnv)
	}
	for _, want := range []string{
		"WK_NODE_DATA_DIR=" + canonicalDataDir,
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR=<unset>",
		"WK_PROMETHEUS_EMBED_DIR=<unset>",
		"WK_BENCH_SINGLE_NODE_QPS=<unset>",
		"WK_BENCH_API_ENABLE=true",
	} {
		if !strings.Contains(nodeEnv, want) {
			t.Fatalf("expected node env %q, got:\n%s", want, nodeEnv)
		}
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

func TestWukongIMSingleNodeScriptRejectsConflictingProductDataDir(t *testing.T) {
	runTimingSensitiveShellScriptTestExclusively(t)
	root := repoRoot(t)
	requestedDataDir := filepath.Join(t.TempDir(), "requested-data")
	conflictingDataDir := filepath.Join(t.TempDir(), "conflicting-data")

	cmd := exec.Command("bash", "scripts/start-wukongim-single-node.sh",
		"--no-build",
		"--bin", filepath.Join(t.TempDir(), "unused-wukongim"),
		"--data-dir", requestedDataDir,
	)
	cmd.Dir = root
	cmd.Env = append(envWithout("WK_NODE_DATA_DIR", "WK_WUKONGIM_SINGLE_NODE_DATA_DIR"),
		"WK_NODE_DATA_DIR="+conflictingDataDir,
		"WK_PROMETHEUS_ENABLE=false",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("conflicting product data directories must fail closed:\n%s", output)
	}
	if !strings.Contains(string(output), "WK_NODE_DATA_DIR conflicts with --data-dir") {
		t.Fatalf("unexpected conflict failure:\n%s", output)
	}
	if _, statErr := os.Stat(requestedDataDir); !os.IsNotExist(statErr) {
		t.Fatalf("conflict must fail before creating requested data directory: %v", statErr)
	}
}

func TestWukongIMSingleNodeScriptAllowsPrometheusDisableOverride(t *testing.T) {
	runTimingSensitiveShellScriptTestExclusively(t)
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outputBin := filepath.Join(t.TempDir(), "wukongim")
	logDir := filepath.Join(t.TempDir(), "logs")
	dataDir := filepath.Join(t.TempDir(), "data")
	embedDir := filepath.Join(t.TempDir(), "prometheus-embed")
	writeFakeGoWukongIMStarter(t, filepath.Join(binDir, "go"), callsDir)
	writeFakePrometheusGitClone(t, filepath.Join(binDir, "git"), callsDir)
	writeFakeWukongIMReadyCurlAfterNodeStart(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/start-wukongim-single-node.sh",
		"--exit-after-ready",
		"--ready-timeout", "5",
		"--poll", "0",
		"--bin", outputBin,
		"--log-dir", logDir,
		"--data-dir", dataDir,
	)
	cmd.Dir = root
	cmd.Env = append(envWithout("WK_PROMETHEUS_ENABLE", "WK_PROMETHEUS_BINARY_PATH", "WK_PROMETHEUS_EMBED_DIR",
		"WK_WUKONGIM_SINGLE_NODE_CONFIG",
		"WK_WUKONGIM_SINGLE_NODE_BIN",
		"WK_WUKONGIM_SINGLE_NODE_LOG_DIR",
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR",
		"WK_WUKONGIM_SINGLE_NODE_READY_URL",
		"WK_WUKONGIM_SINGLE_NODE_READY_TIMEOUT",
		"WK_WUKONGIM_SINGLE_NODE_POLL_INTERVAL"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_PROMETHEUS_ENABLE=false",
		"WK_PROMETHEUS_EMBED_DIR="+embedDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	nodeEnv := readFile(t, filepath.Join(callsDir, "wukongim.env"))
	if !strings.Contains(nodeEnv, "WK_PROMETHEUS_ENABLE=false") {
		t.Fatalf("expected explicit prometheus disable override, got:\n%s", nodeEnv)
	}
	goCalls := readFile(t, filepath.Join(callsDir, "go.calls"))
	if strings.Contains(goCalls, "./cmd/prometheus") {
		t.Fatalf("disabled prometheus should not build embedded prometheus, got:\n%s", goCalls)
	}
}

func writeFakeWukongIMReadyCurlAfterNodeStart(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/curl.calls"
for _ in $(seq 1 50); do
  if [[ -f "$calls_dir/wukongim.calls" ]] && grep -q 'WK_PROMETHEUS_BINARY_PATH=' "$calls_dir/wukongim.env" 2>/dev/null; then
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
