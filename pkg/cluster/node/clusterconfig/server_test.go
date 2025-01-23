package clusterconfig_test

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
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
	resp, err := leader.ProposeUntilAppliedTimeout(timeoutCtx, 1, []byte("test"))
	assert.NoError(t, err)

	assert.Equal(t, uint64(1), resp.Id)
	assert.Equal(t, uint64(1), resp.Index)

}

func newTwoNodes(t *testing.T) (*clusterconfig.Server, *clusterconfig.Server) {

	tt := newTestTransport()

	opts1 := newTestOptions(t, 1, map[uint64]string{1: "", 2: ""}, clusterconfig.WithTransport(tt))
	opts2 := newTestOptions(t, 2, map[uint64]string{1: "", 2: ""}, clusterconfig.WithTransport(tt))
	s1 := clusterconfig.New(opts1)
	s2 := clusterconfig.New(opts2)

	tt.serverMap[1] = s1
	tt.serverMap[2] = s2

	return s1, s2
}

func newTestOptions(t *testing.T, nodeId uint64, initNode map[uint64]string, opt ...clusterconfig.Option) *clusterconfig.Options {
	defaultOpts := make([]clusterconfig.Option, 0)
	defaultOpts = append(defaultOpts, clusterconfig.WithNodeId(nodeId), clusterconfig.WithInitNodes(initNode), clusterconfig.WithConfigPath(t.TempDir()+"/cluster.json"))
	defaultOpts = append(defaultOpts, opt...)
	return clusterconfig.NewOptions(defaultOpts...)
}

type testTransport struct {
	serverMap map[uint64]*clusterconfig.Server
}

func newTestTransport() *testTransport {

	return &testTransport{
		serverMap: make(map[uint64]*clusterconfig.Server),
	}
}

func (t *testTransport) Send(event types.Event) {
	to := event.To
	r, ok := t.serverMap[to]
	if !ok {
		return
	}
	r.StepRaftEvent(event)
}

func waitHasLeader(ctx context.Context, servers ...*clusterconfig.Server) bool {
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

func getLeader(servers ...*clusterconfig.Server) *clusterconfig.Server {
	for _, s := range servers {
		if s.IsLeader() {
			return s
		}
	}
	return nil
}
