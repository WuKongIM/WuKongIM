package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMV2ThreeNodeScriptBuildsStartsAndStopsNodes(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outputBin := filepath.Join(t.TempDir(), "wukongimv2")
	logDir := filepath.Join(t.TempDir(), "logs")
	writeFakeGoWukongIMV2Starter(t, filepath.Join(binDir, "go"), callsDir)
	writeFakeWukongIMV2ReadyCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/start-wukongimv2-three-nodes.sh",
		"--exit-after-ready",
		"--ready-timeout", "5",
		"--poll", "0",
		"--bin", outputBin,
		"--log-dir", logDir,
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "all nodes ready") {
		t.Fatalf("script output missing ready marker:\n%s", output)
	}

	goCalls := readFile(t, filepath.Join(callsDir, "go.calls"))
	if !strings.Contains(goCalls, "build -o "+outputBin+" ./cmd/wukongimv2") {
		t.Fatalf("expected build command, got:\n%s", goCalls)
	}

	nodeCalls := readFile(t, filepath.Join(callsDir, "wukongimv2.calls"))
	for _, want := range []string{
		"-config " + filepath.Join(root, "scripts/wukongimv2/wukongimv2-node1.conf"),
		"-config " + filepath.Join(root, "scripts/wukongimv2/wukongimv2-node2.conf"),
		"-config " + filepath.Join(root, "scripts/wukongimv2/wukongimv2-node3.conf"),
	} {
		if !strings.Contains(nodeCalls, want) {
			t.Fatalf("expected node command %q, got:\n%s", want, nodeCalls)
		}
	}

	curlCalls := readFile(t, filepath.Join(callsDir, "curl.calls"))
	for _, want := range []string{
		"http://127.0.0.1:5011/readyz",
		"http://127.0.0.1:5012/readyz",
		"http://127.0.0.1:5013/readyz",
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

func TestWukongIMV2ThreeNodeScriptDryRunPrintsCommands(t *testing.T) {
	root := repoRoot(t)
	outputBin := filepath.Join(t.TempDir(), "wukongimv2")
	logDir := filepath.Join(t.TempDir(), "logs")

	cmd := exec.Command("bash", "scripts/start-wukongimv2-three-nodes.sh",
		"--dry-run",
		"--bin", outputBin,
		"--log-dir", logDir,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"build_cmd=go build -o " + outputBin + " ./cmd/wukongimv2",
		"node1_config=" + filepath.Join(root, "scripts/wukongimv2/wukongimv2-node1.conf"),
		"node2_ready=http://127.0.0.1:5012/readyz",
		"node3_log=" + filepath.Join(logDir, "node3.log"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, ".conf.example") {
		t.Fatalf("dry-run output should not use example configs:\n%s", text)
	}
}

func writeFakeGoWukongIMV2Starter(t *testing.T, path string, callsDir string) {
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
    if [[ "$2" == "-o" && "$4" == "./cmd/wukongimv2" ]]; then
      cat > "$3" <<'BIN'
#!/usr/bin/env bash
set -euo pipefail
echo "$*" >> "` + callsDir + `/wukongimv2.calls"
printf 'WK_PROMETHEUS_ENABLE=%s\n' "${WK_PROMETHEUS_ENABLE-}" >> "` + callsDir + `/wukongimv2.env"
if [[ ${WK_PROMETHEUS_BINARY_PATH+x} ]]; then
  printf 'WK_PROMETHEUS_BINARY_PATH=%s\n' "$WK_PROMETHEUS_BINARY_PATH" >> "` + callsDir + `/wukongimv2.env"
else
  printf 'WK_PROMETHEUS_BINARY_PATH=<unset>\n' >> "` + callsDir + `/wukongimv2.env"
fi
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

func writeFakeWukongIMV2ReadyCurl(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/curl.calls"
echo "ok"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
