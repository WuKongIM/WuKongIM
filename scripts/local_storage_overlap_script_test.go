package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLocalStorageOverlapCaptureProducesTypedCompleteAndMissingRows(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "chat-lifecycle", "capture-local-storage-overlap.sh")
	dir := t.TempDir()
	metrics := filepath.Join(dir, "node.prom")
	snapshotRoot := filepath.Join(dir, "data", "slotraft-snapshots")
	inventoryDir := filepath.Join(dir, "snapshot-inventory")
	if err := os.Mkdir(inventoryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	inventory := filepath.Join(inventoryDir, "sample-1-node-1.tsv")
	if err := os.MkdirAll(filepath.Join(snapshotRoot, "slot-1", "snap-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshotRoot, "slot-1", "snap-1", "chunk-000000"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshotRoot, "slot-1", "snap-1", "chunk-000001"), []byte("defg"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTimelineTestFileForScripts(t, metrics, strings.Join([]string{
		`wukongim_storage_pebble_compaction_count{store="raft"} 2`,
		`wukongim_storage_pebble_compaction_count{store="meta"} 3`,
		`wukongim_storage_pebble_compactions_in_progress{store="raft"} 1`,
		`wukongim_storage_pebble_compactions_in_progress{store="meta"} 0`,
	}, "\n")+"\n")
	arguments := []string{
		script, "--metrics", metrics, "--snapshot-root", snapshotRoot,
		"--inventory", inventory, "--observed-at", "2026-08-13T10:00:00.123Z", "--run-id", "test-run",
		"--sample", "sample-1", "--node", "node-1",
	}
	output, err := exec.Command("bash", arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("capture complete row: %v: %s", err, output)
	}
	columns := strings.Split(strings.TrimSpace(string(output)), "\t")
	if len(columns) != 11 || columns[0] != "2026-08-13T10:00:00.123Z" || columns[1] != "test-run" || columns[2] != "sample-1" ||
		columns[3] != "node-1" || columns[4] != "complete" || columns[5] != "5" || columns[6] != "1" ||
		columns[7] != "2" || columns[8] != "7" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(columns[9]) ||
		columns[10] != "snapshot-inventory/sample-1-node-1.tsv" {
		t.Fatalf("complete row = %q", output)
	}
	if inventoryBody, readErr := os.ReadFile(inventory); readErr != nil || string(inventoryBody) != "slot-1/snap-1/chunk-000000\t3\nslot-1/snap-1/chunk-000001\t4\n" {
		t.Fatalf("inventory = %q/%v", inventoryBody, readErr)
	}
	if err := os.Remove(inventory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metrics, []byte("wukongim_storage_pebble_compaction_count 1.5\nwukongim_storage_pebble_compactions_in_progress 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err = exec.Command("bash", arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("capture missing row: %v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "2026-08-13T10:00:00.123Z\ttest-run\tsample-1\tnode-1\tmissing\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable" {
		t.Fatalf("missing row = %q", got)
	}
}

func writeTimelineTestFileForScripts(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
