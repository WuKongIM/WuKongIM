package wraft_test

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wraft"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/wpb"
	"github.com/stretchr/testify/assert"
)

func TestClusterFSMManager(t *testing.T) {
	tmpDir := os.TempDir()
	fmt.Println("tmpDir--->", tmpDir)
	clusterPath := path.Join(tmpDir, "cluster.json")
	os.RemoveAll(clusterPath)
	clusterConfigManager := wraft.NewClusterConfigManager(clusterPath)
	clusterConfigManager.Start()
	defer clusterConfigManager.Stop()

	fsmManager := wraft.NewClusterFSMManager(1, clusterConfigManager)
	fsmManager.IntervalDuration = time.Millisecond * 100
	fsmManager.Start()
	defer fsmManager.Stop()

	clusterConfigManager.AddOrUpdatePeer(&wpb.Peer{
		Id:     2,
		Addr:   "1234",
		Status: wpb.Status_WillJoin,
	})

	ready := <-fsmManager.ReadyChan()

	assert.Equal(t, ready.State, wraft.ClusterStatePeerStatusChange)
	assert.Equal(t, ready.Peer.Id, uint64(2))
	assert.Equal(t, ready.Peer.Addr, "1234")
	assert.Equal(t, ready.Peer.Status, wpb.Status_WillJoin)

	clusterConfigManager.UpdatePeerStatus(ready.Peer.Id, wpb.Status_Joining)

	ready = <-fsmManager.ReadyChan()

	assert.Equal(t, ready.State, wraft.ClusterStatePeerStatusChange)
	assert.Equal(t, ready.Peer.Id, uint64(2))
	assert.Equal(t, ready.Peer.Addr, "1234")
	assert.Equal(t, ready.Peer.Status, wpb.Status_Joining)

	fmt.Println(ready)
}
