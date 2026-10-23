package node_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	node "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

type ownerCallsWire func(context.Context, uint64, uint8, []byte) ([]byte, error)

func (f ownerCallsWire) CallRPC(ctx context.Context, owner uint64, service uint8, body []byte) ([]byte, error) {
	return f(ctx, owner, service, body)
}

func ownerCall(uid string) presence.RecoveryCall {
	return presence.RecoveryCall{Groups: []presence.EndpointLookupGroup{{
		Target: presence.RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 4}, UIDs: []string{uid},
	}}}
}

func ownerCallsAdapter(t *testing.T, counts ...int) (*node.Adapter, []presence.RecoveryCall) {
	t.Helper()
	registry := online.NewRegistry(online.RegistryOptions{})
	calls := make([]presence.RecoveryCall, len(counts))
	var session uint64
	for i, count := range counts {
		uid := fmt.Sprintf("caller-%d", i)
		calls[i] = ownerCall(uid)
		for range count {
			session++
			route := online.OwnerRoute{UID: uid, HashSlot: 1, OwnerNodeID: 2, OwnerBootID: 22, SessionID: session, OwnerSeq: 1}
			if err := registry.RegisterPending(online.LocalSession{Route: route}); err != nil {
				t.Fatal(err)
			}
			if err := registry.MarkActive(session); err != nil {
				t.Fatal(err)
			}
		}
	}
	owner := presence.NewOwnerRecovery(registry, 2, 22, func(target presence.RouteTarget) error {
		if target.LeaderTerm != 4 {
			return authority.ErrNotLeader
		}
		return nil
	})
	return node.New(node.Options{OwnerRoutes: owner}), calls
}

func TestOwnerRouteCallsRPCPagesPreserveEachCallerAndOldFrames(t *testing.T) {
	if node.PresenceOwnerRPCServiceID != 14 {
		t.Fatal("owner service ID changed")
	}
	adapter, calls := ownerCallsAdapter(t, 4096, 1)
	var requests, replies [][]byte
	client := node.NewClient(ownerCallsWire(func(ctx context.Context, owner uint64, service uint8, body []byte) ([]byte, error) {
		if owner != 2 || service != node.PresenceOwnerRPCServiceID {
			t.Fatalf("destination owner=%d service=%d", owner, service)
		}
		requests = append(requests, bytes.Clone(body))
		reply, err := adapter.HandlePresenceOwnerRPC(ctx, body)
		replies = append(replies, bytes.Clone(reply))
		return reply, err
	}))
	page, err := client.ReadOwnerRouteCalls(context.Background(), 2, calls, 0)
	if err != nil || page.Start != 0 || page.Next != 1 || len(page.Results) != 1 || len(page.Results[0]) != 1 || len(page.Results[0][0].Snapshot.Routes) != 4096 {
		t.Fatalf("first page start=%d next=%d calls=%d err=%v", page.Start, page.Next, len(page.Results), err)
	}
	if len(requests) != 1 || !bytes.Equal(requests[0], ownerCallsRequest(calls, 0)) || !bytes.HasPrefix(replies[0], []byte{'W', 'K', 'V', 'O', 3}) {
		t.Fatal("single page call changed request bytes, response version or performed hidden pagination")
	}
	page, err = client.ReadOwnerRouteCalls(context.Background(), 2, calls, 1)
	if err != nil || page.Start != 1 || page.Next != 2 || len(page.Results) != 1 || len(page.Results[0]) != 1 || len(page.Results[0][0].Snapshot.Routes) != 1 || page.Results[0][0].Snapshot.Routes[0].UID != "caller-1" {
		t.Fatalf("second page=%+v err=%v", page, err)
	}
	if len(requests) != 2 {
		t.Fatal("client did not leave continuation ownership with the caller")
	}
	// Existing op9 and op10 request and empty response bytes remain exact literals.
	oldGroup := []byte{1, 0, 0, 0, 0, 0, 0, 0, 1, 1, 'u'}
	for _, op := range []byte{9, 10} {
		body := append([]byte{'W', 'K', 'V', 'P', 2, op}, oldGroup...)
		reply, err := adapter.HandlePresenceOwnerRPC(context.Background(), body)
		if err != nil {
			t.Fatal(err)
		}
		want := []byte{'W', 'K', 'V', 'O', 1, 10, 'n', 'o', 't', '_', 'l', 'e', 'a', 'd', 'e', 'r', 0, 0, 0}
		if op == 10 {
			want = append([]byte{'W', 'K', 'V', 'O', 2, 1}, want[5:]...)
		}
		if !bytes.Equal(reply, want) {
			t.Fatalf("operation %d existing response bytes changed: %v", op, reply)
		}
	}
}

func TestOwnerRouteCallsRPCPreservesGroupsAndIndependentErrors(t *testing.T) {
	adapter, calls := ownerCallsAdapter(t, 1, 1)
	stale := calls[0].Groups[0]
	stale.Target.LeaderTerm--
	calls[0].Groups = append(calls[0].Groups, stale)
	client := node.NewClient(ownerCallsWire(func(ctx context.Context, _ uint64, _ uint8, body []byte) ([]byte, error) {
		return adapter.HandlePresenceOwnerRPC(ctx, body)
	}))
	page, err := client.ReadOwnerRouteCalls(context.Background(), 2, calls, 0)
	if err != nil || page.Next != 2 || len(page.Results) != 2 || len(page.Results[0]) != 2 || len(page.Results[1]) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if page.Results[0][0].Err != nil || !errors.Is(page.Results[0][1].Err, authority.ErrNotLeader) || page.Results[1][0].Err != nil || page.Results[1][0].Snapshot.OwnerNodeID != 2 || page.Results[1][0].Snapshot.OwnerBootID != 22 {
		t.Fatalf("group errors or generation lost: %+v", page.Results)
	}
}

func TestOwnerRouteCallsRPCAcceptsExactSharedInputBounds(t *testing.T) {
	adapter, calls := ownerCallsAdapter(t, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	for i := range calls {
		calls[i].Groups = make([]presence.EndpointLookupGroup, 16)
		for j := range calls[i].Groups {
			calls[i].Groups[j] = ownerCall("u").Groups[0]
			calls[i].Groups[j].UIDs = []string{"u", "v"}
		}
	}
	client := node.NewClient(ownerCallsWire(func(ctx context.Context, _ uint64, _ uint8, body []byte) ([]byte, error) {
		return adapter.HandlePresenceOwnerRPC(ctx, body)
	}))
	page, err := client.ReadOwnerRouteCalls(context.Background(), 2, calls, 0)
	if err != nil || page.Next != 16 || len(page.Results) != 16 {
		t.Fatalf("legal 16 calls/256 groups/512 UIDs rejected: page=%+v err=%v", page, err)
	}
	for _, results := range page.Results {
		if len(results) != 16 {
			t.Fatal("group boundaries changed")
		}
	}
}

func TestOwnerRouteCallsRPCRejectsRequestBudgetsBeforeTransportOrMutation(t *testing.T) {
	valid := []presence.RecoveryCall{ownerCall("u")}
	tests := []struct {
		name  string
		calls []presence.RecoveryCall
		start int
	}{
		{"empty", nil, 0}, {"too many callers", make([]presence.RecoveryCall, 17), 0},
		{"empty caller", []presence.RecoveryCall{{}}, 0}, {"negative start", valid, -1}, {"past end", valid, 1},
		{"groups", []presence.RecoveryCall{{Groups: make([]presence.EndpointLookupGroup, 257)}}, 0},
		{"uids", []presence.RecoveryCall{{Groups: []presence.EndpointLookupGroup{{UIDs: make([]string, 513)}}}}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			client := node.NewClient(ownerCallsWire(func(context.Context, uint64, uint8, []byte) ([]byte, error) { called = true; return nil, nil }))
			if _, err := client.ReadOwnerRouteCalls(context.Background(), 2, tc.calls, tc.start); err == nil || called {
				t.Fatalf("invalid request reached transport: err=%v called=%v", err, called)
			}
		})
	}
	adapter, _ := ownerCallsAdapter(t, 0)
	request := ownerCallsRequest(valid, 0)
	for i := range len(request) {
		if _, err := adapter.HandlePresenceOwnerRPC(context.Background(), request[:i]); err == nil {
			t.Fatalf("truncated request accepted at %d", i)
		}
	}
	for _, body := range [][]byte{append(bytes.Clone(request), 0), {'W', 'K', 'V', 'P', 2, 11, 0, 17}, {'W', 'K', 'V', 'P', 2, 11, 0, 1, 0x81, 2}} {
		if _, err := adapter.HandlePresenceOwnerRPC(context.Background(), body); err == nil {
			t.Fatal("malformed owner call envelope accepted")
		}
	}
	owner := &ownerCallsMutationCounter{}
	mutationAdapter := node.New(node.Options{Owner: owner})
	if _, err := mutationAdapter.HandlePresenceOwnerRPC(context.Background(), request[:6]); err == nil || owner.calls != 0 {
		t.Fatal("malformed read dispatched a mutation")
	}
}

func TestOwnerRouteCallsRPCSinglePageRejectsMalformedReplies(t *testing.T) {
	calls := []presence.RecoveryCall{ownerCall("u"), ownerCall("v")}
	valid := ownerCallsReply(0, 1, [][]presence.OwnerRouteResult{{{Snapshot: presence.OwnerRouteSnapshot{OwnerNodeID: 2, OwnerBootID: 22}}}})
	bad := map[string][]byte{
		"wrong version":           append([]byte{'W', 'K', 'V', 'O', 2}, valid[5:]...),
		"wrong start":             ownerCallsReply(1, 2, [][]presence.OwnerRouteResult{{{}}}),
		"no progress":             ownerCallsReply(0, 0, nil),
		"beyond input":            ownerCallsReply(0, 3, [][]presence.OwnerRouteResult{{{}}, {{}}, {{}}}),
		"caller cardinality":      ownerCallsReply(0, 2, [][]presence.OwnerRouteResult{{{}}}),
		"group cardinality":       ownerCallsReply(0, 1, [][]presence.OwnerRouteResult{{{}, {}}}),
		"trailing":                append(bytes.Clone(valid), 0),
		"unknown status":          append([]byte{'W', 'K', 'V', 'O', 3, 0, 1, 1, 1, 1, 'x'}, 0, 0, 0),
		"output budget":           ownerCallsReply(0, 1, [][]presence.OwnerRouteResult{{{Snapshot: presence.OwnerRouteSnapshot{Routes: make([]presence.Route, 4097)}}}}),
		"aggregate output budget": ownerCallsReply(0, 2, [][]presence.OwnerRouteResult{{{Snapshot: presence.OwnerRouteSnapshot{Routes: make([]presence.Route, 2048)}}}, {{Snapshot: presence.OwnerRouteSnapshot{Routes: make([]presence.Route, 2049)}}}}),
	}
	for i := range len(valid) {
		bad[fmt.Sprintf("truncated %d", i)] = valid[:i]
	}
	for name, reply := range bad {
		t.Run(name, func(t *testing.T) {
			attempts := 0
			client := node.NewClient(ownerCallsWire(func(context.Context, uint64, uint8, []byte) ([]byte, error) { attempts++; return reply, nil }))
			if _, err := client.ReadOwnerRouteCalls(context.Background(), 2, calls, 0); err == nil || attempts != 1 {
				t.Fatalf("malformed reply err=%v attempts=%d", err, attempts)
			}
		})
	}
}

func TestOwnerRouteCallsRPCRejectsInvalidProviderPages(t *testing.T) {
	calls := []presence.RecoveryCall{ownerCall("u")}
	for _, page := range []presence.OwnerRouteCallPage{
		{Start: 0, Next: 0}, {Start: 1, Next: 2, Results: [][]presence.OwnerRouteResult{{{}}}},
		{Start: 0, Next: 1, Results: [][]presence.OwnerRouteResult{{{}, {}}}},
		{Start: 0, Next: 1, Results: [][]presence.OwnerRouteResult{{{Snapshot: presence.OwnerRouteSnapshot{Routes: make([]presence.Route, 4097)}}}}},
	} {
		adapter := node.New(node.Options{OwnerRoutes: ownerCallsInvalidPageSource{page: page}})
		if _, err := adapter.HandlePresenceOwnerRPC(context.Background(), ownerCallsRequest(calls, 0)); err == nil {
			t.Fatal("invalid provider page escaped its RPC boundary")
		}
	}
}

type ownerCallsInvalidPageSource struct{ page presence.OwnerRouteCallPage }

func (s ownerCallsInvalidPageSource) ReadOwnerRoutes(context.Context, presence.RouteTarget, []string) (presence.OwnerRouteSnapshot, error) {
	return presence.OwnerRouteSnapshot{}, nil
}
func (s ownerCallsInvalidPageSource) ReadOwnerRouteCalls(context.Context, []presence.RecoveryCall, int) (presence.OwnerRouteCallPage, error) {
	return s.page, nil
}

func TestOwnerRouteCallsRPCUnsupportedIsExactTypedAndNeverRetries(t *testing.T) {
	message := "internal/access/node: unknown presence op id 11"
	for _, tc := range []struct {
		name        string
		err         error
		unsupported bool
	}{
		{"explicit", transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: message}, true},
		{"wrapped", fmt.Errorf("outer: %w", transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: message}), true},
		{"pointer", &transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: message}, true},
		{"local text", errors.New(message), false},
		{"busy", transport.RemoteError{Code: transport.RemoteErrorCodeBusy, Message: message}, false},
		{"wrong op", transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: "internal/access/node: unknown presence op id 10"}, false},
		{"timeout", context.DeadlineExceeded, false}, {"cancel", context.Canceled, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			client := node.NewClient(ownerCallsWire(func(context.Context, uint64, uint8, []byte) ([]byte, error) { attempts++; return nil, tc.err }))
			_, err := client.ReadOwnerRouteCalls(context.Background(), 2, []presence.RecoveryCall{ownerCall("u")}, 0)
			if err == nil || errors.Is(err, presence.ErrOwnerRouteCallsUnsupported) != tc.unsupported || attempts != 1 {
				t.Fatalf("err=%v attempts=%d unsupported=%v", err, attempts, tc.unsupported)
			}
			if !tc.unsupported && !errors.Is(err, tc.err) {
				t.Fatal("original transport failure lost")
			}
		})
	}
}

func TestPresenceRPCReadOnlyClassifiersUseOnlyVerifiedVersionedHeader(t *testing.T) {
	for op := byte(0); op < 16; op++ {
		body := []byte{'W', 'K', 'V', 'P', 2, op}
		wantAuthority := op == 5 || op == 8
		wantOwner := op == 9 || op == 10 || op == 11
		if node.PresenceAuthorityReadOnly(body) != wantAuthority || node.PresenceOwnerReadOnly(body) != wantOwner {
			t.Fatalf("operation %d classification", op)
		}
		if node.PresenceOwnerReadOnly(append(bytes.Clone(body), 255)) != wantOwner {
			t.Fatal("classifier must leave body validation to handler")
		}
	}
	for _, valid := range [][]byte{{'W', 'K', 'V', 'P', 2, 5}, {'W', 'K', 'V', 'P', 2, 11}} {
		for i := range len(valid) {
			if node.PresenceAuthorityReadOnly(valid[:i]) || node.PresenceOwnerReadOnly(valid[:i]) {
				t.Fatal("short header classified read-only")
			}
		}
		for _, version := range []byte{0, 1, 3, 255} {
			bad := bytes.Clone(valid)
			bad[4] = version
			if node.PresenceAuthorityReadOnly(bad) || node.PresenceOwnerReadOnly(bad) {
				t.Fatal("unknown version classified read-only")
			}
		}
		bad := bytes.Clone(valid)
		bad[0] = 'X'
		if node.PresenceAuthorityReadOnly(bad) || node.PresenceOwnerReadOnly(bad) {
			t.Fatal("bad magic classified read-only")
		}
	}
}

type ownerCallsMutationCounter struct{ calls int }

func (m *ownerCallsMutationCounter) ApplyRouteAction(context.Context, presence.RouteAction) error {
	m.calls++
	return nil
}

func ownerCallsRequest(calls []presence.RecoveryCall, start int) []byte {
	b := []byte{'W', 'K', 'V', 'P', 2, 11}
	b = binary.AppendUvarint(b, uint64(start))
	b = binary.AppendUvarint(b, uint64(len(calls)))
	for _, call := range calls {
		b = binary.AppendUvarint(b, uint64(len(call.Groups)))
		for _, g := range call.Groups {
			for _, v := range []uint64{uint64(g.Target.HashSlot), uint64(g.Target.SlotID), g.Target.LeaderNodeID, g.Target.LeaderTerm, g.Target.ConfigEpoch, g.Target.RouteRevision, g.Target.AuthorityEpoch} {
				b = binary.AppendUvarint(b, v)
			}
			b = binary.AppendUvarint(b, uint64(len(g.UIDs)))
			for _, uid := range g.UIDs {
				b = wireString(b, uid)
			}
		}
	}
	return b
}
func wireString(b []byte, s string) []byte {
	return append(binary.AppendUvarint(b, uint64(len(s))), s...)
}
func ownerCallsReply(start, next int, results [][]presence.OwnerRouteResult) []byte {
	b := []byte{'W', 'K', 'V', 'O', 3}
	b = binary.AppendUvarint(b, uint64(start))
	b = binary.AppendUvarint(b, uint64(next))
	b = binary.AppendUvarint(b, uint64(len(results)))
	for _, call := range results {
		b = binary.AppendUvarint(b, uint64(len(call)))
		for _, r := range call {
			b = wireString(b, "ok")
			b = binary.AppendUvarint(b, r.Snapshot.OwnerNodeID)
			b = binary.AppendUvarint(b, r.Snapshot.OwnerBootID)
			b = binary.AppendUvarint(b, uint64(len(r.Snapshot.Routes)))
			for _, route := range r.Snapshot.Routes {
				b = wireString(b, route.UID)
				for _, v := range []uint64{route.OwnerNodeID, route.OwnerBootID, route.OwnerSeq, route.SessionID} {
					b = binary.AppendUvarint(b, v)
				}
				b = wireString(b, route.DeviceID)
				b = append(b, route.DeviceFlag, route.DeviceLevel)
				b = wireString(b, route.Listener)
				b = binary.AppendVarint(b, route.ConnectedUnix)
				b = binary.AppendVarint(b, route.LastSeenUnix)
			}
		}
	}
	return b
}
