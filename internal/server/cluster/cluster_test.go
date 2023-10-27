package cluster_test

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/multiraft"
	"github.com/stretchr/testify/assert"
)

func TestClusterInit(t *testing.T) {

	leaderChangeChan := make(chan struct{}, 1)
	slotCount := 5
	// ---------- cluster 1 ----------
	opts := cluster.NewOptions()
	opts.DataDir = path.Join(os.TempDir(), "cluster", "1")
	opts.SlotCount = slotCount
	fmt.Println("os.TempDir()111--->", os.TempDir())
	opts.NodeID = 1
	opts.Addr = "tcp://127.0.0.1:11000"
	opts.Peers = []multiraft.Peer{
		multiraft.NewPeer(1, "tcp://127.0.0.1:11000"),
		multiraft.NewPeer(2, "tcp://127.0.0.1:12000"),
	}
	opts.LeaderChange = func(leaderID uint64) {
		leaderChangeChan <- struct{}{}
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
	opts.NodeID = 2
	opts.Addr = "tcp://127.0.0.1:12000"
	opts.Peers = []multiraft.Peer{
		multiraft.NewPeer(1, "tcp://127.0.0.1:11000"),
		multiraft.NewPeer(2, "tcp://127.0.0.1:12000"),
	}
	c2 := cluster.New(opts)
	err = c2.Start()
	assert.NoError(t, err)
	defer c2.Stop()
	// defer os.RemoveAll(opts.DataDir)

	<-leaderChangeChan

	time.Sleep(time.Second * 20)

}
