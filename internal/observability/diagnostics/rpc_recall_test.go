package diagnostics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

func TestRPCStageRecallRetainsSlowAndFailedAcrossFastTraffic(t *testing.T) {
	for _, stage := range []Stage{StageRPCClient, StageRPCServer} {
		t.Run(string(stage), func(t *testing.T) {
			store := NewStore(StoreOptions{Capacity: 1024})
			base := time.Unix(1_700_000_000, 0)
			store.Record(rpcRecallEvent("slow", stage, base, 5*time.Second, ResultOK, 21))
			store.Record(rpcRecallEvent("failed", stage, base.Add(time.Millisecond), time.Millisecond, ResultTimeout, 86))
			for i := 0; i < 300; i++ {
				store.Record(rpcRecallEvent(fmt.Sprintf("fast-%d", i), stage, base.Add(time.Duration(i+2)*time.Millisecond), time.Microsecond, ResultOK, 14))
			}
			got := store.Query(context.Background(), Query{Stage: stage, Limit: 500})
			assertRPCRecallContains(t, got, "slow", "failed", "fast-299")
			if len(got.Events) > 256 {
				t.Fatalf("default stage recall retained %d IDs, maximum is 256", len(got.Events))
			}
		})
	}
}

func TestRPCStageRecallCustomBoundsAndSmallRecency(t *testing.T) {
	for _, capacity := range []int{1, 2, 3, 4, 5, 7, 8, 256} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			store := NewStore(StoreOptions{Capacity: 2048, MaxEventsPerKey: capacity})
			base := time.Unix(1_700_000_000, 0)
			for i := 0; i < 600; i++ {
				result := ResultOK
				if i%3 == 0 {
					result = ResultCanceled
				}
				store.Record(rpcRecallEvent(fmt.Sprintf("rpc-%d", i), StageRPCClient, base.Add(time.Duration(i)*time.Millisecond), time.Duration(i%17)*time.Second, result, 21))
			}
			got := store.Query(context.Background(), Query{Stage: StageRPCClient, Limit: 500})
			if len(got.Events) > capacity {
				t.Fatalf("custom per-key bound %d retained %d events", capacity, len(got.Events))
			}
			assertRPCRecallContains(t, got, "rpc-599")
			if capacity < 4 {
				if len(got.Events) != capacity {
					t.Fatalf("small recent-only store returned %d, want %d", len(got.Events), capacity)
				}
				for i, event := range got.Events {
					if want := fmt.Sprintf("rpc-%d", 600-capacity+i); event.TraceID != want {
						t.Fatalf("small recent-only event %d = %q, want %q", i, event.TraceID, want)
					}
				}
			}
			seen := make(map[string]bool)
			for _, event := range got.Events {
				if seen[event.TraceID] {
					t.Fatalf("recall duplicated %q", event.TraceID)
				}
				seen[event.TraceID] = true
			}
		})
	}
}

func TestRPCStageRecallTiesSidesAndQueryFilters(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 128, MaxEventsPerKey: 8})
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 3; i++ {
		store.Record(rpcRecallEvent(fmt.Sprintf("slow-%d", i), StageRPCClient, base.Add(time.Duration(i)*time.Second), time.Second, ResultOK, 21))
		store.Record(rpcRecallEvent(fmt.Sprintf("error-%d", i), StageRPCClient, base.Add(time.Duration(i+3)*time.Second), time.Microsecond, ResultTimeout, 86))
	}
	for i := 0; i < 20; i++ {
		store.Record(rpcRecallEvent(fmt.Sprintf("fast-%d", i), StageRPCClient, base.Add(time.Duration(i+10)*time.Second), time.Nanosecond, ResultOK, 14))
		store.Record(rpcRecallEvent(fmt.Sprintf("server-%d", i), StageRPCServer, base.Add(time.Duration(i+10)*time.Second), time.Hour, ResultError, 14))
	}
	got := store.Query(context.Background(), Query{Stage: StageRPCClient, Limit: 500})
	assertRPCRecallContains(t, got, "slow-1", "slow-2", "error-1", "error-2", "fast-19")
	for _, event := range got.Events {
		if event.TraceID == "slow-0" || event.TraceID == "error-0" || event.Stage != StageRPCClient {
			t.Fatalf("tie replacement or side isolation retained unexpected event: %q %q", event.TraceID, event.Stage)
		}
	}
	filtered := store.Query(context.Background(), Query{Stage: StageRPCClient, Result: ResultTimeout,
		Start: base.Add(4 * time.Second), End: base.Add(5 * time.Second), Limit: 1})
	if len(filtered.Events) != 1 || filtered.Events[0].TraceID != "error-2" {
		t.Fatalf("time/result filters and latest limit returned %#v, want only error-2", filtered.Events)
	}
	combined := store.Query(context.Background(), Query{Stage: StageRPCClient, TraceID: "slow-1", Limit: 1})
	if len(combined.Events) != 1 || combined.Events[0].TraceID != "slow-1" {
		t.Fatalf("exact selector and stage returned %#v, want only slow-1", combined.Events)
	}
}

func TestRPCStageRecallGlobalOverwriteReclaimsPriority(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 12, MaxEventsPerKey: 8})
	base := time.Unix(1_700_000_000, 0)
	store.Record(rpcRecallEvent("expired-longest", StageRPCClient, base, 100*time.Second, ResultOK, 21))
	store.Record(rpcRecallEvent("expired-second", StageRPCClient, base.Add(time.Second), 90*time.Second, ResultOK, 21))
	for i := 1; i <= 12; i++ {
		store.Record(rpcRecallEvent(fmt.Sprintf("first-%d", i), StageRPCClient, base.Add(time.Duration(i+1)*time.Second), time.Microsecond, ResultOK, 14))
	}
	if got := store.Query(context.Background(), Query{TraceID: "expired-longest"}); len(got.Events) != 0 {
		t.Fatal("priority recall pinned an event after its global ring slot was overwritten")
	}
	store.Record(rpcRecallEvent("new-slow", StageRPCClient, base.Add(14*time.Second), 10*time.Second, ResultOK, 86))
	for i := 15; i <= 20; i++ {
		store.Record(rpcRecallEvent(fmt.Sprintf("later-%d", i), StageRPCClient, base.Add(time.Duration(i)*time.Second), time.Microsecond, ResultOK, 14))
	}
	got := store.Query(context.Background(), Query{Stage: StageRPCClient, Limit: 500})
	assertRPCRecallContains(t, got, "new-slow", "later-20")
	for _, event := range got.Events {
		if event.TraceID == "expired-longest" {
			t.Fatal("stage recall resurrected an overwritten global event")
		}
	}
	if all := store.Query(context.Background(), Query{Limit: 500}); len(all.Events) != 12 {
		t.Fatalf("global ring retained %d events, want its unchanged capacity 12", len(all.Events))
	}
}

func TestRPCStageRecallLeavesOtherStagesRecentOnly(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 32, MaxEventsPerKey: 4})
	base := time.Unix(1_700_000_000, 0)
	store.Record(Event{TraceID: "old-slow", Stage: "message.send_durable", At: base, Duration: time.Hour, Result: ResultTimeout})
	for i := 0; i < 8; i++ {
		store.Record(Event{TraceID: fmt.Sprintf("ordinary-%d", i), Stage: "message.send_durable", At: base.Add(time.Duration(i+1) * time.Second)})
	}
	got := store.Query(context.Background(), Query{Stage: "message.send_durable", Limit: 500})
	if len(got.Events) != 4 || got.Events[0].TraceID != "ordinary-4" || got.Events[3].TraceID != "ordinary-7" {
		t.Fatalf("non-RPC stage recency changed: %#v", got.Events)
	}
}

func TestForwardStageRecallRetainsOnlyValidReferencesAcrossFastTraffic(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 1024})
	base := time.Unix(1_700_000_000, 0)
	store.Record(forwardRecallEvent("slow-bound", base, 5*time.Second, ResultOK))
	store.Record(forwardRecallEvent("failed-bound", base.Add(time.Millisecond), time.Millisecond, ResultTimeout))
	missing := forwardRecallEvent("explicit-missing", base.Add(2*time.Millisecond), time.Millisecond, ResultError)
	missing.RPCReference = &sendtrace.RPCReference{State: "remote_untraced"}
	store.Record(missing)
	unknown := forwardRecallEvent("nil-reference", base.Add(3*time.Millisecond), time.Hour, ResultError)
	unknown.RPCReference = nil
	store.Record(unknown)
	unknown.TraceID = "malformed-reference"
	unknown.RPCReference = &sendtrace.RPCReference{State: "bound", TraceID: "not-a-recorder-identity"}
	store.Record(unknown)
	for i := 0; i < 300; i++ {
		store.Record(forwardRecallEvent(fmt.Sprintf("forward-%d", i), base.Add(time.Duration(i+10)*time.Millisecond), time.Microsecond, ResultOK))
	}
	got := store.Query(context.Background(), Query{Stage: "channel.append.forward", Limit: 500})
	assertRPCRecallContains(t, got, "slow-bound", "failed-bound", "explicit-missing", "forward-299")
	if len(got.Events) > 256 {
		t.Fatalf("forward stage returned %d candidates, maximum is the existing 256 IDs", len(got.Events))
	}
	for _, event := range got.Events {
		if event.TraceID == "nil-reference" || event.TraceID == "malformed-reference" {
			t.Fatalf("unbound unknown event acquired priority recall: %q", event.TraceID)
		}
		if event.TraceID == "explicit-missing" && (event.RPCReference == nil || event.RPCReference.State != "remote_untraced" || event.RPCReference.TraceID != "") {
			t.Fatal("explicit missing reference was promoted to a binding")
		}
	}
}

func TestForwardStageRecallCustomBoundsAndGlobalOwnership(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	for _, limit := range []int{1, 3, 4, 8, 256} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			store := NewStore(StoreOptions{Capacity: 1024, MaxEventsPerKey: limit})
			for i := 0; i < 600; i++ {
				result := ResultOK
				if i%3 == 0 {
					result = ResultTimeout
				}
				store.Record(forwardRecallEvent(fmt.Sprintf("forward-%d", i), base.Add(time.Duration(i)*time.Second), time.Duration(i%17)*time.Second, result))
			}
			got := store.Query(context.Background(), Query{Stage: "channel.append.forward", Limit: 500})
			assertRPCRecallContains(t, got, "forward-599")
			if len(got.Events) > limit {
				t.Fatalf("forward lookup exceeded custom hardN=%d: %d events", limit, len(got.Events))
			}
			if limit < 4 && (len(got.Events) != limit || got.Events[0].TraceID != fmt.Sprintf("forward-%d", 600-limit)) {
				t.Fatalf("tiny forward index no longer retains the exact latest %d events", limit)
			}
		})
	}
	store := NewStore(StoreOptions{Capacity: 12, MaxEventsPerKey: 8})
	store.Record(forwardRecallEvent("expired-first", base, 100*time.Second, ResultError))
	store.Record(forwardRecallEvent("expired-second", base.Add(time.Second), 90*time.Second, ResultError))
	for i := 0; i < 12; i++ {
		store.Record(forwardRecallEvent(fmt.Sprintf("first-%d", i), base.Add(time.Duration(i+2)*time.Second), time.Microsecond, ResultOK))
	}
	store.Record(forwardRecallEvent("replacement", base.Add(14*time.Second), 10*time.Second, ResultOK))
	for i := 0; i < 6; i++ {
		store.Record(forwardRecallEvent(fmt.Sprintf("later-%d", i), base.Add(time.Duration(i+15)*time.Second), time.Microsecond, ResultOK))
	}
	got := store.Query(context.Background(), Query{Stage: "channel.append.forward", Limit: 500})
	assertRPCRecallContains(t, got, "replacement", "later-5")
	for _, event := range got.Events {
		if event.TraceID == "expired-first" || event.TraceID == "expired-second" {
			t.Fatal("forward recall resurrected globally overwritten evidence")
		}
	}
	if all := store.Query(context.Background(), Query{Limit: 500}); len(all.Events) != 12 {
		t.Fatalf("forward recall changed global ring capacity: %d", len(all.Events))
	}
}

func forwardRecallEvent(id string, at time.Time, duration time.Duration, result Result) Event {
	return Event{TraceID: id, Stage: "channel.append.forward", At: at, Duration: duration, Result: result,
		RPCReference: &sendtrace.RPCReference{State: "bound", TraceID: "0123456789abcdef0123456789abcdef/1"}}
}

func rpcRecallEvent(id string, stage Stage, at time.Time, duration time.Duration, result Result, service uint16) Event {
	side := rpctrace.SideClient
	if stage == StageRPCServer {
		side = rpctrace.SideServer
	}
	return Event{TraceID: id, Stage: stage, At: at, Duration: duration, Result: result,
		RPC: &rpctrace.Sample{TraceID: id, Side: side, ServiceID: service, At: at, Duration: duration, Result: rpctrace.Result(result)}}
}

func assertRPCRecallContains(t *testing.T, got QueryResult, want ...string) {
	t.Helper()
	seen := make(map[string]bool)
	for _, event := range got.Events {
		seen[event.TraceID] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("stage recall lost retained RPC %q", id)
		}
	}
}
