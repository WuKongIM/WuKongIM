package slot

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/stretchr/testify/assert"
)

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
		tm, err := s.LeaderLastLogTerm(shardNo)
		assert.Nil(t, err)
		assert.Equal(t, term10, tm)
	})

	t.Run("LeaderTermStartIndex", func(t *testing.T) {
		idx, err := s.GetTermStartIndex(shardNo, term1)
		assert.Nil(t, err)
		assert.Equal(t, index1, idx)

		idx, err = s.GetTermStartIndex(shardNo, term10)
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

		idx, err := s.GetTermStartIndex(shardNo, term10)
		assert.Nil(t, err)
		assert.Equal(t, uint64(0), idx)

		idx, err = s.GetTermStartIndex(shardNo, term1)
		assert.Nil(t, err)
		assert.Equal(t, index1, idx)
	})

}
