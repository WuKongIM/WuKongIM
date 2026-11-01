//go:build integration

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type permissionAttemptSink struct {
	mu      sync.Mutex
	enabled bool
	refs    map[sendtrace.AttemptToken][]sendtrace.RPCReference
	phases  map[sendtrace.AttemptToken][]sendtrace.AttemptPhase
}

type permissionTraceDiscovery string

func (d permissionTraceDiscovery) Resolve(transport.NodeID) (string, error) { return string(d), nil }

// Exercise the production permission wrapper and real TCP sampling together.
func TestPermissionAttemptPreservesActiveCaptureThroughRealTransport(t *testing.T) {
	sink := &permissionAttemptSink{enabled: true, refs: make(map[sendtrace.AttemptToken][]sendtrace.RPCReference)}
	restore := sendtrace.SetSink(sink)
	defer restore()
	server, err := transport.NewServer(transport.ServerConfig{RPCTracing: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	if err = server.Handle(uint16(sendPermissionRPCServiceID), func(context.Context, []byte) ([]byte, error) { return []byte(`{"format":1,"groups":[]}`), nil }, transport.ServiceOptions{Concurrency: 1, QueueSize: 8, MaxQueueBytes: 4096, Timeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	if err = server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	client, err := transport.NewClient(transport.ClientConfig{RPCTraceEvery: 3, RequestBudgets: true, Discovery: permissionTraceDiscovery(server.Addr())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Native position 0 is sampled; the next call requires the existing active request.
	if _, err = client.Call(ctx, 2, 0, transport.PriorityRPC, uint16(sendPermissionRPCServiceID), nil); err != nil {
		t.Fatal(err)
	}
	origin := newBoundaryCluster(1)
	origin.rpc = func(ctx context.Context, node multiraft.NodeID, raw []byte) ([]byte, error) {
		return client.Call(ctx, transport.NodeID(node), 0, transport.PriorityRPC, uint16(sendPermissionRPCServiceID), raw)
	}
	store := New(origin, boundaryDB(t))
	defer store.CloseSendPermissionReads()
	token := sendtrace.AttemptToken{Epoch: [16]byte{8}, Ordinal: 1}
	ctx = sendtrace.WithGatewayAttempt(ctx, token)
	ctx, active := rpctrace.WithTerminalCapture(ctx)
	query := sendPermissionRequest{Format: 1, Groups: []sendPermissionGroup{{Fence: SendPermissionFence{LeaderNodeID: 2}}}}
	var first rpctrace.TerminalIdentity
	for i := range 2 {
		if _, err = store.callSendPermission(ctx, query); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = active.Snapshot()
		}
	}
	sink.mu.Lock()
	refs := append([]sendtrace.RPCReference(nil), sink.refs[token]...)
	sink.mu.Unlock()
	if first.State != rpctrace.TerminalTraceBound || active.Snapshot() != first {
		t.Fatalf("active terminal was shadowed: first=%+v final=%+v", first, active.Snapshot())
	}
	if len(refs) != 2 || refs[0].State != "bound" || refs[1].State != "bound" || refs[0].TraceID == refs[1].TraceID || refs[0].TraceID != first.TraceID {
		t.Fatalf("physical envelope identities=%+v active=%+v", refs, first)
	}
	if origin.calls.Load() != 2 {
		t.Fatalf("business RPC calls=%d want2", origin.calls.Load())
	}
}

func (*permissionAttemptSink) RecordSendTrace(sendtrace.Event) {}
func (s *permissionAttemptSink) AttemptLinkEnabled() bool      { return s.enabled }
func (*permissionAttemptSink) BeginSendAttempt(sendtrace.AttemptKey, time.Time) sendtrace.AttemptToken {
	return sendtrace.AttemptToken{}
}
func (*permissionAttemptSink) BindAttemptTrace(sendtrace.AttemptToken, string)                 {}
func (*permissionAttemptSink) RecordAttemptRPC(sendtrace.AttemptToken, sendtrace.RPCReference) {}
func (s *permissionAttemptSink) RecordAttemptPhase(token sendtrace.AttemptToken, phase sendtrace.AttemptPhase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phases != nil {
		s.phases[token] = append(s.phases[token], phase)
	}
}
func (s *permissionAttemptSink) RecordAttemptPermissionRPC(t sendtrace.AttemptToken, r sendtrace.RPCReference) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refs[t] = append(s.refs[t], r)
}

// F6/F7/F10: production cohort, codec and DB; controlled cluster ports, not wire proof.
func TestPermissionAttemptRealSharedCohortKeepsOriginalOwners(t *testing.T) {
	var evidence []map[string]any
	t.Cleanup(func() {
		path := os.Getenv("WK_PERMISSION_ATTEMPT_REPORT")
		if path == "" {
			path = filepath.Join(t.TempDir(), "permission-attempts.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "source_revision": os.Getenv("WK_VALIDATION_SOURCE_REVISION"), "kind": "production proxy/cohort/codec/DB, controlled cluster ports", "cases": evidence}, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err = os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
			t.Error(err)
		}
	})
	for _, mode := range []string{"shared_success", "one_cancel", "one_deadline", "cancel_before_seal", "all_cancel", "close", "disabled", "local", "unsampled", "no_metric"} {
		t.Run(mode, func(t *testing.T) {
			sink := &permissionAttemptSink{enabled: mode != "disabled", refs: make(map[sendtrace.AttemptToken][]sendtrace.RPCReference), phases: make(map[sendtrace.AttemptToken][]sendtrace.AttemptPhase)}
			restore := sendtrace.SetSink(sink)
			defer restore()
			db := boundaryDB(t)
			owner := newBoundaryCluster(2)
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			owner.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
				select {
				case <-entered:
				default:
					close(entered)
				}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			serving := New(owner, db)
			defer serving.CloseSendPermissionReads()
			origin := newBoundaryCluster(1)
			recorder, err := rpctrace.NewRecorder(1)
			if err != nil {
				t.Fatal(err)
			}
			var parentTrace bool
			var captureSeen bool
			origin.rpc = func(ctx context.Context, _ multiraft.NodeID, p []byte) ([]byte, error) {
				parentTrace = rpctrace.From(ctx) != nil
				captureSeen = rpctrace.TerminalCaptureFrom(ctx) != nil
				var trace *rpctrace.Trace
				if mode != "unsampled" {
					trace = recorder.Start(ctx, rpctrace.SideClient, uint16(clusternet.RPCNodeSendPermissions))
					if trace != nil {
						trace.Bind([16]byte{7}, 1)
					}
				}
				body, err := serving.handleSendPermissionRPC(ctx, p)
				trace.Finish(rpctrace.ResultFor(err))
				return body, err
			}
			caller := New(origin, db)
			if mode == "local" {
				caller = serving
			}
			defer caller.CloseSendPermissionReads()
			if mode != "no_metric" {
				caller.SetSendPermissionObserver(&permissionEnvelopeMetric{})
			}
			caller.permissionCohortWindow = time.Hour
			tokens := [2]sendtrace.AttemptToken{{Epoch: [16]byte{9}, Ordinal: 1}, {Epoch: [16]byte{9}, Ordinal: 2}}
			a, cancelA := context.WithCancel(context.Background())
			if mode == "one_deadline" {
				cancelA()
				a, cancelA = context.WithTimeout(context.Background(), 500*time.Millisecond)
			}
			defer cancelA()
			b, cancelB := context.WithTimeout(context.Background(), time.Hour)
			defer cancelB()
			a = sendtrace.WithGatewayAttempt(a, tokens[0])
			b = sendtrace.WithGatewayAttempt(b, tokens[1])
			reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
			first := launchCohortRead(caller, a, reads)
			second := launchCohortRead(caller, b, reads)
			if mode == "cancel_before_seal" {
				until := time.Now().Add(time.Second)
				for {
					caller.permissionCohortMu.Lock()
					owned := caller.permissionCohortRequests
					caller.permissionCohortMu.Unlock()
					if owned == 2 {
						break
					}
					if time.Now().After(until) {
						t.Fatal("both original members not admitted before cancellation")
					}
					time.Sleep(time.Millisecond)
				}
				cancelA()
				if !errors.Is(awaitBoundaryResult(t, first)[0].Err, context.Canceled) {
					t.Error("caller cancellation changed")
				}
				sealTestCohort(t, caller, 2)
			} else {
				sealTestCohort(t, caller, 2)
			}
			awaitBoundaryEvent(t, entered)
			if mode == "one_cancel" {
				cancelA()
				if !errors.Is(awaitBoundaryResult(t, first)[0].Err, context.Canceled) {
					t.Error("caller cancellation changed")
				}
			}
			if mode == "one_deadline" {
				if !errors.Is(awaitBoundaryResult(t, first)[0].Err, context.DeadlineExceeded) {
					t.Error("one caller deadline changed")
				}
			}
			if mode == "all_cancel" {
				cancelA()
				cancelB()
			}
			if mode == "close" {
				caller.CloseSendPermissionReads()
			} else {
				unblock()
			}
			if mode != "one_cancel" && mode != "one_deadline" && mode != "cancel_before_seal" {
				r := awaitBoundaryResult(t, first)
				if mode == "all_cancel" || mode == "close" {
					if r[0].Err == nil {
						t.Error("cancellation/close became success")
					}
				} else if r[0].Err != nil {
					t.Error(r[0].Err)
				}
			}
			r := awaitBoundaryResult(t, second)
			if mode == "all_cancel" || mode == "close" {
				if r[0].Err == nil {
					t.Error("live owner failed to cancel/join")
				}
			} else if r[0].Err != nil {
				t.Error(r[0].Err)
			}
			requireCohortDrained(t, caller)
			sink.mu.Lock()
			counts := [2]int{len(sink.refs[tokens[0]]), len(sink.refs[tokens[1]])}
			phaseCounts := [2]int{len(sink.phases[tokens[0]]), len(sink.phases[tokens[1]])}
			var refs []sendtrace.RPCReference
			for _, x := range sink.refs {
				refs = append(refs, x...)
			}
			sink.mu.Unlock()
			expected := 1
			if mode == "disabled" || mode == "local" || mode == "unsampled" {
				expected = 0
			}
			if counts[1] != expected {
				t.Errorf("original2 refs=%d want%d", counts[1], expected)
			}
			if mode == "cancel_before_seal" {
				if counts[0] != 0 {
					t.Error("preseal canceled member fabricated shared RPC")
				}
			} else if counts[0] != expected {
				t.Errorf("original1 refs=%d want%d", counts[0], expected)
			}
			expectedPhase := 1
			if mode == "disabled" || mode == "local" || mode == "no_metric" {
				expectedPhase = 0
			}
			if phaseCounts[1] != expectedPhase || (mode == "cancel_before_seal" && phaseCounts[0] != 0) || (mode != "cancel_before_seal" && phaseCounts[0] != expectedPhase) {
				t.Errorf("original RPC clock counts=%v want live%d", phaseCounts, expectedPhase)
			}
			for _, ref := range refs {
				if !ref.Valid() || ref.State != "bound" || ref.TraceID != "07000000000000000000000000000000/1" {
					t.Errorf("foreign/unbound reference %+v", ref)
				}
			}
			if parentTrace {
				t.Error("shared execution borrowed one caller Trace")
			}
			if mode != "local" && captureSeen != (mode != "disabled") {
				t.Errorf("capture enablement %t", captureSeen)
			}
			calls := origin.calls.Load()
			if mode == "local" {
				if calls != 0 {
					t.Error("local path invented RPC")
				}
			} else if calls != 1 {
				t.Errorf("RPC count=%d want1", calls)
			}
			evidence = append(evidence, map[string]any{"case": mode, "reference_counts": counts, "origin_RPC_phase_counts": phaseCounts, "RPC_calls": calls, "shared_parent_trace": parentTrace, "capture_seen": captureSeen, "joined": true})
		})
	}
}
