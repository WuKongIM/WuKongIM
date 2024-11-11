package cluster

import (
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/stretchr/testify/assert"
)

func TestAppendLogBatch(t *testing.T) {
	dir := t.TempDir()
	s := NewPebbleShardLogStorage(dir, 8)
	defer s.Close()

	err := s.Open()
	assert.Nil(t, err)

	reqs := []reactor.AppendLogReq{}
	for i := 0; i < 1000; i++ {
		reqs = append(reqs, reactor.AppendLogReq{
			HandleKey: fmt.Sprintf("%d", i%8),
			Logs: []replica.Log{
				{
					Index: uint64(i),
					Term:  uint32(i),
					Data:  []byte(fmt.Sprintf("data-%d", i)),
				}},
		})
	}

	err = s.AppendLogBatch(reqs)
	assert.Nil(t, err)
}
