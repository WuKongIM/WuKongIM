package clusterconfig_test

import (
	"context"
	"fmt"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/stretchr/testify/assert"
)

func TestServerAddOrUpdateNodes(t *testing.T) {

	s1, s2, s3 := newTestClusterServerThree(t)
	defer s1.Stop()
	defer s2.Stop()
	defer s3.Stop()

	var leaderNode *clusterconfig.Server
	if s1.IsLeader() {
		leaderNode = s1
	} else if s2.IsLeader() {
		leaderNode = s2
	} else if s3.IsLeader() {
		leaderNode = s3
	}

	err := leaderNode.AddOrUpdateNodes([]*pb.Node{
		{
			Id:            1,
			ApiServerAddr: "hello",
		},
		{
			Id:            2,
			ApiServerAddr: "hello2",
		},
	})
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 500)

	nodes1 := s1.ConfigManager().GetConfig().GetNodes()
	nodes2 := s2.ConfigManager().GetConfig().GetNodes()
	nodes3 := s3.ConfigManager().GetConfig().GetNodes()

	assert.Equal(t, 2, len(nodes1))
	assert.Equal(t, 2, len(nodes2))
	assert.Equal(t, 2, len(nodes3))

	assert.Equal(t, uint64(1), nodes1[0].Id)
	assert.Equal(t, uint64(2), nodes1[1].Id)

	assert.Equal(t, uint64(1), nodes2[0].Id)
	assert.Equal(t, uint64(2), nodes2[1].Id)

	assert.Equal(t, uint64(1), nodes3[0].Id)
	assert.Equal(t, uint64(2), nodes3[1].Id)

	assert.Equal(t, []byte("hello"), nodes1[0].ApiServerAddr)
	assert.Equal(t, []byte("hello2"), nodes1[1].ApiServerAddr)

	assert.Equal(t, []byte("hello"), nodes2[0].ApiServerAddr)
	assert.Equal(t, []byte("hello2"), nodes2[1].ApiServerAddr)

	assert.Equal(t, []byte("hello"), nodes3[0].ApiServerAddr)
	assert.Equal(t, []byte("hello2"), nodes3[1].ApiServerAddr)

}

func newTestClusterServerThree(t *testing.T) (*clusterconfig.Server, *clusterconfig.Server, *clusterconfig.Server) {
	trans := newTestTransport()

	dataDir := t.TempDir()

	fmt.Println("dataDir---->", dataDir)

	electionTimeout := 5

	s1 := clusterconfig.New(1, clusterconfig.WithElectionTimeoutTick(electionTimeout), clusterconfig.WithTransport(trans), clusterconfig.WithConfigPath(path.Join(dataDir, "1", "clusterconfig.json")), clusterconfig.WithReplicas([]uint64{1, 2, 3}))
	err := s1.Start()
	assert.NoError(t, err)

	trans.OnNodeMessage(1, func(msg clusterconfig.Message) {
		err := s1.Step(context.Background(), msg)
		assert.NoError(t, err)
	})

	s2 := clusterconfig.New(2, clusterconfig.WithElectionTimeoutTick(electionTimeout), clusterconfig.WithTransport(trans), clusterconfig.WithConfigPath(path.Join(dataDir, "2", "clusterconfig.json")), clusterconfig.WithReplicas([]uint64{1, 2, 3}))
	err = s2.Start()
	assert.NoError(t, err)

	trans.OnNodeMessage(2, func(msg clusterconfig.Message) {
		err := s2.Step(context.Background(), msg)
		assert.NoError(t, err)
	})

	s3 := clusterconfig.New(3, clusterconfig.WithElectionTimeoutTick(electionTimeout), clusterconfig.WithTransport(trans), clusterconfig.WithConfigPath(path.Join(dataDir, "3", "clusterconfig.json")), clusterconfig.WithReplicas([]uint64{1, 2, 3}))
	err = s3.Start()
	assert.NoError(t, err)

	trans.OnNodeMessage(3, func(msg clusterconfig.Message) {
		err := s3.Step(context.Background(), msg)
		assert.NoError(t, err)
	})

	s1.MustWaitLeader()
	s2.MustWaitLeader()
	s3.MustWaitLeader()

	return s1, s2, s3

}

type testTransport struct {
	nodeOnMessageMap map[uint64]func(msg clusterconfig.Message)
}

func newTestTransport() *testTransport {
	return &testTransport{
		nodeOnMessageMap: make(map[uint64]func(msg clusterconfig.Message)),
	}
}

func (t *testTransport) Send(msg clusterconfig.Message) error {
	if f, ok := t.nodeOnMessageMap[msg.To]; ok {
		go f(msg)
	}
	return nil
}

func (t *testTransport) OnMessage(f func(msg clusterconfig.Message)) {
}

func (t *testTransport) OnNodeMessage(nodeId uint64, f func(msg clusterconfig.Message)) {
	t.nodeOnMessageMap[nodeId] = f
}
