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

// This candidate ownership regression exercises the real public runtime and
// Install/Release path. Empty durable frontiers isolate retired identity state;
// it does not prove that R68's whole heap growth has the same physical cause.
func TestRuntimeReleaseBoundsRetiredChannelHeap(t *testing.T) {
	const cohort = 50_000
	// One-at-a-time resident admission must not retain another entire identity
	// cohort. The predeclared 2 MiB noise allowance is a regression bound,
	// never the native 6h/5% acceptance rule; it needs empirical validation.
	const maximumRetiredGrowth = uint64(2 << 20)
	started := time.Now()
	report := retiredChannelHeapReport{
		Kind:        "candidate_retired_identity_ownership_regression",
		Source:      os.Getenv("WK_TEST_SOURCE_REV"),
		SourcePatch: os.Getenv("WK_TEST_SOURCE_PATCH_SHA256"),
		Started:     started.UTC(), Cohort: cohort, MaxResident: 1, Voters: 3,
		AllowedGrowth:         maximumRetiredGrowth,
		NativeHeapCauseProved: false, Full12HourQualification: false,
	}
	var owner *replication.Runtime
	completedNormally := false
	// Register the report before construction so every ordinary failure emits
	// a truthful artifact, including runtime construction and cleanup failures.
	defer func() {
		if owner != nil {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			closeErr := owner.Close(closeCtx)
			closeCancel()
			report.CleanupJoined = closeErr == nil
			if closeErr != nil {
				t.Errorf("Runtime.Close: %v", closeErr)
			}
		}
		report.Finished = time.Now().UTC()
		// testing may not mark a panic as failed until after this defer runs.
		report.Passed = completedNormally && !t.Failed()
		if path := os.Getenv("WK_TEST_REPAIR_HEAP_REPORT"); path != "" {
			if err := writeRetiredHeapReport(path, report); err != nil {
				report.Passed = false
				report.ArtifactError = err.Error()
				t.Errorf("heap report: %v", err)
			}
		}
		// The captured Go test output is also a bounded repeatable artifact
		// when no external report path was requested or publication failed.
		data, err := json.Marshal(report)
		if err != nil {
			t.Errorf("encode heap report: %v", err)
			return
		}
		t.Logf("retired_channel_heap_report=%s", data)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var err error
	owner, err = replication.NewRuntime(replication.RuntimeConfig{
		LocalNode: 1, Store: emptyFrontierBoundary{}, Link: emptyPeerBoundary{},
		Goroutines: goroutines.New(), MaxChannels: 1, MaxVoters: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	log := owner.Log()
	// Warm the same fixed pools and public recovery path before either cohort.
	if err := installReleaseCohort(ctx, log, 0, 64); err != nil {
		t.Fatal(err)
	}
	report.InitialHeap = quiescentHeap(owner)
	if err := installReleaseCohort(ctx, log, 64, cohort); err != nil {
		t.Fatal(err)
	}
	report.Completed = cohort
	report.FirstHeap = quiescentHeap(owner)
	if err := installReleaseCohort(ctx, log, 64+cohort, cohort); err != nil {
		t.Fatal(err)
	}
	report.Completed += cohort
	report.SecondHeap = quiescentHeap(owner)
	report.Growth = int64(report.SecondHeap) - int64(report.FirstHeap)
	runtime.KeepAlive(owner)
	if report.SecondHeap > report.FirstHeap && report.SecondHeap-report.FirstHeap > maximumRetiredGrowth {
		t.Fatalf("retired Channel identities retained another cohort: heap grew %d bytes, allowance %d",
			report.SecondHeap-report.FirstHeap, maximumRetiredGrowth)
	}
	completedNormally = true
}

func installReleaseCohort(ctx context.Context, log replication.DurableQuorumLog, start, count int) error {
	for index := start; index < start+count; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := ch.ChannelID{ID: fmt.Sprintf("r69-cold-%08d", index), Type: 1}
		authority := replication.Authority{
			Key: ch.ChannelKeyForID(id), ChannelID: id,
			ID:     replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1},
			Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
		}
		installed, err := log.Install(ctx, authority)
		if err != nil {
			return fmt.Errorf("Install ordinal %d: %w", index, err)
		}
		if installed.Authority != authority.ID || installed.LEO != 0 || installed.HW != 0 {
			return fmt.Errorf("Install ordinal %d: incorrect empty durable frontier", index)
		}
		if !log.Release(authority.Key, authority.ID) {
			return fmt.Errorf("Release ordinal %d: exact quiesced authority refused", index)
		}
	}
	return nil
}

func quiescentHeap(owner *replication.Runtime) uint64 {
	runtime.GC()
	runtime.GC()
	var sample runtime.MemStats
	runtime.ReadMemStats(&sample)
	runtime.KeepAlive(owner)
	return sample.HeapAlloc
}

type retiredChannelHeapReport struct {
	Kind                    string    `json:"kind"`
	Source                  string    `json:"source"`
	SourcePatch             string    `json:"source_patch_sha256,omitempty"`
	Started                 time.Time `json:"started_at"`
	Finished                time.Time `json:"finished_at"`
	Cohort                  int       `json:"cohort_size"`
	MaxResident             int       `json:"maximum_resident_channels"`
	Voters                  int       `json:"voters"`
	Completed               int       `json:"completed_install_release_pairs"`
	InitialHeap             uint64    `json:"initial_live_heap_bytes"`
	FirstHeap               uint64    `json:"first_cohort_live_heap_bytes"`
	SecondHeap              uint64    `json:"second_cohort_live_heap_bytes"`
	Growth                  int64     `json:"second_cohort_growth_bytes"`
	AllowedGrowth           uint64    `json:"allocation_noise_allowance_bytes"`
	CleanupJoined           bool      `json:"cleanup_joined"`
	Passed                  bool      `json:"passed"`
	NativeHeapCauseProved   bool      `json:"native_heap_cause_proved"`
	Full12HourQualification bool      `json:"full_12_hour_qualification"`
	ArtifactError           string    `json:"artifact_error,omitempty"`
}

// writeRetiredHeapReport publishes a complete, synced report without replacing
// earlier evidence. Failed writes never publish a partial successful report.
func writeRetiredHeapReport(path string, report retiredChannelHeapReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".retired-channel-heap-*.json")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Link(name, path)
}

var errUnexpectedBoundaryOperation = errors.New("empty-frontier fixture: unexpected mutating operation")

// The storage boundary is stateless: it returns requested empty durable views
// and never retains IDs. Writes/fetches fail, so this fixture cannot manufacture
// accepted message durability or erase data to make a memory assertion pass.
type emptyFrontierBoundary struct{}

func (emptyFrontierBoundary) Load(ctx context.Context, batch replication.LoadBatch) (replication.LoadBatchResult, error) {
	if err := ctx.Err(); err != nil {
		return replication.LoadBatchResult{}, err
	}
	result := replication.LoadBatchResult{Items: make([]replication.LoadResult, len(batch.Items))}
	for index, request := range batch.Items {
		for _, position := range request.ProbeIndexes {
			result.Items[index].Entries = append(result.Items[index].Entries, replication.EntryProbe{Index: position})
		}
	}
	return result, nil
}
func (emptyFrontierBoundary) LookupCommands(_ context.Context, requests []replication.CommandLookup) []replication.CommandLookupResult {
	return make([]replication.CommandLookupResult, len(requests))
}
func (emptyFrontierBoundary) Sync(_ context.Context, requests []replication.Mutation) []replication.MutationResult {
	result := make([]replication.MutationResult, len(requests))
	for index := range result {
		result[index].Err = errUnexpectedBoundaryOperation
	}
	return result
}
func (emptyFrontierBoundary) Replace(_ context.Context, requests []replication.RecoveryReplacement) []replication.RecoveryReplacementResult {
	result := make([]replication.RecoveryReplacementResult, len(requests))
	for index := range result {
		result[index].Err = errUnexpectedBoundaryOperation
	}
	return result
}
func (emptyFrontierBoundary) Fetch(_ context.Context, requests []replication.FetchRange) []replication.FetchRangeResult {
	result := make([]replication.FetchRangeResult, len(requests))
	for index := range result {
		result[index].Err = errUnexpectedBoundaryOperation
	}
	return result
}

// The network boundary proves only the exact requested empty follower view.
// It retains no requests or identities and never simulates successful writes.
type emptyPeerBoundary struct{}

func (emptyPeerBoundary) Exchange(ctx context.Context, node ch.NodeID, batch replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	if err := ctx.Err(); err != nil {
		return replication.ExchangeBatchResult{}, err
	}
	if batch.Version != replication.ExchangeVersion {
		return replication.ExchangeBatchResult{}, errUnexpectedBoundaryOperation
	}
	result := replication.ExchangeBatchResult{Version: replication.ExchangeVersion, Items: make([]replication.ExchangeItemResult, len(batch.Items))}
	for index, item := range batch.Items {
		if item.Kind != replication.ExchangeProbe || item.Probe == nil || item.Probe.Follower != node || !item.Probe.Valid() {
			return replication.ExchangeBatchResult{}, errUnexpectedBoundaryOperation
		}
		request := item.Probe
		result.Items[index].RequestID = item.RequestID
		result.Items[index].Probe.Proof = replication.ProbeProof{
			ChannelKey: request.ChannelKey, ChannelID: request.ChannelID,
			Leader: request.Leader, Follower: request.Follower,
			Indexes: append([]uint64(nil), request.Indexes...),
		}
		for _, position := range request.Indexes {
			result.Items[index].Probe.Entries = append(result.Items[index].Probe.Entries, replication.EntryProbe{Index: position})
		}
	}
	return result, nil
}
