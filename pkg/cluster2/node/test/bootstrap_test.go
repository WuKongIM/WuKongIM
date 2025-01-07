package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/bootstrap"
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/clusterconfig"
	rafttypes "github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/stretchr/testify/assert"
)

func TestClusterBootstrap(t *testing.T) {

	b1, b2 := newTwoBootstrap(t)
	err := b1.Start()
	assert.NoError(t, err)

	err = b2.Start()
	assert.NoError(t, err)

	defer b1.Stop()
	defer b2.Stop()

	waitAllSlotReady(b1, b2)

}

func newTwoBootstrap(t *testing.T) (*bootstrap.Bootstrap, *bootstrap.Bootstrap) {

	tt := newTestTransport()

	opts1 := newTestOptions(t, 1, map[uint64]string{1: "", 2: ""}, clusterconfig.WithTransport(tt))
	opts2 := newTestOptions(t, 2, map[uint64]string{1: "", 2: ""}, clusterconfig.WithTransport(tt))
	s1 := bootstrap.New(opts1)
	s2 := bootstrap.New(opts2)

	tt.serverMap[1] = s1
	tt.serverMap[2] = s2

	return s1, s2
}

func newTestOptions(t *testing.T, nodeId uint64, initNode map[uint64]string, opt ...clusterconfig.Option) *clusterconfig.Options {

	dir := fmt.Sprintf("%s/%d", t.TempDir(), nodeId)

	fmt.Println("dir:", dir)

	defaultOpts := make([]clusterconfig.Option, 0)
	defaultOpts = append(defaultOpts, clusterconfig.WithNodeId(nodeId), clusterconfig.WithInitNodes(initNode), clusterconfig.WithConfigPath(dir+"/cluster.json"))
	defaultOpts = append(defaultOpts, opt...)
	return clusterconfig.NewOptions(defaultOpts...)
}

type testTransport struct {
	serverMap map[uint64]*bootstrap.Bootstrap
}

func newTestTransport() *testTransport {

	return &testTransport{
		serverMap: make(map[uint64]*bootstrap.Bootstrap),
	}
}

func (t *testTransport) Send(event rafttypes.Event) {
	to := event.To
	r, ok := t.serverMap[to]
	if !ok {
		return
	}
	r.GetConfigServer().StepRaftEvent(event)
}

func waitAllSlotReady(ss ...*bootstrap.Bootstrap) {
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
