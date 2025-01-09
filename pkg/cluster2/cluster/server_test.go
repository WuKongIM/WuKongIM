package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// 测试分布式启动
func TestClusterServerStart(t *testing.T) {

	b1, b2 := newTwoServer(t)
	err := b1.Start()
	assert.NoError(t, err)

	err = b2.Start()
	assert.NoError(t, err)

	defer b1.Stop()
	defer b2.Stop()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	err = b1.WaitAllSlotReady(timeoutCtx, 64)
	assert.NoError(t, err)

	err = b2.WaitAllSlotReady(timeoutCtx, 64)
	assert.NoError(t, err)

}

// 测试提案配置
func TestProposeApiServerAddr(t *testing.T) {

	b1, b2 := newTwoServer(t)
	err := b1.Start()
	assert.NoError(t, err)

	err = b2.Start()
	assert.NoError(t, err)

	defer b1.Stop()
	defer b2.Stop()

	waitApiServerAddr(b1, b2)

}

// 测试节点下线
func TestNodeOffline(t *testing.T) {
	b1, b2, b3 := newThreeBootstrap(t)
	start(t, b1, b2, b3)

	// 等待完全启动
	waitApiServerAddr(b1, b2, b3)

	// 关闭节点1
	b1.Stop()

	// 等待节点1下线
	waitNodeOffline(b1.GetConfigServer().Options().NodeId, b2, b3)
}

func TestNodeOfflineAndOnline(t *testing.T) {
	b1, b2, b3 := newThreeBootstrap(t)
	start(t, b1, b2, b3)

	// 等待完全启动
	waitApiServerAddr(b1, b2, b3)

	// 关闭节点1
	b1.Stop()

	nodeId := b1.GetConfigServer().Options().NodeId
	// 等待节点1下线
	waitNodeOffline(nodeId, b2, b3)

	// 重新启动节点1
	err := b1.Start()
	assert.NoError(t, err)

	// 等待节点1上线
	waitNodeOnline(nodeId, b2, b3)

}

func TestProposeToSlot(t *testing.T) {
	b1, b2 := newTwoServer(t)
	err := b1.Start()
	assert.NoError(t, err)

	err = b2.Start()
	assert.NoError(t, err)

	defer b1.Stop()
	defer b2.Stop()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	err = b1.WaitAllSlotReady(timeoutCtx, 64)
	assert.NoError(t, err)

	err = b2.WaitAllSlotReady(timeoutCtx, 64)
	assert.NoError(t, err)

	resp, err := b2.ProposeToSlotUntilApplied(1, []byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), resp.Index)
}
