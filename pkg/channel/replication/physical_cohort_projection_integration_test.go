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

func projectionTrace(t *testing.T) *rpctrace.Trace {
	t.Helper()
	r, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := r.Start(context.Background(), rpctrace.SideServer, 21)
	if trace == nil {
		t.Fatal("observation fixture admission")
	}
	t.Cleanup(func() { trace.Finish(rpctrace.ResultStopped) })
	return trace
}
func projectionCoverage(t *testing.T, trace *rpctrace.Trace) *physicalCohortJSONCoverage {
	t.Helper()
	b, err := json.Marshal(trace.Finish(rpctrace.ResultOK))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Coverage *physicalCohortJSONCoverage `json:"commit_cohorts"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Coverage
}
func projectionOne(t *testing.T, trace *rpctrace.Trace) physicalCohortJSONRef {
	t.Helper()
	coverage := projectionCoverage(t, trace)
	if coverage == nil || len(coverage.Links) != 1 || coverage.Dropped != 0 {
		t.Fatalf("exact per-item coverage=%+v", coverage)
	}
	ref := coverage.Links[0].CohortRef
	if ref.Instance == "" || ref.Attempt == 0 {
		t.Fatalf("invalid physical ref=%+v", ref)
	}
	return ref
}

func TestPhysicalCohortProjectionDistinguishesIndependentDurableAndStagedReplay(t *testing.T) {
	observer := &recordingCommitObserver{}
	factory := channelstore.NewMessageDBFactoryWithOptions(t.TempDir(), channelstore.MessageDBFactoryOptions{CommitObserver: observer})
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	old := sealedMutation(t, "1:cohort-old", ch.ChannelID{ID: "cohort-old", Type: 1}, 54101)
	if got := adapter.Sync(context.Background(), []replication.Mutation{old}); len(got) != 1 || got[0].Err != nil || got[0].Outcome != ch.AppendOutcomeDurable {
		t.Fatalf("fixture durable preload=%+v", got)
	}
	a, b, c := projectionTrace(t), projectionTrace(t), projectionTrace(t)
	fresh := sealedMutation(t, "1:cohort-fresh", ch.ChannelID{ID: "cohort-fresh", Type: 1}, 54102)
	fresh.CommitObservation = rpctrace.NewCommitObservation(a)
	replay := fresh
	replay.CommitObservation = rpctrace.NewCommitObservation(b)
	old.CommitObservation = rpctrace.NewCommitObservation(c)
	got := adapter.Sync(context.Background(), []replication.Mutation{fresh, replay, old})
	if len(got) != 3 || got[0].Outcome != ch.AppendOutcomeDurable || got[1].Outcome != ch.AppendOutcomeAlreadyDurable || got[2].Outcome != ch.AppendOutcomeAlreadyDurable {
		t.Fatalf("new/staged-replay/durable outcomes=%+v", got)
	}
	for _, r := range got {
		if r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	events := observer.snapshot()
	if len(events) != 2 || events[1].Records != 1 || !events[1].Native.Available {
		t.Fatalf("physical events=%+v", events)
	}
	if ra, rb := projectionOne(t, a), projectionOne(t, b); ra != rb {
		t.Fatalf("dependent replay separated from its physical write: %+v %+v", ra, rb)
	}
	if coverage := projectionCoverage(t, c); coverage != nil {
		t.Fatalf("independent already-durable item inherited sibling physical commit: %+v", coverage)
	}
}

type cohortSingleStoreFactory struct{ factory channelstore.Factory }

func (f cohortSingleStoreFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (channelstore.ChannelStore, error) {
	return f.factory.ChannelStore(key, id)
}

func TestPhysicalCohortProjectionPreservesSingleStoreFallbackAndClassSplit(t *testing.T) {
	for _, kind := range []string{"fallback", "class_split"} {
		t.Run(kind, func(t *testing.T) {
			observer := &recordingCommitObserver{}
			factory := channelstore.NewMessageDBFactoryWithOptions(t.TempDir(), channelstore.MessageDBFactoryOptions{CommitObserver: observer})
			t.Cleanup(func() { _ = factory.Close() })
			var stores channelstore.Factory = factory
			if kind == "fallback" {
				stores = cohortSingleStoreFactory{factory: factory}
			}
			adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: stores, MaxBatchItems: 4, MaxBatchBytes: 64 << 10})
			if err != nil {
				t.Fatal(err)
			}
			a, b := projectionTrace(t), projectionTrace(t)
			first := sealedMutation(t, "1:projection-first", ch.ChannelID{ID: "projection-first", Type: 1}, 54201)
			second := sealedMutation(t, "1:projection-second", ch.ChannelID{ID: "projection-second", Type: 1}, 54202)
			first.CommitObservation = rpctrace.NewCommitObservation(a)
			second.CommitObservation = rpctrace.NewCommitObservation(b)
			if kind == "class_split" {
				second.Class = replication.MutationClassFollowerQuorum
			}
			got := adapter.Sync(context.Background(), []replication.Mutation{first, second})
			if len(got) != 2 {
				t.Fatalf("aligned results=%+v", got)
			}
			for _, r := range got {
				if r.Err != nil || r.Outcome != ch.AppendOutcomeDurable {
					t.Fatalf("actual durable result=%+v", r)
				}
			}
			events := observer.snapshot()
			if len(events) != 2 || !events[0].Native.Available || !events[1].Native.Available {
				t.Fatalf("physical split events=%+v", events)
			}
			if ra, rb := projectionOne(t, a), projectionOne(t, b); ra == rb {
				t.Fatalf("distinct physical commits shared ref=%+v", ra)
			}
		})
	}
}

func TestPhysicalCohortFactoryFilteringCannotShiftObservationOwners(t *testing.T) {
	for _, kind := range []string{"class", "encoding", "open"} {
		t.Run(kind, func(t *testing.T) {
			observer := &recordingCommitObserver{}
			factory := channelstore.NewMessageDBFactoryWithOptions(t.TempDir(), channelstore.MessageDBFactoryOptions{CommitObserver: observer})
			t.Cleanup(func() { _ = factory.Close() })
			traces := []*rpctrace.Trace{projectionTrace(t), projectionTrace(t), projectionTrace(t)}
			items := make([]channelstore.AppendLeaderBatchItem, 3)
			for i, id := range []string{"filter-first", "filter-bad", "filter-last"} {
				mutation := sealedMutation(t, ch.ChannelKey("1:"+id), ch.ChannelID{ID: id, Type: 1}, uint64(54301+i))
				items[i] = channelstore.AppendLeaderBatchItem{ChannelKey: mutation.ChannelKey, ChannelID: mutation.ChannelID, Request: channelstore.AppendLeaderRequest{Records: mutation.Records, Class: channelstore.AppendClassLeaderQuorum, ExactBaseOffset: true, ExpectedBaseOffset: 0, Proposal: mutation.Manifest, CommitObservation: rpctrace.NewCommitObservation(traces[i])}}
			}
			switch kind {
			case "class":
				items[1].Request.Class = channelstore.AppendClass(255)
			case "encoding":
				items[1].Request.Records[0].PublicationMetadata = []byte{1}
			case "open":
				// Fresh zero IDs are accepted; an empty key rejects this acquisition
				// after valid encoding and before it joins the physical batch.
				items[1].ChannelKey = ""
			}
			got := factory.AppendLeaderBatch(context.Background(), items)
			if len(got) != 3 || got[0].Err != nil || got[0].Outcome != ch.AppendOutcomeDurable || got[1].Err == nil || got[1].Outcome != ch.AppendOutcomeDefinitelyNotWritten || got[2].Err != nil || got[2].Outcome != ch.AppendOutcomeDurable {
				t.Fatalf("filtered alignment=%+v", got)
			}
			events := observer.snapshot()
			if len(events) != 1 || events[0].Records != 2 || !events[0].Native.Available {
				t.Fatalf("filtered actual physical commit=%+v", events)
			}
			if first, last := projectionOne(t, traces[0]), projectionOne(t, traces[2]); first != last {
				t.Fatalf("surviving owners lost common cohort: %+v %+v", first, last)
			}
			if coverage := projectionCoverage(t, traces[1]); coverage != nil {
				t.Fatalf("filtered item acquired another owner's cohort: %+v", coverage)
			}
		})
	}
}
