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

func TestLeaderTermSequence(t *testing.T) {

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

	term1 := uint32(1)
	term10 := uint32(10)

	index1 := uint64(1)
	index100 := uint64(100)

	shardNo := "shardNo"

	t.Run("SetLeaderTermStartIndex", func(t *testing.T) {
		err := s.SetLeaderTermStartIndex(shardNo, term1, index1)
		assert.Nil(t, err)

		err = s.SetLeaderTermStartIndex(shardNo, term10, index100)
		assert.Nil(t, err)
	})

	t.Run("LeaderLastTerm", func(t *testing.T) {
		tm, err := s.LeaderLastTerm(shardNo)
		assert.Nil(t, err)
		assert.Equal(t, term10, tm)
	})

	t.Run("LeaderTermStartIndex", func(t *testing.T) {
		idx, err := s.LeaderTermStartIndex(shardNo, term1)
		assert.Nil(t, err)
		assert.Equal(t, index1, idx)

		idx, err = s.LeaderTermStartIndex(shardNo, term10)
		assert.Nil(t, err)
		assert.Equal(t, index100, idx)
	})

	t.Run("LeaderLastTermGreaterThan", func(t *testing.T) {
		tm, err := s.LeaderLastTermGreaterThan(shardNo, term1)
		assert.Nil(t, err)
		assert.Equal(t, term1, tm)

		tm, err = s.LeaderLastTermGreaterThan(shardNo, term1+1)
		assert.Nil(t, err)
		assert.Equal(t, term10, tm)
	})

	t.Run("DeleteLeaderTermStartIndexGreaterThanTerm", func(t *testing.T) {
		err := s.DeleteLeaderTermStartIndexGreaterThanTerm(shardNo, term1)
		assert.Nil(t, err)

		idx, err := s.LeaderTermStartIndex(shardNo, term10)
		assert.Nil(t, err)
		assert.Equal(t, uint64(0), idx)

		idx, err = s.LeaderTermStartIndex(shardNo, term1)
		assert.Nil(t, err)
		assert.Equal(t, index1, idx)
	})

}
