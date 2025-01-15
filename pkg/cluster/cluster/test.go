package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	rafttypes "github.com/WuKongIM/WuKongIM/pkg/raft/types"
)

func newTwoServer(t *testing.T) (*Server, *Server) {

	// tt := newTestTransport()

	// nodeTrs := newTestNodeTransport()

	opts1 := newTestOptions(t, 1, map[uint64]string{1: "127.0.0.1:11111", 2: "127.0.0.1:11112"}, clusterconfig.WithApiServerAddr("http://test1.com"))
	opts2 := newTestOptions(t, 2, map[uint64]string{1: "127.0.0.1:11111", 2: "127.0.0.1:11112"}, clusterconfig.WithApiServerAddr("http://test2.com"))
	s1 := New(NewOptions(WithAddr("tcp://127.0.0.1:11111"), WithConfigOptions(opts1), WithDataDir(fmt.Sprintf("%s/%d", t.TempDir(), 1))))
	s2 := New(NewOptions(WithAddr("tcp://127.0.0.1:11112"), WithConfigOptions(opts2), WithDataDir(fmt.Sprintf("%s/%d", t.TempDir(), 2))))

	return s1, s2
}

func newThreeBootstrap(t *testing.T) (*Server, *Server, *Server) {

	opts1 := newTestOptions(t, 1, map[uint64]string{1: "127.0.0.1:11111", 2: "127.0.0.1:11112", 3: "127.0.0.1:11113"}, clusterconfig.WithPongMaxTick(5), clusterconfig.WithApiServerAddr("http://test1.com"))
	opts2 := newTestOptions(t, 2, map[uint64]string{1: "127.0.0.1:11111", 2: "127.0.0.1:11112", 3: "127.0.0.1:11113"}, clusterconfig.WithPongMaxTick(5), clusterconfig.WithApiServerAddr("http://test2.com"))
	opts3 := newTestOptions(t, 3, map[uint64]string{1: "127.0.0.1:11111", 2: "127.0.0.1:11112", 3: "127.0.0.1:11113"}, clusterconfig.WithPongMaxTick(5), clusterconfig.WithApiServerAddr("http://test3.com"))
	s1 := New(NewOptions(WithAddr("127.0.0.1:11111"), WithConfigOptions(opts1), WithDataDir(fmt.Sprintf("%s/%d", t.TempDir(), 1))))
	s2 := New(NewOptions(WithAddr("127.0.0.1:11112"), WithConfigOptions(opts2), WithDataDir(fmt.Sprintf("%s/%d", t.TempDir(), 2))))
	s3 := New(NewOptions(WithAddr("127.0.0.1:11113"), WithConfigOptions(opts3), WithDataDir(fmt.Sprintf("%s/%d", t.TempDir(), 3))))

	return s1, s2, s3
}

func newTestOptions(t *testing.T, nodeId uint64, initNode map[uint64]string, opt ...clusterconfig.Option) *clusterconfig.Options {

	dir := fmt.Sprintf("%s/%d", t.TempDir(), nodeId)

	defaultOpts := make([]clusterconfig.Option, 0)
	defaultOpts = append(defaultOpts, clusterconfig.WithNodeId(nodeId), clusterconfig.WithInitNodes(initNode), clusterconfig.WithConfigPath(dir+"/cluster.json"))
	defaultOpts = append(defaultOpts, opt...)
	return clusterconfig.NewOptions(defaultOpts...)
}

type testTransport struct {
	serverMap map[uint64]*Server
}

func newTestTransport() *testTransport {

	return &testTransport{
		serverMap: make(map[uint64]*Server),
	}
}

func (t *testTransport) Send(key string, event rafttypes.Event) {
	to := event.To
	r, ok := t.serverMap[to]
	if !ok {
		return
	}
	r.AddSlotEvent(key, event)
}

type testNodeTransport struct {
	serverMap map[uint64]*Server
}

func newTestNodeTransport() *testNodeTransport {

	return &testNodeTransport{
		serverMap: make(map[uint64]*Server),
	}
}

func (t *testNodeTransport) Send(event rafttypes.Event) {
	to := event.To
	r, ok := t.serverMap[to]
	if !ok {
		return
	}
	r.NodeStep(event)
}

type testEvent struct {
}

func (t *testEvent) OnSlotElection(slots []*types.Slot) error {
	return nil
}

func (t *testEvent) OnConfigChange(cfg *types.Config) {

}

func waitAllSlotReady(ss ...*Server) {
	timeoutctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-timeoutctx.Done():

			return
		default:
			count := 0
			for _, s := range ss {
				if len(s.GetConfigServer().GetClusterConfig().Slots) == int(s.GetConfigServer().Options().SlotCount) {
					count++
				}
			}
			if count == len(ss) {
				return
			}

			time.Sleep(time.Millisecond * 10)
		}
	}
}

func waitApiServerAddr(ss ...*Server) {
	timeoutctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-timeoutctx.Done():
			panic("timeout")
		default:
			count := 0
			for _, s := range ss {
				node := s.GetConfigServer().Node(s.GetConfigServer().Options().NodeId)
				if node != nil && node.ApiServerAddr != "" {
					count++
				}
			}
			if count == len(ss) {
				return
			}

			time.Sleep(time.Millisecond * 10)
		}
	}
}

func waitNodeOffline(offlineNodeId uint64, ss ...*Server) {
	timeoutctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-timeoutctx.Done():
			panic("timeout")
		default:
			count := 0
			for _, s := range ss {
				node := s.GetConfigServer().Node(offlineNodeId)
				if node != nil && !node.Online {
					count++
				}
			}
			if count == len(ss) {
				return
			}

			time.Sleep(time.Millisecond * 10)
		}
	}
}

func waitNodeOnline(onlineNodeId uint64, ss ...*Server) {
	timeoutctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-timeoutctx.Done():
			panic("timeout")
		default:
			count := 0
			for _, s := range ss {
				node := s.GetConfigServer().Node(onlineNodeId)
				if node != nil && node.Online {
					count++
				}
			}
			if count == len(ss) {
				return
			}

			time.Sleep(time.Millisecond * 10)
		}
	}
}

// 等待是有槽的领导节点都不是指定的节点id
func waitSlotNotLeader(notLeaderNodeId uint64, ss ...*Server) {
	timeoutctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-timeoutctx.Done():
			panic("timeout")
		default:
			hasLeader := false
			for _, s := range ss {
				for _, slot := range s.GetConfigServer().Slots() {
					if slot.Leader == notLeaderNodeId {
						hasLeader = true
						break
					}
				}
				if hasLeader {
					break
				}
			}
			if !hasLeader {
				return
			}

			time.Sleep(time.Millisecond * 10)
		}
	}
}

func start(t *testing.T, ss ...*Server) {
	for _, s := range ss {
		err := s.Start()
		if err != nil {
			t.Fatal(err)
		}
	}
}
