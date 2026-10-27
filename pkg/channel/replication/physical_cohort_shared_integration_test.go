//go:build integration

package replication

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

type cohortOwnerObserver struct{ traces []*rpctrace.Trace }

func (o *cohortOwnerObserver) BeginAppendPhase(stage rpctrace.Stage) rpctrace.AppendPhaseEnd {
	var spans []rpctrace.Span
	seen := map[*rpctrace.Trace]bool{}
	for _, trace := range o.traces {
		if trace != nil && !seen[trace] {
			seen[trace] = true
			spans = append(spans, trace.Begin(stage))
		}
	}
	return func(result rpctrace.Result, _ int, _ func(int) rpctrace.Result) {
		for _, span := range spans {
			span.End(result)
		}
	}
}

// This optional port is a prewritten contract compile RED until the narrow
// production observation handle is introduced. It conveys owners, not a ctx.
func (o *cohortOwnerObserver) CommitObservation() rpctrace.CommitObservation {
	return rpctrace.NewCommitObservation(o.traces...)
}

type realCohortBatchObserver struct {
	mu     sync.Mutex
	events []messagedb.CommitCoordinatorBatchEvent
}

func (*realCohortBatchObserver) SetCommitCoordinatorQueueDepth(int) {}
func (o *realCohortBatchObserver) ObserveCommitCoordinatorBatch(event messagedb.CommitCoordinatorBatchEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

type realCohortRef struct {
	Instance string `json:"instance"`
	Attempt  uint64 `json:"attempt"`
}

func realCohortSampleRef(t *testing.T, sample *rpctrace.Sample) realCohortRef {
	t.Helper()
	b, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Coverage *struct {
			Links []struct {
				Ref     realCohortRef `json:"cohort_ref"`
				Ordinal uint32        `json:"request_ordinal"`
			} `json:"links"`
			Dropped uint64 `json:"dropped"`
		} `json:"commit_cohorts"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Coverage == nil || len(doc.Coverage.Links) != 1 || doc.Coverage.Dropped != 0 || doc.Coverage.Links[0].Ordinal != 0 {
		t.Fatalf("missing exact shared cohort: %s", b)
	}
	ref := doc.Coverage.Links[0].Ref
	if ref.Instance == "" || ref.Attempt == 0 {
		t.Fatalf("invalid cohort ref=%+v", ref)
	}
	return ref
}

func TestPhysicalCohortLocalSyncFansOutToAllSampledProposalOwners(t *testing.T) {
	observer := &realCohortBatchObserver{}
	factory := channelstore.NewMessageDBFactoryWithOptions(t.TempDir(), channelstore.MessageDBFactoryOptions{CommitObserver: observer})
	t.Cleanup(func() { _ = factory.Close() })
	store, err := NewStoreAdapter(StoreAdapterConfig{Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := rpctrace.NewRecorder(3)
	if err != nil {
		t.Fatal(err)
	}
	traces := make([]*rpctrace.Trace, 3)
	for i := range traces {
		traces[i] = recorder.Start(context.Background(), rpctrace.SideServer, 21)
		if traces[i] == nil {
			t.Fatal("trace fixture admission")
		}
		defer traces[i].Finish(rpctrace.ResultStopped)
	}
	owner := context.Background()
	local := &runtimeLocalDurability{runtime: &Runtime{ctx: owner}, store: store, timeout: 5 * time.Second}
	items := make([]localDurabilityItem, 2)
	completed := make(chan durabilityCompletion, 2)
	for i, key := range []ch.ChannelKey{"1:cohort-owner-A", "1:cohort-owner-B"} {
		q := testReplicateRequest(t, key, string(key), 1, []byte("durable-cohort"))
		observation := &cohortOwnerObserver{traces: []*rpctrace.Trace{traces[0], traces[1], traces[0]}}
		if i == 1 {
			observation = &cohortOwnerObserver{traces: []*rpctrace.Trace{traces[2]}}
		}
		items[i] = localDurabilityItem{proposal: durableProposal{first: q.Manifest.BaseOffset + 1, last: q.Manifest.LastOffset, channelKey: q.ChannelKey, channelID: q.ChannelID, leader: q.Leader, manifest: q.Manifest, records: q.Records}, submittedAt: time.Now(), observation: observation, complete: func(c durabilityCompletion) { completed <- c }}
	}
	if err := local.runBatch(owner, items); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got := <-completed; got.err != nil || got.outcome != ch.AppendOutcomeDurable {
			t.Fatalf("actual local durability=%+v", got)
		}
	}
	observer.mu.Lock()
	events := append([]messagedb.CommitCoordinatorBatchEvent(nil), observer.events...)
	observer.mu.Unlock()
	if len(events) != 1 || events[0].Requests != 1 || events[0].Records != 2 || !events[0].Native.Available {
		t.Fatalf("real DB batch=%+v", events)
	}
	var shared realCohortRef
	for i, trace := range traces {
		sample := trace.Finish(rpctrace.ResultOK)
		localSync := 0
		for _, span := range sample.Spans {
			if span.Stage == rpctrace.StageChannelLocalSync {
				localSync++
				if span.Incomplete || span.Result != rpctrace.ResultOK {
					t.Fatalf("localSync=%+v", span)
				}
			}
		}
		if localSync != 1 {
			t.Fatalf("owner%d localSync count=%d", i, localSync)
		}
		ref := realCohortSampleRef(t, sample)
		if i == 0 {
			shared = ref
		} else if ref != shared {
			t.Fatalf("owner%d ref=%+v differs from actual shared cohort %+v", i, ref, shared)
		}
	}
}
