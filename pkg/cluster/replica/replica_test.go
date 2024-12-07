package replica

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReplicaInit(t *testing.T) {
	r := New(1)

	has := r.HasReady()
	assert.True(t, has)

	rd := r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgInit))

	err := r.Step(Message{
		MsgType: MsgInitResp,
		Config: Config{
			Role: RoleLeader,
			Term: 1,
		},
	})
	assert.NoError(t, err)

}

// 测试日志冲突检测
func TestLogConflictCheck(t *testing.T) {
	r := New(1)

	r.replicaLog.appendLog(Log{Index: 1, Term: 1, Data: []byte("hello")})

	has := r.HasReady()
	assert.True(t, has)

	rd := r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgInit))

	err := r.Step(Message{
		MsgType: MsgInitResp,
		Config: Config{
			Role:   RoleFollower,
			Term:   1,
			Leader: 2,
		},
	})
	assert.NoError(t, err)

	has = r.HasReady()
	assert.True(t, has)

	rd = r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgLogConflictCheck))

	err = r.Step(Message{
		MsgType: MsgLogConflictCheckResp,
		Index:   1,
	})
	assert.NoError(t, err)

	assert.Equal(t, r.replicaLog.lastLogIndex, uint64(0))
	assert.Equal(t, len(r.replicaLog.unstable.logs), 0)

}

// 测试追随者日志同步
func TestLogSync(t *testing.T) {

	// WithSyncIntervalTick(1) 设置只需要tick一次就可以触发同步
	r := New(1, WithSyncIntervalTick(1))
	has := r.HasReady()
	assert.True(t, has)

	rd := r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgInit))

	err := r.Step(Message{
		MsgType: MsgInitResp,
		Config: Config{
			Role:   RoleFollower,
			Term:   1,
			Leader: 2,
		},
	})
	assert.NoError(t, err)

	// tick一次才会触发同步
	r.Tick()

	has = r.HasReady()
	assert.True(t, has)

	rd = r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgSyncReq))

	// 同步返回
	err = r.Step(Message{
		MsgType: MsgSyncResp,
		Index:   1,
		Logs:    []Log{{Index: 1, Term: 1, Data: []byte("hello")}},
	})
	assert.NoError(t, err)

	assert.Equal(t, r.replicaLog.lastLogIndex, uint64(1))

	// 同步成功后，应该不需要tick就可以再次同步
	has = r.HasReady()
	assert.True(t, has)
	rd = r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgSyncReq))

}

// 测试解决日志冲突后的日志同步
func TestLogSyncAfterConflict(t *testing.T) {
	r := New(1, WithSyncIntervalTick(1))

	r.replicaLog.appendLog(Log{Index: 1, Term: 1, Data: []byte("hello")})

	has := r.HasReady()
	assert.True(t, has)

	rd := r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgInit))

	err := r.Step(Message{
		MsgType: MsgInitResp,
		Config: Config{
			Role:   RoleFollower,
			Term:   1,
			Leader: 2,
		},
	})
	assert.NoError(t, err)

	has = r.HasReady()
	assert.True(t, has)

	rd = r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgLogConflictCheck))

	err = r.Step(Message{
		MsgType: MsgLogConflictCheckResp,
		Index:   1,
	})
	assert.NoError(t, err)

	assert.Equal(t, r.replicaLog.lastLogIndex, uint64(0))
	assert.Equal(t, len(r.replicaLog.unstable.logs), 0)

	// tick一次才会触发同步
	r.Tick()

	has = r.HasReady()
	assert.True(t, has)

	rd = r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgSyncReq))

	// 同步返回
	err = r.Step(Message{
		MsgType: MsgSyncResp,
		Index:   1,
		Logs:    []Log{{Index: 1, Term: 1, Data: []byte("hello")}},
	})
	assert.NoError(t, err)

	assert.Equal(t, r.replicaLog.lastLogIndex, uint64(1))
	assert.Equal(t, len(r.replicaLog.unstable.logs), 1)

}

func TestLeaderAndFollowerLogSync(t *testing.T) {
	leader1 := New(1, WithSyncIntervalTick(1))
	initReplica(leader1, Config{
		Role: RoleLeader,
		Term: 1,
	}, t)

	follower2 := New(2, WithSyncIntervalTick(1))
	initReplica(follower2, Config{
		Role:   RoleFollower,
		Term:   1,
		Leader: 1,
	}, t)

	follower3 := New(3, WithSyncIntervalTick(1))
	initReplica(follower3, Config{
		Role:   RoleFollower,
		Term:   1,
		Leader: 1,
	}, t)

	err := leader1.Propose([]byte("hello"))
	assert.NoError(t, err)

	err = leader1.Step(Message{
		MsgType: MsgSyncReq,
		Index:   1,
		From:    2,
		To:      1,
		Term:    1,
	})
	assert.NoError(t, err)

	err = leader1.Step(Message{
		MsgType: MsgSyncReq,
		Index:   1,
		From:    3,
		To:      1,
		Term:    1,
	})
	assert.NoError(t, err)

	rd := leader1.Ready()

	for _, msg := range rd.Messages {
		if msg.To == 2 {
			err = follower2.Step(msg)
			assert.NoError(t, err)
		} else if msg.To == 3 {
			err = follower3.Step(msg)
			assert.NoError(t, err)
		}
	}
	assert.True(t, hasMsg(rd.Messages, MsgSyncResp))

	assert.Equal(t, uint64(1), leader1.replicaLog.committedIndex, follower2.replicaLog.committedIndex, follower3.replicaLog.committedIndex)

}

func TestApplyLogs(t *testing.T) {
	r := New(1, WithSyncIntervalTick(1))

	r.appendLog(Log{Index: 1, Term: 1, Data: []byte("hello")})
	r.appendLog(Log{Index: 2, Term: 1, Data: []byte("world")})

	initReplica(r, Config{
		Role: RoleLeader,
		Term: 1,
	}, t)

	assert.Equal(t, 2, len(r.replicaLog.unstable.logs))

	rd := r.Ready()

	// 存储日志
	for _, msg := range rd.Messages {
		if msg.MsgType == MsgStoreAppend {
			lastIndex := msg.Logs[len(msg.Logs)-1].Index
			err := r.Step(Message{
				MsgType: MsgStoreAppendResp,
				Index:   lastIndex,
			})
			assert.NoError(t, err)
		}
	}

	// 提交日志
	err := r.Step(Message{
		MsgType: MsgSyncReq,
		Index:   1,
		From:    2,
		To:      1,
		Term:    1,
	})
	assert.NoError(t, err)

	// 应用日志
	rd = r.Ready()

	// 存储日志
	for _, msg := range rd.Messages {
		if msg.MsgType == MsgApplyLogs {
			err := r.Step(Message{
				MsgType: MsgApplyLogsResp,
				Index:   msg.CommittedIndex,
			})
			assert.NoError(t, err)
		}
	}

	assert.Equal(t, uint64(2), r.replicaLog.committedIndex)
	assert.Equal(t, uint64(2), r.replicaLog.lastLogIndex)
	assert.Equal(t, uint64(2), r.replicaLog.appliedIndex)
	assert.Equal(t, 0, len(r.replicaLog.unstable.logs))

}

// 测试自动选举
func TestElection(t *testing.T) {
	var nodeId uint64 = 1
	// electionTimeoutTick := 10
	r := New(nodeId, WithSyncIntervalTick(1), WithElectionOn(true))
	initReplica(r, Config{
		Role:     RoleFollower,
		Term:     1,
		Replicas: []uint64{1, 2, 3},
	}, t)

	electionWait := sync.WaitGroup{}
	electionWait.Add(1)
	go func() {
		tk := time.NewTicker(time.Millisecond * 10)
		defer tk.Stop()
		for {
			rd := r.Ready()
			for _, m := range rd.Messages {
				if m.To == nodeId {
					err := r.Step(m)
					assert.NoError(t, err)
				} else {
					if m.MsgType == MsgVoteReq {
						err := r.Step(Message{
							MsgType: MsgVoteResp,
							From:    m.To,
							To:      nodeId,
							Term:    r.term,
						})
						assert.NoError(t, err)
					}
				}
			}
			if r.isLeader() {
				electionWait.Done()
			}
			select {
			case <-tk.C:
				r.Tick()
			}
		}
	}()
	electionWait.Wait()
}

func TestElectionTreeNode(t *testing.T) {
	node1 := New(1, WithSyncIntervalTick(3), WithElectionOn(true))
	node2 := New(2, WithSyncIntervalTick(3), WithElectionOn(true))
	node3 := New(3, WithSyncIntervalTick(3), WithElectionOn(true))

	node1.appendLog(Log{Index: 1, Term: 1, Data: []byte("hello2")})
	node2.appendLog(Log{Index: 1, Term: 1, Data: []byte("hello2")})

	initReplica(node1, Config{
		Role:     RoleFollower,
		Term:     1,
		Replicas: []uint64{1, 2, 3},
	}, t)

	initReplica(node2, Config{
		Role:     RoleFollower,
		Term:     2,
		Replicas: []uint64{1, 2, 3},
	}, t)

	initReplica(node3, Config{
		Role:     RoleFollower,
		Term:     3,
		Replicas: []uint64{1, 2, 3},
	}, t)

	go loopReady(t, context.Background(), node1, node2, node3)

	time.Sleep(time.Second * 5)

}

func loopReady(t *testing.T, ctx context.Context, rs ...*Replica) {

	tk := time.NewTicker(time.Millisecond * 10)
	defer tk.Stop()
	for {
		for _, r := range rs {
			rd := r.Ready()
			for _, m := range rd.Messages {
				for _, rp := range rs {
					if m.To == rp.opts.NodeId {
						err := rp.Step(m)
						assert.NoError(t, err)
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				r.Tick()
			}
		}
	}

}

// 测试学习者转好成追随者
func TestLearnerToFollower(t *testing.T) {
	r := New(1, WithAutoRoleSwith(true), WithLearnerToFollowerMinLogGap(1))

	r.appendLog(Log{Index: 1, Term: 1, Data: []byte("hello")})
	r.appendLog(Log{Index: 2, Term: 1, Data: []byte("world")})

	has := r.HasReady()
	assert.True(t, has)

	rd := r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgInit))

	err := r.Step(Message{
		MsgType: MsgInitResp,
		Config: Config{
			Role:        RoleLeader,
			Term:        1,
			Replicas:    []uint64{2, 3},
			Learners:    []uint64{4},
			MigrateFrom: 2,
			MigrateTo:   4,
		},
	})
	assert.NoError(t, err)

	err = r.Step(Message{
		MsgType: MsgSyncReq,
		Index:   2,
		From:    4,
	})
	assert.NoError(t, err)

	rd = r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgSyncResp))
	assert.True(t, hasMsg(rd.Messages, MsgLearnerToFollower))
}

// 学习者转领导者者
func TestLearnerToLeader(t *testing.T) {
	r := New(1, WithAutoRoleSwith(true))

	r.appendLog(Log{Index: 1, Term: 1, Data: []byte("hello")})
	r.appendLog(Log{Index: 2, Term: 1, Data: []byte("world")})

	has := r.HasReady()
	assert.True(t, has)

	rd := r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgInit))

	err := r.Step(Message{
		MsgType: MsgInitResp,
		Config: Config{
			Role:        RoleLeader,
			Term:        1,
			Replicas:    []uint64{1, 2, 3},
			Learners:    []uint64{4},
			MigrateFrom: 1,
			MigrateTo:   4,
		},
	})
	assert.NoError(t, err)

	err = r.Step(Message{
		MsgType: MsgSyncReq,
		Index:   3,
		From:    4,
	})
	assert.NoError(t, err)

	rd = r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgSyncResp))
	assert.True(t, hasMsg(rd.Messages, MsgLearnerToLeader))

	// 领导者转好过程中，不能提按
	err = r.Propose([]byte("hi"))
	assert.Equal(t, ErrProposalDropped, err)

	err = r.Step(Message{
		MsgType: MsgConfigResp,
		Config: Config{
			Term:     2,
			Leader:   4,
			Role:     RoleFollower,
			Replicas: []uint64{1, 2, 3, 4},
		},
	})
	assert.NoError(t, err)
}

// 追随者转领导者
func TestFollowerToLeader(t *testing.T) {
	r := New(1, WithAutoRoleSwith(true))

	r.appendLog(Log{Index: 1, Term: 1, Data: []byte("hello")})
	r.appendLog(Log{Index: 2, Term: 1, Data: []byte("world")})

	has := r.HasReady()
	assert.True(t, has)

	rd := r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgInit))

	err := r.Step(Message{
		MsgType: MsgInitResp,
		Config: Config{
			Role:        RoleLeader,
			Term:        1,
			Replicas:    []uint64{1, 2, 3},
			MigrateFrom: 1,
			MigrateTo:   3,
		},
	})
	assert.NoError(t, err)

	err = r.Step(Message{
		MsgType: MsgSyncReq,
		Index:   3,
		From:    3,
	})
	assert.NoError(t, err)

	rd = r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgSyncResp))
	assert.True(t, hasMsg(rd.Messages, MsgFollowerToLeader))
}

func TestReplica(t *testing.T) {

	interval := 2
	leader := New(1, WithSyncIntervalTick(interval))

	err := leader.Step(Message{
		MsgType: MsgInitResp,
		Config: Config{
			Role:     RoleLeader,
			Term:     1,
			Replicas: []uint64{1, 2, 3},
		},
	})
	assert.NoError(t, err)

	follower := New(2, WithSyncIntervalTick(interval))
	err = follower.Step(Message{
		MsgType: MsgInitResp,
		Config: Config{
			Role:     RoleFollower,
			Term:     1,
			Leader:   1,
			Replicas: []uint64{1, 2, 3},
		},
	})
	assert.NoError(t, err)

	err = follower.Step(Message{
		MsgType: MsgLogConflictCheckResp,
	})
	assert.NoError(t, err)

	t.Run("sync", func(t *testing.T) {

		readyMsgZero(t, follower, interval)

		rd := follower.Ready()
		assert.Equal(t, 1, len(rd.Messages))

	})

	t.Run("syncFailed", func(t *testing.T) {
		err = follower.Step(Message{
			MsgType: MsgSyncResp,
			Reject:  true,
		})
		assert.NoError(t, err)

		readyMsgZero(t, follower, interval*3)
	})

	t.Run("syncDelay", func(t *testing.T) {
		err = follower.Step(Message{
			MsgType: MsgSyncResp,
			Reject:  false,
		})
		assert.NoError(t, err)

		readyMsgZero(t, follower, interval*3)
	})

	t.Run("syncImmediately", func(t *testing.T) {
		err = follower.Step(Message{
			MsgType: MsgSyncResp,
			Reject:  false,
			Logs: []Log{
				{
					Id:    123,
					Index: 1,
					Term:  1,
				},
			},
		})
		assert.NoError(t, err)

		rd := follower.Ready()
		hasMsg(rd.Messages, MsgSyncReq)

	})

}

func readyMsgZero(t *testing.T, r *Replica, count int) {
	for i := 0; i < count; i++ {
		rd := r.Ready()
		assert.Equal(t, 0, len(rd.Messages))
		r.Tick()
	}
}

func TestLogAppendResp(t *testing.T) {
	leader := New(1, WithSyncIntervalTick(1))
	err := leader.Step(Message{
		MsgType: MsgInitResp,
		Config: Config{
			Role:     RoleLeader,
			Term:     1,
			Leader:   1,
			Replicas: []uint64{1, 2, 3},
		},
	})
	assert.NoError(t, err)

	err = leader.Propose([]byte("hello"))
	assert.NoError(t, err)

	t.Run("appendRespFail", func(t *testing.T) {

		rd := leader.Ready()
		assert.Equal(t, true, hasMsg(rd.Messages, MsgStoreAppend))

		err = leader.Step(Message{
			MsgType: MsgStoreAppendResp,
			Reject:  true,
		})
		assert.NoError(t, err)

		for i := 0; i < leader.opts.RetryTick-1; i++ {
			leader.Tick()
		}
		rd = leader.Ready()
		assert.Equal(t, false, hasMsg(rd.Messages, MsgStoreAppend))

		leader.Tick()

		rd = leader.Ready()
		assert.Equal(t, true, hasMsg(rd.Messages, MsgStoreAppend))

	})
}
