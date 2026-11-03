//go:build integration

package chatlifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review P2: the actual current-report reader must reject ambiguous threshold
// objects, independently of the already strict latency_gate proof child.
func TestHotP95CurrentReaderRejectsAmbiguousThresholdWire(t *testing.T) {
	base := string(p95PersistedReportBody(t))
	want := `"hot_sendack":{"p95":600000000,"p999":1000000000}`
	if !strings.Contains(base, want) {
		t.Fatal("real report fixture did not contain the declared hot threshold")
	}
	for _, hot := range []string{
		`{"P95":600000000,"p999":1000000000}`,
		`{"p95":600000000,"p95":600000000,"p999":1000000000}`,
		`{"p95":600000000,"p99":null,"p999":1000000000}`,
		`{"p95":600000000,"p99":0,"p999":1000000000}`,
		`{"p95":600000000,"p999":1000000000,"P999":1000000000}`,
		`{"p95":600000000,"p999":1000000000,"p999":1000000000}`,
		`{"p95":600000000,"p999":null}`,
		`{"p95":null,"p999":1000000000}`,
		`{"p95":600000000,"p999":1000000000,"unknown":0}`,
	} {
		t.Run(hot, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.json")
			if err := os.WriteFile(path, []byte(strings.Replace(base, want, `"hot_sendack":`+hot, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadCurrentReport(path); err == nil {
				t.Fatal("ambiguous P95 threshold accepted")
			}
		})
	}
}
