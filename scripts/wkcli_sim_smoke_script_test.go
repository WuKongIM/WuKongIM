package scripts_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWkcliSimSmokeScriptDryRunPrintsNodeAndSimulatorCommands(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()

	cmd := exec.Command("bash", "scripts/smoke-wkcli-sim-wukongimv2.sh",
		"--dry-run",
		"--out-dir", outDir,
		"--api-addr", "http://127.0.0.1:15001",
		"--gateway-addr", "127.0.0.1:15100",
		"--cluster-addr", "127.0.0.1:17001",
		"--users", "10",
		"--groups", "2",
		"--members", "5",
		"--rate", "5/s",
		"--duration", "5s",
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"api_addr=http://127.0.0.1:15001",
		"gateway_addr=127.0.0.1:15100",
		"cluster_addr=127.0.0.1:17001",
		"node_data_dir=" + filepath.Join(outDir, "node"),
		"node_log=" + filepath.Join(outDir, "node.log"),
		"sim_output=" + filepath.Join(outDir, "sim.jsonl"),
		"snapshot_output=" + filepath.Join(outDir, "bench-snapshot.json"),
		"node_cmd=env",
		"WK_BENCH_API_ENABLE=true",
		"go run ./cmd/wukongimv2",
		"sim_cmd=go run ./cmd/wkcli sim --server http://127.0.0.1:15001 --users 10 --groups 2 --group-members 5 --rate 5/s --max-runtime 5s",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}
