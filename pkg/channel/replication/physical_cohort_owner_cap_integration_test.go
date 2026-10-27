//go:build integration

package replication_test

import (
	"context"
	"encoding/json"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// This late integration extension was separately authorized after the concrete
// owner cap was selected. It is not part of the frozen test-first RED claim.
func TestPhysicalCohortOwnerCapDropsOnlyObservationAfterRealDurableCommit(t *testing.T) {
	const owners = 70
	base := context.Background()
	dir := t.TempDir()
	observer := &recordingCommitObserver{}
	factory := channelstore.NewMessageDBFactoryWithOptions(dir, channelstore.MessageDBFactoryOptions{CommitObserver: observer})
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := rpctrace.NewRecorder(owners)
	if err != nil {
		t.Fatal(err)
	}
	traces := make([]*rpctrace.Trace, owners)
	for i := range traces {
		traces[i] = recorder.Start(base, rpctrace.SideServer, 21)
		if traces[i] == nil {
			t.Fatalf("owner%d recorder admission failed below its independent active limit", i)
		}
		defer traces[i].Finish(rpctrace.ResultStopped)
	}
	mutation := sealedMutation(t, "1:owner-cap", ch.ChannelID{ID: "owner-cap", Type: 1}, 55101)
	mutation.Committed = 1
	mutation.CommitObservation = rpctrace.NewCommitObservation(traces...)
	got := adapter.Sync(base, []replication.Mutation{mutation})
	if len(got) != 1 || got[0].Err != nil || got[0].Outcome != ch.AppendOutcomeDurable || got[0].LastOffset != 1 {
		t.Fatalf("owner observation overflow changed business result=%+v", got)
	}
	events := observer.snapshot()
	if len(events) != 1 || events[0].Requests != 1 || events[0].Records != 1 || !events[0].Native.Available {
		t.Fatalf("overflow changed actual physical commit=%+v", events)
	}
	var shared physicalCohortJSONRef
	for i, trace := range traces {
		b, err := json.Marshal(trace.Finish(rpctrace.ResultOK))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Coverage *struct {
				Links []struct {
					Ref      physicalCohortJSONRef `json:"cohort_ref"`
					Finalize struct {
						Observed bool `json:"observed"`
						Complete bool `json:"complete"`
					} `json:"finalize"`
				} `json:"links"`
				Dropped       uint64 `json:"dropped"`
				OwnersDropped uint64 `json:"owners_dropped"`
			} `json:"commit_cohorts"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Coverage == nil {
			t.Fatalf("owner%d lost explicit overflow/coverage state", i)
		}
		if i < 64 {
			if len(doc.Coverage.Links) != 1 || doc.Coverage.Dropped != 0 || doc.Coverage.OwnersDropped != 0 || !doc.Coverage.Links[0].Finalize.Observed || !doc.Coverage.Links[0].Finalize.Complete {
				t.Fatalf("retained owner%d link/finalizer=%s", i, b)
			}
			ref := doc.Coverage.Links[0].Ref
			if ref.Instance == "" || ref.Attempt == 0 {
				t.Fatalf("retained owner%d invalid ref=%+v", i, ref)
			}
			if i == 0 {
				shared = ref
			} else if ref != shared {
				t.Fatalf("owner%d not in same actual cohort", i)
			}
		} else if len(doc.Coverage.Links) != 0 || doc.Coverage.OwnersDropped != 1 || doc.Coverage.Dropped != 0 {
			t.Fatalf("overflow owner%d did not remain explicitly unobserved: %s", i, b)
		}
	}
	batchJSON, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	var batchProbe struct {
		Ref *physicalCohortJSONRef `json:"cohort_ref"`
	}
	if err := json.Unmarshal(batchJSON, &batchProbe); err != nil {
		t.Fatal(err)
	}
	if batchProbe.Ref == nil || *batchProbe.Ref != shared {
		t.Fatalf("retained owners did not join actual public batch: ref=%+v shared=%+v", batchProbe.Ref, shared)
	}
	if recorder.Active() != 0 || recorder.Dropped() != 0 {
		t.Fatalf("64-owner cap changed independent recorder admission: active=%d dropped=%d", recorder.Active(), recorder.Dropped())
	}
	if err := factory.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := channelstore.NewMessageDBFactory(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	reader, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: reopened, MaxBatchItems: 4, MaxBatchBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reader.Load(base, replication.LoadBatch{Items: []replication.LoadRequest{{ChannelKey: mutation.ChannelKey, ChannelID: mutation.ChannelID}}})
	if err != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil || loaded.Items[0].State.LEO != 1 || loaded.Items[0].State.Committed != 1 || loaded.Items[0].State.Manifest != mutation.Manifest {
		t.Fatalf("owner cap lost reopened durable frontier: %+v err=%v", loaded, err)
	}
	fetched := reader.Fetch(base, []replication.FetchRange{{ChannelKey: mutation.ChannelKey, ChannelID: mutation.ChannelID, Expected: loaded.Items[0].State, From: 1, Through: 1, MaxBytes: 32 << 10}})
	if len(fetched) != 1 || fetched[0].Err != nil || len(fetched[0].Proposals) != 1 || len(fetched[0].Proposals[0].Records) != 1 || string(fetched[0].Proposals[0].Records[0].Payload) != string(mutation.Records[0].Payload) {
		t.Fatalf("owner cap lost reopened physical row: %+v", fetched)
	}
}
