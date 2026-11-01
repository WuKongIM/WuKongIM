//go:build integration

package proxy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

type permissionEnvelopeSink struct {
	permissionAttemptSink
	phases map[sendtrace.AttemptToken][]sendtrace.AttemptPhase
}

func (s *permissionEnvelopeSink) RecordAttemptPhase(token sendtrace.AttemptToken, phase sendtrace.AttemptPhase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phases[token] = append(s.phases[token], phase)
}

type permissionEnvelopeMetric struct {
	mu   sync.Mutex
	rpcs []time.Duration
}

func (*permissionEnvelopeMetric) ObserveSendPermissionCount(string, int) {}
func (*permissionEnvelopeMetric) ObserveSendPermissionInflight(int)      {}
func (m *permissionEnvelopeMetric) ObserveSendPermissionStage(stage, _ string, duration time.Duration) {
	if stage == "rpc" {
		m.mu.Lock()
		m.rpcs = append(m.rpcs, duration)
		m.mu.Unlock()
	}
}

// This exercises the existing public proxy/cohort, real TCP and production
// service91 handler/DB path. It verifies diagnostics, not the physical latency bug.
func TestPermissionEnvelopeClockReachesSharedOriginalsThroughTCP(t *testing.T) {
	sink := &permissionEnvelopeSink{permissionAttemptSink: permissionAttemptSink{enabled: true, refs: make(map[sendtrace.AttemptToken][]sendtrace.RPCReference)}, phases: make(map[sendtrace.AttemptToken][]sendtrace.AttemptPhase)}
	restore := sendtrace.SetSink(sink)
	defer restore()
	var metric permissionEnvelopeMetric
	var parentTrace atomic.Bool
	var phaseCounts [2]int
	var rpcCount int64
	t.Cleanup(func() {
		path := os.Getenv("WK_PERMISSION_ENVELOPE_REPORT")
		if path == "" {
			path = filepath.Join(t.TempDir(), "permission-envelope.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "kind": "production cohort/codec/DB and real TCP91; diagnostic evidence only", "phase_counts": phaseCounts, "business_RPC_calls": rpcCount, "borrowed_parent_trace": parentTrace.Load()}, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err = os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
			t.Error(err)
		}
	})
	db := boundaryDB(t)
	owner := newBoundaryCluster(2)
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	defer unblock()
	owner.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	serving := New(owner, db)
	defer serving.CloseSendPermissionReads()
	server, err := transport.NewServer(transport.ServerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	if err = server.Handle(uint16(sendPermissionRPCServiceID), serving.handleSendPermissionRPC, transport.ServiceOptions{Concurrency: 4, QueueSize: 8, MaxQueueBytes: 1 << 20, Timeout: 5 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if err = server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	client, err := transport.NewClient(transport.ClientConfig{RequestBudgets: true, Discovery: permissionTraceDiscovery(server.Addr())})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	origin := newBoundaryCluster(1)
	origin.rpc = func(ctx context.Context, node multiraft.NodeID, raw []byte) ([]byte, error) {
		if rpctrace.From(ctx) != nil {
			parentTrace.Store(true)
		}
		return client.Call(ctx, transport.NodeID(node), 0, transport.PriorityRPC, uint16(sendPermissionRPCServiceID), raw)
	}
	caller := New(origin, db)
	defer caller.CloseSendPermissionReads()
	caller.SetSendPermissionObserver(&metric)
	caller.permissionCohortWindow = time.Hour // Existing fixture seals two members explicitly.
	tokens := [2]sendtrace.AttemptToken{{Epoch: [16]byte{31}, Ordinal: 1}, {Epoch: [16]byte{31}, Ordinal: 2}}
	a, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	b, cancelB := context.WithTimeout(context.Background(), time.Hour)
	defer cancelB()
	reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
	first := launchCohortRead(caller, sendtrace.WithGatewayAttempt(a, tokens[0]), reads)
	second := launchCohortRead(caller, sendtrace.WithGatewayAttempt(b, tokens[1]), reads)
	sealTestCohort(t, caller, 2)
	awaitBoundaryEvent(t, entered)
	unblock()
	for _, done := range []<-chan []PermissionMetadataReadResult{first, second} {
		if result := awaitBoundaryResult(t, done); len(result) != 1 || result[0].Err != nil {
			t.Fatalf("ordinary fresh permission result changed: %+v", result)
		}
	}
	requireCohortDrained(t, caller)
	rpcCount = origin.calls.Load()
	metric.mu.Lock()
	durations := append([]time.Duration(nil), metric.rpcs...)
	metric.mu.Unlock()
	if rpcCount != 1 || len(durations) != 1 || durations[0] <= 0 || parentTrace.Load() {
		t.Fatalf("RPC/metric/independent owner changed: calls=%d durations=%v parent=%t", rpcCount, durations, parentTrace.Load())
	}
	for i, token := range tokens {
		sink.mu.Lock()
		phases := append([]sendtrace.AttemptPhase(nil), sink.phases[token]...)
		refs := append([]sendtrace.RPCReference(nil), sink.refs[token]...)
		sink.mu.Unlock()
		for _, phase := range phases {
			if phase.Stage == sendtrace.Stage("message.permission_rpc") {
				phaseCounts[i]++
				if phase.StartedAt.IsZero() || phase.Duration != durations[0] || phase.Result != sendtrace.ResultOK || phase.Boundary != "longest_origin_rpc91" {
					t.Errorf("original%d did not borrow the existing metric clock: %+v", i+1, phase)
				}
			}
		}
		if len(refs) != 1 || refs[0].State != "recorder_disabled" {
			t.Errorf("sampling0 was promoted or its unknown state lost: %+v", refs)
		}
		if phaseCounts[i] != 1 {
			t.Errorf("missing exact original%d service91 RPC interval: got%d want1", i+1, phaseCounts[i])
		}
	}
}
