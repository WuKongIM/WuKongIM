//go:build integration

package cluster

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// These integration variants reuse the existing independent-call proof fixtures
// through the controlled-allocation local registry and real remote wire codec.
// In particular, the deadline case expires the real shared owner context.
func boundedRecoveryProvider(t *testing.T, node PresenceRecoveryNode, local *presence.OwnerRecovery) presence.RecoveryCallBatchOwnersBounded {
	t.Helper()
	provider := NewPresenceRecoveryOwners(node, local)
	if !provider.SupportsBoundedRecoveryResults() {
		t.Fatal("fixture did not select the production bounded provider")
	}
	return provider
}

func requireRecoveryBudgetReleased(t *testing.T, budget *presence.RecoveryResultBudget) {
	t.Helper()
	if stats := budget.Stats(); stats.Rows != 0 || stats.Bytes != 0 {
		t.Fatalf("retained recovery references/credits: %+v", stats)
	}
}

func requireSameRecoveryDeadline(t *testing.T, deadlines []time.Time) {
	t.Helper()
	if len(deadlines) == 0 || deadlines[0].IsZero() {
		t.Fatal("owner reads did not retain an explicit deadline")
	}
	for _, deadline := range deadlines[1:] {
		if !deadline.Equal(deadlines[0]) {
			t.Fatal("a continuation or compatibility read reset the owner deadline")
		}
	}
}

func TestOwnedRecoveryIntegrationLaterUnavailableKeepsCompletedCallProof(t *testing.T) {
	for _, failure := range []string{"busy", "timeout", "deadline"} {
		t.Run(failure, func(t *testing.T) {
			node, calls := recoveryCallsFixture(1, 2, 3)
			local := ownedRecoveryReader(t, 1, calls)
			var operations []uint64
			deadlines := map[uint64][]time.Time{}
			node.rpcByNode = make(map[uint64]clusterpkg.NodeRPCHandler)
			for _, owner := range []uint64{2, 3} {
				reader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
					return recoveryCallsPage(owner, owner*11, c, start, start+1, 1), nil
				}}
				handler := presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: reader})}
				reads := 0
				node.rpcByNode[owner] = recoveryCallsRPC{handle: func(ctx context.Context, p []byte) ([]byte, error) {
					operations = append(operations, owner)
					deadline, _ := ctx.Deadline()
					deadlines[owner] = append(deadlines[owner], deadline)
					reads++
					if owner == 2 && reads == 2 {
						switch failure {
						case "busy":
							return nil, transport.ErrBusy
						case "timeout":
							return nil, transport.ErrTimeout
						default:
							<-ctx.Done()
							return nil, ctx.Err()
						}
					}
					return handler.HandleRPC(ctx, p)
				}}
			}
			budget := presence.NewRecoveryResultBudget(100, 1<<20)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			owned, err := boundedRecoveryProvider(t, node, local).RecoverRouteCallsBounded(ctx, calls, budget, nil)
			defer owned.Release()
			if err != nil || len(owned.Results) != 2 {
				t.Fatalf("completed-call proof unexpectedly aborted: %v", err)
			}
			if got := owned.Results[0][0]; got.Err != nil || len(got.Routes) != 3 {
				t.Fatalf("completed proof lost an owner: err=%v rows=%d", got.Err, len(got.Routes))
			}
			requireRecoveryCallErrors(t, owned.Results[1:])
			if !reflect.DeepEqual(operations, []uint64{2, 2, 3, 3}) {
				t.Fatalf("replay or omitted later owner: %v", operations)
			}
			for _, owner := range []uint64{2, 3} {
				requireSameRecoveryDeadline(t, deadlines[owner])
			}
			owned.Release()
			requireRecoveryBudgetReleased(t, budget)
		})
	}
}

func TestOwnedRecoveryIntegrationRejectsMalformedLaterPage(t *testing.T) {
	mutations := map[string]func(*presence.OwnerRouteCallPage){
		"boot_change": func(p *presence.OwnerRouteCallPage) {
			p.Results[0][0].Snapshot.OwnerBootID++
			p.Results[0][0].Snapshot.Routes[0].OwnerBootID++
		},
		"foreign_uid":  func(p *presence.OwnerRouteCallPage) { p.Results[0][0].Snapshot.Routes[0].UID = "foreign" },
		"zero_session": func(p *presence.OwnerRouteCallPage) { p.Results[0][0].Snapshot.Routes[0].SessionID = 0 },
		"error_with_routes": func(p *presence.OwnerRouteCallPage) {
			p.Results[0][0].Err = authority.ErrNotLeader
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			node, calls := recoveryCallsFixture(1, 2)
			local := ownedRecoveryReader(t, 1, calls)
			var starts []int
			reader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				starts = append(starts, start)
				page := recoveryCallsPage(2, 22, c, start, start+1, 1)
				if start == 1 {
					mutate(&page)
				}
				return page, nil
			}}
			node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: reader})}}
			budget := presence.NewRecoveryResultBudget(100, 1<<20)
			owned, err := boundedRecoveryProvider(t, node, local).RecoverRouteCallsBounded(context.Background(), calls, budget, nil)
			defer owned.Release()
			if err == nil || len(owned.Results) != 0 || !reflect.DeepEqual(starts, []int{0, 1}) {
				t.Fatalf("malformed later proof published or was not exercised: err=%v starts=%v", err, starts)
			}
			requireRecoveryBudgetReleased(t, budget)
		})
	}
}

func TestOwnedRecoveryIntegrationFallbackKeepsExactDeadlineAndReplayBoundary(t *testing.T) {
	for _, mode := range []string{"unsupported", "busy", "timeout", "generic", "malformed", "late_unsupported"} {
		t.Run(mode, func(t *testing.T) {
			node, calls := recoveryCallsFixture(1, 2)
			calls[1].Groups = append(calls[1].Groups, presence.EndpointLookupGroup{Target: calls[1].Groups[0].Target, UIDs: []string{"third"}})
			local := ownedRecoveryReader(t, 1, calls)
			old := recoveryBatchReaderFunc(func(_ context.Context, groups []presence.EndpointLookupGroup) []presence.OwnerRouteResult {
				return recoveryCallsPage(2, 22, []presence.RecoveryCall{{Groups: groups}}, 0, 1, 1).Results[0]
			})
			newReader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				return recoveryCallsPage(2, 22, c, start, start+1, 1), nil
			}}
			oldHandler := presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: old})}
			newHandler := presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: newReader})}
			var ops []byte
			var deadlines []time.Time
			node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: recoveryCallsRPC{handle: func(ctx context.Context, p []byte) ([]byte, error) {
				if len(p) < 6 {
					return nil, errors.New("short request")
				}
				ops = append(ops, p[5])
				deadline, _ := ctx.Deadline()
				deadlines = append(deadlines, deadline)
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
				default:
					if len(ops) == 1 {
						return newHandler.HandleRPC(ctx, p)
					}
					return nil, transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: "internal/access/node: unknown presence op id 11"}
				}
			}}}
			budget := presence.NewRecoveryResultBudget(100, 1<<20)
			owned, err := boundedRecoveryProvider(t, node, local).RecoverRouteCallsBounded(context.Background(), calls, budget, nil)
			defer owned.Release()
			if mode == "unsupported" {
				if err != nil || !reflect.DeepEqual(ops, []byte{11, 9, 10}) {
					t.Fatalf("exact compatibility boundary lost: err=%v ops=%v", err, ops)
				}
				for _, call := range owned.Results {
					for _, group := range call {
						if group.Err != nil || len(group.Routes) != 2 {
							t.Fatal("legacy response lost one owner proof")
						}
					}
				}
			} else {
				want := []byte{11}
				if mode == "late_unsupported" {
					want = []byte{11, 11}
				}
				if !reflect.DeepEqual(ops, want) {
					t.Fatalf("unexpected retry/fallback: %v", ops)
				}
				if err == nil {
					requireRecoveryCallErrors(t, owned.Results)
				} else if len(owned.Results) != 0 {
					t.Fatal("whole-round error retained partial proof")
				}
			}
			requireSameRecoveryDeadline(t, deadlines)
			owned.Release()
			requireRecoveryBudgetReleased(t, budget)
		})
	}
}

type ownedRecoveryFenceNode struct {
	*recoveryCallNode
	changed bool
}

func (n *ownedRecoveryFenceNode) RouteHashSlot(hash uint16) (clusterpkg.Route, error) {
	route := n.route
	route.HashSlot = hash
	if n.changed && hash == 101 {
		route.LeaderTerm++
	}
	return route, nil
}

func TestOwnedRecoveryIntegrationFinalFencesReturnDiscardedCredits(t *testing.T) {
	for _, mode := range []string{"membership", "target_sibling"} {
		t.Run(mode, func(t *testing.T) {
			base, calls := recoveryCallsFixture(1, 2)
			node := &ownedRecoveryFenceNode{recoveryCallNode: base}
			calls[1].Groups[0].Target.HashSlot = 102
			local := ownedRecoveryReader(t, 1, calls)
			snapshots := 0
			base.controller = func(context.Context) (control.Snapshot, error) {
				snapshots++
				s := base.snapshot
				if mode == "membership" && snapshots == 2 {
					s.Nodes = nil
				}
				return s, nil
			}
			remote := accessnode.New(accessnode.Options{OwnerRoutes: ownedRecoveryReader(t, 2, calls)})
			base.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: recoveryCallsRPC{handle: func(ctx context.Context, p []byte) ([]byte, error) {
				body, err := remote.HandlePresenceOwnerRPC(ctx, p)
				if mode == "target_sibling" {
					node.changed = true
				}
				return body, err
			}}}
			budget := presence.NewRecoveryResultBudget(100, 1<<20)
			owned, err := boundedRecoveryProvider(t, node, local).RecoverRouteCallsBounded(context.Background(), calls, budget, nil)
			defer owned.Release()
			if snapshots != 2 {
				t.Fatalf("final membership fence not exercised: %d", snapshots)
			}
			if mode == "membership" {
				if err == nil || len(owned.Results) != 0 {
					t.Fatal("changed owner membership retained proof")
				}
				requireRecoveryBudgetReleased(t, budget)
				return
			}
			if err != nil || len(owned.Results) != 2 {
				t.Fatalf("target failure became whole-round rejection: %v", err)
			}
			requireRecoveryCallErrors(t, owned.Results[:1])
			if got := owned.Results[1][0]; got.Err != nil || len(got.Routes) != 2 || budget.Stats().Rows != 2 {
				t.Fatalf("sibling proof or released rows changed: err=%v rows=%d charged=%d", got.Err, len(got.Routes), budget.Stats().Rows)
			}
			owned.Release()
			requireRecoveryBudgetReleased(t, budget)
		})
	}
}

func TestOwnedRecoveryIntegrationAcrossOwners4096LimitIsCallScoped(t *testing.T) {
	for _, remoteRows := range []int{2048, 2049} {
		t.Run(fmt.Sprint(2048+remoteRows), func(t *testing.T) {
			node, calls := recoveryCallsFixture(1, 2)
			registry := online.NewRegistry(online.RegistryOptions{})
			var session uint64
			for i, call := range calls {
				count := 1
				if i == 0 {
					count = 2048
				}
				for range count {
					session++
					group := call.Groups[0]
					row := online.OwnerRoute{UID: group.UIDs[0], HashSlot: group.Target.HashSlot, OwnerNodeID: 1, OwnerBootID: 11, SessionID: session}
					if err := registry.RegisterPending(online.LocalSession{Route: row}); err != nil {
						t.Fatal(err)
					}
					if err := registry.MarkActive(session); err != nil {
						t.Fatal(err)
					}
				}
			}
			local := presence.NewOwnerRecovery(registry, 1, 11, func(presence.RouteTarget) error { return nil })
			reader := recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				page := recoveryCallsPage(2, 22, c, start, len(c), 1)
				page.Results[0] = recoveryCallsPage(2, 22, c, 0, 1, remoteRows).Results[0]
				return page, nil
			}}
			node.rpcByNode = map[uint64]clusterpkg.NodeRPCHandler{2: presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: reader})}}
			budget := presence.NewRecoveryResultBudget(presence.RecoveryResultMaxRows, presence.RecoveryResultMaxBytes)
			owned, err := boundedRecoveryProvider(t, node, local).RecoverRouteCallsBounded(context.Background(), calls, budget, nil)
			defer owned.Release()
			if err != nil || len(owned.Results) != 2 {
				t.Fatalf("call-scoped boundary became whole-round failure: %v", err)
			}
			if remoteRows == 2048 {
				if got := owned.Results[0][0]; got.Err != nil || len(got.Routes) != 4096 {
					t.Fatalf("exact 4096 proof rejected: err=%v rows=%d", got.Err, len(got.Routes))
				}
			} else {
				requireRecoveryCallErrors(t, owned.Results[:1])
				if budget.Stats().Rows != 2 {
					t.Fatal("overflow call retained charged routes")
				}
			}
			if got := owned.Results[1][0]; got.Err != nil || len(got.Routes) != 2 {
				t.Fatalf("legal sibling rejected: err=%v rows=%d", got.Err, len(got.Routes))
			}
			owned.Release()
			requireRecoveryBudgetReleased(t, budget)
		})
	}
}
