package wraft_test

import (
	"os"
	"path"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wraft"
	"github.com/WuKongIM/WuKongIM/pkg/wraft/wpb"
	"github.com/stretchr/testify/assert"
)

func TestAddOrUpdatePeer(t *testing.T) {
	tmpDir := os.TempDir()
	cm := wraft.NewClusterConfigManager(path.Join(tmpDir, "clusterconfig.json"))

	newPeer := &wpb.Peer{
		Id:     1,
		Addr:   "tcp://127.0.0.1:8080",
		Status: wpb.Status_Joined,
		Role:   wpb.Role_Follower,
		Term:   1,
		Vote:   2,
	}
	cm.AddOrUpdatePeer(newPeer)

	existPeer := cm.GetPeer(newPeer.Id)

	assert.Equal(t, newPeer, existPeer)

	existPeer.Term = 4

	cm.AddOrUpdatePeer(existPeer)
	existPeer2 := cm.GetPeer(newPeer.Id)
	assert.Equal(t, existPeer, existPeer2)
	existPeer2.Term = 5

	assert.NotEqual(t, existPeer.Term, existPeer2.Term)

}

func TestClusterManagerStartAndStop(t *testing.T) {
	tmpDir := os.TempDir()
	cm := wraft.NewClusterConfigManager(path.Join(tmpDir, "clusterconfig.json"))

	cm.Start()

	cm.Stop()
}
