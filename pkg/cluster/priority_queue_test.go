package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPriorityQueue(t *testing.T) {
	queue := NewPriorityQueue()

	// Insert items in the queue
	queue.Put(ChannelEvent{
		Priority: 1,
		Channel: &Channel{
			channelID:   "test1",
			channelType: 1,
		},
	})
	queue.Put(ChannelEvent{
		Priority: 20,
		Channel: &Channel{
			channelID:   "test2",
			channelType: 1,
		},
	})

	ch3 := &Channel{
		channelID:   "test3",
		channelType: 1,
	}
	queue.Put(ChannelEvent{
		Priority: 11,
		Channel:  ch3,
	})

	item := queue.Get()
	assert.Equal(t, "test2", item.Channel.channelID)

	item = queue.Get()
	assert.Equal(t, "test3", item.Channel.channelID)

	item = queue.Get()
	assert.Equal(t, "test1", item.Channel.channelID)
}
