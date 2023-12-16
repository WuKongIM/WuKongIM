package cluster_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/assert"
)

// 测试单节点启动和停止
func TestSingleNodeStartAndStop(t *testing.T) {
	node := cluster.NewNode(1, "127.0.0.1:11000")
	err := node.Start()
	assert.NoError(t, err)
	defer node.Stop()
}

// 测试多节点集群
func TestMultiNodeCluster(t *testing.T) {

	initNodes := map[cluster.NodeID]string{
		1: "127.0.0.1:11000",
		2: "127.0.0.1:12000",
		3: "127.0.0.1:13000",
	}
	// start node1
	node1 := cluster.NewNode(1, initNodes[1], cluster.WithInitNodes(initNodes))
	err := node1.Start()
	assert.NoError(t, err)
	defer node1.Stop()

	// start node2
	node2 := cluster.NewNode(2, initNodes[2], cluster.WithInitNodes(initNodes))
	err = node2.Start()
	assert.NoError(t, err)
	defer node2.Stop()

	// start node3
	node3 := cluster.NewNode(3, initNodes[3], cluster.WithInitNodes(initNodes))
	err = node3.Start()
	assert.NoError(t, err)
	defer node3.Stop()

	node1.MustWaitLeader(time.Second * 5)

	node2.MustWaitLeader(time.Second * 5)

	node3.MustWaitLeader(time.Second * 5)

}

// 测试节点自动加入
func TestNodeJoin(t *testing.T) {
	// start node1
	node1 := cluster.NewNode(1, "127.0.0.1:11000")
	err := node1.Start()
	assert.NoError(t, err)
	defer node1.Stop()

	node1.MustWaitLeader(time.Second * 10)

	// add node2
	err = node1.AddReplica(2, "127.0.0.1:12000")
	assert.NoError(t, err)

	// start node2
	node2 := cluster.NewNode(2, "127.0.0.1:12000")
	err = node2.Start(cluster.NodeStartWithJoin(true))
	assert.NoError(t, err)
	defer node2.Stop()

	node2.MustWaitLeader(time.Second * 5)
}

// 测试单节点启动单个slot
func TestSingleNodeStartSlot(t *testing.T) {
	// start node1
	node1 := cluster.NewNode(1, "127.0.0.1:11000")
	err := node1.Start()
	assert.NoError(t, err)
	defer node1.Stop()

	slot, err := node1.AddSlot(1)
	assert.NoError(t, err)

	err = slot.Start()
	assert.NoError(t, err)

	err = slot.WaitLeader(time.Second * 10)
	assert.NoError(t, err)
}

func TestSlotAddReplica(t *testing.T) {

	initNodes := map[cluster.NodeID]string{
		1: "127.0.0.1:11000",
		2: "127.0.0.1:12000",
	}
	// start node1
	node1 := cluster.NewNode(1, initNodes[1], cluster.WithInitNodes(initNodes))
	err := node1.Start()
	assert.NoError(t, err)
	defer node1.Stop()

	// start node2
	node2 := cluster.NewNode(2, initNodes[2], cluster.WithInitNodes(initNodes))
	err = node2.Start()
	assert.NoError(t, err)
	defer node2.Stop()

	// start slot 1
	slot11, err := node1.AddSlot(1)
	assert.NoError(t, err)
	err = slot11.Start()
	assert.NoError(t, err)

	slot11.MustWaitLeader(time.Second * 10)

	// request add slot 1 replica to node 2
	err = slot11.AddReplica(1, 2)
	assert.NoError(t, err)

	// start slot 1 in node 2
	slot21, err := node2.AddSlot(1)
	assert.NoError(t, err)
	err = slot21.Start()
	assert.NoError(t, err)

}
