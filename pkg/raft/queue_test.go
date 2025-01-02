package raft

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestQueueAppend(t *testing.T) {
	q := newQueue(0, 0, 0)

	log1 := Log{Id: 1, Index: 1, Term: 1, Data: []byte("log1"), Time: time.Now()}
	log2 := Log{Id: 2, Index: 2, Term: 1, Data: []byte("log2"), Time: time.Now()}

	q.append(log1, log2)

	assert.Equal(t, uint64(2), q.lastLogIndex)
	assert.Equal(t, 2, len(q.logs))
	assert.Equal(t, log1, q.logs[0])
	assert.Equal(t, log2, q.logs[1])
}

func TestQueueAppendTo(t *testing.T) {
	q := newQueue(0, 0, 0)

	log1 := Log{Id: 1, Index: 1, Term: 1, Data: []byte("log1"), Time: time.Now()}
	log2 := Log{Id: 2, Index: 2, Term: 1, Data: []byte("log2"), Time: time.Now()}
	log3 := Log{Id: 3, Index: 3, Term: 1, Data: []byte("log3"), Time: time.Now()}

	q.append(log1, log2, log3)

	q.storeTo(2)

	assert.Equal(t, uint64(2), q.storedIndex)
	assert.Equal(t, 1, len(q.logs))
	assert.Equal(t, log3, q.logs[0])
}

func TestQueueStorageToOutOfBound(t *testing.T) {
	q := newQueue(0, 0, 0)

	log1 := Log{Id: 1, Index: 1, Term: 1, Data: []byte("log1"), Time: time.Now()}
	log2 := Log{Id: 2, Index: 2, Term: 1, Data: []byte("log2"), Time: time.Now()}

	q.append(log1, log2)

	assert.Panics(t, func() {
		q.storeTo(3)
	})
}
