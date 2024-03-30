package cluster_test

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster"
	"github.com/stretchr/testify/assert"
)

func TestProposeToSlot(t *testing.T) {

	dataDir := path.Join(os.TempDir(), "cluster")
	fmt.Println("dataDir---->", dataDir)

	initNodes := map[uint64]string{
		1: "127.0.0.1:10001",
		2: "127.0.0.1:10002",
		3: "127.0.0.1:10003",
	}

	var slotCount uint32 = 10

	s1 := newTestServer(t, 1, cluster.WithAddr("127.0.0.1:10001"), cluster.WithSlotCount(slotCount), cluster.WithDataDir(path.Join(dataDir, "1")), cluster.WithInitNodes(initNodes))
	defer s1.Stop()

	s2 := newTestServer(t, 2, cluster.WithAddr("127.0.0.1:10002"), cluster.WithSlotCount(slotCount), cluster.WithDataDir(path.Join(dataDir, "2")), cluster.WithInitNodes(initNodes))
	defer s2.Stop()

	s3 := newTestServer(t, 3, cluster.WithAddr("127.0.0.1:10003"), cluster.WithSlotCount(slotCount), cluster.WithDataDir(path.Join(dataDir, "3")), cluster.WithInitNodes(initNodes))
	defer s3.Stop()

	err := s1.WaitNodeLeader(time.Second * 10)
	assert.NoError(t, err)

	err = s2.WaitNodeLeader(time.Second * 10)
	assert.NoError(t, err)

	err = s3.WaitNodeLeader(time.Second * 10)
	assert.NoError(t, err)

	s1.MustWaitConfigSlotCount(slotCount, time.Second*10)
	s2.MustWaitConfigSlotCount(slotCount, time.Second*10)
	s3.MustWaitConfigSlotCount(slotCount, time.Second*10)

	slotLeader := getSlotLeaderServer(1, s1, s2, s3)
	assert.NotNil(t, slotLeader)

	err = slotLeader.ProposeToSlot(1, []byte("hello"))
	assert.NoError(t, err)

	time.Sleep(time.Second * 2)

	shardNo := cluster.GetSlotShardNo(1)
	logs1, err := s1.Options().ShardLogStorage.Logs(shardNo, 1, 2, 1)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(logs1))
	assert.Equal(t, uint64(1), logs1[0].Index)
	assert.Equal(t, []byte("hello"), logs1[0].Data)

	logs2, err := s2.Options().ShardLogStorage.Logs(shardNo, 1, 2, 1)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(logs2))
	assert.Equal(t, uint64(1), logs2[0].Index)
	assert.Equal(t, []byte("hello"), logs2[0].Data)

	logs3, err := s3.Options().ShardLogStorage.Logs(shardNo, 1, 2, 1)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(logs3))
	assert.Equal(t, uint64(1), logs3[0].Index)
	assert.Equal(t, []byte("hello"), logs3[0].Data)

}

func TestProposeMessageToChannel(t *testing.T) {
	// dataDir := path.Join(os.TempDir(), "cluster")
	// initNodes := map[uint64]string{
	// 	1: "127.0.0.1:10001",
	// 	2: "127.0.0.1:10002",
	// 	3: "127.0.0.1:10003",
	// }

	// var slotCount uint32 = 10

	// s1 := newTestServer(t, 1, cluster.WithAddr("127.0.0.1:10001"), cluster.WithSlotCount(slotCount), cluster.WithDataDir(path.Join(dataDir, "1")), cluster.WithInitNodes(initNodes))
	// defer s1.Stop()

	// s2 := newTestServer(t, 2, cluster.WithAddr("127.0.0.1:10002"), cluster.WithSlotCount(slotCount), cluster.WithDataDir(path.Join(dataDir, "2")), cluster.WithInitNodes(initNodes))
	// defer s2.Stop()

	// s3 := newTestServer(t, 3, cluster.WithAddr("127.0.0.1:10003"), cluster.WithSlotCount(slotCount), cluster.WithDataDir(path.Join(dataDir, "3")), cluster.WithInitNodes(initNodes))
	// defer s3.Stop()

	// err := s1.WaitNodeLeader(time.Second * 10)
	// assert.NoError(t, err)

	// err = s2.WaitNodeLeader(time.Second * 10)
	// assert.NoError(t, err)

	// err = s3.WaitNodeLeader(time.Second * 10)
	// assert.NoError(t, err)

	// s1.MustWaitConfigSlotCount(slotCount, time.Second*10)
	// s2.MustWaitConfigSlotCount(slotCount, time.Second*10)
	// s3.MustWaitConfigSlotCount(slotCount, time.Second*10)

	// channelID := "test"
	// channelType := uint8(2)
	// lastLogIndex, err := s1.ProposeChannelMessage(context.Background(), channelID, channelType, []byte("hello"))
	// assert.NoError(t, err)
	// assert.Equal(t, uint64(1), lastLogIndex)

	// shardNo := cluster.ChannelKey(channelID, channelType)
	// logs1, err := s1.Options().MessageLogStorage.Logs(shardNo, 1, 2, 1)
	// assert.NoError(t, err)
	// assert.Equal(t, 1, len(logs1))
	// assert.Equal(t, uint64(1), logs1[0].Index)
	// assert.Equal(t, []byte("hello"), logs1[0].Data)

	// logs2, err := s2.Options().MessageLogStorage.Logs(shardNo, 1, 2, 1)
	// assert.NoError(t, err)
	// assert.Equal(t, 1, len(logs2))
	// assert.Equal(t, uint64(1), logs2[0].Index)
	// assert.Equal(t, []byte("hello"), logs2[0].Data)

	// logs3, err := s3.Options().MessageLogStorage.Logs(shardNo, 1, 2, 1)
	// assert.NoError(t, err)
	// assert.Equal(t, 1, len(logs3))
	// assert.Equal(t, uint64(1), logs3[0].Index)
	// assert.Equal(t, []byte("hello"), logs3[0].Data)

}

func getSlotLeaderServer(slotId uint32, servers ...*cluster.Server) *cluster.Server {
	for _, s := range servers {
		if s.SlotIsLeader(slotId) {
			return s
		}
	}
	return nil
}

func newTestServer(t *testing.T, nodeId uint64, optList ...cluster.Option) *cluster.Server {
	opts := cluster.NewOptions(optList...)
	opts.NodeID = nodeId
	opts.MessageLogStorage = cluster.NewMemoryShardLogStorage()

	s := cluster.New(nodeId, opts)
	err := s.Start()
	assert.NoError(t, err)
	return s
}
