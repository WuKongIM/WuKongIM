package cluster_test

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster"
	"github.com/stretchr/testify/assert"
)

func TestClusterInit(t *testing.T) {

	leaderChangeChan := make(chan struct{}, 10)
	slotCount := 2
	// ---------- cluster 1 ----------
	opts := cluster.NewOptions()
	opts.DataDir = path.Join(os.TempDir(), "cluster", "1")
	opts.SlotCount = slotCount
	fmt.Println("os.TempDir()111--->", os.TempDir())
	opts.PeerID = 1
	opts.Addr = "127.0.0.1:11000"
	opts.Peers = []cluster.Peer{
		cluster.NewPeer(1, "127.0.0.1:11000"),
		cluster.NewPeer(2, "127.0.0.1:12000"),
	}
	opts.LeaderChange = func(leaderID uint64) {
		if leaderID != 0 {
			leaderChangeChan <- struct{}{}
		}
	}
	c1 := cluster.New(opts)
	err := c1.Start()
	assert.NoError(t, err)
	defer c1.Stop()
	defer os.RemoveAll(opts.DataDir)

	// ---------- cluster 2 ----------
	opts = cluster.NewOptions()
	opts.DataDir = path.Join(os.TempDir(), "cluster", "2")
	opts.SlotCount = slotCount
	opts.PeerID = 2
	opts.Addr = "127.0.0.1:12000"
	opts.Peers = []cluster.Peer{
		cluster.NewPeer(1, "127.0.0.1:11000"),
		cluster.NewPeer(2, "127.0.0.1:12000"),
	}
	c2 := cluster.New(opts)
	err = c2.Start()
	assert.NoError(t, err)
	defer c2.Stop()
	defer os.RemoveAll(opts.DataDir)

	<-leaderChangeChan

	time.Sleep(time.Second * 2)

}

func TestClusterJoin(t *testing.T) {

	// ---------- cluster 1 ----------
	peer1Addr := "127.0.0.1:11000"
	opts1 := newTestClusterOptions(1001, peer1Addr, cluster.WithSlotCount(2))
	fmt.Println("opts1.DataDir--->", opts1.DataDir)
	c1 := cluster.New(opts1)
	err := c1.Start()
	assert.NoError(t, err)
	defer c1.Stop()
	// defer os.RemoveAll(opts1.DataDir)

	time.Sleep(time.Second * 5)

	// ---------- cluster 2 ----------
	fmt.Println("---------- cluster 2 ---------------")
	opts2 := newTestClusterOptions(1002, "127.0.0.1:12000", cluster.WithJoin(fmt.Sprintf("%d@%s", 1, peer1Addr)))
	c2 := cluster.New(opts2)
	err = c2.Start()
	assert.NoError(t, err)
	defer c2.Stop()

	time.Sleep(time.Second * 10)

}

func newTestClusterOptions(peerID uint64, addr string, optList ...cluster.Option) *cluster.Options {
	opts := cluster.NewOptions()
	opts.DataDir = path.Join(os.TempDir(), "cluster", fmt.Sprintf("%d", peerID))
	opts.PeerID = peerID
	opts.Addr = addr
	opts.Peers = []cluster.Peer{
		cluster.NewPeer(peerID, addr),
	}
	if len(optList) > 0 {
		for _, opt := range optList {
			opt(opts)
		}
	}
	return opts
}
