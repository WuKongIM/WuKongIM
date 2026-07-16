package cloudviewstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecorderRetriesTransitionAfterPersistenceFailure(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "textfiles")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	metricsPath := filepath.Join(directory, "cloud-view.prom")
	recorder, err := New("run-1", "", metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(metricsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := recorder.MarkInteractive(); err == nil {
		t.Fatal("MarkInteractive() error = nil, want persistence failure")
	}
	if !recorder.Snapshot().Interactive || recorder.PersistenceHealthy() {
		t.Fatal("failed transition did not remain conservatively dirty in memory")
	}
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !recorder.PersistenceHealthy() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !recorder.PersistenceHealthy() {
		t.Fatal("background persistence retry did not recover")
	}
}
