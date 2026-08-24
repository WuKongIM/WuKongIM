package localbaseline

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadStorageOverlapEvidenceVerifiesClosedInventories(t *testing.T) {
	directory := t.TempDir()
	inventoryDirectory := filepath.Join(directory, "snapshot-inventory")
	if err := os.Mkdir(inventoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 8, 13, 1, 2, 3, 123456789, time.UTC)
	rows := []string{strings.Join(storageOverlapHeader, "\t")}
	for index, sample := range []string{"post-warmup", "periodic-000001", "terminal"} {
		body := fmt.Sprintf("slot-%d/chunk\t%d\n", index+1, index+3)
		identity := writeStorageInventoryFixture(t, inventoryDirectory, sample, body)
		rows = append(rows, fmt.Sprintf("%s\trun-1\t%s\tnode-1\tcomplete\t%d\t0\t1\t%d\t%s\tsnapshot-inventory/%s-node-1.tsv",
			startedAt.Add(time.Duration(index)*25*time.Second).Format(time.RFC3339Nano), sample, index+10, index+3, identity, sample))
	}
	path := filepath.Join(directory, "storage-overlap.tsv")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	evidence, err := ReadStorageOverlapEvidence(path, "run-1")
	if err != nil {
		t.Fatalf("ReadStorageOverlapEvidence() error = %v", err)
	}
	if !evidence.CaptureComplete || len(evidence.Samples) != 3 {
		t.Fatalf("evidence = %+v, want three complete samples", evidence)
	}
	for _, sample := range evidence.Samples {
		if !sample.InventoryVerified {
			t.Fatalf("sample = %+v, want verified inventory", sample)
		}
	}
}

func TestReadStorageOverlapEvidenceFailsClosedOnMissingOrTamperedInput(t *testing.T) {
	t.Run("zero-offset UTC", func(t *testing.T) {
		body := strings.Join(storageOverlapHeader, "\t") + "\n" +
			"2026-08-13T01:02:03+00:00\trun-1\tpost-warmup\tnode-1\tmissing\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\n"
		evidence, err := ParseStorageOverlapEvidence(strings.NewReader(body), "run-1", func(string, int64) ([]byte, error) {
			return nil, fmt.Errorf("unexpected inventory read")
		})
		if err != nil || evidence.CaptureComplete {
			t.Fatalf("zero-offset UTC evidence = %+v, error = %v", evidence, err)
		}
	})

	t.Run("typed missing row", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, "storage-overlap.tsv")
		body := strings.Join(storageOverlapHeader, "\t") + "\n" +
			"2026-08-13T01:02:03Z\trun-1\tpost-warmup\tnode-1\tmissing\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		evidence, err := ReadStorageOverlapEvidence(path, "run-1")
		if err != nil {
			t.Fatalf("ReadStorageOverlapEvidence() error = %v", err)
		}
		if evidence.CaptureComplete {
			t.Fatalf("evidence = %+v, want incomplete", evidence)
		}
	})

	t.Run("inventory digest mismatch", func(t *testing.T) {
		directory := t.TempDir()
		inventoryDirectory := filepath.Join(directory, "snapshot-inventory")
		if err := os.Mkdir(inventoryDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		identity := writeStorageInventoryFixture(t, inventoryDirectory, "post-warmup", "slot/chunk\t3\n")
		path := filepath.Join(directory, "storage-overlap.tsv")
		body := strings.Join(storageOverlapHeader, "\t") + "\n" + fmt.Sprintf(
			"2026-08-13T01:02:03Z\trun-1\tpost-warmup\tnode-1\tcomplete\t1\t0\t1\t3\t%s\tsnapshot-inventory/post-warmup-node-1.tsv\n", identity)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(inventoryDirectory, "post-warmup-node-1.tsv"), []byte("tampered\t3\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadStorageOverlapEvidence(path, "run-1"); err == nil || !strings.Contains(err.Error(), "digest") {
			t.Fatalf("ReadStorageOverlapEvidence() error = %v, want digest failure", err)
		}
	})

	t.Run("wrong run", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, "storage-overlap.tsv")
		body := strings.Join(storageOverlapHeader, "\t") + "\n" +
			"2026-08-13T01:02:03Z\treplacement\tpost-warmup\tnode-1\tmissing\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadStorageOverlapEvidence(path, "run-1"); err == nil || !strings.Contains(err.Error(), "identity") {
			t.Fatalf("ReadStorageOverlapEvidence() error = %v, want identity failure", err)
		}
	})
}

func writeStorageInventoryFixture(t *testing.T, directory, sample, body string) string {
	t.Helper()
	path := filepath.Join(directory, sample+"-node-1.tsv")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
}
