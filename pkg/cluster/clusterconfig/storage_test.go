package clusterconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLeaderTermSequence(t *testing.T) {
	store := NewPebbleShardLogStorage(t.TempDir())
	defer store.Close()
	err := store.Open()
	assert.Nil(t, err)

	term1 := uint32(1)
	term10 := uint32(10)

	index1 := uint64(1)
	index100 := uint64(100)

	t.Run("SetLeaderTermStartIndex", func(t *testing.T) {
		err := store.SetLeaderTermStartIndex(term1, index1)
		assert.Nil(t, err)

		err = store.SetLeaderTermStartIndex(term10, index100)
		assert.Nil(t, err)
	})

	t.Run("LeaderLastTerm", func(t *testing.T) {
		tm, err := store.LeaderLastTerm()
		assert.Nil(t, err)
		assert.Equal(t, term10, tm)
	})

	t.Run("LeaderTermStartIndex", func(t *testing.T) {
		idx, err := store.LeaderTermStartIndex(term1)
		assert.Nil(t, err)
		assert.Equal(t, index1, idx)

		idx, err = store.LeaderTermStartIndex(term10)
		assert.Nil(t, err)
		assert.Equal(t, index100, idx)
	})

	t.Run("LeaderLastTermGreaterThan", func(t *testing.T) {
		tm, err := store.LeaderLastTermGreaterThan(term1)
		assert.Nil(t, err)
		assert.Equal(t, term1, tm)

		tm, err = store.LeaderLastTermGreaterThan(term1 + 1)
		assert.Nil(t, err)
		assert.Equal(t, term10, tm)
	})

	t.Run("DeleteLeaderTermStartIndexGreaterThanTerm", func(t *testing.T) {
		err := store.DeleteLeaderTermStartIndexGreaterThanTerm(term1)
		assert.Nil(t, err)

		idx, err := store.LeaderTermStartIndex(term10)
		assert.Nil(t, err)
		assert.Equal(t, uint64(0), idx)

		idx, err = store.LeaderTermStartIndex(term1)
		assert.Nil(t, err)
		assert.Equal(t, index1, idx)
	})
}
