package cluster_test

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/assert"
)

func TestRaftStartAndStop(t *testing.T) {
	opts := newTestOptions(1)
	raft := cluster.NewRaft(opts)
	err := raft.Start()
	assert.NoError(t, err)
	defer raft.Stop()

	raft.MustWaitLeader(time.Second * 10)
}

func newTestOptions(id cluster.NodeID) *cluster.RaftOptions {
	opts := cluster.NewRaftOptions()
	opts.ID = id
	opts.DataDir = path.Join(os.TempDir(), "raft", id.String())
	opts.NodeRegistry = cluster.NewNodeRegistry()
	fmt.Println("opts.DataDir--->", opts.DataDir)
	return opts
}
