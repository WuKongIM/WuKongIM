package raft

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/stretchr/testify/assert"
)

func TestQueueAppend(t *testing.T) {
	q := newQueue("test", 0, 0)

	log1 := types.Log{Id: 1, Index: 1, Term: 1, Data: []byte("log1"), Time: time.Now()}
	log2 := types.Log{Id: 2, Index: 2, Term: 1, Data: []byte("log2"), Time: time.Now()}

	q.append(log1, log2)

	assert.Equal(t, uint64(2), q.lastLogIndex)
	assert.Equal(t, 2, len(q.logs))
	assert.Equal(t, log1, q.logs[0])
	assert.Equal(t, log2, q.logs[1])
}

func TestQueueAppendTo(t *testing.T) {
	q := newQueue("test", 0, 0)

	log1 := types.Log{Id: 1, Index: 1, Term: 1, Data: []byte("log1"), Time: time.Now()}
	log2 := types.Log{Id: 2, Index: 2, Term: 1, Data: []byte("log2"), Time: time.Now()}
	log3 := types.Log{Id: 3, Index: 3, Term: 1, Data: []byte("log3"), Time: time.Now()}

	q.append(log1, log2, log3)

	q.storeTo(2)

	assert.Equal(t, uint64(2), q.storedIndex)
	assert.Equal(t, 1, len(q.logs))
	assert.Equal(t, log3, q.logs[0])
}

func TestQueueStorageToOutOfBound(t *testing.T) {
	q := newQueue("test", 0, 0)

	log1 := types.Log{Id: 1, Index: 1, Term: 1, Data: []byte("log1"), Time: time.Now()}
	log2 := types.Log{Id: 2, Index: 2, Term: 1, Data: []byte("log2"), Time: time.Now()}

	q.append(log1, log2)

	assert.Panics(t, func() {
		q.storeTo(3)
	})
}

// 测试改进后的 append 方法的各种场景
func TestQueueAppendImproved(t *testing.T) {
	t.Run("empty logs", func(t *testing.T) {
		q := newQueue("test", 0, 0)
		err := q.append()
		assert.NoError(t, err)
		assert.Equal(t, uint64(0), q.lastLogIndex)
		assert.Equal(t, 0, len(q.logs))
	})

	t.Run("non-continuous input logs", func(t *testing.T) {
		q := newQueue("test", 0, 0)
		log1 := types.Log{Index: 1, Term: 1}
		log3 := types.Log{Index: 3, Term: 1} // 跳过了索引2

		err := q.append(log1, log3)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "incoming log sequence broken")
	})

	t.Run("duplicate logs - complete overlap", func(t *testing.T) {
		q := newQueue("test", 0, 0)

		// 先添加一些日志
		log1 := types.Log{Index: 1, Term: 1}
		log2 := types.Log{Index: 2, Term: 1}
		log3 := types.Log{Index: 3, Term: 1}
		err := q.append(log1, log2, log3)
		assert.NoError(t, err)

		// 尝试重复添加已存在的日志
		err = q.append(log2, log3)
		assert.NoError(t, err) // 应该忽略重复日志
		assert.Equal(t, uint64(3), q.lastLogIndex)
		assert.Equal(t, 3, len(q.logs))
	})

	t.Run("log conflict with truncation", func(t *testing.T) {
		q := newQueue("test", 0, 0)

		// 先添加一些日志
		log1 := types.Log{Index: 1, Term: 1}
		log2 := types.Log{Index: 2, Term: 1}
		log3 := types.Log{Index: 3, Term: 1}
		err := q.append(log1, log2, log3)
		assert.NoError(t, err)

		// 添加冲突的日志（从索引2开始，但term不同）
		newLog2 := types.Log{Index: 2, Term: 2}
		newLog3 := types.Log{Index: 3, Term: 2}
		newLog4 := types.Log{Index: 4, Term: 2}

		err = q.append(newLog2, newLog3, newLog4)
		assert.NoError(t, err)

		// 验证日志被正确截断和追加
		assert.Equal(t, uint64(4), q.lastLogIndex)
		assert.Equal(t, 4, len(q.logs))
		assert.Equal(t, uint32(1), q.logs[0].Term) // 第一个日志保持不变
		assert.Equal(t, uint32(2), q.logs[1].Term) // 从第二个日志开始是新的term
		assert.Equal(t, uint32(2), q.logs[2].Term)
		assert.Equal(t, uint32(2), q.logs[3].Term)
	})

	t.Run("log gap detection", func(t *testing.T) {
		q := newQueue("test", 0, 0)

		// 先添加一些日志
		log1 := types.Log{Index: 1, Term: 1}
		log2 := types.Log{Index: 2, Term: 1}
		err := q.append(log1, log2)
		assert.NoError(t, err)

		// 尝试添加有间隙的日志
		log5 := types.Log{Index: 5, Term: 1} // 跳过了3和4
		log6 := types.Log{Index: 6, Term: 1}

		err = q.append(log5, log6)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "log gap detected")

		// 验证原有日志没有被修改
		assert.Equal(t, uint64(2), q.lastLogIndex)
		assert.Equal(t, 2, len(q.logs))
	})
}
