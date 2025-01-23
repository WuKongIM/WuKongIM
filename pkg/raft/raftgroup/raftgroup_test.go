package raftgroup_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/raft"
	"github.com/WuKongIM/WuKongIM/pkg/raft/raftgroup"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/stretchr/testify/assert"
)

func TestElection(t *testing.T) {

	tt := &testTransport{
		groups: make(map[uint64]*raftgroup.RaftGroup),
	}
	rg1 := raftgroup.New(newTestOptions(raftgroup.WithTransport(tt), raftgroup.WithStorage(newTestStorage())))
	rg2 := raftgroup.New(newTestOptions(raftgroup.WithTransport(tt), raftgroup.WithStorage(newTestStorage())))

	tt.groups[1] = rg1
	tt.groups[2] = rg2

	err := rg1.Start()
	assert.Nil(t, err)
	err = rg2.Start()
	assert.Nil(t, err)

	defer rg1.Stop()
	defer rg2.Stop()

	node1 := newTestRaftNode("ch001", 1, 0, types.RaftState{}, raft.WithElectionOn(true), raft.WithReplicas([]uint64{1, 2}))
	node2 := newTestRaftNode("ch001", 2, 0, types.RaftState{}, raft.WithElectionOn(true), raft.WithReplicas([]uint64{1, 2}))
	rg1.AddRaft(node1)
	rg2.AddRaft(node2)

	waitHasLeader(node1)
	waitHasLeader(node2)
}

func TestPropose(t *testing.T) {
	tt := &testTransport{
		groups: make(map[uint64]*raftgroup.RaftGroup),
	}
	rg1 := raftgroup.New(newTestOptions(raftgroup.WithTransport(tt), raftgroup.WithStorage(newTestStorage())))
	rg2 := raftgroup.New(newTestOptions(raftgroup.WithTransport(tt), raftgroup.WithStorage(newTestStorage())))

	tt.groups[1] = rg1
	tt.groups[2] = rg2

	err := rg1.Start()
	assert.Nil(t, err)
	err = rg2.Start()
	assert.Nil(t, err)

	defer rg1.Stop()
	defer rg2.Stop()

	node1 := newTestRaftNode(
		"ch001",
		1,
		0,
		types.RaftState{},
		raft.WithElectionInterval(5),
		raft.WithElectionOn(true),
		raft.WithReplicas([]uint64{1, 2}),
		raft.WithAdvance(rg1.Advance),
	)
	node2 := newTestRaftNode(
		"ch001",
		2,
		0,
		types.RaftState{},
		raft.WithElectionInterval(5),
		raft.WithElectionOn(true),
		raft.WithReplicas([]uint64{1, 2}),
		raft.WithAdvance(rg2.Advance),
	)
	rg1.AddRaft(node1)
	rg2.AddRaft(node2)

	waitHasLeader(node1)
	waitHasLeader(node2)

	groupLeader := getRaftLeader("ch001", rg1, rg2)

	start := time.Now()
	resp, err := groupLeader.Propose("ch001", 100, []byte("hello"))
	assert.Nil(t, err)

	assert.Equal(t, uint64(100), resp.Id)
	assert.Equal(t, uint64(1), resp.Index)

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	waitUtilCommit(timeoutCtx, node1, 1)
	waitUtilCommit(timeoutCtx, node2, 1)

	cost := time.Since(start)
	fmt.Println("cost:", cost)

	node1Store := rg1.Options().Storage.(*testStorage)
	node2Store := rg2.Options().Storage.(*testStorage)

	logs1 := node1Store.logs["ch001"]
	logs2 := node2Store.logs["ch001"]
	assert.Equal(t, 1, len(logs1))
	assert.Equal(t, 1, len(logs2))

	for _, log := range logs1 {
		fmt.Println(log.Record.String())
	}
	for _, log := range logs2 {
		fmt.Println(log.Record.String())
	}

	fmt.Println(resp)

}

func BenchmarkPropose(b *testing.B) {
	tt := &testTransport{
		groups: make(map[uint64]*raftgroup.RaftGroup),
	}
	rg1 := raftgroup.New(newTestOptions(raftgroup.WithTransport(tt), raftgroup.WithStorage(newTestStorage())))
	rg2 := raftgroup.New(newTestOptions(raftgroup.WithTransport(tt), raftgroup.WithStorage(newTestStorage())))

	tt.groups[1] = rg1
	tt.groups[2] = rg2

	err := rg1.Start()
	if err != nil {
		b.Fatalf("Failed to start RaftGroup 1: %v", err)
	}
	err = rg2.Start()
	if err != nil {
		b.Fatalf("Failed to start RaftGroup 2: %v", err)
	}

	defer rg1.Stop()
	defer rg2.Stop()

	node1 := newTestRaftNode("ch001", 1, 0, types.RaftState{}, raft.WithElectionInterval(5), raft.WithElectionOn(true), raft.WithReplicas([]uint64{2}))
	node2 := newTestRaftNode("ch001", 2, 0, types.RaftState{}, raft.WithElectionInterval(5), raft.WithElectionOn(true), raft.WithReplicas([]uint64{1}))
	rg1.AddRaft(node1)
	rg2.AddRaft(node2)

	waitHasLeader(node1)
	waitHasLeader(node2)

	groupLeader := getRaftLeader("ch001", rg1, rg2)

	// 重置计时器以排除初始化时间
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := groupLeader.Propose("ch001", uint64(b.N), []byte("benchmark-data"))
			if err != nil {
				b.Errorf("Propose failed: %v", err)
			}
		}
	})

	// 停止计时器以防止清理时间影响结果
	b.StopTimer()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	// 确保所有日志提交成功
	waitUtilCommit(timeoutCtx, node1, uint64(b.N))
	waitUtilCommit(timeoutCtx, node2, uint64(b.N))

	node1Store := rg1.Options().Storage.(*testStorage)
	node2Store := rg2.Options().Storage.(*testStorage)

	if len(node1Store.logs["ch001"]) != b.N || len(node2Store.logs["ch001"]) != b.N {
		b.Errorf("Log entries mismatch: node1=%d, node2=%d, expected=%d",
			len(node1Store.logs["ch001"]), len(node2Store.logs["ch001"]), b.N)
	}
}

func TestProposeBatch(t *testing.T) {
	tt := &testTransport{
		groups: make(map[uint64]*raftgroup.RaftGroup),
	}
	rg1 := raftgroup.New(newTestOptions(raftgroup.WithTransport(tt), raftgroup.WithStorage(newTestStorage())))
	rg2 := raftgroup.New(newTestOptions(raftgroup.WithTransport(tt), raftgroup.WithStorage(newTestStorage())))

	tt.groups[1] = rg1
	tt.groups[2] = rg2

	err := rg1.Start()
	assert.Nil(t, err)
	err = rg2.Start()
	assert.Nil(t, err)

	defer rg1.Stop()
	defer rg2.Stop()

	node1 := newTestRaftNode("ch001", 1, 0, types.RaftState{}, raft.WithElectionInterval(5), raft.WithElectionOn(true), raft.WithReplicas([]uint64{2}))
	node2 := newTestRaftNode("ch001", 2, 0, types.RaftState{}, raft.WithElectionInterval(5), raft.WithElectionOn(true), raft.WithReplicas([]uint64{1}))
	rg1.AddRaft(node1)
	rg2.AddRaft(node2)

	waitHasLeader(node1)
	waitHasLeader(node2)

	groupLeader := getRaftLeader("ch001", rg1, rg2)

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	reqs := make([]types.ProposeReq, 0)
	for i := 0; i < 10; i++ {
		reqs = append(reqs, types.ProposeReq{
			Id:   uint64(i + 100),
			Data: []byte(fmt.Sprintf("hello %d", i)),
		})
	}
	resps, err := groupLeader.ProposeBatchTimeout(timeoutCtx, "ch001", reqs)
	assert.Nil(t, err)

	waitUtilCommit(timeoutCtx, node1, uint64(len(reqs)))
	waitUtilCommit(timeoutCtx, node2, uint64(len(reqs)))

	for i, resp := range resps {
		assert.Equal(t, uint64(i+100), resp.Id)
		assert.Equal(t, uint64(i+1), resp.Index)
	}

	node1Store := rg1.Options().Storage.(*testStorage)
	node2Store := rg2.Options().Storage.(*testStorage)

	assert.Equal(t, len(reqs), len(node1Store.logs["ch001"]))
	assert.Equal(t, len(reqs), len(node2Store.logs["ch001"]))

}

func TestApply(t *testing.T) {
	tt := &testTransport{
		groups: make(map[uint64]*raftgroup.RaftGroup),
	}
	rg1 := raftgroup.New(newTestOptions(raftgroup.WithTransport(tt), raftgroup.WithStorage(newTestStorage())))
	rg2 := raftgroup.New(newTestOptions(raftgroup.WithTransport(tt), raftgroup.WithStorage(newTestStorage())))

	tt.groups[1] = rg1
	tt.groups[2] = rg2

	err := rg1.Start()
	assert.Nil(t, err)
	err = rg2.Start()
	assert.Nil(t, err)

	defer rg1.Stop()
	defer rg2.Stop()

	node1 := newTestRaftNode("ch001", 1, 0, types.RaftState{}, raft.WithElectionInterval(5), raft.WithElectionOn(true), raft.WithReplicas([]uint64{2}))
	node2 := newTestRaftNode("ch001", 2, 0, types.RaftState{}, raft.WithElectionInterval(5), raft.WithElectionOn(true), raft.WithReplicas([]uint64{1}))
	rg1.AddRaft(node1)
	rg2.AddRaft(node2)

	waitHasLeader(node1)
	waitHasLeader(node2)

	groupLeader := getRaftLeader("ch001", rg1, rg2)
	_, err = groupLeader.Propose("ch001", 100, []byte("hello"))
	assert.Nil(t, err)

	node1Store := rg1.Options().Storage.(*testStorage)
	node2Store := rg2.Options().Storage.(*testStorage)

	applyLogs1 := <-node1Store.applyC
	applyLogs2 := <-node2Store.applyC

	assert.Equal(t, 1, len(applyLogs1))
	assert.Equal(t, 1, len(applyLogs2))

	assert.Equal(t, applyLogs1[0].Index, applyLogs2[0].Index)
	assert.Equal(t, applyLogs1[0].Term, applyLogs2[0].Term)
	assert.Equal(t, applyLogs1[0].Data, applyLogs2[0].Data)
}

func newTestOptions(opt ...raftgroup.Option) *raftgroup.Options {
	opts := raftgroup.NewOptions(opt...)
	return opts
}

func newTestRaftNode(key string, nodeId uint64, lastTermStartLogIndex uint64, raftState types.RaftState, opt ...raft.Option) *raft.Node {

	defaultOpts := make([]raft.Option, 0)
	defaultOpts = append(defaultOpts, raft.WithNodeId(nodeId), raft.WithKey(key))
	defaultOpts = append(defaultOpts, opt...)

	node := raft.NewNode(lastTermStartLogIndex, raftState, raft.NewOptions(defaultOpts...))

	return node

}

type testTransport struct {
	groups map[uint64]*raftgroup.RaftGroup
}

func (t *testTransport) Send(key string, event types.Event) {
	// fmt.Println("event--->", event)
	g := t.groups[event.To]
	g.AddEvent(key, event)

	g.Advance()
}

type testStorage struct {
	logs            map[string][]types.Log
	termStartIndexs map[string]types.TermStartIndexInfo
	applyC          chan []types.Log
	sync.RWMutex
}

func newTestStorage() *testStorage {
	return &testStorage{
		logs:            make(map[string][]types.Log),
		termStartIndexs: make(map[string]types.TermStartIndexInfo),
		applyC:          make(chan []types.Log, 1024),
	}
}

func (t *testStorage) AppendLogs(key string, logs []types.Log, termStartIndexInfo *types.TermStartIndexInfo) error {
	t.Lock()
	defer t.Unlock()
	if _, ok := t.logs[key]; !ok {
		t.logs[key] = make([]types.Log, 0)
	}
	t.logs[key] = append(t.logs[key], logs...)

	if termStartIndexInfo != nil {
		t.termStartIndexs[key] = *termStartIndexInfo
	}

	return nil
}

func (t *testStorage) Apply(key string, logs []types.Log) error {
	t.applyC <- logs
	return nil
}

func (t *testStorage) GetTermStartIndex(key string, term uint32) (uint64, error) {

	termStartIndex, ok := t.termStartIndexs[key]
	if !ok {
		return 0, nil
	}
	return termStartIndex.Index, nil
}

func (t *testStorage) LeaderLastLogTerm(key string) (uint32, error) {

	logs, ok := t.logs[key]
	if !ok {
		return 0, nil
	}
	lastLog := logs[len(logs)-1]

	return lastLog.Term, nil
}

func (t *testStorage) LeaderTermGreaterEqThan(key string, term uint32) (uint32, error) {

	termStartIndex, ok := t.termStartIndexs[key]
	if !ok {
		return 0, nil
	}

	if termStartIndex.Term >= term {
		return termStartIndex.Term, nil
	}

	return 0, nil
}

func (t *testStorage) GetLogs(key string, start, end, maxSize uint64) ([]types.Log, error) {

	t.Lock()
	defer t.Unlock()

	logs, ok := t.logs[key]
	if !ok {
		return nil, nil
	}

	if start > uint64(len(logs)) {
		return nil, nil
	}
	if start-1+maxSize > uint64(len(logs)) {
		return logs[start-1:], nil
	}
	return logs[start-1 : end-1+maxSize], nil
}

func (t *testStorage) GetState(key string) (*types.RaftState, error) {

	logs, ok := t.logs[key]
	if !ok {
		return nil, nil
	}
	lastLog := logs[len(logs)-1]

	return &types.RaftState{
		LastTerm:     lastLog.Term,
		LastLogIndex: lastLog.Index,
		AppliedIndex: lastLog.Index,
	}, nil
}

func (t *testStorage) TruncateLogTo(key string, index uint64) error {

	t.Lock()
	defer t.Unlock()

	logs, ok := t.logs[key]
	if !ok {
		return nil
	}

	if index >= uint64(len(logs)) {
		return nil
	}

	t.logs[key] = logs[:index]

	return nil
}

func (t *testStorage) DeleteLeaderTermStartIndexGreaterThanTerm(key string, term uint32) error {

	t.Lock()
	defer t.Unlock()

	termStartIndex, ok := t.termStartIndexs[key]
	if !ok {
		return nil
	}

	if termStartIndex.Term <= term {
		return nil
	}

	delete(t.termStartIndexs, key)

	return nil
}

func (t *testStorage) SaveConfig(key string, cfg types.Config) error {
	return nil
}

func (t *testStorage) GetConfig(key string) (types.Config, error) {
	return types.Config{}, nil
}

// 等到选举出领导者
func waitHasLeader(rr ...raftgroup.IRaft) {
	for {
		for _, r := range rr {
			if r.LeaderId() > 0 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func getRaftLeader(raftKey string, rr ...*raftgroup.RaftGroup) *raftgroup.RaftGroup {
	for _, r := range rr {
		if r.IsLeader(raftKey) {
			return r
		}
	}
	return nil
}

// waitUtilCommit 等待日志提交到指定的下标
func waitUtilCommit(ctx context.Context, raft raftgroup.IRaft, index uint64) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if raft.CommittedIndex() >= index {
			return nil
		}
		time.Sleep(time.Millisecond * 10)
	}
}
