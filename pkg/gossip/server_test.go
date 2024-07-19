package gossip_test

import (
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gossip"
	"github.com/stretchr/testify/assert"
)

func TestServerJoin(t *testing.T) {
	s1 := gossip.NewServer(1, "127.0.0.1:11000")
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	var wg sync.WaitGroup
	wg.Add(1)
	s2 := gossip.NewServer(2, "127.0.0.1:12000", gossip.WithSeed([]string{"127.0.0.1:11000"}), gossip.WithOnNodeEvent(func(event gossip.NodeEvent) {
		if event.NodeID == 1 && event.EventType == gossip.NodeEventJoin {
			wg.Done()
		}
	}))
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	wg.Wait()

}
