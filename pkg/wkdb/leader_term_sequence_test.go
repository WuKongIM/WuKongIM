package wkdb_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetLeaderTermSequence(t *testing.T) {

	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	var term uint32 = 10
	var index uint64 = 2
	shardNo := "shardNo"

	var term2 uint32 = 14
	var index2 uint64 = 123
	t.Run("SetLeaderTermStartIndex", func(t *testing.T) {
		err := d.SetLeaderTermStartIndex(shardNo, term, index)
		assert.NoError(t, err)

		err = d.SetLeaderTermStartIndex(shardNo, term2, index2)
		assert.NoError(t, err)

	})

	t.Run("LeaderLastTerm", func(t *testing.T) {
		tm, err := d.LeaderLastTerm(shardNo)
		assert.NoError(t, err)
		assert.Equal(t, term2, tm)
	})

	t.Run("LeaderTermStartIndex", func(t *testing.T) {
		idx, err := d.LeaderTermStartIndex(shardNo, term)
		assert.NoError(t, err)
		assert.Equal(t, index, idx)
	})

	t.Run("LeaderLastTermGreaterThan", func(t *testing.T) {
		tm, err := d.LeaderLastTermGreaterEqThan(shardNo, term+1)
		assert.NoError(t, err)
		assert.Equal(t, term2, tm)
	})

	t.Run("DeleteLeaderTermStartIndexGreaterThanTerm", func(t *testing.T) {
		err := d.DeleteLeaderTermStartIndexGreaterThanTerm(shardNo, term)
		assert.NoError(t, err)

		idx, err := d.LeaderTermStartIndex(shardNo, term2)
		assert.NoError(t, err)
		assert.Equal(t, uint64(0), idx)

		idx, err = d.LeaderTermStartIndex(shardNo, term)
		assert.NoError(t, err)
		assert.Equal(t, index, idx)

	})
}
