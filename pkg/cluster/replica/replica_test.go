package replica_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/stretchr/testify/assert"
)

// 测试提案
func TestPropose(t *testing.T) {
	storage := replica.NewMemoryStorage()

	rc := replica.New(1, "test", replica.WithStorage(storage), replica.WithReplicas([]uint64{1}))
	rc.BecomeLeader(1)

	err := rc.Propose([]byte("hello"))
	assert.NoError(t, err)

	hasReady := rc.HasReady()
	assert.True(t, hasReady)

	rd := rc.Ready()

	has, msg := getMessageByType(replica.MsgStoreAppend, rd.Messages)
	assert.True(t, has)

	assert.Equal(t, 1, len(msg.Logs))
	assert.Equal(t, uint64(1), msg.Logs[0].Index)
	assert.Equal(t, []byte("hello"), msg.Logs[0].Data)
}

// // 测试任命领导
// func TestAppointmentLeader(t *testing.T) {
// 	walstorage := replica.NewMemoryStorage()

// 	// rc 1
// 	rc := replica.New(1, "test", replica.WithStorage(walstorage))
// 	err := rc.Step(replica.Message{
// 		MsgType:           replica.MsgAppointLeaderReq,
// 		From:              10001,
// 		To:                1,
// 		Term:              10,
// 		AppointmentLeader: 1,
// 	})
// 	assert.NoError(t, err)
// 	assert.True(t, rc.IsLeader())

// 	rd := rc.Ready()

// 	has, message := getMessageByType(replica.MsgAppointLeaderResp, rd.Messages)
// 	assert.True(t, has)

// 	assert.Equal(t, uint64(10001), message.To)
// 	assert.Equal(t, uint64(1), message.From)

// }

// 测试日志同步机制
func TestReplicaSync(t *testing.T) {
	storage1 := replica.NewMemoryStorage()

	// rc 1
	rc := replica.New(1, "test", replica.WithStorage(storage1), replica.WithReplicas([]uint64{1, 2}))
	rc.BecomeLeader(1)

	// rc1 propose
	err := rc.Propose([]byte("hello")) // 提案数据
	assert.NoError(t, err)

	err = rc.Step(replica.Message{
		MsgType: replica.MsgSync,
		From:    2,
		To:      1,
		Index:   2,
	})
	assert.NoError(t, err)

	rd := rc.Ready()
	has, msg := getMessageByType(replica.MsgApplyLogsReq, rd.Messages)
	assert.True(t, has)

	assert.Equal(t, 1, len(msg.Logs))
	assert.Equal(t, uint64(1), msg.Logs[0].Index)
	assert.Equal(t, []byte("hello"), msg.Logs[0].Data)

}

// 测试追随者日志冲突
func TestFollowLogConflicts(t *testing.T) {
	storage := replica.NewMemoryStorage()
	_ = storage.AppendLog([]replica.Log{
		{
			Index: 1,
			Term:  1,
			Data:  []byte("hello1"),
		},
		{
			Index: 2,
			Term:  1,
			Data:  []byte("hello2"),
		},
		{
			Index: 3,
			Term:  2,
			Data:  []byte("hello11"),
		},
		{
			Index: 4,
			Term:  2,
			Data:  []byte("hello22"),
		},
	})
	_ = storage.SetLeaderTermStartIndex(1, 1)

	rc := replica.New(1, "test", replica.WithStorage(storage))
	rc.BecomeFollower(1, 2)

	rd := rc.Ready()
	has, msg := getMessageByType(replica.MsgLeaderTermStartIndexReq, rd.Messages)
	assert.True(t, has)
	assert.Equal(t, uint32(1), msg.Term)

	err := rc.Step(replica.Message{
		MsgType: replica.MsgLeaderTermStartIndexResp,
		From:    2,
		To:      1,
		Term:    2,
		Index:   3,
	})
	assert.NoError(t, err)

	lastIndex, err := storage.LastIndex()
	assert.NoError(t, err)

	assert.Equal(t, uint64(2), lastIndex)

	leaderLastTerm, err := storage.LeaderLastTerm()
	assert.NoError(t, err)

	assert.Equal(t, uint32(2), leaderLastTerm)

	idx, err := storage.LeaderTermStartIndex(leaderLastTerm)
	assert.NoError(t, err)

	assert.Equal(t, uint64(3), idx)
}

func TestApplyLogs(t *testing.T) {
	storage := replica.NewMemoryStorage()
	_ = storage.AppendLog([]replica.Log{
		{
			Index: 1,
			Term:  1,
			Data:  []byte("hello1"),
		},
		{
			Index: 2,
			Term:  1,
			Data:  []byte("hello2"),
		},
		{
			Index: 3,
			Term:  2,
			Data:  []byte("hello11"),
		},
		{
			Index: 4,
			Term:  2,
			Data:  []byte("hello22"),
		},
	})
	rc := replica.New(1, "test", replica.WithStorage(storage), replica.WithReplicas([]uint64{1, 2}))
	rc.BecomeLeader(1)

	err := rc.Step(replica.Message{
		MsgType: replica.MsgSync,
		From:    2,
		Term:    1,
		Index:   3, // 会提交 3 以前的日志 即 1 2
	})
	assert.NoError(t, err)

	rd := rc.Ready()
	has, applyMsg := getMessageByType(replica.MsgApplyLogsReq, rd.Messages)
	assert.True(t, has)
	assert.Equal(t, 2, len(applyMsg.Logs))

	err = rc.Step(replica.Message{
		MsgType: replica.MsgApplyLogsResp,
		From:    1,
		To:      1,
		Term:    1,
		Logs:    applyMsg.Logs,
	})
	assert.NoError(t, err)

	assert.Equal(t, uint64(2), rc.CommittedIndex())
	assert.Equal(t, uint64(2), rc.AppliedIndex())

}

func TestLogStoreAppend(t *testing.T) {
	storage := replica.NewMemoryStorage()
	rc := replica.New(1, "test", replica.WithStorage(storage), replica.WithReplicas([]uint64{1}))
	rc.BecomeLeader(1)

	err := rc.Propose([]byte("hello"))
	assert.NoError(t, err)

	rd := rc.Ready()
	has, msg := getMessageByType(replica.MsgStoreAppend, rd.Messages)
	assert.True(t, has)

	assert.Equal(t, uint64(1), msg.Logs[0].Index)

	assert.Equal(t, 1, rc.UnstableLogLen())

	err = rc.Step(replica.Message{
		MsgType: replica.MsgStoreAppendResp,
		Index:   msg.Logs[len(msg.Logs)-1].Index,
	})
	assert.NoError(t, err)

	assert.Equal(t, 0, rc.UnstableLogLen())

}

func getMessageByType(ty replica.MsgType, messages []replica.Message) (bool, replica.Message) {
	for _, msg := range messages {
		if msg.MsgType == ty {
			return true, msg
		}
	}
	return false, replica.Message{}
}
