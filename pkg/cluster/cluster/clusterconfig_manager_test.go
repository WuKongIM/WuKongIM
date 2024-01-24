package cluster

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClusterconfigManager(t *testing.T) {

	dataDir := t.TempDir()
	fmt.Println("dataDir--->", dataDir)

	trans := NewMemoryTransport()
	opts := NewOptions()
	opts.NodeID = 1
	opts.DataDir = dataDir
	opts.SlotCount = 256
	opts.Transport = trans
	opts.InitNodes = map[uint64]string{
		1: "127.0.0.1:10001",
	}
	cm := newClusterconfigManager(opts)
	err := cm.start()
	assert.NoError(t, err)
	defer cm.stop()

	err = cm.waitConfigNodeCount(1, time.Second*5)
	assert.NoError(t, err)

	err = cm.waitConfigSlotCount(opts.SlotCount, time.Second*5)
	assert.NoError(t, err)

}
