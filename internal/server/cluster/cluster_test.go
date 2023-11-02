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
	// defer os.RemoveAll(opts.DataDir)

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
	// defer os.RemoveAll(opts.DataDir)

	<-leaderChangeChan

	time.Sleep(time.Second * 20)

}
