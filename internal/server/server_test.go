package server

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestServerStart(t *testing.T) {
	s := NewTestServer(t)
	s.opts.Mode = TestMode
	err := s.Start()
	assert.Nil(t, err)
	err = s.Stop()
	assert.Nil(t, err)
}

// 测试单节点发送消息
func TestSingleSendMessage(t *testing.T) {
	s := NewTestServer(t)
	s.opts.Mode = TestMode
	err := s.Start()
	assert.Nil(t, err)
	defer s.StopNoErr()

	s.MustWaitClusterReady() // 等待服务准备好

	// new client 1
	cli1 := client.New(s.opts.External.TCPAddr, client.WithUID("test1"))
	err = cli1.Connect()
	assert.Nil(t, err)

	// new client 2
	cli2 := client.New(s.opts.External.TCPAddr, client.WithUID("test2"))
	err = cli2.Connect()
	assert.Nil(t, err)

	// send message
	err = cli1.SendMessage(client.NewChannel("test2", 1), []byte("hello"))
	assert.Nil(t, err)

	var wait sync.WaitGroup
	wait.Add(1)

	// cli2 recv
	cli2.SetOnRecv(func(recv *wkproto.RecvPacket) error {
		assert.Equal(t, "hello", string(recv.Payload))
		wait.Done()
		return nil
	})

	wait.Wait()

}

func TestClusterSendMessage(t *testing.T) {
	s1, s2 := NewTestClusterServerTwoNode(t)
	err := s1.Start()
	assert.Nil(t, err)

	err = s2.Start()
	assert.Nil(t, err)

	defer s1.StopNoErr()
	defer s2.StopNoErr()

	MustWaitClusterReady(s1, s2)

	// new client 1
	cli1 := client.New(s1.opts.External.TCPAddr, client.WithUID("test1"))
	err = cli1.Connect()
	assert.Nil(t, err)

	// new client 2
	cli2 := client.New(s2.opts.External.TCPAddr, client.WithUID("test2"))
	err = cli2.Connect()
	assert.Nil(t, err)

	// send message to test2
	err = cli1.SendMessage(client.NewChannel("test2", 1), []byte("hello"))
	assert.Nil(t, err)

	var wait sync.WaitGroup
	wait.Add(1)

	// cli2 recv
	cli2.SetOnRecv(func(recv *wkproto.RecvPacket) error {
		assert.Equal(t, "hello", string(recv.Payload))
		wait.Done()
		return nil
	})

	wait.Wait()
}

func TestClusterSlotMigrate(t *testing.T) {
	s1, s2 := NewTestClusterServerTwoNode(t)
	err := s1.Start()
	assert.Nil(t, err)

	err = s2.Start()
	assert.Nil(t, err)

	defer s1.StopNoErr()
	defer s2.StopNoErr()

	MustWaitClusterReady(s1, s2)

	leaderServer := GetLeaderServer(s1, s2)
	assert.NotNil(t, leaderServer)

	cfg := s1.GetClusterConfig()

	var migrateSlot *pb.Slot
	for _, slot := range cfg.Slots {
		if slot.Leader == s1.opts.Cluster.NodeId {
			migrateSlot = slot
			break
		}
	}
	assert.NotNil(t, migrateSlot)

	fmt.Println("migrateSlot---->", migrateSlot.Id)

	// 迁移slot
	err = leaderServer.MigrateSlot(migrateSlot.Id, s1.opts.Cluster.NodeId, s2.opts.Cluster.NodeId)
	assert.Nil(t, err)

	tk := time.NewTicker(time.Millisecond * 10)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-tk.C:

			cfg = s1.GetClusterConfig()
			for _, slot := range cfg.Slots {
				if slot.Id == migrateSlot.Id {
					if slot.Leader == s2.opts.Cluster.NodeId {
						return
					}
				}
			}

		case <-timeoutCtx.Done():
			assert.Nil(t, timeoutCtx.Err())
			return
		}
	}

}
