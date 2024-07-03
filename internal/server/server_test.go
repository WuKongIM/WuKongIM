package server

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
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

// 槽迁移，从追随者迁移到leader
func TestClusterSlotMigrateForFollowToLeader(t *testing.T) {
	s1, s2 := NewTestClusterServerTwoNode(t, WithClusterSlotReplicaCount(2), WithClusterChannelReactorSubCount(1), WithClusterSlotReactorSubCount(1))
	err := s1.Start()
	assert.Nil(t, err)

	err = s2.Start()
	assert.Nil(t, err)

	defer s1.StopNoErr()
	defer s2.StopNoErr()

	MustWaitClusterReady(s1, s2)

	cfg := s1.GetClusterConfig()

	var migrateSlot *pb.Slot
	for _, slot := range cfg.Slots {
		if slot.Leader == s1.opts.Cluster.NodeId {
			migrateSlot = slot
			break
		}
	}

	err = s1.MigrateSlot(migrateSlot.Id, s1.opts.Cluster.NodeId, s2.opts.Cluster.NodeId)
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

func TestClusterNodeJoin(t *testing.T) {
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
	assert.Equal(t, 2, len(cfg.Nodes))

	// new server
	s3 := NewTestServer(t, WithDemoOn(false), WithClusterSeed("1001@127.0.0.1:11110"), WithClusterServerAddr("0.0.0.0:11115"), WithWSAddr("ws://0.0.0.0:5250"), WithMonitorAddr("0.0.0.0:5350"), WithAddr("tcp://0.0.0.0:5150"), WithHTTPAddr("0.0.0.0:5005"), WithClusterAddr("tcp://0.0.0.0:11115"), WithClusterNodeId(1005))
	err = s3.Start()
	assert.Nil(t, err)
	defer s3.StopNoErr()

	tk := time.NewTicker(time.Millisecond * 10)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-tk.C:
			cfg = s3.GetClusterConfig()
			existLearner := false
			existReplica := false
			for _, slot := range cfg.Slots {
				if wkutil.ArrayContainsUint64(slot.Learners, s3.opts.Cluster.NodeId) {
					existLearner = true
					break
				}
				if wkutil.ArrayContainsUint64(slot.Replicas, s3.opts.Cluster.NodeId) {
					existReplica = true
					break
				}
			}
			if !existLearner && existReplica {
				cfg = s1.GetClusterConfig()
				fmt.Println("cfg--------->", cfg)
				time.Sleep(time.Second * 1)
				return
			}

		case <-timeoutCtx.Done():
			assert.Nil(t, timeoutCtx.Err())
			return
		}
	}

}

func TestClusterChannelMigrate(t *testing.T) {
	s1, s2 := NewTestClusterServerTwoNode(t, WithClusterChannelReplicaCount(1), WithClusterSlotReplicaCount(2))
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

	cfg, err := s1.store.DB().GetChannelClusterConfig("test1@test2", 1)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(cfg.Replicas))

	// 迁移到另外一个节点

	var migrateTo uint64
	if cfg.Replicas[0] == s1.opts.Cluster.NodeId {
		migrateTo = s2.opts.Cluster.NodeId
	} else {
		migrateTo = s1.opts.Cluster.NodeId
	}
	cfg.MigrateFrom = cfg.Replicas[0]
	cfg.MigrateTo = migrateTo
	cfg.Learners = append(cfg.Learners, migrateTo)

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	err = s1.clusterServer.ProposeChannelClusterConfig(timeoutCtx, cfg)
	assert.Nil(t, err)

	s1.clusterServer.UpdateChannelClusterConfig(cfg)
	s2.clusterServer.UpdateChannelClusterConfig(cfg)

	time.Sleep(time.Second * 1)

}
