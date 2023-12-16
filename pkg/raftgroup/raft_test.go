package raftgroup_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raftgroup"
	"github.com/stretchr/testify/assert"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRaftReady(t *testing.T) {
	var nodeID uint64 = 1
	opts := raftgroup.NewRaftOptions()
	opts.NodeID = nodeID
	raftStorage := raftgroup.NewMemoryStorage()
	opts.Storage = raftStorage
	opts.Members = []raftgroup.Member{
		{
			NodeID: nodeID,
		},
	}

	raftNode := raftgroup.NewRaft(opts)

	hasReady := raftNode.HasReady()
	assert.Equal(t, hasReady, false)

	// ------------------ 启动节点 ------------------
	err := raftNode.Bootstrap()
	assert.NoError(t, err)
	hasReady = raftNode.HasReady()
	assert.Equal(t, hasReady, true)

	ready := raftNode.Ready()
	_, msgStorageAppend, _ := raftgroup.SplitLocalStorageMsgs(ready.Messages)
	assert.NotEqual(t, 0, msgStorageAppend.Type)
	assert.Equal(t, 1, len(msgStorageAppend.Entries))
	assert.Equal(t, raftpb.EntryType(raftpb.EntryConfChange), msgStorageAppend.Entries[0].Type)
	assert.Equal(t, uint64(1), msgStorageAppend.Commit)
	assert.Equal(t, uint64(1), msgStorageAppend.Term)

	if !raft.IsEmptyHardState(ready.HardState) {
		err = raftStorage.SetHardState(ready.HardState)
		assert.NoError(t, err)
	}

	err = raftStorage.Append(msgStorageAppend.Entries)
	assert.NoError(t, err)
	raftgroup.LogRaftReady(ready)

	err = raftNode.Step(msgStorageAppend.Responses[0])
	assert.NoError(t, err)

	// ------------------ 应用集群配置 ------------------
	ready = raftNode.Ready()
	raftgroup.LogRaftReady(ready)

	_, _, msgStorageApply := raftgroup.SplitLocalStorageMsgs(ready.Messages)

	var cc raftpb.ConfChange
	err = cc.Unmarshal(msgStorageApply.Entries[0].Data)
	assert.NoError(t, err)

	// 推进
	err = raftNode.Step(msgStorageApply.Responses[0])
	assert.NoError(t, err)

	// ------------------ 发起投票 ------------------
	err = raftNode.Campaign()
	assert.NoError(t, err)

	ready = raftNode.Ready()
	if !raft.IsEmptyHardState(ready.HardState) {
		err = raftStorage.SetHardState(ready.HardState)
		assert.NoError(t, err)
	}
	// 预投票
	_, msgStorageAppend, _ = raftgroup.SplitLocalStorageMsgs(ready.Messages)
	raftgroup.LogRaftReady(ready)
	err = raftStorage.Append(msgStorageAppend.Entries)
	assert.NoError(t, err)

	// 推进
	err = raftNode.Step(msgStorageAppend.Responses[0])
	assert.NoError(t, err)

	// 投票
	ready = raftNode.Ready()
	_, msgStorageAppend, _ = raftgroup.SplitLocalStorageMsgs(ready.Messages)
	raftgroup.LogRaftReady(ready)

	// 推进
	err = raftNode.Step(msgStorageAppend.Responses[0])
	assert.NoError(t, err)

	// ------------------ 成功当选领导 ------------------
	ready = raftNode.Ready()
	raftgroup.LogRaftReady(ready)
	assert.Equal(t, nodeID, ready.Lead)
}

func TestRaftHandleReady(t *testing.T) {
	var nodeID uint64 = 1
	opts := raftgroup.NewRaftOptions()
	opts.NodeID = nodeID
	raftStorage := raftgroup.NewMemoryStorage()
	opts.Storage = raftStorage
	opts.Members = []raftgroup.Member{
		{
			NodeID: nodeID,
		},
	}

	raftNode := raftgroup.NewRaft(opts)

	raftNode.HandleReady()

	err := raftNode.Bootstrap()
	assert.NoError(t, err)

	raftNode.HandleReady()

	err = raftNode.Campaign()
	assert.NoError(t, err)

	raftNode.HandleReady()

	// ------------------ 成功当选领导 ------------------
	assert.Equal(t, nodeID, raftNode.LeaderID())

}
