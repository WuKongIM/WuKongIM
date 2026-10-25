package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

type recoveryCallReader struct {
	page func(context.Context, []presence.RecoveryCall, int) (presence.OwnerRouteCallPage, error)
}

func (r recoveryCallReader) ReadOwnerRouteCalls(ctx context.Context, calls []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
	return r.page(ctx, calls, start)
}
func (r recoveryCallReader) ReadOwnerRoutes(context.Context, presence.RouteTarget, []string) (presence.OwnerRouteSnapshot, error) {
	return presence.OwnerRouteSnapshot{}, errors.New("unexpected legacy read")
}

type recoveryCallNode struct {
	*recoveryCluster
	controller func(context.Context) (control.Snapshot, error)
}

func (n *recoveryCallNode) LocalControllerSnapshot(ctx context.Context) (control.Snapshot, error) {
	if n.controller != nil {
		return n.controller(ctx)
	}
	return n.recoveryCluster.LocalControllerSnapshot(ctx)
}
func recoveryCallsFixture(owners ...uint64) (*recoveryCallNode, []presence.RecoveryCall) {
	target := presence.RouteTarget{HashSlot: 101, SlotID: 5, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}
	node := &recoveryCallNode{recoveryCluster: &recoveryCluster{fakePresenceCluster: &fakePresenceCluster{nodeID: 1, route: clusterpkg.Route{HashSlot: 101, SlotID: 5, Leader: 1, LeaderTerm: 4, ConfigEpoch: 1}}}}
	for _, id := range owners {
		node.snapshot.Nodes = append(node.snapshot.Nodes, control.Node{NodeID: id, JoinState: control.NodeJoinStateActive, Health: control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthFresh}})
	}
	return node, []presence.RecoveryCall{{Groups: []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"one"}}}}, {Groups: []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"two"}}}}}
}
func recoveryCallsPage(owner, boot uint64, calls []presence.RecoveryCall, start, next, count int) presence.OwnerRouteCallPage {
	page := presence.OwnerRouteCallPage{Start: start, Next: next}
	for _, call := range calls[start:next] {
		groups := make([]presence.OwnerRouteResult, len(call.Groups))
		for i, group := range call.Groups {
			groups[i].Snapshot = presence.OwnerRouteSnapshot{OwnerNodeID: owner, OwnerBootID: boot}
			for j := 0; j < count; j++ {
				groups[i].Snapshot.Routes = append(groups[i].Snapshot.Routes, presence.Route{UID: group.UIDs[0], OwnerNodeID: owner, OwnerBootID: boot, SessionID: uint64(j + 1)})
			}
		}
		page.Results = append(page.Results, groups)
	}
	return page
}
func requireRecoveryCallErrors(t *testing.T, got [][]presence.EndpointLookupResult) {
	t.Helper()
	for _, call := range got {
		for _, group := range call {
			if group.Err == nil || len(group.Routes) != 0 {
				t.Fatalf("invalid proof accepted: err=%v routes=%d", group.Err, len(group.Routes))
			}
		}
	}
}

func TestRecoverRouteCallsKeepsIndependent4096Budgets(t *testing.T) {
	node, calls := recoveryCallsFixture(1)
	var starts []int
	reader := recoveryCallReader{page: func(ctx context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
		starts = append(starts, start)
		return recoveryCallsPage(1, 11, c, start, start+1, 3000), nil
	}}
	got := NewPresenceRecoveryOwners(node, reader).RecoverRouteCalls(context.Background(), calls)
	if !reflect.DeepEqual(starts, []int{0, 1}) {
		t.Fatalf("continuations=%v", starts)
	}
	for i := range calls {
		if got[i][0].Err != nil || len(got[i][0].Routes) != 3000 {
			t.Fatalf("call %d: err=%v count=%d", i, got[i][0].Err, len(got[i][0].Routes))
		}
	}
}

func TestRecoverRouteCallsOverflowAcrossOwnersIsCallScoped(t *testing.T) {
	node, calls := recoveryCallsFixture(1, 2)
	local := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
		p := recoveryCallsPage(1, 11, c, start, len(c), 1)
		p.Results[0] = recoveryCallsPage(1, 11, c, 0, 1, 2500).Results[0]
		return p, nil
	}}
	remote := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
		p := recoveryCallsPage(2, 22, c, start, len(c), 1)
		p.Results[0] = recoveryCallsPage(2, 22, c, 0, 1, 2000).Results[0]
		return p, nil
	}}
	node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: remote})}}
	got := NewPresenceRecoveryOwners(node, local).RecoverRouteCalls(context.Background(), calls)
	requireRecoveryCallErrors(t, got[:1])
	if got[1][0].Err != nil || len(got[1][0].Routes) != 2 {
		t.Fatalf("legal sibling rejected: %v/%d", got[1][0].Err, len(got[1][0].Routes))
	}
}

func TestRecoverRouteCallsRejectsMalformedPagesAndIdentities(t *testing.T) {
	mutations := map[string]func(*presence.OwnerRouteCallPage){
		"wrong_start":   func(p *presence.OwnerRouteCallPage) { p.Start++ },
		"no_progress":   func(p *presence.OwnerRouteCallPage) { p.Next = p.Start },
		"beyond_end":    func(p *presence.OwnerRouteCallPage) { p.Next = 3 },
		"missing_call":  func(p *presence.OwnerRouteCallPage) { p.Results = nil },
		"split_call":    func(p *presence.OwnerRouteCallPage) { p.Results[0] = nil },
		"page_overflow": func(p *presence.OwnerRouteCallPage) { p.Results[0][0].Snapshot.Routes = make([]presence.Route, 4097) },
		"wrong_owner":   func(p *presence.OwnerRouteCallPage) { p.Results[0][0].Snapshot.OwnerNodeID = 2 },
		"zero_boot":     func(p *presence.OwnerRouteCallPage) { p.Results[0][0].Snapshot.OwnerBootID = 0 },
		"foreign_uid":   func(p *presence.OwnerRouteCallPage) { p.Results[0][0].Snapshot.Routes[0].UID = "foreign" },
		"route_owner":   func(p *presence.OwnerRouteCallPage) { p.Results[0][0].Snapshot.Routes[0].OwnerNodeID = 2 },
		"route_boot":    func(p *presence.OwnerRouteCallPage) { p.Results[0][0].Snapshot.Routes[0].OwnerBootID = 12 },
		"zero_session":  func(p *presence.OwnerRouteCallPage) { p.Results[0][0].Snapshot.Routes[0].SessionID = 0 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			node, calls := recoveryCallsFixture(1)
			reader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				p := recoveryCallsPage(1, 11, c, start, len(c), 1)
				mutate(&p)
				return p, nil
			}}
			got := NewPresenceRecoveryOwners(node, reader).RecoverRouteCalls(context.Background(), calls)
			// A group identity failure need not poison a sibling call, but never grants
			// successful proof to the malformed original group.
			requireRecoveryCallErrors(t, got[:1])
		})
	}
}

func TestRecoverRouteCallsRejectsBootChangeAcrossPages(t *testing.T) {
	node, calls := recoveryCallsFixture(1)
	reader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
		return recoveryCallsPage(1, uint64(11+start), c, start, start+1, 1), nil
	}}
	requireRecoveryCallErrors(t, NewPresenceRecoveryOwners(node, reader).RecoverRouteCalls(context.Background(), calls))
}

func TestRecoverRouteCallsPreservesGroupErrorAndRechecksFences(t *testing.T) {
	for _, mode := range []string{"group_error", "initial_target", "final_target", "membership"} {
		t.Run(mode, func(t *testing.T) {
			node, calls := recoveryCallsFixture(1)
			if mode == "initial_target" {
				calls[0].Groups[0].Target.LeaderTerm++
			}
			snapshots := 0
			node.controller = func(context.Context) (control.Snapshot, error) {
				snapshots++
				s := node.snapshot
				if mode == "membership" && snapshots == 2 {
					s.Nodes = nil
				}
				return s, nil
			}
			reader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				p := recoveryCallsPage(1, 11, c, start, len(c), 1)
				if mode == "group_error" {
					p.Results[0][0] = presence.OwnerRouteResult{Err: authority.ErrNotLeader}
				}
				if mode == "final_target" {
					node.route.LeaderTerm++
				}
				return p, nil
			}}
			got := NewPresenceRecoveryOwners(node, reader).RecoverRouteCalls(context.Background(), calls)
			if mode == "membership" || mode == "final_target" {
				requireRecoveryCallErrors(t, got)
				return
			}
			if !errors.Is(got[0][0].Err, authority.ErrNotLeader) || got[1][0].Err != nil || len(got[1][0].Routes) != 1 {
				t.Fatalf("aligned rejection lost: %v / %v", got[0][0].Err, got[1][0].Err)
			}
		})
	}
}

type recoveryCallsRPC struct {
	handle func(context.Context, []byte) ([]byte, error)
}

func (h recoveryCallsRPC) HandleRPC(ctx context.Context, p []byte) ([]byte, error) {
	return h.handle(ctx, p)
}

func TestRecoverRouteCallsUsesOnlyFirstUnsupportedFallback(t *testing.T) {
	for _, mode := range []string{"unsupported", "busy", "timeout", "generic", "malformed", "late_unsupported"} {
		t.Run(mode, func(t *testing.T) {
			node, calls := recoveryCallsFixture(2)
			// One singleton and one multi-target original call require old op9 and op10.
			calls[1].Groups = append(calls[1].Groups, presence.EndpointLookupGroup{Target: calls[1].Groups[0].Target, UIDs: []string{"third"}})
			old := recoveryBatchReaderFunc(func(_ context.Context, g []presence.EndpointLookupGroup) []presence.OwnerRouteResult {
				return recoveryCallsPage(2, 22, []presence.RecoveryCall{{Groups: g}}, 0, 1, 1).Results[0]
			})
			newReader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				return recoveryCallsPage(2, 22, c, start, start+1, 1), nil
			}}
			oldHandler := presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: old})}
			newHandler := presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: newReader})}
			var ops []byte
			node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: recoveryCallsRPC{handle: func(ctx context.Context, p []byte) ([]byte, error) {
				if len(p) < 6 {
					return nil, errors.New("short request")
				}
				ops = append(ops, p[5])
				if p[5] != 11 {
					return oldHandler.HandleRPC(ctx, p)
				}
				switch mode {
				case "unsupported":
					return nil, transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: "internal/access/node: unknown presence op id 11"}
				case "busy":
					return nil, transport.ErrBusy
				case "timeout":
					return nil, transport.ErrTimeout
				case "generic":
					return nil, errors.New("internal/access/node: unknown presence op id 11")
				case "malformed":
					return []byte("invalid page"), nil
				case "late_unsupported":
					if len(ops) == 1 {
						return newHandler.HandleRPC(ctx, p)
					}
					return nil, transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: "internal/access/node: unknown presence op id 11"}
				}
				panic("unreachable")
			}}}
			local := recoveryReaderFunc(func(context.Context, presence.RouteTarget, []string) (presence.OwnerRouteSnapshot, error) {
				t.Fatal("local node not in membership")
				return presence.OwnerRouteSnapshot{}, nil
			})
			got := NewPresenceRecoveryOwners(node, local).RecoverRouteCalls(context.Background(), calls)
			if mode == "unsupported" {
				if !reflect.DeepEqual(ops, []byte{11, 9, 10}) {
					t.Fatalf("fallback operations=%v", ops)
				}
				for _, call := range got {
					for _, group := range call {
						if group.Err != nil || len(group.Routes) != 1 {
							t.Fatal("fallback lost original call proof")
						}
					}
				}
			} else {
				requireRecoveryCallErrors(t, got)
				want := 1
				if mode == "late_unsupported" {
					want = 2
				}
				if len(ops) != want {
					t.Fatalf("unexpected retry/fallback=%v", ops)
				}
			}
		})
	}
}

func TestRecoverRouteCallsLegacyLocalReaderAndHealthyOwners(t *testing.T) {
	node, calls := recoveryCallsFixture(1, 2, 3)
	oldCalls := 0
	old := recoveryBatchReaderFunc(func(_ context.Context, g []presence.EndpointLookupGroup) []presence.OwnerRouteResult {
		oldCalls++
		return recoveryCallsPage(1, 11, []presence.RecoveryCall{{Groups: g}}, 0, 1, 1).Results[0]
	})
	node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{}
	for _, id := range []uint64{2, 3} {
		owner := id
		reader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
			return recoveryCallsPage(owner, owner*11, c, start, len(c), 1), nil
		}}
		node.rpcByNode[id] = presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: reader})}
	}
	got := NewPresenceRecoveryOwners(node, old).RecoverRouteCalls(context.Background(), calls)
	if oldCalls != 2 || len(node.calls) != 2 {
		t.Fatalf("owner reads local=%d remote=%d", oldCalls, len(node.calls))
	}
	for _, call := range got {
		if call[0].Err != nil || len(call[0].Routes) != 3 {
			t.Fatal("healthy owner omitted")
		}
	}
}

func TestRecoverRouteCallsBoundsRejectBeforeReads(t *testing.T) {
	for _, mode := range []string{"calls", "groups", "uids", "no_uids"} {
		t.Run(mode, func(t *testing.T) {
			node, calls := recoveryCallsFixture(1)
			switch mode {
			case "calls":
				for len(calls) < 17 {
					calls = append(calls, calls[0])
				}
			case "groups":
				calls = calls[:1]
				for len(calls[0].Groups) < 257 {
					calls[0].Groups = append(calls[0].Groups, calls[0].Groups[0])
				}
			case "uids":
				calls = calls[:1]
				calls[0].Groups[0].UIDs = make([]string, 513)
			case "no_uids":
				calls = calls[:1]
				calls[0].Groups[0].UIDs = nil
			}
			reads := 0
			reader := recoveryCallReader{page: func(context.Context, []presence.RecoveryCall, int) (presence.OwnerRouteCallPage, error) {
				reads++
				return presence.OwnerRouteCallPage{}, nil
			}}
			requireRecoveryCallErrors(t, NewPresenceRecoveryOwners(node, reader).RecoverRouteCalls(context.Background(), calls))
			if reads != 0 {
				t.Fatalf("invalid request made %d owner reads", reads)
			}
		})
	}
}

func TestRecoverRouteCallsOwnerFailureDoesNotImplyEmptyProof(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			node, calls := recoveryCallsFixture(1, 2)
			if stale {
				node.snapshot.Nodes[1].Health.Status = control.NodeDown
				node.snapshot.Nodes[1].Health.Freshness = control.NodeHealthStale
			}
			local := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				return recoveryCallsPage(1, 11, c, start, len(c), 1), nil
			}}
			node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: recoveryCallsRPC{handle: func(context.Context, []byte) ([]byte, error) { return nil, transport.ErrTimeout }}}
			got := NewPresenceRecoveryOwners(node, local).RecoverRouteCalls(context.Background(), calls)
			if !stale {
				requireRecoveryCallErrors(t, got)
			} else {
				for _, call := range got {
					if call[0].Err != nil || len(call[0].Routes) != 1 {
						t.Fatal("stale unavailable owner blocked valid proof")
					}
				}
			}
		})
	}
}

func TestRecoverRouteCallsRequiresBoundedMembership(t *testing.T) {
	for _, mode := range []string{"empty", "too_many", "read_error"} {
		t.Run(mode, func(t *testing.T) {
			node, calls := recoveryCallsFixture(1)
			if mode == "empty" {
				node.snapshot.Nodes = nil
			}
			if mode == "too_many" {
				for id := uint64(2); id <= 257; id++ {
					node.snapshot.Nodes = append(node.snapshot.Nodes, control.Node{NodeID: id, JoinState: control.NodeJoinStateActive})
				}
			}
			if mode == "read_error" {
				node.controller = func(context.Context) (control.Snapshot, error) {
					return control.Snapshot{}, errors.New("unavailable controller")
				}
			}
			reads := 0
			reader := recoveryCallReader{page: func(context.Context, []presence.RecoveryCall, int) (presence.OwnerRouteCallPage, error) {
				reads++
				return presence.OwnerRouteCallPage{}, nil
			}}
			requireRecoveryCallErrors(t, NewPresenceRecoveryOwners(node, reader).RecoverRouteCalls(context.Background(), calls))
			if reads != 0 {
				t.Fatalf("unproven membership read owners=%d", reads)
			}
		})
	}
}

func TestRecoverRouteCallsLaterUnavailableKeepsCompletedCallProof(t *testing.T) {
	for _, failure := range []error{transport.ErrBusy, transport.ErrTimeout, context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			node, calls := recoveryCallsFixture(1, 2, 3)
			local := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				return recoveryCallsPage(1, 11, c, start, len(c), 1), nil
			}}
			var operations []uint64
			node.rpcByNode = make(map[uint64]clusterpkg.NodeRPCHandler)
			for _, owner := range []uint64{2, 3} {
				owner := owner
				reader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
					return recoveryCallsPage(owner, owner*11, c, start, start+1, 1), nil
				}}
				handler := presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: reader})}
				reads := 0
				node.rpcByNode[owner] = recoveryCallsRPC{handle: func(ctx context.Context, p []byte) ([]byte, error) {
					operations = append(operations, owner)
					reads++
					if owner == 2 && reads == 2 {
						return nil, failure
					}
					return handler.HandleRPC(ctx, p)
				}}
			}
			got := NewPresenceRecoveryOwners(node, local).RecoverRouteCalls(context.Background(), calls)
			if got[0][0].Err != nil || len(got[0][0].Routes) != 3 {
				t.Fatalf("completed proof lost or healthy owner omitted: %v/%d", got[0][0].Err, len(got[0][0].Routes))
			}
			requireRecoveryCallErrors(t, got[1:])
			if !reflect.DeepEqual(operations, []uint64{2, 2, 3, 3}) {
				t.Fatalf("retry, fallback, or omitted owner: %v", operations)
			}
		})
	}
}
