package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

// 测试分布式启动
func TestClusterServerStart(t *testing.T) {

	s1, s2 := newTwoServer(t)
	err := s1.Start()
	assert.NoError(t, err)

	err = s2.Start()
	assert.NoError(t, err)

	defer s1.Stop()
	defer s2.Stop()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	err = s1.WaitAllSlotReady(timeoutCtx, 64)
	assert.NoError(t, err)

	err = s2.WaitAllSlotReady(timeoutCtx, 64)
	assert.NoError(t, err)

}

// 测试提案配置
func TestProposeApiServerAddr(t *testing.T) {

	s1, s2 := newTwoServer(t)
	err := s1.Start()
	assert.NoError(t, err)

	err = s2.Start()
	assert.NoError(t, err)

	defer s1.Stop()
	defer s2.Stop()

	waitApiServerAddr(s1, s2)

}

// 测试节点下线
func TestNodeOffline(t *testing.T) {
	s1, s2, s3 := newThreeBootstrap(t)
	start(t, s1, s2, s3)

	// 等待完全启动
	waitApiServerAddr(s1, s2, s3)

	// 关闭节点1
	s1.Stop()

	// 等待节点1下线
	waitNodeOffline(s1.GetConfigServer().Options().NodeId, s2, s3)
}

func TestNodeOfflineAndOnline(t *testing.T) {
	s1, s2, s3 := newThreeBootstrap(t)
	start(t, s1, s2, s3)

	// 等待完全启动
	waitApiServerAddr(s1, s2, s3)

	// 关闭节点1
	s1.Stop()

	nodeId := s1.GetConfigServer().Options().NodeId
	// 等待节点1下线
	waitNodeOffline(nodeId, s2, s3)

	// 重新启动节点1
	err := s1.Start()
	assert.NoError(t, err)

	// 等待节点1上线
	waitNodeOnline(nodeId, s2, s3)

}

func TestProposeToSlot(t *testing.T) {
	s1, s2 := newTwoServer(t)
	err := s1.Start()
	assert.NoError(t, err)

	err = s2.Start()
	assert.NoError(t, err)

	defer s1.Stop()
	defer s2.Stop()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	err = s1.WaitAllSlotReady(timeoutCtx, 64)
	assert.NoError(t, err)

	err = s2.WaitAllSlotReady(timeoutCtx, 64)
	assert.NoError(t, err)

	resp, err := s2.ProposeToSlotUntilApplied(1, []byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), resp.Index)
}

func TestAppendMessages(t *testing.T) {
	s1, s2 := newTwoServer(t)
	err := s1.Start()
	assert.NoError(t, err)

	err = s2.Start()
	assert.NoError(t, err)

	defer s1.Stop()
	defer s2.Stop()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	err = s1.WaitAllSlotReady(timeoutCtx, 64)
	assert.NoError(t, err)

	err = s2.WaitAllSlotReady(timeoutCtx, 64)
	assert.NoError(t, err)

	timeoutCtx, cancel = context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	var messages = make([]wkdb.Message, 0)
	messages = append(messages, wkdb.Message{
		RecvPacket: wkproto.RecvPacket{
			ChannelID:   "ch1",
			ChannelType: 1,
			Payload:     []byte("hello"),
		},
		Term: 1,
	})

	start := time.Now()
	fmt.Println("append message start")
	_, err = s1.GetStore().AppendMessages(timeoutCtx, "ch1", 2, messages)
	assert.NoError(t, err)

	cost := time.Since(start)
	fmt.Println("cost--->", cost)

}
