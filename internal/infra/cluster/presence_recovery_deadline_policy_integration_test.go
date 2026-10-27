//go:build integration

package cluster

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/transport/testkit"
)

// deadlinePolicyNode keeps routing/membership deterministic while sending every
// remote owner request over a real negotiated TCP transport and owner codec.
type deadlinePolicyNode struct {
	*recoveryCallNode
	client    *transport.Client
	mu        sync.Mutex
	deadlines map[uint64][]time.Time
}

func (n *deadlinePolicyNode) CallRPC(ctx context.Context, owner uint64, service uint8, payload []byte) ([]byte, error) {
	deadline, _ := ctx.Deadline()
	n.mu.Lock()
	n.deadlines[owner] = append(n.deadlines[owner], deadline)
	n.mu.Unlock()
	return n.client.Call(ctx, transport.NodeID(owner), uint64(service)+3, transport.PriorityRPC, uint16(service), payload)
}

type deadlinePolicyBehavior struct {
	delay             time.Duration
	timeout           bool
	oneCallPages      bool
	timeoutAfterFirst bool
	waitAfterFirst    bool
	legacy            bool
}

type deadlinePolicyLocal struct {
	*presence.OwnerRecovery
	reads atomic.Int32
}

func (l *deadlinePolicyLocal) ReadOwnerRoutes(ctx context.Context, target presence.RouteTarget, uids []string) (presence.OwnerRouteSnapshot, error) {
	l.reads.Add(1)
	return l.OwnerRecovery.ReadOwnerRoutes(ctx, target, uids)
}
func (l *deadlinePolicyLocal) ReadOwnerRoutesByTargets(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.OwnerRouteResult {
	l.reads.Add(1)
	return l.OwnerRecovery.ReadOwnerRoutesByTargets(ctx, groups)
}
func (l *deadlinePolicyLocal) ReadOwnerRouteCalls(ctx context.Context, calls []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
	l.reads.Add(1)
	return l.OwnerRecovery.ReadOwnerRouteCalls(ctx, calls, start)
}
func (l *deadlinePolicyLocal) ReadOwnerRouteCallsBounded(ctx context.Context, calls []presence.RecoveryCall, start int, budget *presence.RecoveryResultBudget) (presence.OwnedOwnerRouteCallPage, error) {
	l.reads.Add(1)
	return l.OwnerRecovery.ReadOwnerRouteCallsBounded(ctx, calls, start, budget)
}

type deadlinePolicyTCP struct {
	node      *deadlinePolicyNode
	local     *deadlinePolicyLocal
	provider  *PresenceRecoveryOwners
	calls     []presence.RecoveryCall
	contacts  map[uint64]*atomic.Int32
	succeeded map[uint64]*atomic.Int32
}

func newDeadlinePolicyTCP(t *testing.T, origin uint64, owners []uint64, behavior map[uint64]deadlinePolicyBehavior) *deadlinePolicyTCP {
	t.Helper()
	node, calls := recoveryCallsFixture(owners...)
	node.nodeID = origin
	local := &deadlinePolicyLocal{OwnerRecovery: ownedRecoveryReader(t, origin, calls)}
	fixture := &deadlinePolicyTCP{node: &deadlinePolicyNode{recoveryCallNode: node, deadlines: make(map[uint64][]time.Time)}, local: local, calls: calls, contacts: make(map[uint64]*atomic.Int32), succeeded: make(map[uint64]*atomic.Int32)}
	discovery := testkit.StaticDiscovery{}
	var servers []*transport.Server
	for _, owner := range owners {
		if owner == origin {
			continue
		}
		fixture.contacts[owner] = &atomic.Int32{}
		fixture.succeeded[owner] = &atomic.Int32{}
		var reader presence.OwnerRouteReader = ownedRecoveryReader(t, owner, calls)
		b := behavior[owner]
		if b.oneCallPages {
			reader = recoveryCallReader{page: func(_ context.Context, c []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
				return recoveryCallsPage(owner, owner*11, c, start, start+1, 1), nil
			}}
		}
		adapter := accessnode.New(accessnode.Options{OwnerRoutes: reader})
		server, err := transport.NewServer(transport.ServerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		handler := func(ctx context.Context, payload []byte) ([]byte, error) {
			sequence := fixture.contacts[owner].Add(1)
			if b.timeout || (b.timeoutAfterFirst && sequence > 1) {
				return nil, transport.ErrTimeout
			}
			if b.legacy && accessnode.PresenceOwnerTraceOperation(payload) == 11 {
				return nil, errors.New("internal/access/node: unknown presence op id 11")
			}
			if b.waitAfterFirst && sequence > 1 {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			if b.delay > 0 {
				timer := time.NewTimer(b.delay)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			reply, err := adapter.HandlePresenceOwnerRPC(ctx, payload)
			if err == nil {
				fixture.succeeded[owner].Add(1)
			}
			return reply, err
		}
		err = server.Handle(uint16(accessnode.PresenceOwnerRPCServiceID), handler, transport.ServiceOptions{Alias: "presence-owner-policy-test", Concurrency: 4, QueueSize: 16, MaxQueueBytes: 1 << 20, Timeout: 30 * time.Second, QueueTimeout: 5 * time.Second, CancelRunningFor: accessnode.PresenceOwnerReadOnly})
		if err != nil {
			server.Stop()
			t.Fatal(err)
		}
		if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
			server.Stop()
			t.Fatal(err)
		}
		servers = append(servers, server)
		discovery[transport.NodeID(owner)] = server.Addr()
	}
	client, err := transport.NewClient(transport.ClientConfig{RequestBudgets: true, PoolSize: 16, Discovery: discovery})
	if err != nil {
		for _, server := range servers {
			server.Stop()
		}
		t.Fatal(err)
	}
	fixture.node.client = client
	fixture.provider = NewPresenceRecoveryOwners(fixture.node, local)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.StopContext(ctx); err != nil {
			t.Errorf("TCP client failed to join: %v", err)
		}
		for _, server := range servers {
			if err := server.StopContext(ctx); err != nil {
				t.Errorf("TCP owner failed to join: %v", err)
			}
		}
	})
	return fixture
}

func (f *deadlinePolicyTCP) recover(t *testing.T, ctx context.Context, mode string, calls []presence.RecoveryCall) ([][]presence.EndpointLookupResult, func()) {
	t.Helper()
	switch mode {
	case "targets":
		return [][]presence.EndpointLookupResult{f.provider.RecoverRoutesByTargets(ctx, calls[0].Groups)}, func() {}
	case "calls":
		return f.provider.RecoverRouteCalls(ctx, calls), func() {}
	default:
		budget := presence.NewRecoveryResultBudget(100, 1<<20)
		owned, err := f.provider.RecoverRouteCallsBounded(ctx, calls, budget, nil)
		release := func() { owned.Release(); requireRecoveryBudgetReleased(t, budget) }
		if err != nil {
			release()
			return [][]presence.EndpointLookupResult{{{Err: err}}}, func() {}
		}
		return owned.Results, release
	}
}

// The 350ms reader is a policy stimulus, not a claim that an artificial delay
// reproduces the cloud or R17 scheduler's physical failure mechanism.
func TestPresenceRecoveryDeadlinePolicyTCPSlowOwnerCompletes(t *testing.T) {
	for _, mode := range []string{"targets", "calls", "bounded"} {
		t.Run(mode, func(t *testing.T) {
			f := newDeadlinePolicyTCP(t, 3, []uint64{1, 2, 3}, map[uint64]deadlinePolicyBehavior{1: {delay: 350 * time.Millisecond}})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			got, release := f.recover(t, ctx, mode, f.calls[:1])
			defer release()
			if len(got) != 1 || len(got[0]) != 1 || got[0][0].Err != nil || len(got[0][0].Routes) != 3 {
				t.Fatalf("healthy 350ms owner must complete all-owner proof within original parent: err=%v routes=%d", got[0][0].Err, len(got[0][0].Routes))
			}
			if ctx.Err() != nil {
				t.Fatal("original parent was consumed")
			}
		})
	}
}

func TestPresenceRecoveryDeadlinePolicyTCPEarlierParentWins(t *testing.T) {
	f := newDeadlinePolicyTCP(t, 3, []uint64{1, 2, 3}, map[uint64]deadlinePolicyBehavior{1: {delay: 350 * time.Millisecond}})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	got, release := f.recover(t, ctx, "bounded", f.calls[:1])
	defer release()
	if !errors.Is(got[0][0].Err, context.DeadlineExceeded) || len(got[0][0].Routes) != 0 {
		t.Fatalf("earlier original parent lost: %v", got[0][0].Err)
	}
	if f.contacts[2].Load() != 0 || f.local.reads.Load() != 0 {
		t.Fatal("read another owner after parent deadline")
	}
}

func TestPresenceRecoveryDeadlinePolicyTCPStopsWhenAllCallsFailed(t *testing.T) {
	for _, mode := range []string{"calls", "bounded"} {
		t.Run(mode, func(t *testing.T) {
			f := newDeadlinePolicyTCP(t, 3, []uint64{1, 2, 3}, map[uint64]deadlinePolicyBehavior{1: {timeout: true}})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			got, release := f.recover(t, ctx, mode, f.calls)
			defer release()
			requireRecoveryCallErrors(t, got)
			if f.contacts[1].Load() != 1 || f.contacts[2].Load() != 0 || f.local.reads.Load() != 0 {
				t.Fatalf("all calls failed, but unneeded owner reads remain: first=%d second=%d local=%d", f.contacts[1].Load(), f.contacts[2].Load(), f.local.reads.Load())
			}
		})
	}
}

func TestPresenceRecoveryDeadlinePolicyTCPViablePrefixNeedsEveryOwner(t *testing.T) {
	for _, mode := range []string{"calls", "bounded"} {
		t.Run(mode, func(t *testing.T) {
			f := newDeadlinePolicyTCP(t, 3, []uint64{1, 2, 3}, map[uint64]deadlinePolicyBehavior{1: {oneCallPages: true, timeoutAfterFirst: true}})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			got, release := f.recover(t, ctx, mode, f.calls)
			defer release()
			if len(got) != 2 || got[0][0].Err != nil || len(got[0][0].Routes) != 3 {
				t.Fatalf("viable completed prefix lost all-owner proof: err=%v routes=%d", got[0][0].Err, len(got[0][0].Routes))
			}
			requireRecoveryCallErrors(t, got[1:])
			if f.contacts[2].Load() != 1 || f.local.reads.Load() != 1 {
				t.Fatal("remaining owners omitted while one call remained viable")
			}
		})
	}
}

func TestPresenceRecoveryDeadlinePolicyTCPOneOwnerDeadlineAcrossPagesAndFallback(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "pages"
		if legacy {
			name = "fallback"
		}
		t.Run(name, func(t *testing.T) {
			f := newDeadlinePolicyTCP(t, 3, []uint64{1}, map[uint64]deadlinePolicyBehavior{1: {oneCallPages: !legacy, legacy: legacy}})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			got, release := f.recover(t, ctx, "bounded", f.calls)
			defer release()
			for _, call := range got {
				if call[0].Err != nil || len(call[0].Routes) != 1 {
					t.Fatalf("page/fallback proof=%v", call[0].Err)
				}
			}
			f.node.mu.Lock()
			deadlines := append([]time.Time(nil), f.node.deadlines[1]...)
			f.node.mu.Unlock()
			want := 2
			if legacy {
				want = 3
			}
			if len(deadlines) != want {
				t.Fatalf("unexpected page/fallback RPC count=%d want=%d", len(deadlines), want)
			}
			requireSameRecoveryDeadline(t, deadlines)
		})
	}
}

func TestPresenceRecoveryDeadlinePolicyTCPChainExpiryIsNotStaleAbsence(t *testing.T) {
	f := newDeadlinePolicyTCP(t, 4, []uint64{1, 2, 3, 4}, map[uint64]deadlinePolicyBehavior{1: {delay: 900 * time.Millisecond}, 2: {delay: 900 * time.Millisecond}, 3: {delay: 900 * time.Millisecond}})
	f.node.snapshot.Nodes[2].Health = control.NodeHealth{Status: control.NodeDown, Freshness: control.NodeHealthStale}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, release := f.recover(t, ctx, "bounded", f.calls[:1])
	defer release()
	if len(got[0]) != 1 || got[0][0].Err == nil || len(got[0][0].Routes) != 0 {
		t.Fatal("chain budget expiry fabricated stale-owner absence or partial proof")
	}
	if ctx.Err() != nil {
		t.Fatal("test did not isolate read-chain budget from original parent")
	}
	evidence, ok := presence.RecoveryProviderFailure(got[0][0].Err)
	if !ok || evidence.Reason.String() != "read_chain_budget" {
		t.Fatal("read-chain limit was not distinguished from caller cancellation or owner absence")
	}
	if f.succeeded[1].Load() != 1 || f.succeeded[2].Load() != 1 || f.contacts[3].Load() != 1 || f.local.reads.Load() != 0 {
		t.Fatalf("chain did not preserve completed owners/stop unqueried owner: completed=%d/%d third=%d local=%d", f.succeeded[1].Load(), f.succeeded[2].Load(), f.contacts[3].Load(), f.local.reads.Load())
	}
}

func TestPresenceRecoveryDeadlinePolicyTCPLastOwnerCompletedPrefixSurvivesChain(t *testing.T) {
	f := newDeadlinePolicyTCP(t, 4, []uint64{1, 2, 3}, map[uint64]deadlinePolicyBehavior{1: {delay: 900 * time.Millisecond}, 2: {delay: 900 * time.Millisecond}, 3: {delay: 50 * time.Millisecond, oneCallPages: true, waitAfterFirst: true}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, release := f.recover(t, ctx, "bounded", f.calls)
	defer release()
	if len(got) != 2 || got[0][0].Err != nil || len(got[0][0].Routes) != 3 {
		t.Fatalf("last-owner completed prefix lost to expired child instead of original-parent final validation: err=%v routes=%d", got[0][0].Err, len(got[0][0].Routes))
	}
	if !errors.Is(got[1][0].Err, authority.ErrRouteNotReady) || len(got[1][0].Routes) != 0 {
		t.Fatal("unfinished call acquired proof")
	}
	if ctx.Err() != nil {
		t.Fatal("original parent expired before final proof validation")
	}
}

// Cancellation during final membership validation must reject a completed proof
// through every public provider path. One local owner removes network timing.
func TestPresenceRecoveryDeadlinePolicyFinalParentCancellation(t *testing.T) {
	for _, mode := range []string{"targets", "calls", "bounded"} {
		t.Run(mode, func(t *testing.T) {
			f := newDeadlinePolicyTCP(t, 3, []uint64{3}, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			snapshots := 0
			f.node.controller = func(snapshotCtx context.Context) (control.Snapshot, error) {
				snapshots++
				if snapshots == 2 {
					if err := snapshotCtx.Err(); err != nil {
						t.Errorf("final validation received canceled child context: %v", err)
					}
					cancel()
				}
				return f.node.snapshot, nil
			}
			got, release := f.recover(t, ctx, mode, f.calls[:1])
			defer release()
			if snapshots != 2 {
				t.Fatalf("final-snapshot cancellation was not reached: snapshots=%d", snapshots)
			}
			if len(got) != 1 || len(got[0]) != 1 {
				t.Fatal("final cancellation changed result alignment")
			}
			if !errors.Is(got[0][0].Err, context.Canceled) || len(got[0][0].Routes) != 0 {
				t.Fatalf("completed proof published after original parent canceled during final validation: err=%v routes=%d", got[0][0].Err, len(got[0][0].Routes))
			}
		})
	}
}
