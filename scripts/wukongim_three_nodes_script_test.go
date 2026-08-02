package scripts_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMThreeNodeScriptDryRunPrintsCommands(t *testing.T) {
	root := repoRoot(t)
	outputBin := filepath.Join(t.TempDir(), "wukongim")
	logDir := filepath.Join(t.TempDir(), "logs")
	dataRoot := filepath.Join(t.TempDir(), "data-root")

	cmd := exec.Command("bash", "scripts/start-wukongim-three-nodes.sh",
		"--dry-run",
		"--bin", outputBin,
		"--log-dir", logDir,
		"--data-root", dataRoot,
	)
	cmd.Dir = root
	cmd.Env = envWithout(
		"WK_PROMETHEUS_ENABLE",
		"WK_PROMETHEUS_LISTEN_ADDR",
		"WK_WUKONGIM_THREE_NODES_BIN",
		"WK_WUKONGIM_THREE_NODES_LOG_DIR",
		"WK_WUKONGIM_THREE_NODES_DATA_ROOT",
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
		"build_cmd=env GOWORK=off go build -buildvcs=true -o " + outputBin + " ./cmd/wukongim",
		"prometheus_enable=true",
		"prometheus_listen_addr=127.0.0.1:9091",
		"prometheus_scrape_targets=[\"127.0.0.1:5011\",\"127.0.0.1:5012\",\"127.0.0.1:5013\"]",
		"node1_config=" + filepath.Join(root, "scripts/wukongim/wukongim-node1.toml"),
		"node2_ready=http://127.0.0.1:5012/readyz",
		"node3_log=" + filepath.Join(logDir, "node3.log"),
		"data_root=" + dataRoot,
		"node1_data=" + filepath.Join(dataRoot, "wukongim-node-1"),
		"node3_data=" + filepath.Join(dataRoot, "wukongim-node-3"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, ".toml.example") {
		t.Fatalf("dry-run output should not use example configs:\n%s", text)
	}
}

func TestWukongIMThreeNodeScriptDryRunPrintsTaggedBuild(t *testing.T) {
	root := repoRoot(t)
	outputBin := filepath.Join(t.TempDir(), "wukongim")

	cmd := exec.Command("bash", "scripts/start-wukongim-three-nodes.sh",
		"--dry-run",
		"--build-tags", "e2e",
		"--bin", outputBin,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"build_cmd=env GOWORK=off go build -buildvcs=true -tags=e2e -o " + outputBin + " ./cmd/wukongim",
		"node1_env=WK_METRICS_ENABLE=true WK_PROMETHEUS_ENABLE=true",
		"node3_env=WK_METRICS_ENABLE=true WK_PROMETHEUS_ENABLE=false",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}

func TestWukongIMThreeNodeScriptRejectsEmptyDataRoot(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("bash", "scripts/start-wukongim-three-nodes.sh", "--dry-run", "--data-root", "")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("empty data root should fail:\n%s", output)
	}
	if !strings.Contains(string(output), "--data-root must not be empty") {
		t.Fatalf("unexpected empty data root error:\n%s", output)
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
