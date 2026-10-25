package node_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"unsafe"

	node "github.com/WuKongIM/WuKongIM/internal/access/node"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

type boundedOwnerClient interface {
	ReadOwnerRouteCallsBounded(context.Context, uint64, []presence.RecoveryCall, int, *presence.RecoveryResultBudget) (presence.OwnedOwnerRouteCallPage, error)
	ReadOwnerRoutesByTargetsBounded(context.Context, uint64, []presence.EndpointLookupGroup, *presence.RecoveryResultBudget) (presence.OwnedOwnerRouteCallPage, error)
}

func requireBoundedOwnerClient(t *testing.T, wire ownerCallsWire) boundedOwnerClient {
	t.Helper()
	client, ok := any(node.NewClient(wire)).(boundedOwnerClient)
	if !ok {
		t.Fatal("public owner Client cannot reserve decoded recovery ownership before allocation")
	}
	return client
}

func ownedReply(version byte, groups int, routes []presence.Route) []byte {
	results := make([]presence.OwnerRouteResult, groups)
	results[0].Snapshot = presence.OwnerRouteSnapshot{OwnerNodeID: 2, OwnerBootID: 22, Routes: routes}
	for i := 1; i < groups; i++ {
		results[i].Snapshot = presence.OwnerRouteSnapshot{OwnerNodeID: 2, OwnerBootID: 22}
	}
	v3 := ownerCallsReply(0, 1, [][]presence.OwnerRouteResult{results})
	if version == 3 {
		return v3
	}
	if version == 2 {
		return append([]byte{'W', 'K', 'V', 'O', 2}, v3[8:]...)
	}
	return append([]byte{'W', 'K', 'V', 'O', 1}, v3[9:]...)
}

func ownedRead(ctx context.Context, client boundedOwnerClient, version byte, groups int, budget *presence.RecoveryResultBudget) (presence.OwnedOwnerRouteCallPage, error) {
	call := ownerCall("u")
	for len(call.Groups) < groups {
		call.Groups = append(call.Groups, ownerCall("v").Groups[0])
	}
	if version == 3 {
		return client.ReadOwnerRouteCallsBounded(ctx, 2, []presence.RecoveryCall{call}, 0, budget)
	}
	return client.ReadOwnerRoutesByTargetsBounded(ctx, 2, call.Groups, budget)
}

func TestOwnedOwnerRPCReservesExactDecodedBackingAndReleases(t *testing.T) {
	for _, version := range []byte{1, 2, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			groups := 1
			if version == 2 {
				groups = 2
			}
			route := presence.Route{UID: "user-123", OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 4, SessionID: 8, DeviceID: "device", Listener: "tcp", ConnectedUnix: -1, LastSeenUnix: 8}
			reply := ownedReply(version, groups, []presence.Route{route})
			client := requireBoundedOwnerClient(t, func(_ context.Context, owner uint64, service uint8, body []byte) ([]byte, error) {
				wantOp := byte(11)
				if version < 3 {
					wantOp = version + 8
				}
				if owner != 2 || service != node.PresenceOwnerRPCServiceID || body[5] != wantOp {
					t.Fatalf("changed existing destination/op: %d %d %v", owner, service, body)
				}
				return reply, nil
			})
			// One outer slice, aligned groups, one route and 17 string bytes.
			payload := uint64(unsafe.Sizeof([]presence.OwnerRouteResult{})) + uint64(groups)*uint64(unsafe.Sizeof(presence.OwnerRouteResult{})) + uint64(unsafe.Sizeof(presence.Route{})) + 17
			budget := presence.NewRecoveryResultBudget(1, payload+uint64(unsafe.Sizeof(presence.RecoveryResultLease{})))
			owned, err := ownedRead(context.Background(), client, version, groups, budget)
			if err != nil || owned.Lease == nil || owned.Page.Start != 0 || owned.Page.Next != 1 || len(owned.Page.Results) != 1 || len(owned.Page.Results[0]) != groups {
				t.Fatalf("owned page=%+v err=%v", owned, err)
			}
			stats := budget.Stats()
			if stats.Rows != 1 || stats.Bytes != payload+uint64(unsafe.Sizeof(presence.RecoveryResultLease{})) {
				t.Fatalf("wrong exact ownership: %+v payload=%d", stats, payload)
			}
			clear(reply)
			got := owned.Page.Results[0][0].Snapshot.Routes[0]
			if got != route {
				t.Fatalf("decoded route aliases wire or changed: %+v", got)
			}
			owned.Release()
			owned.Release()
			if stats := budget.Stats(); stats.Rows != 0 || stats.Bytes != 0 || len(owned.Page.Results) != 0 || owned.Lease != nil {
				t.Fatalf("released page still owns storage: %+v %+v", stats, owned)
			}
		})
	}
}

func TestOwnedOwnerRPCDenialDoesNotDecodeLargeReply(t *testing.T) {
	for _, version := range []byte{1, 2, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			groups := 1
			if version == 2 {
				groups = 2
			}
			routes := make([]presence.Route, 4096)
			for i := range routes {
				routes[i] = presence.Route{UID: "not-a-short-uid", DeviceID: "phone", Listener: "tcp"}
			}
			large := ownedReply(version, groups, routes)
			small := ownedReply(version, groups, routes[:1])
			reply := small
			client := requireBoundedOwnerClient(t, func(context.Context, uint64, uint8, []byte) ([]byte, error) { return reply, nil })
			for _, limits := range [][2]uint64{{0, 1 << 20}, {4096, 1}} {
				budget := presence.NewRecoveryResultBudget(limits[0], limits[1])
				measure := func() {
					owned, err := ownedRead(context.Background(), client, version, groups, budget)
					if !errors.Is(err, presence.ErrRecoveryResultBudget) || owned.Lease != nil || len(owned.Page.Results) != 0 {
						t.Fatalf("denial allocated result: %+v %v", owned, err)
					}
				}
				reply = small
				smallAllocs := testing.AllocsPerRun(3, measure)
				reply = large
				largeAllocs := testing.AllocsPerRun(3, measure)
				if largeAllocs > smallAllocs+1 {
					t.Fatalf("denied decode allocations scale with routes: small=%v large=%v", smallAllocs, largeAllocs)
				}
				if stats := budget.Stats(); stats.Rows != 0 || stats.Bytes != 0 || stats.HighWaterRows != 0 || stats.HighWaterBytes != 0 {
					t.Fatalf("denial took credits: %+v", stats)
				}
			}
		})
	}
}

func TestOwnedOwnerRPCRejectsMalformedBeforeReservation(t *testing.T) {
	for _, version := range []byte{1, 2, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			groups := 1
			if version == 2 {
				groups = 2
			}
			valid := ownedReply(version, groups, []presence.Route{{UID: "u"}})
			bad := [][]byte{append(bytes.Clone(valid), 0), ownedReply(version, groups, make([]presence.Route, 4097))}
			for i := range len(valid) {
				bad = append(bad, valid[:i])
			}
			statusOffset := 5
			if version == 2 {
				statusOffset = 6
			}
			if version == 3 {
				statusOffset = 9
			}
			huge := append(bytes.Clone(valid[:statusOffset]), binary.AppendUvarint(nil, ^uint64(0))...)
			bad = append(bad, huge)
			for i, reply := range bad {
				client := requireBoundedOwnerClient(t, func(context.Context, uint64, uint8, []byte) ([]byte, error) { return reply, nil })
				budget := presence.NewRecoveryResultBudget(4096, 1<<20)
				owned, err := ownedRead(context.Background(), client, version, groups, budget)
				if err == nil || owned.Lease != nil || len(owned.Page.Results) != 0 {
					t.Fatalf("case %d returned malformed proof", i)
				}
				if stats := budget.Stats(); stats.Rows != 0 || stats.Bytes != 0 || stats.HighWaterBytes != 0 {
					t.Fatalf("case %d reserved before validating: %+v", i, stats)
				}
			}
		})
	}
}

func TestOwnedOwnerRPCCancellationAndUnsupportedNeverReplay(t *testing.T) {
	for _, version := range []byte{1, 2, 3} {
		groups := 1
		if version == 2 {
			groups = 2
		}
		ctx, cancel := context.WithCancel(context.Background())
		client := requireBoundedOwnerClient(t, func(context.Context, uint64, uint8, []byte) ([]byte, error) {
			cancel()
			return ownedReply(version, groups, nil), nil
		})
		budget := presence.NewRecoveryResultBudget(10, 1<<20)
		owned, err := ownedRead(ctx, client, version, groups, budget)
		if !errors.Is(err, context.Canceled) || owned.Lease != nil || budget.Stats().HighWaterBytes != 0 {
			t.Fatalf("version %d ignored cancellation: %+v %v", version, owned, err)
		}
	}
	for _, remoteErr := range []error{transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: "internal/access/node: unknown presence op id 11"}, errors.New("internal/access/node: unknown presence op id 11"), context.DeadlineExceeded, transport.ErrBusy} {
		attempts := 0
		client := requireBoundedOwnerClient(t, func(context.Context, uint64, uint8, []byte) ([]byte, error) { attempts++; return nil, remoteErr })
		owned, err := ownedRead(context.Background(), client, 3, 1, presence.NewRecoveryResultBudget(10, 1<<20))
		_, typed := remoteErr.(transport.RemoteError)
		if attempts != 1 || owned.Lease != nil || err == nil || errors.Is(err, presence.ErrOwnerRouteCallsUnsupported) != typed {
			t.Fatalf("hidden fallback or wrong classification: attempts=%d err=%v", attempts, err)
		}
	}
}

func TestOwnedOwnerRPCLegacyErrorSemantics(t *testing.T) {
	for _, version := range []byte{1, 2, 3} {
		groups := 1
		if version == 2 {
			groups = 2
		}
		reply := ownedReply(version, groups, nil)
		offset := 5
		if version == 2 {
			offset = 6
		}
		if version == 3 {
			offset = 9
		}
		reply = append(append(bytes.Clone(reply[:offset]), wireString(nil, "not_leader")...), reply[offset+3:]...)
		client := requireBoundedOwnerClient(t, func(context.Context, uint64, uint8, []byte) ([]byte, error) { return reply, nil })
		budget := presence.NewRecoveryResultBudget(10, 1<<20)
		owned, err := ownedRead(context.Background(), client, version, groups, budget)
		if version == 1 {
			if !errors.Is(err, authority.ErrNotLeader) || owned.Lease != nil || budget.Stats().Bytes != 0 {
				t.Fatalf("v1 error changed: %+v %v", owned, err)
			}
		} else {
			if err != nil || !errors.Is(owned.Page.Results[0][0].Err, authority.ErrNotLeader) {
				t.Fatalf("independent group error changed: %+v %v", owned, err)
			}
			owned.Release()
		}
	}
}

func TestOwnedOwnerRPCContinuationSharesBudgetUntilEachPageRelease(t *testing.T) {
	calls := []presence.RecoveryCall{ownerCall("u"), ownerCall("v")}
	reads := 0
	client := requireBoundedOwnerClient(t, func(_ context.Context, _ uint64, _ uint8, body []byte) ([]byte, error) {
		reads++
		start := int(body[6])
		result := presence.OwnerRouteResult{Snapshot: presence.OwnerRouteSnapshot{OwnerNodeID: 2, OwnerBootID: 22, Routes: []presence.Route{{UID: calls[start].Groups[0].UIDs[0]}}}}
		return ownerCallsReply(start, start+1, [][]presence.OwnerRouteResult{{result}}), nil
	})
	budget := presence.NewRecoveryResultBudget(2, 1<<20)
	a, err := client.ReadOwnerRouteCallsBounded(context.Background(), 2, calls, 0, budget)
	if err != nil {
		t.Fatal(err)
	}
	b, err := client.ReadOwnerRouteCallsBounded(context.Background(), 2, calls, 1, budget)
	if err != nil || b.Page.Start != 1 || b.Page.Next != 2 || budget.Stats().Rows != 2 {
		t.Fatalf("continuation=%+v err=%v stats=%+v", b, err, budget.Stats())
	}
	denied, err := client.ReadOwnerRouteCallsBounded(context.Background(), 2, calls, 0, budget)
	if !errors.Is(err, presence.ErrRecoveryResultBudget) || denied.Lease != nil {
		t.Fatalf("retained pages lost ownership: %+v %v", denied, err)
	}
	a.Release()
	if budget.Stats().Rows != 1 || b.Page.Results[0][0].Snapshot.Routes[0].UID != "v" {
		t.Fatal("one page release invalidated another")
	}
	b.Release()
	if stats := budget.Stats(); stats.Bytes != 0 || stats.Rows != 0 || stats.HighWaterRows != 2 || reads != 3 {
		t.Fatalf("page leak or hidden retry: %+v reads=%d", stats, reads)
	}
}
