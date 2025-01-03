package raftgroup_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raft/raft"
	"github.com/WuKongIM/WuKongIM/pkg/raft/raftgroup"
	"github.com/stretchr/testify/assert"
)

func TestAppendLog(t *testing.T) {
	rg := raftgroup.New(newTestOptions())
	err := rg.Start()
	assert.Nil(t, err)
	defer rg.Stop()

	// rg.AddRaft()
}

func newTestOptions() *raftgroup.Options {

	defaultOpts := make([]raftgroup.Option, 0)

	opts := raftgroup.NewOptions(defaultOpts...)
	return opts
}

func newRaftNode(nodeId uint64) *raft.Node {

	node := raft.NewNode(raft.NewOptions(raft.WithNodeId(nodeId)))

	return node

}
