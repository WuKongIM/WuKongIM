package clusterv2

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
)

func TestNodeProposeDelegatesToService(t *testing.T) {
	proposer := &recordingProposer{}
	node, err := New(validNodeConfig(t), WithProposer(proposer))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if err := node.Propose(context.Background(), ProposeRequest{Key: "u1", Command: []byte("cmd")}); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if proposer.calls != 1 || proposer.last.Key != "u1" {
		t.Fatalf("proposer calls=%d last=%#v, want key u1", proposer.calls, proposer.last)
	}
}

func TestNodeInitializesDefaultProposerWhenOptionMissing(t *testing.T) {
	node, err := New(validNodeConfig(t), withController(control.NewStaticController(nodeControlSnapshot())))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})

	err = node.Propose(context.Background(), ProposeRequest{Key: "u1", Command: []byte("cmd")})
	if errors.Is(err, ErrNotStarted) {
		t.Fatalf("Propose() error = %v, want default proposer initialized", err)
	}
}

func TestNodeInitializesDefaultChannelsWhenOptionMissing(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	_, err = node.AppendChannel(context.Background(), channelv2.AppendRequest{ChannelID: channelv2.ChannelID{ID: "missing", Type: 1}})
	if errors.Is(err, ErrNotStarted) {
		t.Fatalf("AppendChannel() error = %v, want default channel service initialized", err)
	}
}

func TestNodeInitializesDefaultControllerV2WhenOptionMissing(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Channel.TickInterval = time.Millisecond
	cfg.Control.ClusterID = "node-default-control"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 4
	cfg.Slots.ReplicaCount = 1

	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	snap := node.Snapshot()
	if !snap.RoutesReady || snap.StateRevision == 0 || snap.SlotCount != 1 || snap.HashSlotCount != 4 {
		t.Fatalf("Snapshot() = %#v, want ControllerV2-backed ready routes", snap)
	}
	route, err := node.RouteHashSlot(0)
	if err != nil && !errors.Is(err, ErrNoSlotLeader) {
		t.Fatalf("RouteHashSlot() error = %v, want route or no slot leader", err)
	}
	if err == nil && route.SlotID != 1 {
		t.Fatalf("RouteHashSlot() = %#v, want slot 1", route)
	}
}

func TestNodeDefaultControllerV2ThreeVotersConvergeOverTransport(t *testing.T) {
	addrs := []string{freeTCPAddr(t), freeTCPAddr(t), freeTCPAddr(t)}
	voters := []ControlVoter{
		{NodeID: 1, Addr: addrs[0]},
		{NodeID: 2, Addr: addrs[1]},
		{NodeID: 3, Addr: addrs[2]},
	}
	nodes := make([]*Node, 0, len(voters))
	for _, voter := range voters {
		cfg := Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: t.TempDir()}
		cfg.Control.ClusterID = "node-default-control-three"
		cfg.Control.Voters = voters
		cfg.Control.AllowBootstrap = true
		cfg.Slots.InitialSlotCount = 1
		cfg.Slots.HashSlotCount = 4
		cfg.Slots.ReplicaCount = 3
		node, err := New(cfg)
		if err != nil {
			t.Fatalf("New(node=%d) error = %v", voter.NodeID, err)
		}
		nodes = append(nodes, node)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startErrs := make(chan error, len(nodes))
	for _, node := range nodes {
		node := node
		go func() { startErrs <- node.Start(startCtx) }()
		t.Cleanup(func() { _ = node.Stop(context.Background()) })
	}
	for range nodes {
		if err := <-startErrs; err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := WaitClusterReady(readyCtx, nodes...); err != nil {
		t.Fatalf("WaitClusterReady() error = %v", err)
	}
}

func TestNodeStartMarksDefaultChannelsReadyWithoutController(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if snap := node.Snapshot(); !snap.ChannelsReady {
		t.Fatalf("Snapshot() ChannelsReady = false, want true")
	}
}

func TestNodeStopDiscardsDefaultChannelsForRestart(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if node.channels != nil {
		t.Fatal("default channels retained after Stop, want discarded")
	}
	if node.defaultChannelStore != nil {
		t.Fatal("default channel store retained after Stop, want discarded")
	}
}

func TestNodeStartFailureDiscardsDefaultChannels(t *testing.T) {
	boom := errors.New("boom")
	var calls []string
	node, err := New(validNodeConfig(t), withResources(namedTestResource("boom", &recordingResource{calls: &calls, startErr: boom})))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Start() error = %v, want boom", err)
	}
	if node.channels != nil {
		t.Fatal("default channels retained after failed Start, want discarded")
	}
	if node.defaultChannelStore != nil {
		t.Fatal("default channel store retained after failed Start, want discarded")
	}
}

type recordingProposer struct {
	calls int
	last  propose.Request
}

func (p *recordingProposer) Propose(_ context.Context, req propose.Request) error {
	p.calls++
	p.last = req
	return nil
}
