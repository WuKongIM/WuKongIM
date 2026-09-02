package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScenarioRuntimeProfileRejectsMissingMalformedAndUnknownScale(t *testing.T) {
	dir := t.TempDir()
	if _, err := nodeRuntimeProfileForScenario(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("missing scenario unexpectedly produced a runtime profile")
	}
	malformed := filepath.Join(dir, "malformed.yaml")
	if err := os.WriteFile(malformed, []byte("objectives: [unterminated"), 0o600); err != nil {
		t.Fatalf("write malformed scenario: %v", err)
	}
	if _, err := nodeRuntimeProfileForScenario(malformed); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("malformed scenario error = %v", err)
	}
	unknown := filepath.Join(dir, "unknown.yaml")
	if err := os.WriteFile(unknown, []byte("objectives:\n  scale: unreviewed-scale\n"), 0o600); err != nil {
		t.Fatalf("write unknown scenario: %v", err)
	}
	if _, err := nodeRuntimeProfileForScenario(unknown); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("unknown scale error = %v", err)
	}
}

func TestRenderScenarioRequiresEveryMutableRunField(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "duration", body: "run:\n  id: old\n  report_dir: old\n", want: "run.duration"},
		{name: "id", body: "run:\n  duration: 30m\n  report_dir: old\n", want: "run.id"},
		{name: "report directory", body: "run:\n  duration: 30m\n  id: old\n", want: "run.report_dir"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenario := filepath.Join(t.TempDir(), "scenario.yaml")
			if err := os.WriteFile(scenario, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("write scenario: %v", err)
			}
			err := renderScenario(dir, BundleSpec{ScenarioPath: scenario, RunID: "run-contract", Duration: 30 * time.Minute})
			if !errors.Is(err, ErrInvalidBundle) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("render error = %v, want %s", err, tt.want)
			}
		})
	}
}

func TestBundleRecordEqualityRejectsCardinalityBeforeContent(t *testing.T) {
	record := FileRecord{Path: "config/scenario.yaml", Mode: 0o640, Size: 1, SHA256: strings.Repeat("a", 64)}
	if recordsEqual([]FileRecord{record}, nil) {
		t.Fatal("different record cardinality accepted")
	}
	if !recordsEqual([]FileRecord{record}, []FileRecord{record}) {
		t.Fatal("identical record inventory rejected")
	}
}
