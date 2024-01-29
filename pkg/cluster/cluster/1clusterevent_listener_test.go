package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClusterEventInitNode(t *testing.T) {
	opts := NewOptions()
	opts.NodeID = 1
	opts.DataDir = t.TempDir()
	opts.InitNodes = map[uint64]string{
		1: "127.0.0.1:10001",
	}
	cl := newClusterEventListener(opts)
	err := cl.start()
	assert.NoError(t, err)
	defer cl.stop()

	event := cl.wait()
	has, nodeAddMsg := getEventMessageByType(ClusterEventTypeNodeAdd, event.Messages)
	assert.True(t, has)
	assert.Equal(t, 1, len(nodeAddMsg.Nodes))

	has, slotAddMsg := getEventMessageByType(ClusterEventTypeSlotAdd, event.Messages)
	assert.True(t, has)
	assert.Equal(t, int(opts.SlotCount), len(slotAddMsg.Slots))

}

func getEventMessageByType(tp ClusterEventType, msgs []EventMessage) (bool, EventMessage) {
	for _, msg := range msgs {
		if msg.Type == tp {
			return true, msg
		}
	}
	return false, EmptyEventMessage
}
