package cluster

import (
	"testing"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/stretchr/testify/assert"
)

func TestGetLogs(t *testing.T) {
	dir := t.TempDir()

	db := NewPebbleShardLogStorage(dir)
	err := db.Open()
	assert.NoError(t, err)

	defer db.Close()

	shardNo := "test"

	err = db.AppendLog(shardNo, []replica.Log{
		{Index: 1, Term: 1, Data: []byte("hello")},
		{Index: 2, Term: 1, Data: []byte("world")},
	})
	assert.NoError(t, err)

	logs, err := db.Logs(shardNo, 1, 3, 10)
	assert.NoError(t, err)

	assert.Equal(t, 2, len(logs))
	assert.Equal(t, uint64(1), logs[0].Index)
	assert.Equal(t, uint32(1), logs[0].Term)
	assert.Equal(t, []byte("hello"), logs[0].Data)

	assert.Equal(t, uint64(2), logs[1].Index)
	assert.Equal(t, uint32(1), logs[1].Term)
	assert.Equal(t, []byte("world"), logs[1].Data)

}

func TestTruncateLogTo(t *testing.T) {
	dir := t.TempDir()

	db := NewPebbleShardLogStorage(dir)
	err := db.Open()
	assert.NoError(t, err)

	defer db.Close()

	shardNo := "test"

	err = db.AppendLog(shardNo, []replica.Log{
		{Index: 1, Term: 1, Data: []byte("hello")},
		{Index: 2, Term: 1, Data: []byte("world")},
		{Index: 3, Term: 1, Data: []byte("world1")},
	})
	assert.NoError(t, err)

	err = db.TruncateLogTo(shardNo, 2)
	assert.NoError(t, err)

	logs, err := db.Logs(shardNo, 1, 3, 10)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(logs))
	assert.Equal(t, uint64(1), logs[0].Index)
	assert.Equal(t, uint32(1), logs[0].Term)
	assert.Equal(t, []byte("hello"), logs[0].Data)

	lastIndex, err := db.LastIndex(shardNo)
	assert.NoError(t, err)

	assert.Equal(t, uint64(1), lastIndex)

}

func TestSetLeaderTermStartIndex(t *testing.T) {
	dir := t.TempDir()
	db := NewPebbleShardLogStorage(dir)
	err := db.Open()
	assert.NoError(t, err)

	defer db.Close()

	shardNo := "test"

	err = db.SetLeaderTermStartIndex(shardNo, 1, 1)
	assert.NoError(t, err)

	err = db.SetLeaderTermStartIndex(shardNo, 2, 10)
	assert.NoError(t, err)

	index, err := db.LeaderTermStartIndex(shardNo, 1)
	assert.NoError(t, err)

	assert.Equal(t, uint64(1), index)

	lastTerm, err := db.LeaderLastTerm(shardNo)
	assert.NoError(t, err)

	assert.Equal(t, uint32(2), lastTerm)

	err = db.DeleteLeaderTermStartIndexGreaterThanTerm(shardNo, 2)
	assert.NoError(t, err)

	index, err = db.LeaderTermStartIndex(shardNo, 2)
	assert.NoError(t, err)
	assert.Equal(t, uint64(10), index)

}
