//go:build integration

package controller_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"go.etcd.io/raft/v3/raftpb"
)

// Failure inventory: a fenced write needs its actual majority/apply receipt,
// while a read still needs a fresh ReadIndex. Withholding only read responses
// must not stall a valid Claim; withholding Append acknowledgements must. The
// external link uses real public Runtime.Step and never fabricates ReadState,
// commit/applied indexes, capability receipts or successful proposal outcomes.
type presenceClaimWire struct {
	mu              sync.RWMutex
	nodes           map[uint64]*controller.Runtime
	leader          uint64
	holdReads       bool
	holdWrites      bool
	appendAfter     uint64
	blockCommands   bool
	isolate         uint64
	probeHook       func(context.Context)
	readSeen        chan struct{}
	appendSeen      chan struct{}
	queue           chan raftpb.Message
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	dropped         atomic.Int32
	blockedCommands atomic.Int32
}

func newPresenceClaimWire(t *testing.T) *presenceClaimWire {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w := &presenceClaimWire{nodes: make(map[uint64]*controller.Runtime), readSeen: make(chan struct{}, 1), appendSeen: make(chan struct{}, 1), queue: make(chan raftpb.Message, 1024), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-w.queue:
				w.mu.RLock()
				node, leader, read, write, after := w.nodes[m.To], w.leader, w.holdReads, w.holdWrites, w.appendAfter
				blockCommands, isolate := w.blockCommands, w.isolate
				w.mu.RUnlock()
				if node == nil {
					continue
				}
				if isolate != 0 && (m.From == isolate || m.To == isolate) {
					continue
				}
				if blockCommands && m.Type == raftpb.MsgApp {
					blocked := false
					for _, entry := range m.Entries {
						if entry.Type == raftpb.EntryNormal && len(entry.Data) != 0 {
							blocked = true
							break
						}
					}
					if blocked {
						w.blockedCommands.Add(1)
						continue
					}
				}
				if m.From == leader && m.Type == raftpb.MsgApp {
					for _, entry := range m.Entries {
						if entry.Index > after {
							select {
							case w.appendSeen <- struct{}{}:
							default:
							}
							break
						}
					}
				}
				if m.To == leader && m.Type == raftpb.MsgHeartbeatResp && len(m.Context) != 0 && read {
					select {
					case w.readSeen <- struct{}{}:
					default:
					}
					// Preserve the real peer's liveness response, but lose its read
					// context. A later real heartbeat can supply a fresh read ACK.
					m.Context = nil
				}
				if m.To == leader && m.Type == raftpb.MsgAppResp && write {
					continue
				}
				step, stop := context.WithTimeout(ctx, 200*time.Millisecond)
				_ = node.Step(step, m)
				stop()
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-w.done:
		case <-time.After(time.Second):
			t.Error("public Raft link did not join")
		}
		if w.dropped.Load() != 0 {
			t.Errorf("fixture queue dropped %d messages", w.dropped.Load())
		}
	})
	return w
}

func (w *presenceClaimWire) Send(batch []raftpb.Message) {
	for _, m := range batch {
		// The bounded transport owns private wire bytes after Send returns.
		data, err := m.Marshal()
		if err != nil {
			panic(err)
		}
		var owned raftpb.Message
		if err := owned.Unmarshal(data); err != nil {
			panic(err)
		}
		select {
		case w.queue <- owned:
		case <-w.ctx.Done():
			return
		default:
			w.dropped.Add(1)
		}
	}
}

func (w *presenceClaimWire) arm(leader uint64, read, write bool, after uint64) {
	w.mu.Lock()
	w.leader, w.holdReads, w.holdWrites, w.appendAfter = leader, read, write, after
	w.mu.Unlock()
}

func newPresenceClaimCluster(t *testing.T, configure ...func(*presenceClaimWire)) (*presenceClaimWire, func() *controller.Runtime, func()) {
	t.Helper()
	w := newPresenceClaimWire(t)
	for _, setup := range configure {
		w.mu.Lock()
		setup(w)
		w.mu.Unlock()
	}
	voters := []controller.Voter{{NodeID: 1, Addr: "claim-write-1"}, {NodeID: 2, Addr: "claim-write-2"}, {NodeID: 3, Addr: "claim-write-3"}}
	configs := make([]controller.RuntimeConfig, 3)
	for i := range configs {
		configs[i] = controller.RuntimeConfig{NodeID: uint64(i + 1), Addr: voters[i].Addr, StateDir: t.TempDir(), ClusterID: "presence-claim-write", Role: controller.RuntimeRoleVoter, Voters: voters, AllowBootstrap: true, InitialSlotCount: 1, HashSlotCount: 256, ReplicaCount: 3, TickInterval: 5 * time.Millisecond, RaftTransport: w,
			PresenceCapabilityProbe: func(_ context.Context, id uint64, addr string) (controller.PresenceCapabilities, error) {
				if id < 1 || id > 3 || addr != fmt.Sprintf("claim-write-%d", id) {
					return controller.PresenceCapabilities{}, fmt.Errorf("unexpected external capability target")
				}
				w.mu.Lock()
				hook := w.probeHook
				w.probeHook = nil
				w.mu.Unlock()
				if hook != nil {
					hook(w.ctx)
				}
				return controller.PresenceCapabilities{NodeID: id, BootID: 100 + id, ControllerStateVersion: 2, ControllerCommandVersion: 2, PresenceProtocolVersion: 1}, nil
			},
		}
	}
	start := func() {
		for _, cfg := range configs {
			n, err := controller.NewRuntime(cfg)
			if err != nil {
				t.Fatal(err)
			}
			w.mu.Lock()
			w.nodes[cfg.NodeID] = n
			w.mu.Unlock()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, n := range w.nodes {
			if err := n.Start(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for _, n := range w.nodes {
			if err := n.Stop(ctx); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(stop)
	start()
	leader := func() *controller.Runtime {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			for id, n := range w.nodes {
				s, err := n.ControllerRaftStatus(ctx)
				if err != nil || s.Role != "leader" || s.LeaderID != id {
					continue
				}
				st, err := n.LocalState(ctx)
				if err == nil && st.Revision != 0 && len(st.Slots) == 1 {
					return n
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal("Controller cluster did not elect/materialize")
				return nil
			case <-tick.C:
			}
		}
	}
	restart := func() { w.arm(0, false, false, 0); stop(); start() }
	return w, leader, restart
}

func TestPresenceClaimWriteEntryGuards(t *testing.T) {
	t.Run("before_start", func(t *testing.T) {
		n, err := controller.NewRuntime(controller.RuntimeConfig{NodeID: 1, StateDir: t.TempDir(), ClusterID: "claim-not-started", Voters: []controller.Voter{{NodeID: 1, Addr: "n1"}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := n.ClaimPresenceGeneration(context.Background(), controller.ClaimPresenceGenerationRequest{}); !errors.Is(err, controller.ErrNotStarted) {
			t.Fatalf("unstarted Claim: %v", err)
		}
	})
	for _, mode := range []string{"canceled", "follower", "stopped", "stop_after_basis", "leader_loss_after_basis"} {
		t.Run(mode, func(t *testing.T) {
			w, leader, _ := newPresenceClaimCluster(t)
			n := leader()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			m, err := n.EnsurePresenceOwnerSet(ctx)
			if err != nil {
				t.Fatal(err)
			}
			before, err := n.ControllerRaftStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			req := controller.ClaimPresenceGenerationRequest{Fence: controller.PresenceFence{HashSlot: 37, SlotID: 1, LeaderNodeID: before.NodeID, LeaderTerm: 9, ConfigEpoch: 1}, OwnerSetEpoch: m.Epoch, ServingBoot: 100 + before.NodeID}
			want := error(controller.ErrNotLeader)
			switch mode {
			case "canceled":
				cancel()
				want = context.Canceled
			case "follower":
				for id, peer := range w.nodes {
					if id != before.NodeID {
						n = peer
						break
					}
				}
			case "stopped":
				if err := n.Stop(ctx); err != nil {
					t.Fatal(err)
				}
				want = controllerraft.ErrStopped
			case "stop_after_basis":
				w.mu.Lock()
				w.probeHook = func(context.Context) {
					if err := n.Stop(ctx); err != nil {
						t.Error(err)
					}
				}
				w.mu.Unlock()
				want = controllerraft.ErrStopped
			case "leader_loss_after_basis":
				w.mu.Lock()
				w.probeHook = func(context.Context) {
					w.mu.Lock()
					w.isolate = before.NodeID
					w.mu.Unlock()
					tick := time.NewTicker(5 * time.Millisecond)
					defer tick.Stop()
					for {
						s, err := n.ControllerRaftStatus(ctx)
						if err != nil {
							t.Error(err)
							return
						}
						if s.Role != "leader" {
							for id, peer := range w.nodes {
								if id == before.NodeID {
									continue
								}
								other, err := peer.ControllerRaftStatus(ctx)
								if err == nil && other.Role == "leader" {
									return
								}
							}
						}
						select {
						case <-ctx.Done():
							t.Error("actual leadership did not change")
							return
						case <-tick.C:
						}
					}
				}
				w.mu.Unlock()
			}
			if _, err := n.ClaimPresenceGeneration(ctx, req); !errors.Is(err, want) {
				t.Fatalf("Claim %s = %v; want %v", mode, err, want)
			}
		})
	}
	t.Run("no_materialized_state", func(t *testing.T) {
		w, _, _ := newPresenceClaimCluster(t, func(w *presenceClaimWire) { w.blockCommands = true })
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			for id, n := range w.nodes {
				s, err := n.ControllerRaftStatus(ctx)
				if err != nil || s.Role != "leader" || w.blockedCommands.Load() == 0 {
					continue
				}
				st, err := n.LocalState(ctx)
				if err != nil || st.Revision != 0 {
					t.Fatal("fixture did not retain an empty real materialized state")
				}
				call, stop := context.WithTimeout(ctx, 150*time.Millisecond)
				_, err = n.ClaimPresenceGeneration(call, controller.ClaimPresenceGenerationRequest{Fence: controller.PresenceFence{HashSlot: 37, SlotID: 1, LeaderNodeID: id, LeaderTerm: 9, ConfigEpoch: 1}, OwnerSetEpoch: 1, ServingBoot: 100 + id})
				stop()
				if !errors.Is(err, controller.ErrPresenceGenerationNotReady) {
					t.Fatalf("empty materialized state did not fail at its guard: %v", err)
				}
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("real leader without materialized Init did not appear")
			case <-tick.C:
			}
		}
	})
}

func TestPresenceClaimWriteCommitsWithoutASecondReadRound(t *testing.T) {
	w, leader, restart := newPresenceClaimCluster(t)
	n := leader()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	m, err := n.EnsurePresenceOwnerSet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := n.ControllerRaftStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w.arm(before.NodeID, true, false, before.LastIndex)
	readCtx, readCancel := context.WithCancel(ctx)
	readDone := make(chan error, 1)
	go func() { _, err := n.GetPresenceGeneration(readCtx, 37); readDone <- err }()
	t.Cleanup(func() {
		readCancel()
		w.arm(0, false, false, 0)
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Error("read caller did not join")
		}
	})
	select {
	case <-w.readSeen:
	case <-ctx.Done():
		t.Fatal("real ReadIndex response did not reach controlled link")
	}
	writeCtx, writeCancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer writeCancel()
	request := controller.ClaimPresenceGenerationRequest{Fence: controller.PresenceFence{HashSlot: 37, SlotID: 1, LeaderNodeID: before.NodeID, LeaderTerm: 9, ConfigEpoch: 1}, OwnerSetEpoch: m.Epoch, ServingBoot: 100 + before.NodeID}
	g, err := n.ClaimPresenceGeneration(writeCtx, request)
	if err != nil {
		t.Fatalf("fenced write waited for an unnecessary second read quorum: %v", err)
	}
	if g.Generation == 0 || g.Generation != g.AppliedRaftIndex || g.PreviousGeneration != 0 || g.Fence != request.Fence {
		t.Fatalf("not an actual exact apply receipt: %+v", g)
	}
	select {
	case err := <-readDone:
		readDone <- err
		t.Fatalf("read bypassed withheld quorum: %v", err)
	default:
	}
	w.arm(0, false, false, 0)
	select {
	case err := <-readDone:
		readDone <- err
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("read did not recover after actual read ACKs")
	}
	readCancel()
	restart()
	after, err := leader().GetPresenceGeneration(ctx, 37)
	if err != nil || after.Generation != g {
		t.Fatalf("exact receipt did not survive real Controller restart: got=%+v error=%v want=%+v", after.Generation, err, g)
	}
	t.Logf("durable receipt after restart: G=%d previous=%d M=%d", g.Generation, g.PreviousGeneration, g.OwnerSetEpoch)
}

func TestPresenceClaimWriteStillNeedsActualAppendQuorum(t *testing.T) {
	w, leader, _ := newPresenceClaimCluster(t)
	n := leader()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m, err := n.EnsurePresenceOwnerSet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := n.ControllerRaftStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w.arm(before.NodeID, false, true, before.LastIndex)
	write, stop := context.WithTimeout(ctx, 150*time.Millisecond)
	defer stop()
	_, err = n.ClaimPresenceGeneration(write, controller.ClaimPresenceGenerationRequest{Fence: controller.PresenceFence{HashSlot: 37, SlotID: 1, LeaderNodeID: before.NodeID, LeaderTerm: 9, ConfigEpoch: 1}, OwnerSetEpoch: m.Epoch, ServingBoot: 100 + before.NodeID})
	if err == nil {
		t.Fatal("Claim acknowledged without actual Append quorum")
	}
	select {
	case <-w.appendSeen:
	default:
		t.Fatal("control never reached an actual new Append")
	}
	// The admitted command is unknown, not proven absent. Do not reopen/retry it.
	t.Logf("unacknowledged proposal retained unknown outcome: %v", err)
}
