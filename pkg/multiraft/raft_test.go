package multiraft_test

import (
	"context"
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft"
	"github.com/stretchr/testify/assert"
	"go.etcd.io/raft/v3/raftpb"
)

func newTestRaftOptions(peerID uint64, peerAddr string) *multiraft.RaftOptions {
	opts := multiraft.NewRaftOptions()
	opts.Peers = []multiraft.Peer{
		multiraft.NewPeer(peerID, ""),
	}
	opts.ID = peerID // node id
	opts.DataDir = path.Join(os.TempDir(), "raft", fmt.Sprintf("%d", peerID))
	fmt.Println("dir--->", opts.DataDir)
	opts.RaftStorage = multiraft.NewMemoryRaftStorage()
	// trans := multiraft.NewDefaultTransporter(peerID, peerAddr)
	// err := trans.Start()
	// if err != nil {
	// 	panic(err)
	// }
	// opts.Transporter = trans
	return opts
}

// TestSingRaft is a test for a single raft node
func TestSingRaft(t *testing.T) {
	applyChan := make(chan []raftpb.Entry, 1)

	opts := newTestRaftOptions(101, "tcp://0.0.0.0:0")
	opts.OnApply = func(entries []raftpb.Entry) error {
		fmt.Println("entry.Data--->", string(entries[0].Data))
		applyChan <- entries
		return nil
	}
	defer os.RemoveAll(opts.DataDir)

	s := multiraft.NewRaft(opts)
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	err = s.Bootstrap([]multiraft.Peer{{ID: 101}})
	assert.NoError(t, err)

	fmt.Println("----------->>>>>1")
	time.Sleep(time.Millisecond * 100)
	err = s.Campaign(context.Background())
	assert.NoError(t, err)

	fmt.Println("----------->>>>>2")

	entries := <-applyChan
	assert.Equal(t, 1, len(entries))

}

func TestRaftBenchmark(t *testing.T) {
	for i := 0; i < 256; i++ {
		id := uint64(i + 1)
		go startRaft(id, t)

	}

	time.Sleep(time.Second * 10)

}

func startRaft(id uint64, t *testing.T) {
	opts := newTestRaftOptions(id, "tcp://0.0.0.0:0")
	dataDir := path.Join(opts.DataDir, fmt.Sprintf("%d", id))
	os.MkdirAll(path.Join(dataDir, "wal"), 0755)
	storage := multiraft.NewWalBoltRaftStorage(path.Join(dataDir, "wal"), path.Join(dataDir, "raft.db"))
	err := storage.Open()
	if err != nil {
		panic(err)
	}
	opts.Peers = []multiraft.Peer{{ID: id}, {ID: 2}}
	opts.RaftStorage = storage
	s := multiraft.NewRaft(opts)
	err = s.Start()
	assert.NoError(t, err)
	defer s.Stop()
}

func TestMultiRaft(t *testing.T) {

	ctx := context.Background()

	peer1 := multiraft.NewPeer(101, "tcp://127.0.0.1:10001")
	peer2 := multiraft.NewPeer(102, "tcp://127.0.0.1:10002")

	peer1Finished := make(chan struct{}, 1)
	peer2Finished := make(chan struct{}, 1)
	// node 1
	opts1 := newTestRaftOptions(peer1.ID, peer1.Addr)
	opts1.OnApply = func(entries []raftpb.Entry) error {
		for _, entry := range entries {
			fmt.Println("peer1------entry.Data--->", entry.Type.String(), string(entry.Data))
			if entry.Type == raftpb.EntryNormal {
				if string(entry.Data) == "world" {
					peer1Finished <- struct{}{}
				}
			}
		}
		return nil
	}
	// opts1.Peers = []multiraft.Peer{
	// 	peer1,
	// 	peer2,
	// }

	// peer1Startup := make(chan struct{}, 1)
	opts1.LeaderChange = func(newLeader, oldLeader uint64) {
		// if oldLeader == 0 && newLeader != 0 {
		// 	peer1Startup <- struct{}{}
		// }

	}
	s1 := multiraft.NewRaft(opts1)
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()
	defer os.RemoveAll(opts1.DataDir)

	// err = s1.Campaign() // 开始选举
	assert.NoError(t, err)

	// <-peer1Startup

	// node2
	peer2Startup := make(chan struct{}, 1)
	opts2 := newTestRaftOptions(peer2.ID, peer2.Addr)
	opts2.OnApply = func(entries []raftpb.Entry) error {
		for _, entry := range entries {
			fmt.Println("peer2------entry.Data--->", entry.Type.String(), string(entry.Data))
			if entry.Type == raftpb.EntryNormal {
				if string(entry.Data) == "world" {
					peer2Finished <- struct{}{}
				}
			}

		}
		return nil
	}
	opts2.LeaderChange = func(newLeader, oldLeader uint64) {
		if oldLeader == 0 && newLeader != 0 {
			peer2Startup <- struct{}{}
		}

	}
	s2 := multiraft.NewRaft(opts2)
	s2.GetOptions().OnSend = func(ctx context.Context, msg raftpb.Message) error {
		if msg.To == peer1.ID {
			go s1.OnRaftMessage(msg)
		}
		return nil
	}
	s1.GetOptions().OnSend = func(ctx context.Context, msg raftpb.Message) error {
		if msg.To == peer2.ID {
			go s2.OnRaftMessage(msg)
		}
		return nil
	}
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()
	defer os.RemoveAll(opts2.DataDir)

	err = s2.Bootstrap([]multiraft.Peer{{ID: peer2.ID}})
	assert.NoError(t, err)

	// time.Sleep(time.Millisecond * 100)

	// err = s2.Campaign() // 开始选举
	// assert.NoError(t, err)

	<-peer2Startup

	fmt.Println("-------------------start propose1-------------------")
	err = s2.Propose(ctx, []byte("hello"))
	assert.NoError(t, err)

	time.Sleep(time.Second)

	// node relationship
	// err = s2.AddPeer(peer1)
	err = s2.ProposeConfChange(ctx, raftpb.ConfChange{
		Type:   raftpb.ConfChangeAddNode,
		NodeID: peer1.ID,
	})
	assert.NoError(t, err)

	// err = opts1.Transporter.AddPeer(peer2)
	assert.NoError(t, err)

	time.Sleep(time.Second)

	err = s2.ProposeConfChange(ctx, raftpb.ConfChange{
		Type:   raftpb.ConfChangeAddNode,
		NodeID: peer2.ID,
	})
	assert.NoError(t, err)

	// err = s2.AddPeer(peer2)

	// s1.HandleReady()

	fmt.Println("-------------------start propose2-------------------")

	err = s2.Propose(context.Background(), []byte("world"))
	assert.NoError(t, err)

	<-peer1Finished
	<-peer2Finished

}

type testStateMachine struct {
	OnApply func(replicaID uint32, enties []raftpb.Entry) error
}

func (s *testStateMachine) Apply(replicaID uint32, enties []raftpb.Entry) error {

	if s.OnApply != nil {
		s.OnApply(replicaID, enties)
	}
	return nil
}
