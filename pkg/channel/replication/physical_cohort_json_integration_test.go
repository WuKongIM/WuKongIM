//go:build integration

package replication_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// These JSON probes deliberately use only the published contract. They compile
// before the optional production cohort fields exist, so absence is a behavior
// failure after the real durable-write prerequisites have been verified.
type physicalCohortJSONRef struct {
	Instance string `json:"instance"`
	Attempt  uint64 `json:"attempt"`
}

type physicalCohortJSONLink struct {
	CohortRef      physicalCohortJSONRef `json:"cohort_ref"`
	RequestOrdinal uint32                `json:"request_ordinal"`
}

type physicalCohortJSONCoverage struct {
	Links   []physicalCohortJSONLink `json:"links"`
	Dropped uint64                   `json:"dropped"`
}

// TestStoreAdapterRealCommitJSONHasPhysicalCohortReference verifies the public
// store seam against a real message database. A local server-shaped Trace does
// not claim that a business RPC or a cross-node peer exchange was executed.
func TestStoreAdapterRealCommitJSONHasPhysicalCohortReference(t *testing.T) {
	base := context.Background()
	dir := t.TempDir()
	observer := &recordingCommitObserver{}
	factory := channelstore.NewMessageDBFactoryWithOptions(dir, channelstore.MessageDBFactoryOptions{CommitObserver: observer})
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := recorder.Start(base, rpctrace.SideServer, 86)
	if trace == nil {
		t.Fatal("fixture did not admit its selected observation")
	}
	t.Cleanup(func() { trace.Finish(rpctrace.ResultError) })
	key := ch.ChannelKey("1:physical-cohort-json")
	id := ch.ChannelID{ID: "physical-cohort-json", Type: 1}
	mutation := sealedMutation(t, key, id, 53001)
	mutation.Committed = 1
	results := adapter.Sync(rpctrace.With(base, trace), []replication.Mutation{mutation})
	if len(results) != 1 || results[0].Err != nil || results[0].Outcome != ch.AppendOutcomeDurable || results[0].LastOffset != 1 {
		t.Fatalf("fixture durable write failed: %+v", results)
	}
	events := observer.snapshot()
	if len(events) != 1 || events[0].Requests != 1 || events[0].Records != 1 || events[0].Err != nil || !events[0].Native.Available {
		t.Fatalf("fixture did not observe one real nonempty native commit: %+v", events)
	}
	sample := trace.Finish(rpctrace.ResultOK)
	if sample == nil || sample.ServiceID != 86 || sample.Side != rpctrace.SideServer || !sample.Unbound {
		t.Fatalf("fixture trace identity changed: %+v", sample)
	}
	wantStages := []rpctrace.Stage{rpctrace.StageCommitAdmission, rpctrace.StageCommitQueue, rpctrace.StageCommitCollect, rpctrace.StageCommitBuild, rpctrace.StageCommitDurable, rpctrace.StageCommitPublish}
	if len(sample.Spans) != len(wantStages) {
		t.Fatalf("fixture stages=%+v, want six existing coordinator phases", sample.Spans)
	}
	for i, want := range wantStages {
		got := sample.Spans[i]
		if got.Stage != want || got.Incomplete || got.Result != rpctrace.ResultOK {
			t.Fatalf("fixture phase[%d]=%+v, want completed %s", i, got, want)
		}
	}
	if err := factory.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := channelstore.NewMessageDBFactory(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	readAdapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: reopened, MaxBatchItems: 4, MaxBatchBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := ch.DeriveProposalEntries(mutation.Manifest, len(mutation.Records), func(i int) ch.Record { return mutation.Records[i] })
	if !ok || len(entries) != 1 {
		t.Fatal("fixture proposal identity is invalid")
	}
	loaded, err := readAdapter.Load(base, replication.LoadBatch{Items: []replication.LoadRequest{{ChannelKey: key, ChannelID: id, ProbeIndexes: []uint64{1, 2}}}})
	if err != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil {
		t.Fatalf("fixture reopened Load=%+v err=%v", loaded, err)
	}
	wantState := replication.ReplicaState{LEO: 1, Committed: 1, Manifest: mutation.Manifest, TailIdentity: entries[0]}
	wantEntries := []replication.EntryProbe{{Index: 1, Present: true, Identity: entries[0]}, {Index: 2}}
	if loaded.Items[0].State != wantState || !reflect.DeepEqual(loaded.Items[0].Entries, wantEntries) {
		t.Fatalf("fixture reopened frontier=%+v, want durable exact proposal", loaded.Items[0])
	}
	fetched := readAdapter.Fetch(base, []replication.FetchRange{{ChannelKey: key, ChannelID: id, Expected: wantState, From: 1, Through: 1, MaxBytes: 32 << 10}})
	if len(fetched) != 1 || fetched[0].Err != nil || fetched[0].State != wantState || len(fetched[0].Proposals) != 1 {
		t.Fatalf("fixture reopened Fetch=%+v", fetched)
	}
	wantRecords := append([]ch.Record(nil), mutation.Records...)
	wantRecords[0].Index = 1
	if fetched[0].Proposals[0].Manifest != mutation.Manifest || !reflect.DeepEqual(fetched[0].Proposals[0].Records, wantRecords) {
		t.Fatalf("fixture reopened row=%+v, want original payload and identity", fetched[0].Proposals[0])
	}

	sampleJSON, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	batchJSON, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("durable/native/six-phase/reopen/row prerequisites passed; sample=%s batch=%s", sampleJSON, batchJSON)
	var rpcProbe struct {
		CommitCohorts *physicalCohortJSONCoverage `json:"commit_cohorts"`
	}
	var batchProbe struct {
		CohortRef *physicalCohortJSONRef `json:"cohort_ref"`
	}
	if err := json.Unmarshal(sampleJSON, &rpcProbe); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(batchJSON, &batchProbe); err != nil {
		t.Fatal(err)
	}
	if rpcProbe.CommitCohorts == nil || batchProbe.CohortRef == nil {
		t.Fatalf("missing physical cohort reference: sample_absent=%t batch_absent=%t", rpcProbe.CommitCohorts == nil, batchProbe.CohortRef == nil)
	}
	coverage := rpcProbe.CommitCohorts
	if len(coverage.Links) != 1 || coverage.Dropped != 0 {
		t.Fatalf("cohort coverage=%+v, want one whole link without drops", coverage)
	}
	link := coverage.Links[0]
	if link.CohortRef.Instance == "" || link.CohortRef.Attempt == 0 || link.RequestOrdinal != 0 || link.CohortRef != *batchProbe.CohortRef {
		t.Fatalf("cohort reference does not join this actual batch: link=%+v batch=%+v", link, batchProbe.CohortRef)
	}
}
