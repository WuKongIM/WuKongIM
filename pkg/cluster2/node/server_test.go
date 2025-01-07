package node_test

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node"
	"github.com/stretchr/testify/assert"
)

func TestServerPropose(t *testing.T) {
	s1, s2 := newTwoNodes(t)
	err := s1.Start()
	assert.NoError(t, err)
	err = s2.Start()
	assert.NoError(t, err)

	defer s1.Stop()
	defer s2.Stop()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	waitHasLeader(timeoutCtx, s1, s2)

	leader := getLeader(s1, s2)
	assert.NotNil(t, leader)

	// propose
	timeoutCtx, cancel = context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	resp, err := leader.ProposeUntilApplied(timeoutCtx, 1, []byte("test"))
	assert.NoError(t, err)

	assert.Equal(t, uint64(1), resp.Id)
	assert.Equal(t, uint64(1), resp.Index)

}

func newTwoNodes(t *testing.T) (*node.Server, *node.Server) {

	tt := newTestTransport()

	opts1 := newTestOptions(t, 1, map[uint64]string{1: "", 2: ""}, node.WithTransport(tt))
	opts2 := newTestOptions(t, 2, map[uint64]string{1: "", 2: ""}, node.WithTransport(tt))
	s1 := node.New(opts1)
	s2 := node.New(opts2)

	tt.serverMap[1] = s1
	tt.serverMap[2] = s2

	return s1, s2
}

func newTestOptions(t *testing.T, nodeId uint64, initNode map[uint64]string, opt ...node.Option) *node.Options {
	defaultOpts := make([]node.Option, 0)
	defaultOpts = append(defaultOpts, node.WithNodeId(nodeId), node.WithInitNodes(initNode), node.WithConfigPath(t.TempDir()+"/cluster.json"))
	defaultOpts = append(defaultOpts, opt...)
	return node.NewOptions(defaultOpts...)
}

type testTransport struct {
	serverMap map[uint64]*node.Server
}

func newTestTransport() *testTransport {

	return &testTransport{
		serverMap: make(map[uint64]*node.Server),
	}
}

func (t *testTransport) Send(event node.Event) {
	to := event.To
	if event.Type == node.RaftEvent {
		to = event.Event.To
	}
	r, ok := t.serverMap[to]
	if !ok {
		return
	}
	r.AddEvent(event)
}

func waitHasLeader(ctx context.Context, servers ...*node.Server) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		count := 0
		for _, s := range servers {
			if s.LeaderId() != 0 {
				count++
			}
		}
		if count == len(servers) {
			return true
		}
		time.Sleep(time.Millisecond * 10)
	}
}

func getLeader(servers ...*node.Server) *node.Server {
	for _, s := range servers {
		if s.IsLeader() {
			return s
		}
	}
	return nil
}
