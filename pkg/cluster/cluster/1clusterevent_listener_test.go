package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClusterEventInitNode(t *testing.T) {
	opts := NewOptions()
	opts.NodeID = 1
	opts.DataDir = t.TempDir()
	cl := newClusterEventListener(opts)
	err := cl.Start()
	assert.NoError(t, err)
	defer cl.Stop()

}
