package clusterevent_test

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterevent"
	"github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"
	"github.com/stretchr/testify/assert"
)

func TestClusterEventStartAndStop(t *testing.T) {
	opts := clusterevent.NewOptions()
	opts.NodeID = 1
	opts.DataDir = path.Join(os.TempDir(), "cluster_event_manager_test")
	opts.InitNodes = map[uint64]string{
		1: "127.0.0.1:10001",
		2: "127.0.0.1:10002",
		3: "127.0.0.1:10003",
		4: "127.0.0.1:10004",
	}
	fmt.Println(opts.DataDir)
	defer os.RemoveAll(opts.DataDir)
	c := clusterevent.NewClusterEventManager(opts)
	c.SetNodeLeaderID(1)

	err := c.Start()
	assert.NoError(t, err)
	defer c.Stop()

	clusterConfig := c.GetClusterConfig()
	assert.Equal(t, 4, len(clusterConfig.Nodes))

}

func TestSlotEventElection(t *testing.T) {
	opts := clusterevent.NewOptions()
	opts.NodeID = 1
	opts.SlotCount = 10
	opts.DataDir = path.Join(os.TempDir(), "cluster_event_manager_test")
	opts.Heartbeat = time.Millisecond * 100
	opts.InitNodes = map[uint64]string{
		1: "127.0.0.1:10001",
		2: "127.0.0.1:10002",
		3: "127.0.0.1:10003",
		4: "127.0.0.1:10004",
	}
	fmt.Println(opts.DataDir)
	defer os.RemoveAll(opts.DataDir)

	c := clusterevent.NewClusterEventManager(opts)
	c.SetNodeLeaderID(1) // 设置节点1为领导

	err := c.Start()
	assert.NoError(t, err)
	defer c.Stop()

	var clusterEvt clusterevent.ClusterEvent
	for clusterEvt = range c.Watch() {
		if clusterEvt.SlotEvent != nil {
			if pb.SlotEventType_SlotEventTypeInit == clusterEvt.SlotEvent.EventType {
				break
			}
		}
	}

	for _, st := range clusterEvt.SlotEvent.Slots {
		c.AddOrUpdateSlotNoSave(st)
	}
	c.SaveAndVersionInc()

	c.SetNodeOnline(2, false) // 设置节点2下线

	// 设置其他节点配置为最新
	c.SetNodeConfigVersion(1, 2)
	c.SetNodeConfigVersion(2, 2)
	c.SetNodeConfigVersion(3, 2)

	for clusterEvt = range c.Watch() {
		if clusterEvt.SlotEvent != nil {
			if pb.SlotEventType_SlotEventTypeElection == clusterEvt.SlotEvent.EventType {
				break
			}
		}
	}
}
