package raftgroup_test

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raftgroup"
	"github.com/stretchr/testify/assert"
)

func TestServerStartAndStop(t *testing.T) {
	s := raftgroup.New(1, "127.0.0.1:11000")
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()
}

func TestServerAddRaft(t *testing.T) {
	var nodeID uint64 = 1
	dataDir := path.Join(os.TempDir(), "raftserver")
	s := raftgroup.New(nodeID, "127.0.0.1:11000", raftgroup.WithDataDir(dataDir))
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	raft := s.AddRaft(nodeID, 1, "127.0.0.1:11000", raftgroup.RaftOptionWithMembers([]raftgroup.Member{
		{
			NodeID:     nodeID,
			ShardID:    1,
			ServerAddr: "127.0.0.1:11000",
		},
	}))

	err = raft.Bootstrap()
	assert.NoError(t, err)

	raft.HandleReady()

	err = raft.Campaign()
	assert.NoError(t, err)

}

func TestTwoServerAddRaft(t *testing.T) {
	var nodeID1 uint64 = 1
	var nodeID2 uint64 = 2
	dataDir := path.Join(os.TempDir(), "raftserver")

	fmt.Println("dataDir--->", dataDir)

	defer os.RemoveAll(dataDir)

	members := []raftgroup.Member{
		{
			NodeID:     nodeID1,
			ShardID:    1,
			ServerAddr: "127.0.0.1:11000",
		},
		{
			NodeID:     nodeID2,
			ShardID:    1,
			ServerAddr: "127.0.0.1:12000",
		},
	}
	// start server 1
	s1 := raftgroup.New(members[0].NodeID, members[0].ServerAddr, raftgroup.WithDataDir(path.Join(dataDir, fmt.Sprintf("%d", nodeID1))))
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	// start server 2
	s2 := raftgroup.New(members[1].NodeID, members[1].ServerAddr, raftgroup.WithDataDir(path.Join(dataDir, fmt.Sprintf("%d", nodeID2))))
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	// add raft shard 1 to server 1
	raft1 := s1.AddRaft(members[0].NodeID, members[0].ShardID, members[0].ServerAddr, raftgroup.RaftOptionWithMembers(members))
	err = raft1.Bootstrap()
	assert.NoError(t, err)

	// add raft shard 1 to  server 2
	raft2 := s2.AddRaft(members[1].NodeID, members[1].ShardID, members[1].ServerAddr, raftgroup.RaftOptionWithMembers(members))
	err = raft2.Bootstrap()
	assert.NoError(t, err)

	err = raft1.WaitLeaderChange(time.Second * 10)
	assert.NoError(t, err)

	err = raft2.WaitLeaderChange(time.Second * 10)
	assert.NoError(t, err)

	err = raft1.Propose([]byte("hello world"))
	assert.NoError(t, err)

}

func TestTwoServerAndManyRaft(t *testing.T) {
	var nodeID1 uint64 = 1
	var nodeID2 uint64 = 2
	dataDir := path.Join(os.TempDir(), "raftserver")
	fmt.Println("dataDir--->11", dataDir)
	// defer os.RemoveAll(dataDir)
	members := []raftgroup.Member{
		{
			NodeID:     nodeID1,
			ShardID:    1,
			ServerAddr: "127.0.0.1:11000",
		},
		{
			NodeID:     nodeID2,
			ShardID:    1,
			ServerAddr: "127.0.0.1:12000",
		},
	}
	// start server 1
	s1 := raftgroup.New(members[0].NodeID, members[0].ServerAddr, raftgroup.WithDataDir(path.Join(dataDir, fmt.Sprintf("%d", nodeID1))))
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	// start server 2
	s2 := raftgroup.New(members[1].NodeID, members[1].ServerAddr, raftgroup.WithDataDir(path.Join(dataDir, fmt.Sprintf("%d", nodeID2))))
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	var slotNum = 10

	for i := 1; i <= slotNum; i++ {
		shardID := uint32(i)
		go func(shardID uint32) {
			// add raft shard 1 to server 1
			raft1 := s1.AddRaft(members[0].NodeID, shardID, members[0].ServerAddr, raftgroup.RaftOptionWithMembers(members))
			err = raft1.Bootstrap()
			assert.NoError(t, err)

			// add raft shard 1 to  server 2
			raft2 := s2.AddRaft(members[1].NodeID, shardID, members[1].ServerAddr, raftgroup.RaftOptionWithMembers(members))
			err = raft2.Bootstrap()
			assert.NoError(t, err)
		}(shardID)
	}

	err = s1.WaitAllLeaderChange(time.Second * 20)
	assert.NoError(t, err)

	err = s2.WaitAllLeaderChange(time.Second * 20)
	assert.NoError(t, err)

}
