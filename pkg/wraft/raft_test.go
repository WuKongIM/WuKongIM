// Copyright (c) 2022 Shanghai Xinbida Network Technology Co., Ltd. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package wraft_test

import (
	"context"
	"fmt"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wraft"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/wpb"
	"github.com/stretchr/testify/assert"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// 1. 测试单节点
// 2. 测试二个节点
// 3. 测试三个节点
// 4. 测试三个节点 关闭一个节点
// 5. 测试三个节点 关闭二个节点

func newRaftNode(id types.ID, addr string, peers []*wraft.Peer, fs wraft.FSM, t *testing.T) *wraft.RaftNode {

	logOpts := wklog.NewOptions()
	logOpts.Level = zapcore.DebugLevel
	wklog.Configure(logOpts)

	tmpDir := os.TempDir()
	cfg := wraft.NewRaftNodeConfig()
	cfg.ID = id
	cfg.Addr = addr
	cfg.Storage = &testStorage{}
	cfg.Peers = peers
	// cfg.Transport = &testTransporter{}
	cfg.LogWALPath = path.Join(tmpDir, id.String(), "wal")
	cfg.MetaDBPath = path.Join(tmpDir, id.String(), "meta.db")

	fmt.Println("tmpDir--->", tmpDir)

	raftNode := wraft.NewRaftNode(fs, cfg)

	return raftNode
}

func removeRaftData(cfg *wraft.RaftNodeConfig) {
	os.RemoveAll(cfg.LogWALPath)
	os.RemoveAll(cfg.MetaDBPath)
}

// 1. 测试单节点
func TestSingleNode(t *testing.T) {
	fs := &testFSM{}
	raftNode := newRaftNode(1, "tcp://0.0.0.0:11110", []*wraft.Peer{wraft.NewPeer(0x1, "")}, fs, t)

	fs.node = raftNode

	defer removeRaftData(raftNode.GetConfig())

	var wait sync.WaitGroup
	wait.Add(1)
	raftNode.OnLead = func(lead uint64) {
		wait.Done()
	}

	err := raftNode.Start()
	assert.NoError(t, err)

	defer raftNode.Stop()

	wait.Wait()

	fmt.Println("--------end12----------")

}

func TestDoubleNode(t *testing.T) {
	fs1 := &testFSM{}
	node1 := newRaftNode(0x1, "tcp://0.0.0.0:11110", []*wraft.Peer{wraft.NewPeer(0x1, "tcp://0.0.0.0:11110"), wraft.NewPeer(0x2, "tcp://0.0.0.0:11111")}, fs1, t)
	fs1.node = node1

	fs2 := &testFSM{}
	node2 := newRaftNode(0x2, "tcp://0.0.0.0:11111", []*wraft.Peer{wraft.NewPeer(0x1, "tcp://0.0.0.0:11110"), wraft.NewPeer(0x2, "tcp://0.0.0.0:11111")}, fs2, t)
	fs2.node = node2

	defer removeRaftData(node1.GetConfig())
	defer removeRaftData(node2.GetConfig())

	var wait sync.WaitGroup
	wait.Add(1)
	wait.Add(1)
	node1.OnLead = func(lead uint64) {
		wait.Done()
	}

	node2.OnLead = func(lead uint64) {
		wait.Done()
	}

	err := node1.Start()
	assert.NoError(t, err)

	defer node1.Stop()

	err = node2.Start()
	assert.NoError(t, err)

	defer node2.Stop()

	wait.Wait()

	time.Sleep(time.Second * 2)
	fmt.Println("------------------------end223---------------------")

}

func TestNodeJoin(t *testing.T) {
	fs1 := &testFSM{}
	node1 := newRaftNode(0x1, "tcp://0.0.0.0:11110", []*wraft.Peer{wraft.NewPeer(0x1, "tcp://0.0.0.0:11110")}, fs1, t)
	fs1.node = node1
	defer removeRaftData(node1.GetConfig())
	defer node1.Stop()

	var node1Lead uint64 = 0
	var node2Lead uint64 = 0

	var leadWait sync.WaitGroup
	leadWait.Add(1)

	node1.OnLead = func(lead uint64) {
		node1Lead = lead

		if node1Lead == node2Lead {
			leadWait.Done()
		}
	}

	err := node1.Start()
	assert.NoError(t, err)

	fs2 := &testFSM{}
	node2 := newRaftNode(0x2, "tcp://0.0.0.0:11111", []*wraft.Peer{wraft.NewPeer(0x2, "tcp://0.0.0.0:11111")}, fs2, t)
	defer removeRaftData(node2.GetConfig())
	fs2.node = node2

	defer node2.Stop()

	node2.OnLead = func(lead uint64) {
		node2Lead = lead
		if node1Lead == node2Lead {
			leadWait.Done()
		}
	}

	err = node2.Start()
	assert.NoError(t, err)

	err = node1.ProposePeer(context.Background(), wraft.NewPeer(0x2, "tcp://0.0.0.0:11111"))
	assert.NoError(t, err)

	err = node2.ProposePeer(context.Background(), wraft.NewPeer(0x1, "tcp://0.0.0.0:11110"))
	assert.NoError(t, err)

	leadWait.Wait()

	fmt.Println("-----------lead---->", node1Lead)
	// send data
	var resp *wpb.CMDResp
	if node1Lead == 1 {
		resp, err = node1.Propose(context.Background(), []byte("hello"))
		assert.NoError(t, err)
	} else {
		resp, err = node2.Propose(context.Background(), []byte("hello"))
		assert.NoError(t, err)
	}
	fmt.Println("resp.Data---------->", resp.Data)

	assert.Equal(t, "hello", string(resp.Data))

	fmt.Println("====================================endmzddd3====================================")

}

type testFSM struct {
	node *wraft.RaftNode
}

func (t *testFSM) Apply(ap wraft.ToApply) error {
	t.node.Debug("Apply----start-->", zap.Any("ap", ap.Entries))
	for _, e := range ap.Entries {
		switch e.Type {
		case raftpb.EntryConfChange:
			var cc raftpb.ConfChange
			cc.Unmarshal(e.Data)

			t.node.Debug("Apply Config", zap.String("config", cc.String()))

			_ = t.node.ApplyConfChange(cc)

			switch cc.Type {
			case raftpb.ConfChangeAddNode:
				t.node.AddConfChange(cc)
			case raftpb.ConfChangeRemoveNode:
				t.node.RemoveConfChange(cc)
			case raftpb.ConfChangeUpdateNode:
				t.node.UpdateConfChange(cc)
			}
			t.node.TriggerProposeConfChange(cc, ap.RaftAdvancedC)

		case raftpb.EntryNormal:
			t.node.Debug("Apply Normal", zap.String("data", string(e.Data)))

			if len(e.Data) > 0 {
				req := &wpb.CMDReq{}
				err := req.Unmarshal(e.Data)
				if err != nil {
					t.node.Error("Unmarshal", zap.Error(err))
					continue
				}
				resp := &wpb.CMDResp{}
				resp.Data = req.Data
				resp.Id = req.Id
				t.node.Trigger(resp)
			}
			t.node.AppliedTo(e.Index)
		}
	}
	select {
	case <-ap.Notifyc:
	case <-t.node.StopChan():
		t.node.Debug("Apply----stop-->", zap.Any("ap", ap.Entries))
		return nil
	}
	t.node.Debug("Apply----end-->", zap.Any("ap", ap.Entries))
	return nil
}

type testStorage struct {
}

func (t *testStorage) Save(st raftpb.HardState, ents []raftpb.Entry) error {

	return nil
}

func (t *testStorage) SaveSnap(snap raftpb.Snapshot) error {
	return nil
}

func (t *testStorage) Close() error {
	return nil
}

func (t *testStorage) Release(snap raftpb.Snapshot) error {
	return nil
}

func (t *testStorage) Sync() error {
	return nil
}
