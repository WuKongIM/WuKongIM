package scripts_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMThreeNodeScriptBuildsStartsAndStopsNodes(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outputBin := filepath.Join(t.TempDir(), "wukongim")
	logDir := filepath.Join(t.TempDir(), "logs")
	prometheusEmbedDir := t.TempDir()
	prometheusAsset := filepath.Join(prometheusEmbedDir, "prometheus-testos-testarch")
	if err := os.WriteFile(prometheusAsset, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGoWukongIMStarter(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeWukongIMReadyCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/start-wukongim-three-nodes.sh",
		"--exit-after-ready",
		"--ready-timeout", "5",
		"--poll", "0",
		"--bin", outputBin,
		"--log-dir", logDir,
	)
	cmd.Dir = root
	cmd.Env = append(envWithout(
		"WK_PROMETHEUS_ENABLE",
		"WK_PROMETHEUS_BINARY_PATH",
		"WK_PROMETHEUS_EMBED_DIR",
		"WK_PROMETHEUS_LISTEN_ADDR",
		"WK_WUKONGIM_THREE_NODES_BIN",
		"WK_WUKONGIM_THREE_NODES_LOG_DIR",
		"WK_WUKONGIM_THREE_NODES_READY_TIMEOUT",
		"WK_WUKONGIM_THREE_NODES_POLL_INTERVAL",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_ENABLE",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_LISTEN_ADDR",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_DATA_DIR",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_RETENTION_TIME",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_RETENTION_SIZE",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_SCRAPE_INTERVAL",
	),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_PROMETHEUS_EMBED_DIR="+prometheusEmbedDir,
		"WK_WUKONGIM_THREE_NODES_READY_TIMEOUT=99",
		"WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE=false",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "all nodes ready") {
		t.Fatalf("script output missing ready marker:\n%s", output)
	}

	goCalls := readFile(t, filepath.Join(callsDir, "go.calls"))
	if !strings.Contains(goCalls, "build -o "+outputBin+" ./cmd/wukongim") {
		t.Fatalf("expected build command, got:\n%s", goCalls)
	}

	nodeCalls := readFile(t, filepath.Join(callsDir, "wukongim.calls"))
	for _, want := range []string{
		"-config " + filepath.Join(root, "scripts/wukongim/wukongim-node1.toml"),
		"-config " + filepath.Join(root, "scripts/wukongim/wukongim-node2.toml"),
		"-config " + filepath.Join(root, "scripts/wukongim/wukongim-node3.toml"),
	} {
		if !strings.Contains(nodeCalls, want) {
			t.Fatalf("expected node command %q, got:\n%s", want, nodeCalls)
		}
	}

	nodeEnv := readFile(t, filepath.Join(callsDir, "wukongim.env"))
	for _, want := range []string{
		"wukongim-node1.toml WK_METRICS_ENABLE=true",
		"wukongim-node1.toml WK_PROMETHEUS_ENABLE=true",
		"wukongim-node1.toml WK_PROMETHEUS_LISTEN_ADDR=127.0.0.1:9091",
		"wukongim-node1.toml WK_PROMETHEUS_SCRAPE_TARGETS=[\"127.0.0.1:5011\",\"127.0.0.1:5012\",\"127.0.0.1:5013\"]",
		"wukongim-node2.toml WK_METRICS_ENABLE=true",
		"wukongim-node2.toml WK_PROMETHEUS_ENABLE=false",
		"wukongim-node3.toml WK_METRICS_ENABLE=true",
		"wukongim-node3.toml WK_PROMETHEUS_ENABLE=false",
		"wukongim-node1.toml WK_WUKONGIM_THREE_NODES_READY_TIMEOUT=<unset>",
		"wukongim-node1.toml WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE=<unset>",
		"wukongim-node1.toml WK_PROMETHEUS_EMBED_DIR=<unset>",
	} {
		if !strings.Contains(nodeEnv, want) {
			t.Fatalf("expected node env %q, got:\n%s", want, nodeEnv)
		}
	}

	curlCalls := readFile(t, filepath.Join(callsDir, "curl.calls"))
	for _, want := range []string{
		"http://127.0.0.1:5011/readyz",
		"http://127.0.0.1:5012/readyz",
		"http://127.0.0.1:5013/readyz",
		"http://127.0.0.1:9091/-/ready",
	} {
		if !strings.Contains(curlCalls, want) {
			t.Fatalf("expected ready probe %q, got:\n%s", want, curlCalls)
		}
	}
	for _, name := range []string{"node1.log", "node2.log", "node3.log"} {
		if _, err := os.Stat(filepath.Join(logDir, name)); err != nil {
			t.Fatalf("expected log file %s: %v", name, err)
		}
	}
}

func TestWukongIMThreeNodeScriptReportsNodeExitWithEmptyAllowList(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	outputBin := filepath.Join(t.TempDir(), "wukongim")
	logDir := filepath.Join(t.TempDir(), "logs")
	writeFakeGoWukongIMExits(t, filepath.Join(binDir, "go"), 23)
	if err := os.WriteFile(filepath.Join(binDir, "curl"), []byte("#!/usr/bin/env bash\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "scripts/start-wukongim-three-nodes.sh",
		"--no-prometheus",
		"--ready-timeout", "2",
		"--poll", "0",
		"--bin", outputBin,
		"--log-dir", logDir,
	)
	cmd.Dir = root
	cmd.Env = append(envWithout(
		"WK_PROMETHEUS_ENABLE",
		"WK_PROMETHEUS_BINARY_PATH",
		"WK_WUKONGIM_THREE_NODES_ALLOW_NODE_EXIT",
	), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script should fail when all nodes exit immediately:\n%s", output)
	}
	text := string(output)
	if strings.Contains(text, "unbound variable") {
		t.Fatalf("empty allow-node-exit list must be safe under set -u:\n%s", text)
	}
	if !strings.Contains(text, "exited early with status 23") {
		t.Fatalf("script should preserve the node exit status:\n%s", text)
	}
}

func TestWukongIMThreeNodeScriptDryRunPrintsCommands(t *testing.T) {
	root := repoRoot(t)
	outputBin := filepath.Join(t.TempDir(), "wukongim")
	logDir := filepath.Join(t.TempDir(), "logs")

	cmd := exec.Command("bash", "scripts/start-wukongim-three-nodes.sh",
		"--dry-run",
		"--bin", outputBin,
		"--log-dir", logDir,
	)
	cmd.Dir = root
	cmd.Env = envWithout(
		"WK_PROMETHEUS_ENABLE",
		"WK_PROMETHEUS_LISTEN_ADDR",
		"WK_WUKONGIM_THREE_NODES_BIN",
		"WK_WUKONGIM_THREE_NODES_LOG_DIR",
		"WK_WUKONGIM_THREE_NODES_READY_TIMEOUT",
		"WK_WUKONGIM_THREE_NODES_POLL_INTERVAL",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_ENABLE",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_LISTEN_ADDR",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_DATA_DIR",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_RETENTION_TIME",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_RETENTION_SIZE",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_SCRAPE_INTERVAL",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"build_cmd=go build -o " + outputBin + " ./cmd/wukongim",
		"prometheus_enable=true",
		"prometheus_listen_addr=127.0.0.1:9091",
		"prometheus_scrape_targets=[\"127.0.0.1:5011\",\"127.0.0.1:5012\",\"127.0.0.1:5013\"]",
		"node1_config=" + filepath.Join(root, "scripts/wukongim/wukongim-node1.toml"),
		"node2_ready=http://127.0.0.1:5012/readyz",
		"node3_log=" + filepath.Join(logDir, "node3.log"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, ".toml.example") {
		t.Fatalf("dry-run output should not use example configs:\n%s", text)
	}
}

func TestWukongIMThreeNodeScriptDryRunPrintsPidDirAndAllowedNodeExit(t *testing.T) {
	root := repoRoot(t)
	outputBin := filepath.Join(t.TempDir(), "wukongim")
	logDir := filepath.Join(t.TempDir(), "logs")
	pidDir := filepath.Join(t.TempDir(), "pids")

	cmd := exec.Command("bash", "scripts/start-wukongim-three-nodes.sh",
		"--dry-run",
		"--bin", outputBin,
		"--log-dir", logDir,
		"--pid-dir", pidDir,
		"--allow-node-exit", "2",
	)
	cmd.Dir = root
	cmd.Env = envWithout(
		"WK_PROMETHEUS_ENABLE",
		"WK_PROMETHEUS_LISTEN_ADDR",
		"WK_WUKONGIM_THREE_NODES_BIN",
		"WK_WUKONGIM_THREE_NODES_LOG_DIR",
		"WK_WUKONGIM_THREE_NODES_READY_TIMEOUT",
		"WK_WUKONGIM_THREE_NODES_POLL_INTERVAL",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_ENABLE",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_LISTEN_ADDR",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_DATA_DIR",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_RETENTION_TIME",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_RETENTION_SIZE",
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_SCRAPE_INTERVAL",
		"WK_WUKONGIM_THREE_NODES_PID_DIR",
		"WK_WUKONGIM_THREE_NODES_ALLOW_NODE_EXIT",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"pid_dir=" + pidDir,
		"allow_node_exit=2",
		"node1_pid_file=" + filepath.Join(pidDir, "node1.pid"),
		"node2_pid_file=" + filepath.Join(pidDir, "node2.pid"),
		"node3_pid_file=" + filepath.Join(pidDir, "node3.pid"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func writeFakeGoWukongIMStarter(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/go.calls"
case "$1" in
  env)
    case "$2" in
      GOOS) echo testos ;;
      GOARCH) echo testarch ;;
      *) echo "unexpected go env arg: $*" >&2; exit 2 ;;
    esac
    exit 0
    ;;
  build)
    if [[ "$2" == "-o" && "$4" == "./cmd/wukongim" ]]; then
      cat > "$3" <<'BIN'
#!/usr/bin/env bash
set -euo pipefail
echo "$*" >> "` + callsDir + `/wukongim.calls"
config="<none>"
while [[ $# -gt 0 ]]; do
  case "$1" in
    -config)
      config="$(basename "$2")"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
{
  printf '%s WK_METRICS_ENABLE=%s\n' "$config" "${WK_METRICS_ENABLE-}"
  printf '%s WK_PROMETHEUS_ENABLE=%s\n' "$config" "${WK_PROMETHEUS_ENABLE-}"
  printf '%s WK_PROMETHEUS_LISTEN_ADDR=%s\n' "$config" "${WK_PROMETHEUS_LISTEN_ADDR-}"
  printf '%s WK_PROMETHEUS_DATA_DIR=%s\n' "$config" "${WK_PROMETHEUS_DATA_DIR-}"
  printf '%s WK_PROMETHEUS_SCRAPE_TARGETS=%s\n' "$config" "${WK_PROMETHEUS_SCRAPE_TARGETS-}"
	printf '%s WK_WUKONGIM_THREE_NODES_READY_TIMEOUT=%s\n' "$config" "${WK_WUKONGIM_THREE_NODES_READY_TIMEOUT-<unset>}"
	printf '%s WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE=%s\n' "$config" "${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE-<unset>}"
	printf '%s WK_PROMETHEUS_EMBED_DIR=%s\n' "$config" "${WK_PROMETHEUS_EMBED_DIR-<unset>}"
  if [[ ${WK_PROMETHEUS_BINARY_PATH+x} ]]; then
    printf '%s WK_PROMETHEUS_BINARY_PATH=%s\n' "$config" "$WK_PROMETHEUS_BINARY_PATH"
  else
    printf '%s WK_PROMETHEUS_BINARY_PATH=<unset>\n' "$config"
  fi
} >> "` + callsDir + `/wukongim.env"
trap 'exit 0' TERM INT
while true; do
  sleep 1
done
BIN
      chmod +x "$3"
      exit 0
    fi
    if [[ "$2" == "-o" && "$4" == "./cmd/prometheus" ]]; then
      cat > "$3" <<'PROM'
#!/usr/bin/env bash
echo fake prometheus
PROM
      chmod +x "$3"
      exit 0
    fi
      echo "unexpected go build args: $*" >&2
      exit 2
      ;;
  *)
    echo "unexpected go args: $*" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeGoWukongIMExits(t *testing.T, path string, exitCode int) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == "build" && "$2" == "-o" && "$4" == "./cmd/wukongim" ]]; then
  cat > "$3" <<'BIN'
#!/usr/bin/env bash
exit ` + fmt.Sprintf("%d", exitCode) + `
BIN
  chmod +x "$3"
  exit 0
fi
echo "unexpected go args: $*" >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeWukongIMReadyCurl(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/curl.calls"
for _ in $(seq 1 50); do
  if [[ -f "$calls_dir/wukongim.env" ]] && [[ "$(grep -c ' WK_PROMETHEUS_ENABLE=' "$calls_dir/wukongim.env" 2>/dev/null || true)" -ge 3 ]]; then
    echo "ok"
    exit 0
  fi
  sleep 0.02
done
echo "nodes did not start" >&2
exit 7
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
