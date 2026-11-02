//go:build integration

package replication_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	replication "github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	goroutines "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// TestRuntimeSparseResidentRetryAllowanceHeap compares unused public retry
// allowances while the exact same empty Channels remain installed. It proves
// only an isolated allocation bound, never a native run's physical heap cause.
func TestRuntimeSparseResidentRetryAllowanceHeap(t *testing.T) {
	const residentChannels = 10_000
	const extraAllowance = int64(8 << 20)
	const extraHeapGuard = uint64(256 << 20)
	report := sparseResidentHeapReport{
		Kind: "candidate_sparse_resident_retry_allowance", Source: os.Getenv("WK_TEST_SOURCE_REV"),
		SourcePatch: os.Getenv("WK_TEST_SOURCE_PATCH_SHA256"), Started: time.Now().UTC(),
		ResidentChannels: residentChannels, RetentionCohorts: [4]int{1, 64, 64, 1},
		ExtraAllowance: extraAllowance, ExtraHeapGuard: extraHeapGuard, Outcome: "incomplete",
	}
	completedNormally := false
	defer func() {
		report.Finished = time.Now().UTC()
		report.Passed = completedNormally && !t.Failed()
		if path := os.Getenv("WK_TEST_SPARSE_RESIDENT_HEAP_REPORT"); path != "" {
			if err := writeSparseResidentHeapReport(path, report); err != nil {
				// An unpublished experiment cannot establish a valid resource RED.
				report.Completed = false
				report.ResourceRED = false
				report.Outcome = "invalid_artifact"
				report.Passed = false
				report.ArtifactError = err.Error()
				t.Errorf("sparse resident heap report: %v", err)
			}
		}
		body, err := json.Marshal(report)
		if err != nil {
			t.Errorf("encode sparse resident report: %v", err)
			return
		}
		t.Logf("sparse_resident_heap_report=%s", body)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	for _, allowance := range report.RetentionCohorts {
		sample, err := sparseResidentHeapCohort(ctx, residentChannels, allowance, extraHeapGuard)
		report.Samples = append(report.Samples, sample)
		if err != nil {
			report.Error = err.Error()
			t.Fatalf("incomplete resident experiment: %v", err)
		}
		if !sample.Completed || !sample.CleanupJoined || sample.Installed != residentChannels || sample.Released != residentChannels {
			report.Error = "resident cohort or cleanup incomplete"
			t.Fatal(report.Error)
		}
	}
	report.Completed = true
	report.ExtraHeap = [2]int64{
		report.Samples[1].ResidentDelta - report.Samples[0].ResidentDelta,
		report.Samples[2].ResidentDelta - report.Samples[3].ResidentDelta,
	}
	firstExceeded, secondExceeded := report.ExtraHeap[0] > extraAllowance, report.ExtraHeap[1] > extraAllowance
	if firstExceeded || secondExceeded {
		report.ResourceRED = firstExceeded && secondExceeded
		report.Outcome = "order_dependent_resource_comparison"
		if report.ResourceRED {
			report.Outcome = "genuine_resource_red"
		}
		t.Fatalf("unused retry allowance retained extra heap: forward %d reverse %d, fixed allowance %d",
			report.ExtraHeap[0], report.ExtraHeap[1], extraAllowance)
	}
	report.Outcome = "within_fixed_resource_allowance"
	completedNormally = true
}

// sparseResidentHeapCohort retains the owner through both GC samples and never
// releases a measured Channel before the resident sample. Boundary adapters are
// stateless and refuse writes; pool warm-up and MaxChannels are identical.
func sparseResidentHeapCohort(ctx context.Context, count, allowance int, guard uint64) (sample sparseResidentHeapSample, resultErr error) {
	sample.RetainedCommandAllowance = allowance
	owner, err := replication.NewRuntime(replication.RuntimeConfig{
		LocalNode: 1, Store: emptyFrontierBoundary{}, Link: emptyPeerBoundary{},
		Goroutines: goroutines.New(), MaxChannels: count, MaxVoters: 3, MaxRetainedCommands: allowance,
	})
	if err != nil {
		return sample, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := owner.Close(closeCtx)
		cancel()
		sample.CleanupJoined = err == nil
		resultErr = errors.Join(resultErr, err)
	}()
	log := owner.Log()
	if err := installReleaseCohort(ctx, log, 0, 64); err != nil {
		return sample, fmt.Errorf("warm public recovery: %w", err)
	}
	sample.BaselineHeap = quiescentHeap(owner)
	for index := 0; index < count; index++ {
		if err := ctx.Err(); err != nil {
			return sample, err
		}
		authority := sparseResidentAuthority(index)
		installed, err := log.Install(ctx, authority)
		if err != nil {
			return sample, fmt.Errorf("resident Install ordinal %d: %w", index, err)
		}
		if installed.Authority != authority.ID || installed.LEO != 0 || installed.HW != 0 {
			return sample, fmt.Errorf("resident Install ordinal %d: incorrect empty frontier", index)
		}
		sample.Installed++
		if sample.Installed%1000 == 0 {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			if stats.HeapAlloc > sample.BaselineHeap && stats.HeapAlloc-sample.BaselineHeap > guard {
				return sample, fmt.Errorf("local experiment heap guard exceeded after %d installs", sample.Installed)
			}
		}
	}
	sample.ResidentHeap = quiescentHeap(owner)
	sample.ResidentDelta = int64(sample.ResidentHeap) - int64(sample.BaselineHeap)
	runtime.KeepAlive(owner)
	for index := 0; index < count; index++ {
		if err := ctx.Err(); err != nil {
			return sample, err
		}
		authority := sparseResidentAuthority(index)
		if !log.Release(authority.Key, authority.ID) {
			return sample, fmt.Errorf("resident Release ordinal %d: exact quiesced authority refused", index)
		}
		sample.Released++
	}
	sample.Completed = sample.Installed == count && sample.Released == count
	return sample, nil
}

func sparseResidentAuthority(index int) replication.Authority {
	id := ch.ChannelID{ID: fmt.Sprintf("r70-resident-%08d", index), Type: 1}
	return replication.Authority{
		Key: ch.ChannelKeyForID(id), ChannelID: id,
		ID:     replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
}

// Samples are process HeapAlloc deltas with the runtime owner alive. They do not
// assert a per-Channel object layout, retained graph, or native acceptance rule.
type sparseResidentHeapSample struct {
	RetainedCommandAllowance int    `json:"retained_command_allowance"`
	Installed                int    `json:"installed"`
	Released                 int    `json:"released_after_sample"`
	BaselineHeap             uint64 `json:"baseline_heap_bytes"`
	ResidentHeap             uint64 `json:"resident_heap_bytes"`
	ResidentDelta            int64  `json:"resident_delta_bytes"`
	Completed                bool   `json:"completed"`
	CleanupJoined            bool   `json:"cleanup_joined"`
}

type sparseResidentHeapReport struct {
	Kind             string                     `json:"kind"`
	Source           string                     `json:"source"`
	SourcePatch      string                     `json:"source_patch_sha256,omitempty"`
	Started          time.Time                  `json:"started_at"`
	Finished         time.Time                  `json:"finished_at"`
	ResidentChannels int                        `json:"resident_channels"`
	RetentionCohorts [4]int                     `json:"retention_cohorts"`
	Samples          []sparseResidentHeapSample `json:"samples"`
	ExtraAllowance   int64                      `json:"extra_heap_allowance_bytes"`
	ExtraHeapGuard   uint64                     `json:"extra_heap_guard_bytes"`
	ExtraHeap        [2]int64                   `json:"forward_reverse_extra_heap_bytes"`
	Completed        bool                       `json:"completed"`
	ResourceRED      bool                       `json:"resource_red"`
	Passed           bool                       `json:"passed"`
	Outcome          string                     `json:"outcome"`
	Error            string                     `json:"error,omitempty"`
	ArtifactError    string                     `json:"artifact_error,omitempty"`
	NativeCauseProof bool                       `json:"native_heap_cause_proved"`
}

// Publish a complete synced artifact exclusively; stdout still carries the
// truthful failed report if publication or any earlier phase fails.
func writeSparseResidentHeapReport(path string, report sparseResidentHeapReport) error {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".sparse-resident-heap-*.json")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(append(body, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Link(name, path)
}
