package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMV2ThreeNode10kBenchScriptSetsEvidenceDefaults(t *testing.T) {
	root := repoRoot(t)
	callsDir := t.TempDir()
	baseScript := filepath.Join(t.TempDir(), "bench-base.sh")
	writeFakeBenchBase(t, baseScript, callsDir)

	cmd := exec.Command("bash", "scripts/bench-wukongimv2-three-nodes-10kch.sh", "--no-start", "--qps", "100")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"WK_BENCH_THREE_NODE_BASE_SCRIPT="+baseScript,
		"WK_BENCH_THREE_NODE_TIMESTAMP=20260530-010203",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}

	env := readFile(t, filepath.Join(callsDir, "env.txt"))
	for _, want := range []string{
		"WK_BENCH_CHANNELS=10000",
		"WK_BENCH_THREE_NODE_OUT_DIR=" + filepath.Join(root, "docs/development/perf-runs/20260530-010203-three-node-10kch"),
		"WK_CLUSTER_MAX_CHANNELS=12000",
		"WK_PPROF_ENABLE=true",
		"WK_BENCH_PROFILE_SECONDS=10",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("10k bench env missing %q:\n%s", want, env)
		}
	}

	args := readFile(t, filepath.Join(callsDir, "args.txt"))
	if !strings.Contains(args, "--no-start --qps 100") {
		t.Fatalf("wrapper should pass through args, got:\n%s", args)
	}
}

func TestWukongIMV2ThreeNodeBenchScriptCollectsLocalEvidence(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongimv2-three-nodes-1000ch.sh"))

	for _, want := range []string{
		"scripts/start-wukongimv2-three-nodes.sh",
		"collect_node_logs",
		"capture_node_pprof",
		"write_evidence_summary",
		"/debug/pprof/goroutine?debug=2",
		"/debug/pprof/heap",
		"summary.md",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("bench script missing local evidence hook %q", want)
		}
	}
	if strings.Contains(script, "docker compose") {
		t.Fatalf("three-node bench script should use local startup scripts, not docker compose")
	}
}

func writeFakeBenchBase(t *testing.T, path string, callsDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "` + callsDir + `"
printf '%s\n' "$*" > "` + callsDir + `/args.txt"
env | LC_ALL=C sort | grep -E '^(WK_BENCH_|WK_CLUSTER_|WK_PPROF_)' > "` + callsDir + `/env.txt"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
