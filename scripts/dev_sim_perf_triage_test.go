package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevSimPerfTriageCollectsEvidenceForSampledScenario(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	clusterDir := t.TempDir()
	writeFakeNodeLogFiles(t, clusterDir)
	writeFakePerfDocker(t, filepath.Join(binDir, "docker"), callsDir)
	writeFakePerfCurl(t, filepath.Join(binDir, "curl"), callsDir, `{"state":"running","connected_users":40,"active_users":38,"reconnected_users":2,"messages_sent":7,"send_errors":0,"recv_errors":0,"last_error":""}`)

	cmd := exec.Command("bash", "scripts/dev-sim-perf-triage.sh", "sampled-correctness", "--duration", "0", "--poll", "0", "--profile-seconds", "1", "--out-dir", outDir, "--no-build")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_DEV_SIM_TRIAGE_TIMESTAMP=20260521-010203",
		"WK_DEV_SIM_TRIAGE_CLUSTER_DATA_DIR="+clusterDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "triage evidence collected") {
		t.Fatalf("script output missing completion marker:\n%s", output)
	}

	runDir := filepath.Join(outDir, "20260521-010203-sampled-correctness")
	for _, rel := range []string{
		"summary.md",
		"env.txt",
		"git.txt",
		"compose-config.yml",
		"status.jsonl",
		"docker-stats.jsonl",
		"logs/compose/wk-node1.log",
		"logs/compose/wk-node2.log",
		"logs/compose/wk-node3.log",
		"logs/compose/wk-sim.log",
		"logs/app/wk-node1.log",
		"logs/app/wk-node2.log",
		"logs/app/wk-node3.log",
		"logs/debug/wk-node1.log",
		"logs/error/wk-node1.log",
		"logs/error/wk-node2.log",
		"logs/error/wk-node3.log",
		"logs/error/wk-sim.log",
		"logs/warn/wk-node1.log",
		"logs/warn/wk-node2.log",
		"logs/warn/wk-node3.log",
		"logs/warn/wk-sim.log",
		"metrics/node1.prom",
		"metrics/node2.prom",
		"metrics/node3.prom",
		"pprof/node1-goroutine.txt",
		"pprof/node1-heap.pb.gz",
		"pprof/node1-cpu.pb.gz",
	} {
		if _, err := os.Stat(filepath.Join(runDir, rel)); err != nil {
			t.Fatalf("expected evidence file %s: %v", rel, err)
		}
	}

	envText := readFile(t, filepath.Join(runDir, "env.txt"))
	for _, want := range []string{
		"SCENARIO=sampled-correctness",
		"WK_SIM_VERIFY_RECV=sampled",
		"WK_SIM_USERS=40",
		"WK_SIM_UID_PREFIX=sampled-correctness-20260521-010203",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf("env.txt missing %q:\n%s", want, envText)
		}
	}

	dockerCalls := readFile(t, filepath.Join(callsDir, "docker.calls"))
	for _, want := range []string{
		"compose --profile dev-sim config",
		"compose --profile dev-sim up -d wk-node1 wk-node2 wk-node3 wk-sim",
		"stats --no-stream --format {{json .}}",
		"compose --profile dev-sim logs --no-color --tail=2000 wk-node1",
	} {
		if !strings.Contains(dockerCalls, want) {
			t.Fatalf("docker calls missing %q:\n%s", want, dockerCalls)
		}
	}

	curlCalls := readFile(t, filepath.Join(callsDir, "curl.calls"))
	for _, want := range []string{
		"http://127.0.0.1:19091/status",
		"http://127.0.0.1:15001/metrics",
		"http://127.0.0.1:15002/debug/pprof/goroutine?debug=2",
		"http://127.0.0.1:15003/debug/pprof/profile?seconds=1",
	} {
		if !strings.Contains(curlCalls, want) {
			t.Fatalf("curl calls missing %q:\n%s", want, curlCalls)
		}
	}

	errorLog := readFile(t, filepath.Join(runDir, "logs/error/wk-node1.log"))
	if !strings.Contains(errorLog, `"level":"error"`) || strings.Contains(errorLog, `"level":"warn"`) {
		t.Fatalf("error split should use internal/log error.log only, got:\n%s", errorLog)
	}
	warnLog := readFile(t, filepath.Join(runDir, "logs/warn/wk-node1.log"))
	if !strings.Contains(warnLog, `"msg":"dedicated warn"`) || strings.Contains(warnLog, `"level":"error"`) || strings.Contains(warnLog, `"msg":"slow fanout"`) {
		t.Fatalf("warn split should use internal/log warn.log only, got:\n%s", warnLog)
	}

	summary := readFile(t, filepath.Join(runDir, "summary.md"))
	if !strings.Contains(summary, "result: PASS") || !strings.Contains(summary, "send_errors: 0") || !strings.Contains(summary, "recv_errors: 0") {
		t.Fatalf("summary should report a clean pass:\n%s", summary)
	}
}

func TestDevSimPerfTriageFailsAfterCollectingEvidenceWhenStatusHasErrors(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	callsDir := t.TempDir()
	outDir := t.TempDir()
	clusterDir := t.TempDir()
	writeFakeNodeLogFiles(t, clusterDir)
	writeFakePerfDocker(t, filepath.Join(binDir, "docker"), callsDir)
	writeFakePerfCurl(t, filepath.Join(binDir, "curl"), callsDir, `{"state":"running","connected_users":40,"active_users":37,"reconnected_users":5,"messages_sent":7,"send_errors":2,"recv_errors":1,"last_error":"context deadline exceeded"}`)

	cmd := exec.Command("bash", "scripts/dev-sim-perf-triage.sh", "group-fanout", "--duration", "0", "--poll", "0", "--profile-seconds", "1", "--out-dir", outDir, "--no-build")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WK_DEV_SIM_TRIAGE_TIMESTAMP=20260521-020304",
		"WK_DEV_SIM_TRIAGE_CLUSTER_DATA_DIR="+clusterDir,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script should fail when final status has errors:\n%s", output)
	}
	if !strings.Contains(string(output), "triage captured failing evidence") {
		t.Fatalf("script should say evidence was captured before failing:\n%s", output)
	}

	runDir := filepath.Join(outDir, "20260521-020304-group-fanout")
	for _, rel := range []string{"summary.md", "status.jsonl", "logs/compose/wk-sim.log", "logs/error/wk-sim.log", "metrics/node1.prom", "pprof/node3-cpu.pb.gz"} {
		if _, err := os.Stat(filepath.Join(runDir, rel)); err != nil {
			t.Fatalf("expected evidence file %s after failure: %v", rel, err)
		}
	}
	summary := readFile(t, filepath.Join(runDir, "summary.md"))
	for _, want := range []string{"result: FAIL", "active_users: 37", "reconnected_users: 5", "send_errors: 2", "recv_errors: 1", "last_error: context deadline exceeded"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func writeFakeNodeLogFiles(t *testing.T, clusterDir string) {
	t.Helper()
	for i := 1; i <= 3; i++ {
		logDir := filepath.Join(clusterDir, "node"+string(rune('0'+i)), "logs")
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			t.Fatal(err)
		}
		app := `{"level":"info","msg":"starting"}
{"level":"warn","msg":"slow fanout"}
{"level":"error","msg":"failed append"}
`
		if err := os.WriteFile(filepath.Join(logDir, "app.log"), []byte(app), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(logDir, "error.log"), []byte(`{"level":"error","msg":"failed append"}
`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(logDir, "warn.log"), []byte(`{"level":"warn","msg":"dedicated warn"}
`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(logDir, "debug.log"), []byte(app), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFakePerfDocker(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/docker.calls"
case "$*" in
  "compose --profile dev-sim config")
    echo 'services: {}'
    ;;
  "compose --profile dev-sim up -d wk-node1 wk-node2 wk-node3 wk-sim"|"compose --profile dev-sim up -d --build wk-node1 wk-node2 wk-node3 wk-sim")
    exit 0
    ;;
  "stats --no-stream --format {{json .}}")
    echo '{"Name":"wk-node1","CPUPerc":"1.0%"}'
    ;;
  "compose --profile dev-sim logs --no-color --tail=2000 wk-node1")
    echo 'wk-node1 info line'
    echo 'wk-node1 WARN slow fanout'
    echo 'wk-node1 ERROR failed append'
    ;;
  "compose --profile dev-sim logs --no-color --tail=2000 wk-node2")
    echo 'wk-node2 log line'
    ;;
  "compose --profile dev-sim logs --no-color --tail=2000 wk-node3")
    echo 'wk-node3 log line'
    ;;
  "compose --profile dev-sim logs --no-color --tail=2000 wk-sim")
    echo 'wk-sim log line'
    ;;
  *)
    echo "unexpected docker args: $*" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakePerfCurl(t *testing.T, path string, callsDir string, statusJSON string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
calls_dir="` + callsDir + `"
echo "$*" >> "$calls_dir/curl.calls"
url="${@: -1}"
case "$url" in
  "http://127.0.0.1:19091/status")
    echo '` + statusJSON + `'
    ;;
  http://127.0.0.1:1500*/metrics)
    echo 'wk_metric 1'
    ;;
  http://127.0.0.1:1500*/debug/pprof/goroutine?debug=2)
    echo 'goroutine profile'
    ;;
  http://127.0.0.1:1500*/debug/pprof/heap)
    echo 'heap profile'
    ;;
  http://127.0.0.1:1500*/debug/pprof/profile?seconds=*)
    echo 'cpu profile'
    ;;
  *)
    echo "unexpected curl url: $url" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
