package cluster

import (
	"testing"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/stretchr/testify/assert"
)

func TestChannelListener(t *testing.T) {
	opts := NewOptions()
	opts.NodeID = 1
	opts.ShardLogStorage = NewMemoryShardLogStorage()

	lis := NewChannelListener(opts)
	err := lis.Start()
	assert.NoError(t, err)
	defer lis.Stop()

	channelID := "test"
	channelType := uint8(2)
	ch := NewChannel(channelID, channelType, 0, []uint64{1, 2, 3}, opts)

	lis.Add(ch)

	err = ch.AppointLeader(10001, 1)
	assert.NoError(t, err)

	for {
		ready := lis.Wait()
		if ready.channel == nil {
			continue
		}
		for _, msg := range ready.Messages {
			if msg.MsgType == replica.MsgAppointLeaderResp {
				return
			}
		}
	}
}
