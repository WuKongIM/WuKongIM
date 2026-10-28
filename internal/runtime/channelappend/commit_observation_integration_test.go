//go:build integration

package channelappend

import (
	"context"
	"encoding/json"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func commitBridgeCoverage(t *testing.T, trace *rpctrace.Trace) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(trace.Finish(rpctrace.ResultOK))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cohorts json.RawMessage `json:"commit_cohorts"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cohorts
}
func commitBridgeRef(t *testing.T, trace *rpctrace.Trace) string {
	t.Helper()
	b := commitBridgeCoverage(t, trace)
	var coverage struct {
		Links []struct {
			Ref struct {
				Instance string `json:"instance"`
				Attempt  uint64 `json:"attempt"`
			} `json:"cohort_ref"`
		} `json:"links"`
		Dropped uint64 `json:"dropped"`
	}
	if err := json.Unmarshal(b, &coverage); err != nil {
		t.Fatal(err)
	}
	if len(coverage.Links) != 1 || coverage.Dropped != 0 || coverage.Links[0].Ref.Instance == "" || coverage.Links[0].Ref.Attempt == 0 {
		t.Fatalf("missing actual owner cohort: %s", b)
	}
	ref, err := json.Marshal(coverage.Links[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	return string(ref)
}
func commitBridgeAdapter(t *testing.T, channel string) Appender {
	t.Helper()
	factory := channelstore.NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	return bridgeAppender(func(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
		if ctx.Err() != nil || rpctrace.From(ctx) != nil {
			t.Fatal("shared append inherited caller cancellation or a single Trace")
		}
		var observation rpctrace.CommitObservation
		if phase := rpctrace.AppendPhaseObserverFrom(ctx); phase != nil {
			port, ok := phase.(interface {
				CommitObservation() rpctrace.CommitObservation
			})
			if !ok {
				t.Fatal("first-effect observer omitted narrow commit owner port")
			}
			observation = port.CommitObservation()
		}
		records := make([]ch.Record, len(req.Messages))
		for i, msg := range req.Messages {
			records[i] = ch.Record{ID: msg.MessageID, Epoch: 3, FromUID: msg.FromUID, ClientMsgNo: msg.ClientMsgNo, Payload: msg.Payload, SizeBytes: len(msg.Payload), ServerTimestampMS: msg.ServerTimestampMS}
		}
		manifest, _, ok := ch.SealProposalManifest(ch.ProposalManifest{Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7, CommandID: ch.CommandID{31: 1}, LastOffset: uint64(len(records))}, records)
		if !ok {
			t.Fatal("invalid test proposal")
		}
		result := adapter.Sync(context.Background(), []replication.Mutation{{ChannelKey: ch.ChannelKey("1:" + channel), ChannelID: ch.ChannelID{ID: channel, Type: 1}, Manifest: manifest, Records: records, CommitObservation: observation}})
		if len(result) != 1 || result[0].Err != nil || result[0].Outcome != ch.AppendOutcomeDurable {
			t.Fatalf("actual physical bridge commit=%+v", result)
		}
		out := AppendBatchResult{Items: make([]AppendBatchItemResult, len(req.Messages))}
		for i, msg := range req.Messages {
			out.Items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: uint64(i + 1)}
		}
		return out, nil
	})
}
func commitBridgeAssignIDs(items []preparedSend) {
	for i := range items {
		items[i].Command.MessageID = uint64(61101 + i)
		items[i].ServerTimestampMS = 1700000000000
	}
}

func TestCommitObservationFirstEffectProjectionKeepsAllOriginalOwners(t *testing.T) {
	a, b, inactive, later := bridgeTrace(t), bridgeTrace(t), bridgeTrace(t), bridgeTrace(t)
	items := []preparedSend{bridgeItem(t, a, "same"), bridgeItem(t, b, "same"), bridgeItem(t, inactive, "inactive"), bridgeItem(t, later, "later"), bridgeItem(t, nil, "unsampled")}
	commitBridgeAssignIDs(items)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	items[2].Context = canceled
	items[3].future.rpcTrace.selectFirstAppend(9)
	result := bridgeRun(10, items, commitBridgeAdapter(t, "first-effect-cohort"), nil)
	if len(result.items) != len(items) {
		t.Fatalf("aligned business completions=%d", len(result.items))
	}
	if ra, rb := commitBridgeRef(t, a), commitBridgeRef(t, b); ra != rb {
		t.Fatalf("coalesced original owners lost shared physical ref: %s/%s", ra, rb)
	}
	for _, trace := range []*rpctrace.Trace{inactive, later} {
		if coverage := commitBridgeCoverage(t, trace); len(coverage) != 0 && string(coverage) != "null" {
			t.Fatalf("inactive/later owner acquired cohort: %s", coverage)
		}
	}
}

func TestCommitObservationRetryProjectionOmitsRecoveredAndInactiveOwners(t *testing.T) {
	recovered, retryA, retryB, omitted := bridgeTrace(t), bridgeTrace(t), bridgeTrace(t), bridgeTrace(t)
	items := []preparedSend{bridgeItem(t, recovered, "hit"), bridgeItem(t, retryA, "miss"), bridgeItem(t, retryB, "miss"), bridgeItem(t, omitted, "omitted")}
	commitBridgeAssignIDs(items)
	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	items[3].Context = cancelCtx
	idempotency := &sequencedIdempotencyForAppendTest{results: []idempotencyResultForAppendTest{{ok: true, result: SendResult{Reason: ReasonSuccess}}, {ok: false}, {ok: false}}}
	actual := commitBridgeAdapter(t, "retry-owner-cohort")
	calls := 0
	bridgeRun(5, items, bridgeAppender(func(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
		calls++
		if calls == 1 {
			cancel()
			return AppendBatchResult{}, ErrAppendFailed
		}
		if len(req.Messages) != 1 || req.Messages[0].ClientMsgNo != "miss" {
			t.Fatalf("retry rows=%+v", req.Messages)
		}
		return actual.AppendBatch(ctx, req)
	}), idempotency)
	if calls != 2 {
		t.Fatalf("initial/retry count=%d", calls)
	}
	if ra, rb := commitBridgeRef(t, retryA), commitBridgeRef(t, retryB); ra != rb {
		t.Fatalf("retry duplicate owners not projected together: %s/%s", ra, rb)
	}
	for _, trace := range []*rpctrace.Trace{recovered, omitted} {
		if coverage := commitBridgeCoverage(t, trace); len(coverage) != 0 && string(coverage) != "null" {
			t.Fatalf("recovered/omitted owner inherited retry cohort: %s", coverage)
		}
	}
}
