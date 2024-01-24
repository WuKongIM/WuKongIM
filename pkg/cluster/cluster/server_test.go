package cluster_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster"
	"github.com/stretchr/testify/assert"
)

func TestProposeMessageToChannel(t *testing.T) {
	opts := cluster.NewOptions()
	opts.NodeID = 1
	s := cluster.New(opts)
	err := s.Start()
	assert.NoError(t, err)

	defer s.Stop()

	channelID := "test"
	var channelType uint8 = 2
	_, err = s.ProposeMessageToChannel(channelID, channelType, []byte("hello"))
	assert.NoError(t, err)
}
