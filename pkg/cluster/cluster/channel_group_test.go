package cluster_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster"
	"github.com/stretchr/testify/assert"
)

func TestChannelGroup(t *testing.T) {

	trans := cluster.NewMemoryTransport()

	storage1 := cluster.NewMemoryShardLogStorage()
	storage2 := cluster.NewMemoryShardLogStorage()

	// 创建集群
	opts := cluster.NewOptions()

	opts.NodeID = 1
	opts.ShardLogStorage = storage1
	opts.Transport = trans
	opts.ChannelGroupScanInterval = time.Second * 5

	// cg 1
	cg1 := cluster.NewChannelGroup(opts)
	err := cg1.Start()
	assert.NoError(t, err)
	defer cg1.Stop()

	ch1 := cluster.NewChannel(&cluster.ChannelClusterConfig{
		ChannelID:   "ch1",
		ChannelType: 1,
		Replicas:    []uint64{1, 2},
	}, 0, opts)
	cg1.Add(ch1)

	err = ch1.AppointLeader(1001, 1)
	assert.NoError(t, err)

	trans.OnNodeMessage(1, func(m cluster.Message) {
		fmt.Println("node 1 receive message", m.MsgType.String())
		channelID, channelType := cluster.ChannelFromChannelKey(m.ShardNo)
		err = cg1.HandleMessage(channelID, channelType, m)
		assert.NoError(t, err)
	})

	// cg 2
	opts = cluster.NewOptions()
	opts.NodeID = 2
	opts.ShardLogStorage = storage2
	opts.Transport = trans
	opts.ChannelGroupScanInterval = time.Second * 5
	cg2 := cluster.NewChannelGroup(opts)
	err = cg2.Start()
	assert.NoError(t, err)
	defer cg2.Stop()

	trans.OnNodeMessage(2, func(m cluster.Message) {
		fmt.Println("node 2 receive message", m.MsgType.String())
		channelID, channelType := cluster.ChannelFromChannelKey(m.ShardNo)
		err = cg2.HandleMessage(channelID, channelType, m)
		assert.NoError(t, err)
	})

	ch2 := cluster.NewChannel(&cluster.ChannelClusterConfig{
		ChannelID:   "ch1",
		ChannelType: 1,
		Replicas:    []uint64{1, 2},
	}, 0, opts)
	cg2.Add(ch2)
	err = ch2.AppointLeaderTo(1001, 1, 1)
	assert.NoError(t, err)

	_, err = ch1.ProposeAndWaitCommit([]byte("hello world"), time.Second*30)
	assert.NoError(t, err)

	logs1, err := storage1.Logs(ch1.ChannelKey(), 1, 2, 1)
	assert.NoError(t, err)

	logs2, err := storage2.Logs(ch2.ChannelKey(), 1, 2, 1)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(logs1))
	assert.Equal(t, logs1, logs2)
	assert.Equal(t, []byte("hello world"), logs1[0].Data, logs2[0].Data)

}
