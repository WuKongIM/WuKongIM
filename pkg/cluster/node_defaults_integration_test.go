//go:build integration

package cluster

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestNodeDefaultControllerThreeVotersConvergeOverTransport(t *testing.T) {
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

func TestNodeDefaultSeedJoinMirrorSyncsFromSeedAddresses(t *testing.T) {
	seedAddr := freeTCPAddr(t)
	seed, err := New(Config{
		NodeID:     1,
		ListenAddr: seedAddr,
		DataDir:    t.TempDir(),
		Control: ControlConfig{
			ClusterID:      "seed-join-sync",
			Voters:         []ControlVoter{{NodeID: 1, Addr: seedAddr}},
			AllowBootstrap: true,
		},
		Slots: SlotConfig{InitialSlotCount: 1, HashSlotCount: 4, ReplicaCount: 1},
	})
	if err != nil {
		t.Fatalf("New(seed) error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := seed.Start(ctx); err != nil {
		t.Fatalf("Start(seed) error = %v", err)
	}
	t.Cleanup(func() { _ = seed.Stop(context.Background()) })

	joinAddr := freeTCPAddr(t)
	joining, err := New(Config{
		NodeID:     4,
		ListenAddr: joinAddr,
		DataDir:    t.TempDir(),
		Control:    ControlConfig{ClusterID: "seed-join-sync"},
		Join: JoinConfig{
			Seeds:         []string{seedAddr},
			AdvertiseAddr: joinAddr,
			Token:         "join-secret",
		},
		Slots: SlotConfig{ReplicaCount: 1},
	})
	if err != nil {
		t.Fatalf("New(joining) error = %v", err)
	}
	joinCtx, joinCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer joinCancel()
	if err := joining.Start(joinCtx); err != nil {
		t.Fatalf("Start(joining) error = %v", err)
	}
	t.Cleanup(func() { _ = joining.Stop(context.Background()) })

	snapshot, err := joining.LocalControlSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalControlSnapshot(joining) error = %v", err)
	}
	if snapshot.Revision == 0 || snapshot.ControllerID != 1 {
		t.Fatalf("joining snapshot = %#v, want seed control snapshot", snapshot)
	}
	conn, err := net.DialTimeout("tcp", joinAddr, time.Second)
	if err != nil {
		t.Fatalf("dial joining mirror transport %s: %v", joinAddr, err)
	}
	_ = conn.Close()
}

func TestNodeDefaultSeedJoinMirrorSyncsThroughFollowerSeedRedirect(t *testing.T) {
	addrs := []string{freeTCPAddr(t), freeTCPAddr(t), freeTCPAddr(t)}
	voters := []ControlVoter{
		{NodeID: 1, Addr: addrs[0]},
		{NodeID: 2, Addr: addrs[1]},
		{NodeID: 3, Addr: addrs[2]},
	}
	nodes := make([]*Node, 0, len(voters))
	for _, voter := range voters {
		cfg := Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: t.TempDir()}
		cfg.Control.ClusterID = "seed-join-follower-sync"
		cfg.Control.Voters = voters
		cfg.Control.AllowBootstrap = true
		cfg.Slots.InitialSlotCount = 1
		cfg.Slots.HashSlotCount = 4
		cfg.Slots.ReplicaCount = 3
		node, err := New(cfg)
		if err != nil {
			t.Fatalf("New(seed node=%d) error = %v", voter.NodeID, err)
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
			t.Fatalf("Start(seed cluster) error = %v", err)
		}
	}
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := WaitControllerWriteReady(readyCtx, nodes...); err != nil {
		t.Fatalf("WaitControllerWriteReady() error = %v", err)
	}

	var followerAddr string
	for _, node := range nodes {
		runtime, ok := node.control.(*control.Runtime)
		if !ok {
			t.Fatalf("node %d control = %T, want *control.Runtime", node.NodeID(), node.control)
		}
		if runtime.LeaderID() != node.NodeID() {
			followerAddr = node.cfg.ListenAddr
			break
		}
	}
	if followerAddr == "" {
		t.Fatal("no Controller follower found")
	}

	joinAddr := freeTCPAddr(t)
	joining, err := New(Config{
		NodeID:     4,
		ListenAddr: joinAddr,
		DataDir:    t.TempDir(),
		Control:    ControlConfig{ClusterID: "seed-join-follower-sync"},
		Join: JoinConfig{
			Seeds:         []string{followerAddr},
			AdvertiseAddr: joinAddr,
			Token:         "join-secret",
		},
		Slots: SlotConfig{ReplicaCount: 3},
	})
	if err != nil {
		t.Fatalf("New(joining) error = %v", err)
	}
	joinCtx, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer joinCancel()
	if err := joining.Start(joinCtx); err != nil {
		t.Fatalf("Start(joining) error = %v", err)
	}
	t.Cleanup(func() { _ = joining.Stop(context.Background()) })
}

func waitForControllerTasksDrained(t *testing.T, ctx context.Context, nodes ...*Node) {
	t.Helper()
	latest := make([]control.Snapshot, len(nodes))
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		converged := len(nodes) > 0
		var revision uint64
		var clusterID string
		for i, node := range nodes {
			if node == nil {
				converged = false
				continue
			}
			snapshot, err := node.LocalControlSnapshot(ctx)
			if err != nil {
				converged = false
				continue
			}
			latest[i] = snapshot
			if snapshot.ClusterID == "" || snapshot.Revision == 0 || len(snapshot.Tasks) != 0 {
				converged = false
				continue
			}
			if revision == 0 {
				revision = snapshot.Revision
				clusterID = snapshot.ClusterID
				continue
			}
			if snapshot.Revision != revision || snapshot.ClusterID != clusterID {
				converged = false
				continue
			}
		}
		if converged {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("wait for Controller tasks to drain: %v; latest=%+v", ctx.Err(), latest)
		case <-ticker.C:
		}
	}
}

func TestNodeDefaultControllerForwardsControlWriteOverTransport(t *testing.T) {
	addrs := []string{freeTCPAddr(t), freeTCPAddr(t), freeTCPAddr(t)}
	voters := []ControlVoter{
		{NodeID: 1, Addr: addrs[0]},
		{NodeID: 2, Addr: addrs[1]},
		{NodeID: 3, Addr: addrs[2]},
	}
	nodes := make([]*Node, 0, len(voters))
	for _, voter := range voters {
		cfg := Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: t.TempDir()}
		cfg.Control.ClusterID = "node-default-control-write"
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
	err := WaitControllerWriteReady(readyCtx, nodes...)
	readyCancel()
	if err != nil {
		t.Fatalf("WaitControllerWriteReady(initial) error = %v", err)
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
	waitForControllerTasksDrained(t, drainCtx, nodes...)
	drainCancel()

	probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = WaitControllerWriteReady(probeCtx, nodes...)
	probeCancel()
	if err != nil {
		t.Fatalf("WaitControllerWriteReady(post-drain) error = %v", err)
	}

	var follower *control.Runtime
	for _, node := range nodes {
		runtime, ok := node.control.(*control.Runtime)
		if !ok {
			t.Fatalf("node %d control = %T, want *control.Runtime", node.NodeID(), node.control)
		}
		if runtime.LeaderID() != node.NodeID() {
			follower = runtime
			break
		}
	}
	if follower == nil {
		t.Fatal("no follower runtime found")
	}

	result, err := follower.JoinNode(context.Background(), control.JoinNodeRequest{
		NodeID:         4,
		Name:           "node-4",
		Addr:           "n4",
		Roles:          []control.Role{control.RoleData},
		CapacityWeight: 2,
	})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if !result.Created || result.Node.NodeID != 4 || result.Node.JoinState != control.NodeJoinStateJoining {
		t.Fatalf("JoinNode() = %#v, want forwarded joining node creation", result)
	}
}
