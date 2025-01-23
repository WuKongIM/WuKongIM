package raft_test

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/raft"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/stretchr/testify/assert"
)

func TestElection(t *testing.T) {

	raft1, raft2, raft3 := newThreeRaft()
	err := raft1.Start()
	assert.Nil(t, err)

	err = raft2.Start()
	assert.Nil(t, err)

	err = raft3.Start()
	assert.Nil(t, err)

	defer raft1.Stop()
	defer raft2.Stop()
	defer raft3.Stop()

	waitBecomeLeader(raft1, raft2, raft3)

}

func TestElection2(t *testing.T) {
	opts1, opts2, opts3 := newThreeOptions()

	s1Storage := opts1.Storage.(*testStorage)
	s2Storage := opts2.Storage.(*testStorage)
	s3Storage := opts3.Storage.(*testStorage)

	s1Storage.logs = append(s1Storage.logs, types.Log{
		Index: 1,
		Term:  1,
		Data:  []byte("log1-1"),
	})
	s2Storage.logs = append(s2Storage.logs, types.Log{
		Index: 1,
		Term:  1,
		Data:  []byte("log1-1"),
	}, types.Log{
		Index: 2,
		Term:  1,
		Data:  []byte("log1-2"),
	})

	s3Storage.logs = append(s3Storage.logs, types.Log{
		Index: 1,
		Term:  1,
		Data:  []byte("log1-1"),
	}, types.Log{
		Index: 2,
		Term:  1,
		Data:  []byte("log1-2"),
	})

	s1 := raft.New(opts1)
	s2 := raft.New(opts2)
	s3 := raft.New(opts3)

	// 设置传输层
	tt := &testTransport{
		raftMap: map[uint64]*raft.Raft{
			1: s1,
			2: s2,
			3: s3,
		},
	}

	opts1.Transport = tt
	opts2.Transport = tt
	opts3.Transport = tt

	raftStart(t, s1, s2, s3)
	defer raftStop(s1, s2, s3)

	// 无任选举多少次 s1不应该当选
	electionCount := 10
	for i := 0; i < electionCount; i++ {
		raftCampaign(t, s1, s2, s3)
		waitBecomeLeader(s1, s2, s3)
		assert.Equal(t, false, s1.IsLeader())
	}

}

func TestPropose(t *testing.T) {
	raft1, raft2, raft3 := newThreeRaft()
	err := raft1.Start()
	assert.Nil(t, err)

	err = raft2.Start()
	assert.Nil(t, err)

	err = raft3.Start()
	assert.Nil(t, err)

	defer raft1.Stop()
	defer raft2.Stop()
	defer raft3.Stop()

	waitBecomeLeader(raft1, raft2, raft3)

	leader := getLeader(raft1, raft2, raft3)
	_, err = leader.Propose(1, []byte("test"))
	assert.Nil(t, err)

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raft1.WaitUtilCommit(timeoutCtx, 1)
	raft2.WaitUtilCommit(timeoutCtx, 1)
	raft3.WaitUtilCommit(timeoutCtx, 1)

	node1Logs := raft1.Options().Storage.(*testStorage).logs
	node2Logs := raft2.Options().Storage.(*testStorage).logs
	node3Logs := raft3.Options().Storage.(*testStorage).logs

	assert.Equal(t, 1, len(node1Logs))
	assert.Equal(t, 1, len(node2Logs))
	assert.Equal(t, 1, len(node3Logs))

	assert.Equal(t, node1Logs[0].Index, node2Logs[0].Index, node3Logs[0].Index)
	assert.Equal(t, node1Logs[0].Data, node2Logs[0].Data, node3Logs[0].Data)

}

// 冲突一
// s1有消息M1,M2任期都为1 s2有消息M1，M3，M3的任期为2
// 测试s2成为领导后，M3应该覆盖s1的M2， 因为M3的任期更大
func TestLogConflict1(t *testing.T) {
	opts1, opts2, opts3 := newThreeOptions()

	s1Storage := opts1.Storage.(*testStorage)
	s2Storage := opts2.Storage.(*testStorage)
	s3Storage := opts3.Storage.(*testStorage)

	s1Storage.logs = append(s1Storage.logs, types.Log{
		Index: 1,
		Term:  1,
		Data:  []byte("M1"),
	}, types.Log{
		Index: 2,
		Term:  1,
		Data:  []byte("M2"),
	})
	s1Storage.saveTermStartIndex(&types.TermStartIndexInfo{
		Term:  1,
		Index: 1,
	})

	s2Storage.logs = append(s2Storage.logs, types.Log{
		Index: 1,
		Term:  1,
		Data:  []byte("M1"),
	}, types.Log{
		Index: 2,
		Term:  2,
		Data:  []byte("M3"),
	})
	s2Storage.saveTermStartIndex(&types.TermStartIndexInfo{
		Term:  1,
		Index: 1,
	})
	s2Storage.saveTermStartIndex(&types.TermStartIndexInfo{
		Term:  2,
		Index: 2,
	})

	s1 := raft.New(opts1)
	s2 := raft.New(opts2)
	s3 := raft.New(opts3)

	// 设置传输层
	tt := &testTransport{
		raftMap: map[uint64]*raft.Raft{
			1: s1,
			2: s2,
			3: s3,
		},
	}

	opts1.Transport = tt
	opts2.Transport = tt
	opts3.Transport = tt

	raftStart(t, s1, s2, s3)
	defer raftStop(s1, s2, s3)

	// s2成为领导者，观察s1的日志M3是否覆盖M2
	s2.BecomeLeader(2)

	time.Sleep(time.Millisecond * 400)

	for i := 0; i < 2; i++ {
		assert.Equal(t, s1Storage.logs[i].Data, s2Storage.logs[i].Data)
		assert.Equal(t, s1Storage.logs[i].Term, s2Storage.logs[i].Term)
		assert.Equal(t, s1Storage.logs[i].Index, s2Storage.logs[i].Index)

		assert.Equal(t, s3Storage.logs[i].Data, s2Storage.logs[i].Data)
		assert.Equal(t, s3Storage.logs[i].Term, s2Storage.logs[i].Term)
		assert.Equal(t, s3Storage.logs[i].Index, s2Storage.logs[i].Index)
	}

}

func TestLogConflict2(t *testing.T) {
	opts1, opts2, opts3 := newThreeOptions()

	s1Storage := opts1.Storage.(*testStorage)
	s2Storage := opts2.Storage.(*testStorage)
	s3Storage := opts3.Storage.(*testStorage)

	s1Storage.logs = append(s1Storage.logs, types.Log{
		Index: 1,
		Term:  1,
		Data:  []byte("M1"),
	}, types.Log{
		Index: 2,
		Term:  1,
		Data:  []byte("M2"),
	}, types.Log{
		Index: 3,
		Term:  1,
		Data:  []byte("M3"),
	})
	s1Storage.saveTermStartIndex(&types.TermStartIndexInfo{
		Term:  1,
		Index: 1,
	})

	s2Storage.logs = append(s2Storage.logs, types.Log{
		Index: 1,
		Term:  1,
		Data:  []byte("M1"),
	}, types.Log{
		Index: 2,
		Term:  2,
		Data:  []byte("M3"),
	})
	s2Storage.saveTermStartIndex(&types.TermStartIndexInfo{
		Term:  1,
		Index: 1,
	})
	s2Storage.saveTermStartIndex(&types.TermStartIndexInfo{
		Term:  2,
		Index: 2,
	})

	s1 := raft.New(opts1)
	s2 := raft.New(opts2)
	s3 := raft.New(opts3)

	// 设置传输层
	tt := &testTransport{
		raftMap: map[uint64]*raft.Raft{
			1: s1,
			2: s2,
			3: s3,
		},
	}

	opts1.Transport = tt
	opts2.Transport = tt
	opts3.Transport = tt

	raftStart(t, s1, s2, s3)
	defer raftStop(s1, s2, s3)

	// s2成为领导者，观察s1的日志M3是否覆盖M2
	s2.BecomeLeader(2)

	time.Sleep(time.Millisecond * 400)

	for i := 0; i < 2; i++ {
		assert.Equal(t, s1Storage.logs[i].Data, s2Storage.logs[i].Data)
		assert.Equal(t, s1Storage.logs[i].Term, s2Storage.logs[i].Term)
		assert.Equal(t, s1Storage.logs[i].Index, s2Storage.logs[i].Index)

		assert.Equal(t, s3Storage.logs[i].Data, s2Storage.logs[i].Data)
		assert.Equal(t, s3Storage.logs[i].Term, s2Storage.logs[i].Term)
		assert.Equal(t, s3Storage.logs[i].Index, s2Storage.logs[i].Index)
	}

}

func TestProposeUntilApplied(t *testing.T) {
	raft1, raft2, raft3 := newThreeRaft()
	err := raft1.Start()
	assert.Nil(t, err)

	err = raft2.Start()
	assert.Nil(t, err)

	err = raft3.Start()
	assert.Nil(t, err)

	defer raft1.Stop()
	defer raft2.Stop()
	defer raft3.Stop()

	waitBecomeLeader(raft1, raft2, raft3)

	leader := getLeader(raft1, raft2, raft3)

	_, err = leader.ProposeUntilApplied(1, []byte("test"))
	assert.Nil(t, err)

	node1Logs := raft1.Options().Storage.(*testStorage).logs
	node2Logs := raft2.Options().Storage.(*testStorage).logs
	node3Logs := raft3.Options().Storage.(*testStorage).logs

	assert.Equal(t, 1, len(node1Logs))
	assert.Equal(t, 1, len(node2Logs))
	assert.Equal(t, 1, len(node3Logs))

	assert.Equal(t, node1Logs[0].Index, node2Logs[0].Index, node3Logs[0].Index)
	assert.Equal(t, node1Logs[0].Data, node2Logs[0].Data, node3Logs[0].Data)
}

func newTestOptions(nodeId uint64, replicas []uint64, opt ...raft.Option) *raft.Options {
	optList := make([]raft.Option, 0)
	optList = append(optList, raft.WithElectionInterval(5), raft.WithNodeId(nodeId), raft.WithReplicas(replicas), raft.WithTransport(&testTransport{}), raft.WithStorage(newTestStorage(nodeId)))
	optList = append(optList, opt...)
	opts := raft.NewOptions(optList...)
	return opts
}

type testTransport struct {
	raftMap map[uint64]*raft.Raft
}

func (t *testTransport) Send(event types.Event) {
	r, ok := t.raftMap[event.To]
	if !ok {
		return
	}
	r.Step(event)
}

type testStorage struct {
	nodeId          uint64
	logs            []types.Log
	termStartIndexs []*types.TermStartIndexInfo
}

func newTestStorage(nodeId uint64) *testStorage {
	return &testStorage{
		nodeId: nodeId,
	}
}

func (s *testStorage) AppendLogs(logs []types.Log, termStartIndex *types.TermStartIndexInfo) error {
	s.logs = append(s.logs, logs...)
	if termStartIndex != nil {
		s.termStartIndexs = append(s.termStartIndexs, termStartIndex)
	}
	return nil
}

func (s *testStorage) saveTermStartIndex(termStartIndex *types.TermStartIndexInfo) {
	s.termStartIndexs = append(s.termStartIndexs, termStartIndex)
}

func (s *testStorage) GetLogs(start, end uint64, limitSize uint64) ([]types.Log, error) {
	return s.logs[start-1:], nil
}

func (s *testStorage) GetState() (types.RaftState, error) {
	if len(s.logs) == 0 {
		return types.RaftState{}, nil
	}
	lastLog := s.logs[len(s.logs)-1]
	return types.RaftState{
		LastLogIndex: lastLog.Index,
		LastTerm:     lastLog.Term,
		AppliedIndex: lastLog.Index,
	}, nil
}

func (s *testStorage) GetTermStartIndex(term uint32) (uint64, error) {
	for _, tsi := range s.termStartIndexs {
		if tsi.Term == term {
			return tsi.Index, nil
		}
	}
	return 0, nil
}

func (s *testStorage) TruncateLogTo(index uint64) error {
	s.logs = s.logs[:index]
	return nil
}

func (s *testStorage) DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error {
	newTermStartIndexs := make([]*types.TermStartIndexInfo, 0)
	for _, tsi := range s.termStartIndexs {
		if tsi.Term <= term {
			newTermStartIndexs = append(newTermStartIndexs, tsi)
		}
	}
	s.termStartIndexs = newTermStartIndexs
	return nil
}

func (s *testStorage) LeaderTermGreaterEqThan(term uint32) (uint32, error) {

	for _, tsi := range s.termStartIndexs {
		if tsi.Term >= term {
			return tsi.Term, nil
		}
	}
	return 0, nil
}

func (s *testStorage) LeaderLastTerm() (uint32, error) {
	if len(s.termStartIndexs) == 0 {
		return 0, nil
	}
	return s.termStartIndexs[len(s.termStartIndexs)-1].Term, nil
}

func (s *testStorage) Apply(logs []types.Log) error {

	return nil
}

func (s *testStorage) SaveConfig(cfg types.Config) error {
	return nil
}

// 等到某个节点成为领导者
func waitBecomeLeader(rr ...*raft.Raft) {
	for {
		for _, r := range rr {
			if r.IsLeader() {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func getLeader(rr ...*raft.Raft) *raft.Raft {
	for _, r := range rr {
		if r.IsLeader() {
			return r
		}
	}
	return nil
}

func newThreeRaft() (*raft.Raft, *raft.Raft, *raft.Raft) {
	// node1
	opts1 := newTestOptions(1, []uint64{1, 2, 3}, raft.WithElectionOn(true))
	raft1 := raft.New(opts1)

	// node2
	opts2 := newTestOptions(2, []uint64{1, 2, 3}, raft.WithElectionOn(true))
	raft2 := raft.New(opts2)

	// node3
	opts3 := newTestOptions(3, []uint64{1, 2, 3}, raft.WithElectionOn(true))
	raft3 := raft.New(opts3)

	tt := &testTransport{
		raftMap: map[uint64]*raft.Raft{
			1: raft1,
			2: raft2,
			3: raft3,
		},
	}

	opts1.Transport = tt
	opts2.Transport = tt
	opts3.Transport = tt

	return raft1, raft2, raft3
}

func newThreeOptions(opt ...raft.Option) (*raft.Options, *raft.Options, *raft.Options) {

	defaultOpts := make([]raft.Option, 0)
	defaultOpts = append(defaultOpts, raft.WithElectionOn(true))
	defaultOpts = append(defaultOpts, opt...)

	// node1
	opts1 := newTestOptions(1, []uint64{1, 2, 3}, defaultOpts...)

	// node2
	opts2 := newTestOptions(2, []uint64{1, 2, 3}, defaultOpts...)

	// node3
	opts3 := newTestOptions(3, []uint64{1, 2, 3}, defaultOpts...)

	return opts1, opts2, opts3
}

func raftStart(t *testing.T, rafts ...*raft.Raft) {
	for _, r := range rafts {
		err := r.Start()
		assert.Nil(t, err)
	}
}

func raftCampaign(_ *testing.T, rafts ...*raft.Raft) {
	for _, r := range rafts {
		r.Step(types.Event{
			Type: types.Campaign,
		})
	}
}

func raftStop(rafts ...*raft.Raft) {
	for _, r := range rafts {
		r.Stop()
	}
}
