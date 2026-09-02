package cloudanalysis

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

func TestWorkloadSourceFailsClosedOnContextIdentityAndFilesystemErrors(t *testing.T) {
	source := newWorkloadSummarySource(t.TempDir())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.inspect(canceled, "run-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled inspect error = %v", err)
	}
	if _, err := source.inspect(context.Background(), " "); !errors.Is(err, errInvalidWorkloadSummary) {
		t.Fatalf("blank identity inspect error = %v", err)
	}

	notDirectory := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notDirectory, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	brokenSummary := &workloadSummarySource{summaryPath: filepath.Join(notDirectory, "summary.json")}
	if _, err := brokenSummary.inspect(context.Background(), "run-1"); err == nil || !strings.Contains(err.Error(), "read workload summary") {
		t.Fatalf("summary filesystem error = %v", err)
	}
	brokenLive := &workloadSummarySource{
		summaryPath: filepath.Join(t.TempDir(), "missing-summary.json"),
		statusPath:  filepath.Join(notDirectory, "status.json"),
	}
	if _, err := brokenLive.inspect(context.Background(), "run-1"); err == nil || !strings.Contains(err.Error(), "read workload live status") {
		t.Fatalf("live filesystem error = %v", err)
	}
}

func TestWorkloadLiveStatusRejectsImpossibleCountsAndOverflow(t *testing.T) {
	invalidConnections := []analysis.WorkloadConnectionCounts{
		{Target: 0},
		{Target: 2, Online: 1, Starting: 1, Closing: 1, TrafficReady: 1},
		{Target: 2, Online: 1, TrafficReady: 2},
	}
	for _, value := range invalidConnections {
		if validWorkloadConnections(value) {
			t.Fatalf("validWorkloadConnections(%+v) = true", value)
		}
	}
	connections := analysis.WorkloadConnectionCounts{Target: math.MaxInt}
	if addWorkloadConnections(&connections, analysis.WorkloadConnectionCounts{Target: 1}) {
		t.Fatal("addWorkloadConnections accepted integer overflow")
	}
	closes := analysis.WorkloadSessionCloseCounts{Expired: math.MaxUint64}
	if addWorkloadCloseCounts(&closes, analysis.WorkloadSessionCloseCounts{Expired: 1}) {
		t.Fatal("addWorkloadCloseCounts accepted uint64 overflow")
	}
}

func TestWorkloadSummaryValidatorsEnforceCardinalityAndTerminalVocabulary(t *testing.T) {
	limits := make([]workloadDiagnosticLimit, 33)
	if validWorkloadLimits(limits) {
		t.Fatal("validWorkloadLimits accepted more than 32 entries")
	}
	if validWorkloadLimits([]workloadDiagnosticLimit{{Name: "latency", Actual: math.NaN(), Limit: 1}}) {
		t.Fatal("validWorkloadLimits accepted NaN evidence")
	}
	started := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	duplicateWindows := []workloadDiagnosticWindow{
		{Phase: "run", StartedAt: started, EndedAt: started.Add(time.Minute)},
		{Phase: "run", StartedAt: started.Add(time.Minute), EndedAt: started.Add(2 * time.Minute)},
	}
	if validWorkloadPhaseWindows(duplicateWindows) {
		t.Fatal("validWorkloadPhaseWindows accepted duplicate phases")
	}
	if validWorkloadTerminal("failed", 4, "invented_verdict") {
		t.Fatal("validWorkloadTerminal accepted an unknown verdict")
	}
	if hasRequiredWorkloadArrayFields([]byte(`{"name":"not-an-array"}`), "name") {
		t.Fatal("hasRequiredWorkloadArrayFields accepted an object")
	}
}
