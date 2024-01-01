package cluster_test

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/assert"
)

func TestServerStartAndStop(t *testing.T) {
	s := cluster.NewServer(1, cluster.WithListenAddr("127.0.0.1:10001"))
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

}

func TestServerWaitLeader(t *testing.T) {
	dataDir1 := path.Join(os.TempDir(), "cluster", "1")

	initNodes := map[uint64]string{
		1: "127.0.0.1:10001",
		2: "127.0.0.1:10002",
	}
	fmt.Println("dataDir1--->", dataDir1)
	s1 := cluster.NewServer(1, cluster.WithListenAddr("127.0.0.1:10001"), cluster.WithHeartbeat(time.Millisecond*100), cluster.WithInitNodes(initNodes), cluster.WithDataDir(dataDir1))
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	dataDir2 := path.Join(os.TempDir(), "cluster", "2")
	s2 := cluster.NewServer(2, cluster.WithListenAddr("127.0.0.1:10002"), cluster.WithHeartbeat(time.Millisecond*100), cluster.WithInitNodes(initNodes), cluster.WithDataDir(dataDir2))
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	s2.MustWaitLeader(time.Second * 20)

}

func TestServerSlotLeaderElectionForTwo(t *testing.T) {
	dataDir1 := path.Join(os.TempDir(), "cluster", "1")

	initNodes := map[uint64]string{
		1: "127.0.0.1:10001",
		2: "127.0.0.1:10002",
	}
	var slotCount uint32 = 10
	fmt.Println("dataDir1--->", dataDir1)
	s1 := cluster.NewServer(1, cluster.WithListenAddr("127.0.0.1:10001"), cluster.WithSlotCount(slotCount), cluster.WithHeartbeat(time.Millisecond*100), cluster.WithInitNodes(initNodes), cluster.WithDataDir(dataDir1))
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	dataDir2 := path.Join(os.TempDir(), "cluster", "2")
	s2 := cluster.NewServer(2, cluster.WithListenAddr("127.0.0.1:10002"), cluster.WithSlotCount(slotCount), cluster.WithHeartbeat(time.Millisecond*100), cluster.WithInitNodes(initNodes), cluster.WithDataDir(dataDir2))
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	s2.BecomeLeader() // s2设置为领导

	s2.FakeSetNodeOnline(1, false) // 设置节点1下线

	s1.MustWaitAllSlotLeaderIs(2, time.Second*20) // 等待所有的slot的领导都为2

}

func TestServerSlotLeaderElectionForTree(t *testing.T) {
	dataDir1 := path.Join(os.TempDir(), "cluster", "1")

	initNodes := map[uint64]string{
		1: "127.0.0.1:10001",
		2: "127.0.0.1:10002",
		3: "127.0.0.1:10003",
	}
	var slotCount uint32 = 256
	fmt.Println("dataDir1--->", dataDir1)
	s1 := cluster.NewServer(1, cluster.WithListenAddr("127.0.0.1:10001"), cluster.WithSlotCount(slotCount), cluster.WithHeartbeat(time.Millisecond*100), cluster.WithInitNodes(initNodes), cluster.WithDataDir(dataDir1))
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	dataDir2 := path.Join(os.TempDir(), "cluster", "2")
	s2 := cluster.NewServer(2, cluster.WithListenAddr("127.0.0.1:10002"), cluster.WithSlotCount(slotCount), cluster.WithHeartbeat(time.Millisecond*100), cluster.WithInitNodes(initNodes), cluster.WithDataDir(dataDir2))
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	dataDir3 := path.Join(os.TempDir(), "cluster", "3")
	s3 := cluster.NewServer(3, cluster.WithListenAddr("127.0.0.1:10003"), cluster.WithSlotCount(slotCount), cluster.WithHeartbeat(time.Millisecond*100), cluster.WithInitNodes(initNodes), cluster.WithDataDir(dataDir3))
	err = s3.Start()
	assert.NoError(t, err)
	defer s3.Stop()

	s1.BecomeLeader()              // s1成为领导
	s1.FakeSetNodeOnline(2, false) // 将节点2下线

	s3.MustWaitSlotLeaderNotIs(2, time.Second*20) // 等待所有的slot的领导都不包含2

}
