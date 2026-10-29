//go:build integration

package cluster

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channelwrapper "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

// The only replacement is the existing external PeerLink seam. Every probe
// and durability reply still comes from a real follower ExchangeServer/store.
// An explicitly held I/O boundary models a dependency that has seen cancellation
// but has not returned; a timeout cannot transfer its storage ownership away.
type nodeStopReplicationPeer struct {
	server         *replication.ExchangeServer
	store          *channelstore.MessageDBFactory
	hold           bool
	entered        chan struct{}
	canceled       chan struct{}
	release        chan struct{}
	returned       chan struct{}
	storeAfterHold chan error
	enterOnce      sync.Once
	cancelOnce     sync.Once
	releaseOnce    sync.Once
	returnOnce     sync.Once
	replicateCalls atomic.Int32
}

func (p *nodeStopReplicationPeer) Exchange(ctx context.Context, target ch.NodeID, batch replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	for _, item := range batch.Items {
		if item.Kind != replication.ExchangeReplicate {
			continue
		}
		p.replicateCalls.Add(1)
		if p.hold {
			p.enterOnce.Do(func() { close(p.entered) })
			<-ctx.Done()
			p.cancelOnce.Do(func() { close(p.canceled) })
			<-p.release
			p.storeAfterHold <- nodeStopReplicationStoreAvailable(p.store)
			p.returnOnce.Do(func() { close(p.returned) })
		}
	}
	return p.server.Handle(ctx, 1, batch)
}

func (p *nodeStopReplicationPeer) unblock() {
	p.releaseOnce.Do(func() { close(p.release) })
}

func nodeStopReplicationStoreAvailable(factory *channelstore.MessageDBFactory) error {
	id := ch.ChannelID{ID: "node-stop-storage-owner", Type: 1}
	lease, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	if err != nil {
		return err
	}
	return lease.Close()
}

type nodeStopReplicationFixture struct {
	node        *Node
	runtime     *replication.Runtime
	store       *channelstore.MessageDBFactory
	peer        *nodeStopReplicationPeer
	controller  *stopBudgetController
	controlStop atomic.Int32
	dependency  *stopOwnedResource
	authority   replication.Authority
	proposal    replication.Proposal
}

// This assembles the same concrete collaborators and default ownership fields
// as ensureDefaultRuntime, without replacing Runtime.Close or Store.Close.
func newNodeStopReplicationFixture(t *testing.T, holdPeer bool) *nodeStopReplicationFixture {
	t.Helper()
	f := &nodeStopReplicationFixture{dependency: &stopOwnedResource{}}
	cfg := validNodeConfig(t)
	cfg.Slots.HashSlotCount = 256
	cfg.Channel.ReplicaCount = 2
	snapshot := nodeControlSnapshot()
	snapshot.HashSlots.Count = 256
	snapshot.HashSlots.Ranges[0].To = 255
	f.controller = &stopBudgetController{
		Controller: control.NewStaticController(snapshot),
		stopHook:   func() { f.controlStop.Add(1) },
	}
	var err error
	f.node, err = New(cfg, withController(f.controller), WithProposer(&recordingProposer{}), withResources(namedTestResource("replication-dependent-resource", f.dependency)))
	if err != nil {
		t.Fatal(err)
	}
	f.store = channelstore.NewMessageDBFactory(filepath.Join(cfg.DataDir, "owned-channel-store"))
	localStore, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: f.store, MaxBatchItems: 8, MaxBatchBytes: 32768})
	if err != nil {
		t.Fatal(err)
	}
	remoteStore, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 8, MaxBatchBytes: 32768})
	if err != nil {
		t.Fatal(err)
	}
	server, err := replication.NewExchangeServer(replication.ExchangeServerConfig{LocalNode: 2, Store: remoteStore, MaxBatchItems: 8, MaxBatchBytes: 32768})
	if err != nil {
		t.Fatal(err)
	}
	f.peer = &nodeStopReplicationPeer{
		server: server, store: f.store, hold: holdPeer,
		entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), returned: make(chan struct{}), storeAfterHold: make(chan error, 1),
	}
	f.runtime, err = replication.NewRuntime(replication.RuntimeConfig{
		LocalNode: 1, Store: localStore, Link: f.peer, Goroutines: f.node.cfg.Goroutines,
		LocalWorkers: 1, PeerWorkers: 1, RepairWorkers: 1, PeerTargetFlight: 1,
		BatchItems: 1, BatchBytes: 4096, QueueItems: 16, QueueBytes: 32768,
		TargetItems: 8, TargetBytes: 16384, RecoveryPageBytes: 2048,
		MaxChannels: 16, ExchangeTimeout: time.Second, CloseTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := channelwrapper.NewService(channelwrapper.Config{
		LocalNode: 1, Store: f.store, QuorumLog: f.runtime.Log(), Goroutines: f.node.cfg.Goroutines,
		ReactorCount: 1, StoreAppendWorkers: 1, StoreApplyWorkers: 1, RPCWorkers: 1, MailboxSize: 16, MaxChannels: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.node.channels = service
	f.node.defaultChannels = true
	f.node.defaultChannelReplication = f.runtime
	f.node.defaultChannelStore = f.store
	t.Cleanup(func() {
		f.peer.unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = f.node.Stop(ctx)
		_ = f.runtime.Close(ctx) // Saved concrete owner also cleans up baseline RED.
		_ = f.store.Close()
	})
	if err := f.node.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := ch.ChannelID{ID: "node-stop-owned-quorum", Type: 1}
	f.authority = replication.Authority{
		Key: ch.ChannelKeyForID(id), ChannelID: id,
		ID:     replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1},
		Leader: 1, Voters: []ch.NodeID{1, 2}, WriteQuorum: 2,
	}
	if _, err := f.runtime.Log().Install(context.Background(), f.authority); err != nil {
		t.Fatal(err)
	}
	f.proposal = replication.Proposal{
		Key: f.authority.Key, Expected: f.authority.ID, CommandID: ch.CommandID{31: 1},
		Records: []ch.Record{{ID: 1, Epoch: 1, FromUID: "sender", ClientMsgNo: "node-stop-owned", Payload: []byte("payload"), SizeBytes: len("payload"), ServerTimestampMS: 1}},
	}
	return f
}

func nodeStopReplicationIncomplete(t *testing.T, err error) {
	t.Helper()
	var incomplete interface{ StopIncomplete() bool }
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &incomplete) || !incomplete.StopIncomplete() {
		t.Errorf("accepted replication owner reported ordinary/joined cleanup: %v", err)
	}
}

func nodeStopReplicationRetained(t *testing.T, f *nodeStopReplicationFixture) {
	t.Helper()
	if !f.node.defaultChannels || f.node.defaultChannelReplication != f.runtime || f.node.defaultChannelStore != f.store {
		t.Error("unfinished replication cleanup lost its default marker/runtime/store identity")
	}
	if f.controlStop.Load() != 0 || !f.controller.running || f.dependency.closed.Load() {
		t.Error("Node disposed Controller/later resources while replication still owned work")
	}
	if err := nodeStopReplicationStoreAvailable(f.store); err != nil {
		t.Errorf("unfinished replication owner lost usable concrete storage: %v", err)
	}
	if s := f.node.Snapshot(); s.RoutesReady || s.SlotsReady || s.ChannelsReady {
		t.Error("incomplete replication Stop left readiness open")
	}
	if _, err := f.node.RouteKey("held-replication-owner"); !errors.Is(err, ErrStopping) {
		t.Errorf("incomplete replication Stop admitted foreground routing: %v", err)
	}
}

func TestNodeStopOwnershipRetainsConcreteReplicationUntilJoined(t *testing.T) {
	for _, phase := range []string{"peer_io", "returned_callback"} {
		t.Run(phase, func(t *testing.T) {
			f := newNodeStopReplicationFixture(t, phase == "peer_io")
			callbackEntered, releaseCallback, callbackReturned := make(chan error, 1), make(chan struct{}), make(chan struct{})
			callbackStore := make(chan error, 1)
			var callbackOnce sync.Once
			unblockCallback := func() { callbackOnce.Do(func() { close(releaseCallback) }) }
			commitDone := make(chan error, 1)
			commitStarted, joinedCaller := false, false
			t.Cleanup(func() {
				f.peer.unblock()
				unblockCallback()
				if commitStarted && !joinedCaller {
					select {
					case <-commitDone:
					case <-time.After(3 * time.Second):
						t.Error("accepted quorum caller/callback failed to join cleanup")
					}
				}
			})
			if phase == "peer_io" {
				commitStarted = true
				go func() { _, err := f.runtime.Log().Commit(context.Background(), f.proposal); commitDone <- err }()
				waitStopOwner(t, f.peer.entered)
			} else {
				// SubmitCommit is the actual runtime log's existing optional method,
				// also used by channel/worker. No substitute cleanup owner is supplied.
				submitter, ok := f.runtime.Log().(interface {
					SubmitCommit(context.Context, replication.Proposal, func(replication.Receipt, error)) error
				})
				if !ok {
					t.Fatal("real quorum log lacks its production async commit method")
				}
				if err := submitter.SubmitCommit(context.Background(), f.proposal, func(_ replication.Receipt, err error) {
					callbackEntered <- err
					<-releaseCallback
					callbackStore <- nodeStopReplicationStoreAvailable(f.store)
					close(callbackReturned)
					commitDone <- err
				}); err != nil {
					t.Fatal(err)
				}
				commitStarted = true
				select {
				case err := <-callbackEntered:
					if err != nil || f.peer.replicateCalls.Load() == 0 {
						t.Fatalf("real peer quorum did not enter durable callback: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("real accepted commit callback did not enter")
				}
			}
			stop, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			err := f.node.Stop(stop)
			cancel()
			nodeStopReplicationIncomplete(t, err)
			nodeStopReplicationRetained(t, f)
			if phase == "peer_io" {
				waitStopOwner(t, f.peer.canceled) // Proves real Runtime.Close was reached.
			} else {
				select {
				case <-callbackReturned:
					t.Error("held callback returned without explicit release")
				default:
				}
			}
			// A second Stop and a restart must retain the same generation while
			// its original peer/callback is still explicitly held.
			retry, retryCancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			err = f.node.Stop(retry)
			retryCancel()
			nodeStopReplicationIncomplete(t, err)
			nodeStopReplicationRetained(t, f)
			restart, restartCancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			err = f.node.Start(restart)
			restartCancel()
			nodeStopReplicationIncomplete(t, err)
			nodeStopReplicationRetained(t, f)
			f.peer.unblock()
			unblockCallback()
			if phase == "peer_io" {
				waitStopOwner(t, f.peer.returned)
				if err := <-f.peer.storeAfterHold; err != nil {
					t.Errorf("accepted peer observed prematurely closed store: %v", err)
				}
			} else {
				waitStopOwner(t, callbackReturned)
				if err := <-callbackStore; err != nil {
					t.Errorf("accepted callback observed prematurely closed store: %v", err)
				}
			}
			select {
			case <-commitDone:
				joinedCaller = true
			case <-time.After(3 * time.Second):
				t.Fatal("accepted commit did not join after release")
			}
			join, joinCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer joinCancel()
			if err := f.node.Stop(join); err != nil {
				t.Errorf("retry Stop did not join retained concrete replication: %v", err)
			}
			if f.node.defaultChannels || f.node.defaultChannelReplication != nil || f.node.defaultChannelStore != nil || !f.dependency.closed.Load() || f.controller.running {
				t.Error("joined Stop did not dispose default runtime/store and later dependencies")
			}
			if err := nodeStopReplicationStoreAvailable(f.store); !errors.Is(err, ch.ErrClosed) {
				t.Errorf("joined Stop left the old concrete factory usable: %v", err)
			}
			t.Logf("node_replication_stop_owner phase=%s original_peer_calls=%d joined=%t", phase, f.peer.replicateCalls.Load(), joinedCaller)
		})
	}
}

func TestNodeStopOwnershipConcreteReplicationNormalControl(t *testing.T) {
	f := newNodeStopReplicationFixture(t, false)
	receipt, err := f.runtime.Log().Commit(context.Background(), f.proposal)
	if err != nil || receipt.HW != 1 || f.peer.replicateCalls.Load() == 0 {
		t.Fatalf("real local+peer quorum control failed: receipt=%+v err=%v", receipt, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := f.node.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if f.node.defaultChannels || f.node.defaultChannelReplication != nil || f.node.defaultChannelStore != nil || f.controller.running || !f.dependency.closed.Load() {
		t.Error("normal joined Stop retained default owners or later resources")
	}
	if err := nodeStopReplicationStoreAvailable(f.store); !errors.Is(err, ch.ErrClosed) {
		t.Errorf("normal joined Stop left concrete storage open: %v", err)
	}
}
