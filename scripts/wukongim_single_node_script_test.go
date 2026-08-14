package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestWukongIMSingleNodeScriptDryRunPrintsCommand(t *testing.T) {
	root := repoRoot(t)
	outputBin := filepath.Join(t.TempDir(), "wukongim")
	logDir := filepath.Join(t.TempDir(), "logs")
	dataDir := filepath.Join(t.TempDir(), "data")

	cmd := exec.Command("bash", "scripts/start-wukongim-single-node.sh",
		"--dry-run",
		"--bin", outputBin,
		"--log-dir", logDir,
		"--data-dir", dataDir,
	)
	cmd.Dir = root
	cmd.Env = envWithout("WK_PROMETHEUS_ENABLE", "WK_PROMETHEUS_BINARY_PATH",
		"WK_WUKONGIM_SINGLE_NODE_CONFIG",
		"WK_WUKONGIM_SINGLE_NODE_BIN",
		"WK_WUKONGIM_SINGLE_NODE_LOG_DIR",
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR",
		"WK_WUKONGIM_SINGLE_NODE_READY_URL",
		"WK_WUKONGIM_SINGLE_NODE_READY_TIMEOUT",
		"WK_WUKONGIM_SINGLE_NODE_POLL_INTERVAL")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"build_cmd=GOWORK=off go build -o " + outputBin + " ./cmd/wukongim",
		"config=" + filepath.Join(root, "scripts/wukongim/wukongim.toml"),
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
	if strings.Contains(text, ".toml.example") {
		t.Fatalf("dry-run output should not use example configs:\n%s", text)
	}
}

func TestWukongIMSingleNodeScriptDefaultsUseIsolatedDataDir(t *testing.T) {
	root := repoRoot(t)
	singleDataDir := filepath.Join(root, "data/wukongim-single-node-data")
	threeNode1DataDir := filepath.Join(root, "data/wukongim-node-1")

	cmd := exec.Command("bash", "scripts/start-wukongim-single-node.sh", "--dry-run")
	cmd.Dir = root
	cmd.Env = envWithout("WK_PROMETHEUS_ENABLE", "WK_PROMETHEUS_BINARY_PATH",
		"WK_WUKONGIM_SINGLE_NODE_CONFIG",
		"WK_WUKONGIM_SINGLE_NODE_BIN",
		"WK_WUKONGIM_SINGLE_NODE_DATA_DIR")
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

	config := readFile(t, filepath.Join(root, "scripts/wukongim/wukongim.toml"))
	if !strings.Contains(config, `data_dir = "./data/wukongim-single-node-data"`) {
		t.Fatalf("single-node config should use isolated data dir:\n%s", config)
	}
	if strings.Contains(config, `data_dir = "./data/wukongim-node-1"`) {
		t.Fatalf("single-node config should not reuse three-node node1 data dir:\n%s", config)
	}
}

func TestWukongIMSingleNodeScriptRejectsBroadManagedCleanupTargets(t *testing.T) {
	root := repoRoot(t)
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "filesystem root", path: "/"},
		{name: "repository root", path: root},
		{name: "shared repository data", path: filepath.Join(root, "data")},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("bash", "scripts/start-wukongim-single-node.sh",
				"--clean", "--no-build", "--data-dir", test.path,
				"--bin", filepath.Join(t.TempDir(), "unused-wukongim"),
				"--log-dir", filepath.Join(t.TempDir(), "logs"),
			)
			cmd.Dir = root
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("broad cleanup target must fail closed:\n%s", output)
			}
			if !strings.Contains(string(output), "data directory is too broad for managed cleanup") {
				t.Fatalf("unexpected broad-target failure:\n%s", output)
			}
		})
	}
}
