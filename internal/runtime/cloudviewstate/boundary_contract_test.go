package cloudviewstate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecorderPersistsAndRestoresExactRunState(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	statePath := filepath.Join(directory, "cloud-view.json")
	metricsPath := filepath.Join(directory, "cloud-view.prom")
	recorder, err := New("run-1", statePath, metricsPath)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := recorder.MarkInteractive(); err != nil {
		t.Fatalf("MarkInteractive(): %v", err)
	}
	if err := recorder.MarkOperatorModified(); err != nil {
		t.Fatalf("MarkOperatorModified(): %v", err)
	}
	if err := recorder.MarkInteractive(); err != nil {
		t.Fatalf("idempotent MarkInteractive(): %v", err)
	}

	state, healthy := recorder.StatusSnapshot()
	if !healthy || state.RunID != "run-1" || !state.Interactive || !state.OperatorModified || state.UpdatedAt.IsZero() {
		t.Fatalf("StatusSnapshot() = (%+v, %v)", state, healthy)
	}
	restored, err := New("run-1", statePath, metricsPath)
	if err != nil {
		t.Fatalf("New(restore): %v", err)
	}
	if got := restored.Snapshot(); got != state {
		t.Fatalf("restored state = %+v, want %+v", got, state)
	}
	metrics, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("ReadFile(metrics): %v", err)
	}
	if !strings.Contains(string(metrics), "wukongim_cloud_view_interactive 1") ||
		!strings.Contains(string(metrics), "wukongim_cloud_view_operator_modified 1") {
		t.Fatalf("metrics projection = %q", metrics)
	}
}

func TestReadRejectsStateOutsideExactBoundedContract(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	missing := filepath.Join(directory, "missing.json")
	if _, found, err := read(missing, "run-1"); err != nil || found {
		t.Fatalf("read(missing) = found %v, err %v", found, err)
	}

	valid := []byte(`{"run_id":"run-1","interactive":false,"operator_modified":false,"updated_at":"2026-08-01T12:00:00Z"}`)
	tests := []struct {
		name string
		body []byte
	}{
		{name: "wrong identity", body: bytes.Replace(valid, []byte("run-1"), []byte("run-2"), 1)},
		{name: "unknown field", body: bytes.Replace(valid, []byte("}"), []byte(`,"extra":true}`), 1)},
		{name: "trailing value", body: append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{name: "oversized trailing whitespace", body: append(append([]byte(nil), valid...), bytes.Repeat([]byte(" "), 64<<10)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(directory, strings.ReplaceAll(test.name, " ", "-")+".json")
			if err := os.WriteFile(path, test.body, 0o600); err != nil {
				t.Fatalf("WriteFile(): %v", err)
			}
			if _, _, err := read(path, "run-1"); err == nil {
				t.Fatal("read() error = nil, want bounded state rejection")
			}
		})
	}
}

func TestNewRejectsMissingRunIdentity(t *testing.T) {
	t.Parallel()

	if _, err := New(" \t", "", ""); err == nil {
		t.Fatal("New() error = nil, want missing run identity rejection")
	}
}

func TestWriteAtomicReplacesFileAndMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	if err := writeAtomic(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("writeAtomic(first): %v", err)
	}
	if err := writeAtomic(path, []byte("second"), 0o640); err != nil {
		t.Fatalf("writeAtomic(second): %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(): %v", err)
	}
	if string(body) != "second" || info.Mode().Perm() != 0o640 {
		t.Fatalf("atomic file = body %q mode %o", body, info.Mode().Perm())
	}
}

func TestReadAcceptsStateWithoutExpectedIdentity(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	want := State{RunID: "run-any", Interactive: true, UpdatedAt: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	body := []byte(`{"run_id":"run-any","interactive":true,"operator_modified":false,"updated_at":"2026-08-01T12:00:00Z"}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	got, found, err := read(path, "")
	if err != nil || !found || got != want {
		t.Fatalf("read() = (%+v, %v, %v), want (%+v, true, nil)", got, found, err, want)
	}
}
