//go:build integration

package cluster

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// These tests use the public Node lifecycle and real TCP dispatch. Injected
// resources expose whether shutdown destroyed dependencies of a live RPC.
type stopOwnedChannels struct {
	noopChannelService
	closed atomic.Bool
}

func (s *stopOwnedChannels) Close() error { s.closed.Store(true); return nil }

type stopOwnedResource struct {
	start  func(context.Context) error
	closed atomic.Bool
}

func (s *stopOwnedResource) Start(ctx context.Context) error {
	if s.start != nil {
		return s.start(ctx)
	}
	return nil
}
func (s *stopOwnedResource) Stop(context.Context) error { s.closed.Store(true); return nil }

type stopOwnerDiscovery string

func (d stopOwnerDiscovery) Resolve(transport.NodeID) (string, error) { return string(d), nil }

func waitStopOwner(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("RPC ownership synchronization timed out")
	}
}

func TestNodeStopOwnershipRetainsRPCDependencies(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "already_canceled"}[alreadyCanceled], func(t *testing.T) {
			cfg := validNodeConfig(t)
			cfg.ListenAddr = freeTCPAddr(t)
			channels, dependency := &stopOwnedChannels{}, &stopOwnedResource{}
			n, err := New(cfg, WithProposer(&recordingProposer{}), withResources(namedTestResource("dependency", dependency)))
			if err != nil {
				t.Fatal(err)
			}
			n.channels = channels
			entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			observedClosed := make(chan bool, 1)
			n.RegisterRPC(250, clusternet.HandlerFunc(func(context.Context, []byte) ([]byte, error) {
				close(entered)
				<-release
				observedClosed <- channels.closed.Load() || dependency.closed.Load()
				close(returned)
				return nil, nil
			}))
			client, err := transport.NewClient(transport.ClientConfig{RequestBudgets: true, PoolSize: 1, Discovery: stopOwnerDiscovery(cfg.ListenAddr)})
			if err != nil {
				t.Fatal(err)
			}
			callDone := make(chan error, 1)
			callStarted := false
			t.Cleanup(func() {
				unblock()
				client.Stop()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := n.Stop(ctx); err != nil {
					t.Errorf("cleanup Stop: %v", err)
				}
				if callStarted {
					select {
					case <-callDone:
					case <-ctx.Done():
						t.Error("caller did not join")
					}
				}
			})
			if err := n.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			callStarted = true
			go func() {
				_, err := client.Call(context.Background(), transport.NodeID(cfg.NodeID), 0, transport.PriorityRPC, 250, []byte("mutation"))
				callDone <- err
			}()
			waitStopOwner(t, entered)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			if alreadyCanceled {
				cancel()
			}
			err = n.Stop(ctx)
			cancel()
			if !errors.Is(err, transport.ErrStopIncomplete) {
				t.Errorf("live RPC reported joined: %v", err)
			}
			var incomplete interface{ StopIncomplete() bool }
			if !errors.As(err, &incomplete) || !incomplete.StopIncomplete() {
				t.Errorf("live RPC omitted neutral ownership marker: %v", err)
			}
			if channels.closed.Load() || dependency.closed.Load() {
				t.Error("Stop closed dependencies before RPC returned")
			}
			if s := n.Snapshot(); s.RoutesReady || s.SlotsReady || s.ChannelsReady {
				t.Error("incomplete Stop did not fence readiness")
			}
			if _, err := n.RouteKey("owned-user"); !errors.Is(err, ErrStopping) {
				t.Errorf("incomplete Stop admitted foreground work: %v", err)
			}
			unblock()
			waitStopOwner(t, returned)
			if <-observedClosed {
				t.Error("admitted RPC observed destroyed dependency")
			}
			join, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer joinCancel()
			if err := n.Stop(join); err != nil {
				t.Fatalf("retry Stop: %v", err)
			}
			if !channels.closed.Load() || !dependency.closed.Load() {
				t.Error("joined Stop retained dependencies")
			}
		})
	}
}

func TestNodeStartupRollbackRetainsRPCOwnerAcrossRetry(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.ListenAddr = freeTCPAddr(t)
	channels, dependency := &stopOwnedChannels{}, &stopOwnedResource{}
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	client, err := transport.NewClient(transport.ClientConfig{RequestBudgets: true, PoolSize: 1, Discovery: stopOwnerDiscovery(cfg.ListenAddr)})
	if err != nil {
		t.Fatal(err)
	}
	callDone := make(chan error, 1)
	callStarted := false
	boom := errors.New("later resource start failed")
	startCtx, startCancel := context.WithCancel(context.Background())
	defer startCancel()
	var startCalls int
	failing := &stopOwnedResource{start: func(context.Context) error {
		startCalls++
		if startCalls > 1 {
			return boom
		}
		callStarted = true
		go func() {
			_, err := client.Call(context.Background(), transport.NodeID(cfg.NodeID), 0, transport.PriorityRPC, 250, []byte("mutation"))
			callDone <- err
		}()
		waitStopOwner(t, entered)
		startCancel()
		return boom
	}}
	n, err := New(cfg, WithProposer(&recordingProposer{}), withResources(namedTestResource("dependency", dependency), namedTestResource("failed", failing)))
	if err != nil {
		t.Fatal(err)
	}
	n.channels = channels
	n.RegisterRPC(250, clusternet.HandlerFunc(func(context.Context, []byte) ([]byte, error) {
		close(entered)
		<-release
		close(returned)
		return nil, nil
	}))
	t.Cleanup(func() {
		unblock()
		client.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := n.Stop(ctx); err != nil {
			t.Errorf("cleanup Stop: %v", err)
		}
		if callStarted {
			select {
			case <-callDone:
			case <-ctx.Done():
				t.Error("caller did not join")
			}
		}
	})
	err = n.Start(startCtx)
	if !errors.Is(err, boom) || !errors.Is(err, transport.ErrStopIncomplete) {
		t.Errorf("startup lost incomplete rollback: %v", err)
	}
	if dependency.closed.Load() || channels.closed.Load() {
		t.Error("rollback closed dependency of live RPC")
	}
	// A second Start must join the retained generation before constructing one.
	retry, retryCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = n.Start(retry)
	retryCancel()
	if !errors.Is(err, transport.ErrStopIncomplete) {
		t.Errorf("restart replaced an owned generation: %v", err)
	}
	if dependency.closed.Load() || channels.closed.Load() {
		t.Error("restart closed dependency of live RPC")
	}
	unblock()
	waitStopOwner(t, returned)
	join, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer joinCancel()
	if err := n.Stop(join); err != nil {
		t.Fatalf("joined rollback: %v", err)
	}
	if !dependency.closed.Load() || !channels.closed.Load() {
		t.Error("joined rollback omitted cleanup")
	}
}

type stopOwnedObserver func(transport.Event)

func (f stopOwnedObserver) ObserveTransport(e transport.Event) { f(e) }

func TestNodeStopOwnershipRetainsOutboundObserverDependency(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.ListenAddr = freeTCPAddr(t)
	blocked, release := make(chan struct{}), make(chan struct{})
	var blockedOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	cfg.Transport.Observer = stopOwnedObserver(func(e transport.Event) {
		if e.Name == "client_rpc" && e.ServiceID == 250 {
			blockedOnce.Do(func() { close(blocked); <-release })
		}
	})
	channels := &stopOwnedChannels{}
	n, err := New(cfg, WithProposer(&recordingProposer{}))
	if err != nil {
		t.Fatal(err)
	}
	n.channels = channels
	n.RegisterRPC(250, clusternet.HandlerFunc(func(context.Context, []byte) ([]byte, error) { return nil, nil }))
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := n.Stop(ctx); err != nil {
			t.Errorf("cleanup Stop: %v", err)
		}
	})
	if err := n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = n.CallRPC(ctx, cfg.NodeID, 250, nil)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	waitStopOwner(t, blocked)
	stop, stopCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = n.Stop(stop)
	stopCancel()
	if !errors.Is(err, transport.ErrStopIncomplete) {
		t.Errorf("outbound observer still owned but Stop reported joined: %v", err)
	}
	if channels.closed.Load() {
		t.Error("client observer lost dependency before joining")
	}
	unblock()
	join, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer joinCancel()
	if err := n.Stop(join); err != nil {
		t.Fatalf("retry outbound join: %v", err)
	}
	if !channels.closed.Load() {
		t.Error("joined outbound Stop omitted cleanup")
	}
}

// The existing Controller constructor seam models the real Runtime.Stop entry
// contract: an expired context leaves its running backend owned by the node.
type stopBudgetController struct {
	control.Controller
	running  bool
	stopHook func()
}

func (c *stopBudgetController) Start(ctx context.Context) error {
	if err := c.Controller.Start(ctx); err != nil {
		return err
	}
	c.running = true
	return nil
}
func (c *stopBudgetController) Stop(ctx context.Context) error {
	if c.stopHook != nil {
		c.stopHook()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.running = false
	return c.Controller.Stop(ctx)
}

type expireAfterQuiescence struct{ cancel context.CancelFunc }

func (*expireAfterQuiescence) Start(context.Context) error     { return nil }
func (*expireAfterQuiescence) Stop(context.Context) error      { return nil }
func (r *expireAfterQuiescence) Quiesce(context.Context) error { r.cancel(); return nil }

func TestNodeStopBudgetExpiresAfterRPCJoinRetainsController(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controller := &stopBudgetController{Controller: control.NewStaticController(nodeControlSnapshot())}
	channels := &stopOwnedChannels{}
	n, err := New(validNodeConfig(t), withController(controller), WithProposer(&recordingProposer{}),
		withResources(namedTestResource("quiesced-rpc", &expireAfterQuiescence{cancel: cancel})))
	if err != nil {
		t.Fatal(err)
	}
	n.channels = channels
	t.Cleanup(func() { _ = n.Stop(context.Background()) })
	if err := n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err = n.Stop(ctx)
	var incomplete interface{ StopIncomplete() bool }
	if !errors.Is(err, context.Canceled) || !errors.As(err, &incomplete) {
		t.Errorf("unjoined Controller reported ordinary diagnostic: %v", err)
	}
	if channels.closed.Load() {
		t.Error("expired budget destroyed dependencies before Controller could stop")
	}
	if !controller.running {
		t.Error("fixture did not retain the canceled Controller owner")
	}
	if err := n.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if controller.running || !channels.closed.Load() {
		t.Error("later Stop did not join retained Controller and dependencies")
	}
}

func TestNodeStopControllerBudgetRetainsRemainingDependencies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controller := &stopBudgetController{Controller: control.NewStaticController(nodeControlSnapshot()), stopHook: cancel}
	dependency := &stopOwnedResource{}
	n, err := New(validNodeConfig(t), withController(controller), WithProposer(&recordingProposer{}),
		withResources(namedTestResource("controller-dependency", dependency)))
	if err != nil {
		t.Fatal(err)
	}
	n.channels = &stopOwnedChannels{}
	t.Cleanup(func() { _ = n.Stop(context.Background()) })
	if err := n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err = n.Stop(ctx)
	var incomplete interface{ StopIncomplete() bool }
	if !errors.Is(err, context.Canceled) || !errors.As(err, &incomplete) {
		t.Errorf("Controller budget failure lost ownership: %v", err)
	}
	if dependency.closed.Load() {
		t.Error("Controller budget failure destroyed remaining dependency")
	}
	if !controller.running {
		t.Error("fixture did not retain canceled Controller")
	}
	if err := n.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if controller.running || !dependency.closed.Load() {
		t.Error("retry failed to join remaining Controller dependency")
	}
}
