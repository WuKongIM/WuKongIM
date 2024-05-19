package cluster

import (
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

var _ reactor.IHandler = &slot{}

type slot struct {
	rc         *replica.Replica
	key        string
	st         *pb.Slot
	isPrepared bool
	wklog.Log
	opts *Options

	mu sync.Mutex

	pausePropopose atomic.Bool // 是否暂停提案
}

func newSlot(st *pb.Slot, opts *Options) *slot {
	s := &slot{
		key:        SlotIdToKey(st.Id),
		st:         st,
		opts:       opts,
		Log:        wklog.NewWKLog(fmt.Sprintf("slot[%d]", st.Id)),
		isPrepared: true,
	}
	appliedIdx, err := opts.SlotLogStorage.AppliedIndex(s.key)
	if err != nil {
		s.Panic("get applied index error", zap.Error(err))

	}
	s.rc = replica.New(opts.NodeId, replica.WithLogPrefix(fmt.Sprintf("slot-%d", st.Id)), replica.WithAppliedIndex(appliedIdx), replica.WithElectionOn(false), replica.WithConfig(&replica.Config{
		Replicas: st.Replicas,
	}), replica.WithStorage(newProxyReplicaStorage(s.key, s.opts.SlotLogStorage)))
	return s
}

func (s *slot) becomeFollower(term uint32, leader uint64) {
	s.rc.BecomeFollower(term, leader)
}

func (s *slot) becomeLeader(term uint32) {
	s.rc.BecomeLeader(term)
}

func (s *slot) changeRole(role replica.Role) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch role {
	case replica.RoleLeader:
		s.rc.BecomeLeader(s.rc.Term())
	case replica.RoleCandidate:
		s.rc.BecomeCandidateWithTerm(s.rc.Term())
	case replica.RoleFollower:
		s.rc.BecomeFollower(s.rc.Term(), s.rc.LeaderId())
	}
}

func (s *slot) update(st *pb.Slot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st = st

	isLearner := false
	var learnerIds []uint64
	if len(st.Learners) > 0 {
		for _, learner := range st.Learners {
			if learner.LearnerId == s.opts.NodeId {
				isLearner = true
			}
			learnerIds = append(learnerIds, learner.LearnerId)
		}
	}

	cfg := &replica.Config{
		Replicas: st.Replicas,
		Learners: learnerIds,
	}

	s.rc.SetConfig(cfg)

	if isLearner {
		s.rc.BecomeLearner(st.Term, st.Leader)
	} else {
		if st.Status == pb.SlotStatus_SlotStatusLeaderTransfer { // 槽进入领导者转移状态
			if st.Leader == s.opts.NodeId && st.LeaderTransferTo != st.Leader { // 如果当前槽领导将要被转移，则先暂停提案，等需要转移的节点的日志追上来
				s.pausePropopose.Store(true)
			}
		} else if st.Status == pb.SlotStatus_SlotStatusNormal { // 槽进入正常状态

			if st.Leader == s.opts.NodeId {
				s.pausePropopose.Store(false)
			}
		}

		if st.Status == pb.SlotStatus_SlotStatusCandidate { // 槽进入候选者状态
			if s.rc.IsLeader() { // 领导节点不能直接转candidate，replica里会panic，领导转换成follower是一样的
				s.rc.BecomeFollower(st.Term, 0)
			} else {
				s.rc.BecomeCandidateWithTerm(st.Term)
			}

		} else {
			if st.Leader == s.opts.NodeId {
				s.rc.BecomeLeader(st.Term)
			} else {
				s.rc.BecomeFollower(st.Term, st.Leader)
			}
		}
	}

}

// --------------------------IHandler-------------------------------

func (s *slot) LastLogIndexAndTerm() (uint64, uint32) {

	return s.rc.LastLogIndex(), s.rc.Term()
}

func (s *slot) HasReady() bool {
	return s.rc.HasReady()
}

func (s *slot) Ready() replica.Ready {

	return s.rc.Ready()
}

func (s *slot) GetAndMergeLogs(lastIndex uint64, msg replica.Message) ([]replica.Log, error) {

	unstableLogs := msg.Logs
	startIndex := msg.Index
	if len(unstableLogs) > 0 {
		startIndex = unstableLogs[len(unstableLogs)-1].Index + 1
	}
	shardNo := s.key
	var err error
	if lastIndex == 0 {
		lastIndex, err = s.opts.SlotLogStorage.LastIndex(shardNo)
		if err != nil {
			s.Error("GetAndMergeLogs: get last index error", zap.Error(err))
			return nil, err
		}
	}

	var resultLogs []replica.Log
	if startIndex <= lastIndex {
		logs, err := s.getLogs(startIndex, lastIndex+1, uint64(s.opts.LogSyncLimitSizeOfEach))
		if err != nil {
			s.Error("get logs error", zap.Error(err), zap.Uint64("startIndex", startIndex), zap.Uint64("lastIndex", lastIndex))
			return nil, err
		}
		startLogLen := len(logs)
		// 检查logs的连续性，只保留连续的日志
		for i, log := range logs {
			if log.Index != startIndex+uint64(i) {
				logs = logs[:i]
				break
			}
		}
		if len(logs) != startLogLen {
			s.Warn("the log is not continuous and has been truncated ", zap.Uint64("lastIndex", lastIndex), zap.Uint64("msgIndex", msg.Index), zap.Int("startLogLen", startLogLen), zap.Int("endLogLen", len(logs)))
		}

		s.Debug("getLogs...", zap.Uint64("startIndex", startIndex), zap.Uint64("lastIndex", lastIndex+1), zap.Int("logs", len(logs)))
		resultLogs = extend(unstableLogs, logs)
	} else {
		resultLogs = unstableLogs
	}
	return resultLogs, nil

}

func (s *slot) ApplyLog(startLogIndex, endLogIndex uint64) error {

	if s.opts.OnSlotApply != nil {
		start := time.Now()
		defer func() {
			s.Debug("apply log", zap.Duration("cost", time.Since(start)), zap.Uint64("startLogIndex", startLogIndex), zap.Uint64("endLogIndex", endLogIndex))

		}()
		logs, err := s.getLogs(startLogIndex, endLogIndex, 0)
		if err != nil {
			s.Panic("get logs error", zap.Error(err))
		}
		if len(logs) == 0 {
			s.Panic("logs is empty", zap.Uint64("startLogIndex", startLogIndex), zap.Uint64("endLogIndex", endLogIndex))
		}
		err = s.opts.OnSlotApply(s.st.Id, logs)
		if err != nil {
			s.Panic("on slot apply error", zap.Error(err))
		}
	}
	return nil
}

func (s *slot) SlowDown() {
	s.rc.SlowDown()
}

func (s *slot) SpeedLevel() replica.SpeedLevel {
	return s.rc.SpeedLevel()
}

func (s *slot) SetSpeedLevel(level replica.SpeedLevel) {
	s.rc.SetSpeedLevel(level)
}

func (s *slot) SetHardState(hd replica.HardState) {

}

func (s *slot) Tick() {
	s.rc.Tick()
}

func (s *slot) Step(m replica.Message) error {

	return s.rc.Step(m)
}

func (s *slot) SetAppliedIndex(index uint64) error {
	shardNo := s.key
	err := s.opts.SlotLogStorage.SetAppliedIndex(shardNo, index)
	if err != nil {
		s.Error("set applied index error", zap.Error(err))
	}
	return nil
}

func (s *slot) IsPrepared() bool {
	return s.isPrepared
}

func (s *slot) LeaderId() uint64 {
	return s.rc.LeaderId()
}

func (s *slot) PausePropopose() bool {

	return s.pausePropopose.Load()
}

func (s *slot) getLogs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {
	shardNo := s.key
	logs, err := s.opts.SlotLogStorage.Logs(shardNo, startLogIndex, endLogIndex, limitSize)
	if err != nil {
		s.Error("get logs error", zap.Error(err))
		return nil, err
	}
	return logs, nil
}

func extend(dst, vals []replica.Log) []replica.Log {
	need := len(dst) + len(vals)
	if need <= cap(dst) {
		return append(dst, vals...) // does not allocate
	}
	buf := make([]replica.Log, need) // allocates precisely what's needed
	copy(buf, dst)
	copy(buf[len(dst):], vals)
	return buf
}
