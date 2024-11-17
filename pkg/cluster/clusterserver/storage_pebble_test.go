package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/errgroup"
)

func TestAppend(t *testing.T) {

	traceObj := trace.New(
		context.Background(),
		trace.NewOptions(
			trace.WithServiceName("test"),
			trace.WithServiceHostName("host"),
		))
	trace.SetGlobalTrace(traceObj)

	dir := t.TempDir()
	s := NewPebbleShardLogStorage(dir, 8)
	defer s.Close()

	err := s.Open()
	assert.Nil(t, err)

	reqs := []reactor.AppendLogReq{}
	for i := 0; i < 100000; i++ {
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

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	g, _ := errgroup.WithContext(timeoutCtx)
	for _, req := range reqs {
		req := req
		g.Go(func() error {
			err = s.Append(req)
			assert.Nil(t, err)
			return nil
		})
	}
	_ = g.Wait()
}
