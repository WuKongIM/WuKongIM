//go:build integration

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/metrics"
)

func TestMixedSendDiagnosticFilesAreBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	w := newMixedSendDiagnosticFile(t, path, 8)
	if n, err := w.Write([]byte("12345678")); n != 8 || err != nil {
		t.Fatalf("first write = %d, %v", n, err)
	}
	if n, err := w.Write([]byte("9")); n != 0 || err == nil || !w.overflow {
		t.Fatalf("overflow = %d, %v, %t", n, err, w.overflow)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 8 || info.Mode().Perm() != 0600 {
		t.Fatalf("bounded private file = %v, %v", info, err)
	}
	data, err := readMixedSendDiagnosticFile(path)
	if err != nil || string(data) != "12345678" {
		t.Fatalf("read = %q, %v", data, err)
	}
	if err := os.WriteFile(path, make([]byte, (64<<10)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMixedSendDiagnosticFile(path); err == nil {
		t.Fatal("oversized system snapshot accepted")
	}
	if _, err := readMixedSendDiagnosticFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing signal silently accepted")
	}
}

func TestMixedSendCounterWindowRetainsOnlyBoundaryFiles(t *testing.T) {
	dir := t.TempDir()
	apps := []*App{{metrics: metrics.New(1, "one")}}
	apps[0].metrics.ChannelRuntime.ObserveAppendStage("runtime_append", "ok", time.Second)
	finish := startMixedSendCounterWindow(t, apps, dir, 3000, 500)
	apps[0].metrics.ChannelRuntime.ObserveAppendStage("runtime_append", "ok", time.Millisecond)
	finish()
	first, err := os.ReadFile(filepath.Join(dir, "after.json"))
	if err != nil {
		t.Fatal(err)
	}
	finish()
	second, err := os.ReadFile(filepath.Join(dir, "after.json"))
	if err != nil || string(first) != string(second) {
		t.Fatal("finish rewrote completed evidence")
	}
	var before, after mixedSendSnapshot
	for name, target := range map[string]*mixedSendSnapshot{"before": &before, "after": &after} {
		data, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil || json.Unmarshal(data, target) != nil {
			t.Fatalf("invalid %s snapshot: %v", name, err)
		}
	}
	if len(before.Families) != 1 || before.Families[0].Metric[0].Histogram.GetSampleCount() != 1 || after.Families[0].Metric[0].Histogram.GetSampleCount() != 2 {
		t.Fatal("boundary snapshots did not detach warmup and measured counters")
	}
	if len(after.Runtime) != 5 {
		t.Fatal("missing process runtime counters")
	}
	var window struct {
		Schema    string `json:"schema"`
		Completed bool   `json:"handlers_completed"`
		Profile   bool   `json:"profile_enabled"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "window.json"))
	if err != nil || json.Unmarshal(data, &window) != nil || window.Schema != "mixed-send-counters/v1" || !window.Completed || window.Profile {
		t.Fatal("incorrect counter-only completion receipt")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 3 {
		t.Fatal("counter-only mode emitted unexpected files")
	}
}

func TestMixedSendCounterWindowMarksEarlyExitIncomplete(t *testing.T) {
	dir := t.TempDir()
	t.Run("early_exit", func(t *testing.T) { _ = startMixedSendCounterWindow(t, nil, dir, 3000, 500) })
	data, err := os.ReadFile(filepath.Join(dir, "window.json"))
	var window map[string]any
	if err != nil || json.Unmarshal(data, &window) != nil || window["handlers_completed"] != false {
		t.Fatal("cleanup claimed a completed measurement")
	}
}
