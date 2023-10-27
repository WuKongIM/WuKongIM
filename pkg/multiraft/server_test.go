package multiraft_test

import (
	"context"
	"fmt"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft"
	"github.com/stretchr/testify/assert"
	"go.etcd.io/raft/v3/raftpb"
)

func newTestOptions(peerID uint64, addr string, onApply func(replicaID uint32, enties []raftpb.Entry) error) *multiraft.Options {
	opts := multiraft.NewOptions()
	opts.Peers = []multiraft.Peer{
		multiraft.NewPeer(101, "tcp://127.0.0.1:11000"),
		multiraft.NewPeer(102, "tcp://127.0.0.1:12000"),
		multiraft.NewPeer(103, "tcp://127.0.0.1:13000"),
	}
	opts.PeerID = peerID
	opts.Addr = addr
	opts.RootDir = path.Join(os.TempDir(), "raft", fmt.Sprintf("%d", peerID))
	opts.ReplicaRaftStorage = multiraft.NewReplicaBoltRaftStorage(path.Join(opts.RootDir, "raft.db"))
	opts.StateMachine = &testStateMachine{
		OnApply: onApply,
	}

	fmt.Println("data---->", opts.RootDir)

	return opts
}

func TestSingleMultiRaft(t *testing.T) {

	// 1. Create a new multiraft server
	var peerID uint64 = 101
	opts := newTestOptions(peerID, "tcp://127.0.0.1:11000", nil)

	s := multiraft.New(opts)
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	// 2. Start a new replica
	var replicaID1 uint32 = 1
	replicaOpts := multiraft.NewReplicaOptions()
	replicaOpts.PeerID = peerID
	replicaOpts.MaxReplicaCount = 3
	_, err = s.StartReplica(replicaID1, replicaOpts)
	assert.NoError(t, err)
	defer s.StopReplica(replicaID1)

	time.Sleep(time.Second)

}

func TestDoublePeerSingleReplicaMultiRaft(t *testing.T) {
	var peer1 = multiraft.NewPeer(101, "tcp://127.0.0.1:11000")
	var peer2 = multiraft.NewPeer(102, "tcp://127.0.0.1:12000")

	leaderChan := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(2)
	opts1 := newTestOptions(peer1.ID, peer1.Addr, func(replicaID uint32, enties []raftpb.Entry) error {
		for _, entry := range enties {
			if entry.Type == raftpb.EntryNormal {
				if string(entry.Data) == "hello" {
					wg.Done()
				}
			}
		}
		return nil
	})
	opts2 := newTestOptions(peer2.ID, peer2.Addr, func(replicaID uint32, enties []raftpb.Entry) error {
		for _, entry := range enties {
			if entry.Type == raftpb.EntryNormal {
				if string(entry.Data) == "hello" {
					wg.Done()
				}
			}
		}
		return nil
	})

	s1 := multiraft.New(opts1)
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	s2 := multiraft.New(opts2)
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	var replicaID1 uint32 = 1
	var leaderID uint64

	replicaOpts1 := multiraft.NewReplicaOptions()
	replicaOpts1.Peers = []multiraft.Peer{peer1, peer2}
	replicaOpts1.MaxReplicaCount = 3
	replicaOpts1.LeaderChange = func(newLeaderID, oldLeaderID uint64) {
		if oldLeaderID != newLeaderID {
			leaderID = newLeaderID
			if leaderChan != nil {
				leaderChan <- struct{}{}
			}

		}
	}
	_, err = s1.StartReplica(replicaID1, replicaOpts1)
	assert.NoError(t, err)
	defer s1.StopReplica(replicaID1)

	replicaOpts2 := multiraft.NewReplicaOptions()
	replicaOpts2.Peers = []multiraft.Peer{peer1, peer2}
	replicaOpts2.MaxReplicaCount = 3
	replicaOpts2.LeaderChange = func(newLeaderID, oldLeaderID uint64) {
		if oldLeaderID != newLeaderID {
			leaderID = newLeaderID
			if leaderChan != nil {
				leaderChan <- struct{}{}
			}

		}
	}
	_, err = s2.StartReplica(replicaID1, replicaOpts2)
	assert.NoError(t, err)
	defer s2.StopReplica(replicaID1)

	<-leaderChan
	close(leaderChan)
	leaderChan = nil

	if leaderID == peer1.ID {
		err = s1.Propose(context.Background(), replicaID1, []byte("hello"))
		assert.NoError(t, err)
	} else {
		err = s2.Propose(context.Background(), replicaID1, []byte("hello"))
		assert.NoError(t, err)
	}

	wg.Wait()
}

func TestDoublePeerDoubleReplicaMultiRaft(t *testing.T) {
	var peer1 = multiraft.NewPeer(101, "tcp://127.0.0.1:11000")
	var peer2 = multiraft.NewPeer(102, "tcp://127.0.0.1:12000")
	var wg1 sync.WaitGroup
	wg1.Add(2)

	var wg2 sync.WaitGroup
	wg2.Add(2)

	opts1 := newTestOptions(peer1.ID, peer1.Addr, func(replicaID uint32, enties []raftpb.Entry) error {
		for _, entry := range enties {
			if entry.Type == raftpb.EntryNormal {
				if replicaID == 1 {
					if string(entry.Data) == "hello" {
						wg1.Done()
					}
				}
				if replicaID == 2 {
					if string(entry.Data) == "hello2" {
						wg2.Done()
					}
				}
			}
		}
		return nil
	})
	defer os.RemoveAll(opts1.RootDir)
	opts2 := newTestOptions(peer2.ID, peer2.Addr, func(replicaID uint32, enties []raftpb.Entry) error {
		for _, entry := range enties {
			if entry.Type == raftpb.EntryNormal {
				if replicaID == 1 {
					if string(entry.Data) == "hello" {
						wg1.Done()
					}
				}
				if replicaID == 2 {
					if string(entry.Data) == "hello2" {
						wg2.Done()
					}
				}

			}
		}
		return nil
	})
	defer os.RemoveAll(opts2.RootDir)

	s1 := multiraft.New(opts1)
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	s2 := multiraft.New(opts2)
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	var replicaID1 uint32 = 1
	var replicaID2 uint32 = 2
	var leaderID1 uint64
	var leaderID2 uint64
	leaderChan1 := make(chan struct{}, 1)
	leaderChan2 := make(chan struct{}, 1)

	// ---------- replica 1 ------------

	// replica 1
	replicaOpts1 := multiraft.NewReplicaOptions()
	replicaOpts1.Peers = []multiraft.Peer{peer1, peer2}
	replicaOpts1.MaxReplicaCount = 3
	replicaOpts1.LeaderChange = func(newLeaderID, oldLeaderID uint64) {
		if oldLeaderID != newLeaderID {
			leaderID1 = newLeaderID
			if leaderChan1 != nil {
				leaderChan1 <- struct{}{}
			}

		}
	}
	_, err = s1.StartReplica(replicaID1, replicaOpts1)
	assert.NoError(t, err)
	defer s1.StopReplica(replicaID1)

	// replica 11
	replicaOpts11 := multiraft.NewReplicaOptions()
	replicaOpts11.Peers = []multiraft.Peer{peer1, peer2}
	replicaOpts11.MaxReplicaCount = 3
	replicaOpts11.LeaderChange = func(newLeaderID, oldLeaderID uint64) {
		if oldLeaderID != newLeaderID {
			leaderID1 = newLeaderID
			if leaderChan1 != nil {
				leaderChan1 <- struct{}{}
			}

		}
	}
	_, err = s2.StartReplica(replicaID1, replicaOpts11)
	assert.NoError(t, err)
	defer s2.StopReplica(replicaID1)

	fmt.Println("-----------------replica 2----------------")
	// ---------- replica 2 ------------

	// replica 1
	replicaOpts2 := multiraft.NewReplicaOptions()
	replicaOpts2.Peers = []multiraft.Peer{peer1, peer2}
	replicaOpts2.MaxReplicaCount = 3
	replicaOpts2.LeaderChange = func(newLeaderID, oldLeaderID uint64) {
		if oldLeaderID != newLeaderID {
			leaderID2 = newLeaderID
			if leaderChan2 != nil {
				leaderChan2 <- struct{}{}
			}

		}
	}
	_, err = s1.StartReplica(replicaID2, replicaOpts2)
	assert.NoError(t, err)
	defer s1.StopReplica(replicaID2)

	// replica 22
	replicaOpts22 := multiraft.NewReplicaOptions()
	replicaOpts22.Peers = []multiraft.Peer{peer1, peer2}
	replicaOpts22.MaxReplicaCount = 3
	replicaOpts22.LeaderChange = func(newLeaderID, oldLeaderID uint64) {
		if oldLeaderID != newLeaderID {
			leaderID2 = newLeaderID
			if leaderChan2 != nil {
				leaderChan2 <- struct{}{}
			}

		}
	}
	_, err = s2.StartReplica(replicaID2, replicaOpts22)
	assert.NoError(t, err)
	defer s2.StopReplica(replicaID2)

	<-leaderChan1
	close(leaderChan1)
	leaderChan1 = nil

	<-leaderChan2
	close(leaderChan2)
	leaderChan2 = nil

	time.Sleep(time.Millisecond * 100)

	if leaderID1 == peer1.ID {
		err = s1.Propose(context.Background(), replicaID1, []byte("hello"))
		assert.NoError(t, err)
	} else {
		err = s2.Propose(context.Background(), replicaID1, []byte("hello"))
		assert.NoError(t, err)
	}

	if leaderID2 == peer1.ID {
		err = s1.Propose(context.Background(), replicaID2, []byte("hello2"))
		assert.NoError(t, err)
	} else {
		err = s2.Propose(context.Background(), replicaID2, []byte("hello2"))
		assert.NoError(t, err)
	}

	wg1.Wait()
	wg2.Wait()

}

func TestReplica(t *testing.T) {
	var peer1 = multiraft.NewPeer(101, "tcp://127.0.0.1:11000")
	var peer2 = multiraft.NewPeer(102, "tcp://127.0.0.1:12000")
	opts1 := newTestOptions(peer1.ID, peer1.Addr, nil)
	s1 := multiraft.New(opts1)
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	// opts2 := newTestOptions(peer2.ID, peer2.Addr, nil)
	// s2 := multiraft.New(opts2)
	// err = s2.Start()
	// assert.NoError(t, err)
	// defer s2.Stop()

	// replica 1

	for i := 0; i < 256; i++ {
		replicaID := uint32(i)
		replicaOpts := multiraft.NewReplicaOptions()
		replicaOpts.Peers = []multiraft.Peer{peer1, peer2}
		replicaOpts.MaxReplicaCount = 3
		_, err = s1.StartReplica(uint32(i), replicaOpts)
		assert.NoError(t, err)
		defer s1.StopReplica(replicaID)
	}

	// for i := 0; i < 100; i++ {
	// 	replicaID := uint32(i)
	// 	replicaOpts := multiraft.NewReplicaOptions()
	// 	replicaOpts.Peers = []multiraft.Peer{peer1, peer2}
	// 	replicaOpts.MaxReplicaCount = 3
	// 	_, err = s2.StartReplica(uint32(i), replicaOpts)
	// 	assert.NoError(t, err)
	// 	defer s2.StopReplica(replicaID)
	// }

	time.Sleep(time.Second * 10)

}
