package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWkcliTopThreeNodesScriptCollectsTopAndBenchEvidence(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	wkcliPath := filepath.Join(binDir, "wkcli")
	startPath := filepath.Join(binDir, "start-three.sh")
	writeFakeTopValidationWkcli(t, wkcliPath, callsDir)
	writeFakeTopValidationStartScript(t, startPath, callsDir)
	writeFakeTopValidationCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/validate-wkcli-top-three-nodes.sh",
		"--out-dir", outDir,
		"--wkcli-bin", wkcliPath,
		"--no-build",
		"--start-script", startPath,
		"--ready-timeout", "5",
		"--warmup", "0",
		"--top-wait", "0",
		"--msgs", "1200",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	for _, rel := range []string{
		"top-before.json",
		"bench-send.json",
		"top-after-bench.json",
		"top-health.txt",
		"summary.md",
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Fatalf("expected evidence file %s: %v", rel, err)
		}
	}

	wkcliCalls := readFile(t, filepath.Join(callsDir, "wkcli.calls"))
	for _, want := range []string{
		"top --once --json --view all --window 10s --limit 20 --server http://127.0.0.1:5011 --server http://127.0.0.1:5012 --server http://127.0.0.1:5013",
		"bench send --gateway 127.0.0.1:5111 --gateway 127.0.0.1:5112 --gateway 127.0.0.1:5113 --clients 6 --msgs 1200 --channels 60 --channel-prefix top-smoke --channel-type group --size 128B --throughput 600 --connect-rate 30 --no-progress --json",
	} {
		if !strings.Contains(wkcliCalls, want) {
			t.Fatalf("wkcli calls missing %q:\n%s", want, wkcliCalls)
		}
	}

	startCalls := readFile(t, filepath.Join(callsDir, "start.calls"))
	if !strings.Contains(startCalls, "--clean --ready-timeout 5") {
		t.Fatalf("start script should clean and pass ready timeout, calls:\n%s", startCalls)
	}

	health := readFile(t, filepath.Join(outDir, "top-health.txt"))
	for _, want := range []string{
		"top: passed",
		"bench: passed",
		"forbid_alert gateway/session_error: ok",
	} {
		if !strings.Contains(health, want) {
			t.Fatalf("top health missing %q:\n%s", want, health)
		}
	}

	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- top_health_status: passed",
		"- bench_status: passed",
		"- top_before: top-before.json",
		"- top_after_bench: top-after-bench.json",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestWkcliTopThreeNodesScriptFailsOnGatewaySessionErrorAlert(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	wkcliPath := filepath.Join(binDir, "wkcli")
	startPath := filepath.Join(binDir, "start-three.sh")
	writeFakeTopValidationWkcli(t, wkcliPath, callsDir)
	writeFakeTopValidationStartScript(t, startPath, callsDir)
	writeFakeTopValidationCurl(t, filepath.Join(binDir, "curl"), callsDir)

	cmd := exec.Command("bash", "scripts/validate-wkcli-top-three-nodes.sh",
		"--out-dir", outDir,
		"--wkcli-bin", wkcliPath,
		"--no-build",
		"--start-script", startPath,
		"--ready-timeout", "5",
		"--warmup", "0",
		"--top-wait", "0",
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_FAKE_TOP_SESSION_ALERT=1",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script should fail when top reports gateway/session_error:\n%s", output)
	}

	health := readFile(t, filepath.Join(outDir, "top-health.txt"))
	for _, want := range []string{
		"top: failed",
		"forbid_alert gateway/session_error: active",
	} {
		if !strings.Contains(health, want) {
			t.Fatalf("top health missing %q:\n%s", want, health)
		}
	}
	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	for _, want := range []string{
		"- top_health_status: failed",
		"- exit_status: 8",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func writeFakeTopValidationWkcli(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/wkcli.calls"
if [[ "${1:-}" == "top" ]]; then
  alert_active=0
  if [[ -n "${WK_FAKE_TOP_SESSION_ALERT:-}" ]]; then
    alert_active=1
  fi
  if [[ "$alert_active" -eq 1 ]]; then
    cat <<'JSON'
{"generated_at":"2026-06-14T00:00:00Z","ready_nodes":3,"total_nodes":3,"verdict":{"level":"ok","summary":"all nodes ok"},"nodes":[
{"version":"top/v1","scope":"local_node","generated_at":"2026-06-14T00:00:00Z","window_seconds":10,"node":{"id":1,"name":"node-1","ready":true},"verdict":{"level":"ok","summary":"runtime healthy"},"traffic":{"send_per_sec":100,"sendack_per_sec":100,"sendack_error_per_sec":0,"sendack_error_rate":0,"append_per_sec":100,"append_p99_ms":2},"clients":{"connections":0},"resources":{"cpu_percent":1.2,"memory_rss_bytes":134217728,"memory_vms_bytes":268435456,"goroutines":42,"threads":8},"alerts":{"counts":{"active":1,"recent":1,"warning":0,"error":1,"critical":0},"active":[{"id":"top-alert-1","severity":"error","component":"gateway","kind":"session_error","message":"gateway session errors observed","evidence":{"protocol":"wkproto","close_reason":"protocol_error","class":"protocol_error","count":"1","window":"10s"}}]},"sources":{"collector":{"available":true,"sample_count":3},"cluster_snapshot":{"available":true,"sample_count":1},"metrics":{"enabled":false,"required":false}}},
{"version":"top/v1","scope":"local_node","generated_at":"2026-06-14T00:00:00Z","window_seconds":10,"node":{"id":2,"name":"node-2","ready":true},"verdict":{"level":"ok","summary":"runtime healthy"},"traffic":{"send_per_sec":100,"sendack_per_sec":100,"sendack_error_per_sec":0,"sendack_error_rate":0,"append_per_sec":100,"append_p99_ms":2},"clients":{"connections":0},"resources":{"cpu_percent":0.8,"memory_rss_bytes":134217728,"memory_vms_bytes":268435456,"goroutines":39,"threads":8},"sources":{"collector":{"available":true,"sample_count":3},"cluster_snapshot":{"available":true,"sample_count":1},"metrics":{"enabled":false,"required":false}}},
{"version":"top/v1","scope":"local_node","generated_at":"2026-06-14T00:00:00Z","window_seconds":10,"node":{"id":3,"name":"node-3","ready":true},"verdict":{"level":"ok","summary":"runtime healthy"},"traffic":{"send_per_sec":100,"sendack_per_sec":100,"sendack_error_per_sec":0,"sendack_error_rate":0,"append_per_sec":100,"append_p99_ms":2},"clients":{"connections":0},"resources":{"cpu_percent":0.5,"memory_rss_bytes":134217728,"memory_vms_bytes":268435456,"goroutines":41,"threads":8},"sources":{"collector":{"available":true,"sample_count":3},"cluster_snapshot":{"available":true,"sample_count":1},"metrics":{"enabled":false,"required":false}}}
]}
JSON
    exit 0
  fi
  cat <<'JSON'
{"generated_at":"2026-06-14T00:00:00Z","ready_nodes":3,"total_nodes":3,"verdict":{"level":"ok","summary":"all nodes ok"},"nodes":[
{"version":"top/v1","scope":"local_node","generated_at":"2026-06-14T00:00:00Z","window_seconds":10,"node":{"id":1,"name":"node-1","ready":true},"verdict":{"level":"ok","summary":"runtime healthy"},"traffic":{"send_per_sec":100,"sendack_per_sec":100,"sendack_error_per_sec":0,"sendack_error_rate":0,"append_per_sec":100,"append_p99_ms":2},"clients":{"connections":0},"resources":{"cpu_percent":1.2,"memory_rss_bytes":134217728,"memory_vms_bytes":268435456,"goroutines":42,"threads":8},"sources":{"collector":{"available":true,"sample_count":3},"cluster_snapshot":{"available":true,"sample_count":1},"metrics":{"enabled":false,"required":false}}},
{"version":"top/v1","scope":"local_node","generated_at":"2026-06-14T00:00:00Z","window_seconds":10,"node":{"id":2,"name":"node-2","ready":true},"verdict":{"level":"ok","summary":"runtime healthy"},"traffic":{"send_per_sec":100,"sendack_per_sec":100,"sendack_error_per_sec":0,"sendack_error_rate":0,"append_per_sec":100,"append_p99_ms":2},"clients":{"connections":0},"resources":{"cpu_percent":0.8,"memory_rss_bytes":134217728,"memory_vms_bytes":268435456,"goroutines":39,"threads":8},"sources":{"collector":{"available":true,"sample_count":3},"cluster_snapshot":{"available":true,"sample_count":1},"metrics":{"enabled":false,"required":false}}},
{"version":"top/v1","scope":"local_node","generated_at":"2026-06-14T00:00:00Z","window_seconds":10,"node":{"id":3,"name":"node-3","ready":true},"verdict":{"level":"ok","summary":"runtime healthy"},"traffic":{"send_per_sec":100,"sendack_per_sec":100,"sendack_error_per_sec":0,"sendack_error_rate":0,"append_per_sec":100,"append_p99_ms":2},"clients":{"connections":0},"resources":{"cpu_percent":0.5,"memory_rss_bytes":134217728,"memory_vms_bytes":268435456,"goroutines":41,"threads":8},"sources":{"collector":{"available":true,"sample_count":3},"cluster_snapshot":{"available":true,"sample_count":1},"metrics":{"enabled":false,"required":false}}}
]}
JSON
  exit 0
fi
if [[ "${1:-}" == "bench" && "${2:-}" == "send" ]]; then
  cat <<'JSON'
{"result":"pass","duration_ms":2000,"success":1200,"errors":0,"throughput_msgs_per_sec":600,"traffic":{"clients":6,"gateways":3,"channels":60,"messages":1200,"payload_bytes":128,"batch":1},"sendack_reasons":{"success":1200}}
JSON
  exit 0
fi
echo "unexpected wkcli args: $*" >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeTopValidationStartScript(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/start.calls"
trap 'exit 0' TERM INT
while true; do
  sleep 1
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakeTopValidationCurl(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" >> "` + callsDir + `/curl.calls"
echo ok
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
