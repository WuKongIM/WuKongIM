package cluster

import (
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
)

func TestChannelGroup(t *testing.T) {

	trans := NewMemoryTransport()

	storage1 := NewMemoryShardLogStorage()
	storage2 := NewMemoryShardLogStorage()

	// 创建集群
	opts := NewOptions()

	opts.NodeID = 1
	opts.ShardLogStorage = storage1
	opts.Transport = trans
	opts.ChannelGroupScanInterval = time.Second * 5

	// cg 1
	cg1 := newChannelGroup(opts)
	err := cg1.start()
	assert.NoError(t, err)
	defer cg1.stop()

	ch1 := newChannel(&ChannelClusterConfig{
		ChannelID:   "ch1",
		ChannelType: 1,
		Replicas:    []uint64{1, 2},
	}, 0, opts)
	cg1.add(ch1)

	err = ch1.appointLeader(1001, 1)
	assert.NoError(t, err)

	trans.OnNodeMessage(1, func(msg *proto.Message) {
		m, _ := NewMessageFromProto(msg)
		fmt.Println("node 1 receive message", m.MsgType.String())
		channelID, channelType := ChannelFromChannelKey(m.ShardNo)
		err = cg1.handleMessage(channelID, channelType, m)
		assert.NoError(t, err)
	})

	// cg 2
	opts = NewOptions()
	opts.NodeID = 2
	opts.ShardLogStorage = storage2
	opts.Transport = trans
	opts.ChannelGroupScanInterval = time.Second * 5
	cg2 := newChannelGroup(opts)
	err = cg2.start()
	assert.NoError(t, err)
	defer cg2.stop()

	trans.OnNodeMessage(2, func(msg *proto.Message) {
		m, _ := NewMessageFromProto(msg)
		fmt.Println("node 2 receive message", m.MsgType.String())
		channelID, channelType := ChannelFromChannelKey(m.ShardNo)
		err = cg2.handleMessage(channelID, channelType, m)
		assert.NoError(t, err)
	})

	ch2 := newChannel(&ChannelClusterConfig{
		ChannelID:   "ch1",
		ChannelType: 1,
		Replicas:    []uint64{1, 2},
	}, 0, opts)
	cg2.add(ch2)
	err = ch2.appointLeaderTo(1001, 1, 1)
	assert.NoError(t, err)

	_, err = ch1.proposeAndWaitCommit([]byte("hello world"), time.Second*30)
	assert.NoError(t, err)

	logs1, err := storage1.Logs(ch1.channelKey(), 1, 2, 1)
	assert.NoError(t, err)

	logs2, err := storage2.Logs(ch2.channelKey(), 1, 2, 1)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(logs1))
	assert.Equal(t, logs1, logs2)
	assert.Equal(t, []byte("hello world"), logs1[0].Data, logs2[0].Data)

}
