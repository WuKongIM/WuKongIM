package cluster

import (
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
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

	leaderId atomic.Uint64

	mu             sync.Mutex
	learnerToLock  sync.Mutex
	s              *Server
	pausePropopose atomic.Bool // 是否暂停提案

}

func newSlot(st *pb.Slot, sr *Server) *slot {
	s := &slot{
		key:        SlotIdToKey(st.Id),
		st:         st.Clone(),
		opts:       sr.opts,
		s:          sr,
		Log:        wklog.NewWKLog(fmt.Sprintf("slot[%d]", st.Id)),
		isPrepared: true,
	}
	appliedIdx, err := sr.opts.SlotLogStorage.AppliedIndex(s.key)
	if err != nil {
		s.Panic("get applied index error", zap.Error(err))

	}

	lastIndex, lastTerm, err := sr.opts.SlotLogStorage.LastIndexAndTerm(s.key)
	if err != nil {
		s.Panic("get last index and term error", zap.Error(err))
	}

	s.rc = replica.New(
		sr.opts.NodeId,
		replica.WithLogPrefix(fmt.Sprintf("slot-%d", st.Id)),
		replica.WithAppliedIndex(appliedIdx),
		replica.WithLastIndex(lastIndex),
		replica.WithLastTerm(lastTerm),
		replica.WithElectionOn(false),
		replica.WithStorage(newProxyReplicaStorage(s.key, s.opts.SlotLogStorage)),
		replica.WithAutoRoleSwith(true),
	)
	return s
}

func (s *slot) switchConfig(st *pb.Slot) {
	s.mu.Lock()
	s.st = st.Clone()
	s.mu.Unlock()

	s.Info("switch config", zap.String("config", st.String()))

	isLearner := false
	var learnerIds []uint64
	if len(st.Learners) > 0 {
		for _, learnerId := range st.Learners {
			if learnerId == s.opts.NodeId {
				isLearner = true
			}
			learnerIds = append(learnerIds, learnerId)
		}
	}

	var role replica.Role

	if st.Leader == s.opts.NodeId {
		role = replica.RoleLeader
	} else {
		if isLearner {
			role = replica.RoleLearner
		} else {
			role = replica.RoleFollower
		}
	}

	cfg := replica.Config{
		MigrateFrom: st.MigrateFrom,
		MigrateTo:   st.MigrateTo,
		Replicas:    st.Replicas,
		Learners:    learnerIds,
		Role:        role,
		Term:        st.Term,
		Leader:      st.Leader,
	}

	s.s.slotManager.slotReactor.Step(s.key, replica.Message{
		MsgType: replica.MsgConfigResp,
		Config:  cfg,
	})

	if st.Status == pb.SlotStatus_SlotStatusCandidate {
		s.pausePropopose.Store(true)
	} else if st.Status == pb.SlotStatus_SlotStatusNormal {
		s.pausePropopose.Store(false)
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

func (s *slot) GetLogs(startLogIndex, endLogIndex uint64) ([]replica.Log, error) {

	return s.getLogs(startLogIndex, endLogIndex, 0)
}

func (s *slot) ApplyLogs(startIndex, endIndex uint64) (uint64, error) {

	if s.opts.OnSlotApply != nil {
		start := time.Now()
		defer func() {
			end := time.Since(start)
			if end > time.Millisecond*100 {
				s.Debug("slot apply log", zap.Duration("cost", end))
			}

		}()
		logs, err := s.getLogs(startIndex, endIndex, 0)
		if err != nil {
			s.Error("get logs error", zap.Error(err))
			return 0, err
		}
		if len(logs) == 0 {
			s.Warn("appy logs,but logs is empty", zap.Uint64("startIndex", startIndex), zap.Uint64("endIndex", endIndex))
			return 0, nil
		}
		appliedSize := uint64(0)
		for _, log := range logs {
			appliedSize += uint64(log.LogSize())
		}

		err = s.opts.OnSlotApply(s.st.Id, logs)
		if err != nil {
			s.Panic("on slot apply error", zap.Error(err))
		}
		err = s.opts.SlotLogStorage.SetAppliedIndex(s.key, logs[len(logs)-1].Index)
		if err != nil {
			s.Error("set applied index error", zap.Error(err))
			return 0, err
		}
		return appliedSize, nil
	}
	return 0, nil
}

func (s *slot) AppliedIndex() (uint64, error) {
	return s.opts.SlotLogStorage.AppliedIndex(s.key)
}

func (s *slot) SetHardState(hd replica.HardState) {
	if s.leaderId.Load() != 0 && hd.LeaderId != s.leaderId.Load() {
		s.Info("slot leader change", zap.Uint64("oldLeader", s.leaderId.Load()), zap.Uint64("newLeader", hd.LeaderId))
	}
	s.leaderId.Store(hd.LeaderId)

}

func (s *slot) Tick() {
	s.rc.Tick()
}

func (s *slot) Step(m replica.Message) error {

	return s.rc.Step(m)
}

func (s *slot) IsPrepared() bool {
	return s.isPrepared
}

func (s *slot) LeaderId() uint64 {
	return s.st.Leader
}

func (s *slot) PausePropopose() bool {

	return s.pausePropopose.Load()
}

func (s *slot) LearnerToFollower(learnerId uint64) error {
	return s.learnerTo(learnerId)
}

func (s *slot) LearnerToLeader(learnerId uint64) error {
	return s.learnerTo(learnerId)
}

func (s *slot) FollowerToLeader(followerId uint64) error {

	s.learnerToLock.Lock()
	defer s.learnerToLock.Unlock()

	existSlot := s.s.clusterEventServer.Slot(s.st.Id)
	if existSlot == nil {
		s.Error("FollowerToLeader: slot not found", zap.Uint32("slotId", s.st.Id))
		return fmt.Errorf("slot not found")
	}

	if !wkutil.ArrayContainsUint64(existSlot.Replicas, followerId) {
		s.Error("FollowerToLeader: follower not in replicas", zap.Uint64("followerId", followerId))
		return fmt.Errorf("follower not in replicas")
	}

	if existSlot.MigrateFrom == 0 || existSlot.MigrateTo == 0 { // 没有迁移信息，不进行转换
		s.Info("FollowerToLeader: no migrate", zap.Uint64("followerId", followerId))
		return nil
	}

	slot := existSlot.Clone()

	slot.Leader = followerId
	slot.Term = slot.Term + 1
	slot.MigrateFrom = 0
	slot.MigrateTo = 0

	err := s.s.clusterEventServer.ProposeSlots([]*pb.Slot{slot})
	if err != nil {
		s.Error("FollowerToLeader: propose slot error", zap.Error(err))
		return err
	}
	return nil
}

func (s *slot) learnerTo(learnerId uint64) error {

	s.learnerToLock.Lock()
	defer s.learnerToLock.Unlock()

	existSlot := s.s.clusterEventServer.Slot(s.st.Id)
	if existSlot == nil {
		s.Error("learnerTo: slot not found")
		return fmt.Errorf("slot not found")
	}
	if len(existSlot.Learners) == 0 {
		return nil
	}
	slot := existSlot.Clone()

	if existSlot.Leader == existSlot.MigrateFrom {
		slot.Leader = slot.MigrateTo
		slot.Term = slot.Term + 1
	}

	for _, learner := range slot.Learners {
		slot.Learners = wkutil.RemoveUint64(slot.Learners, learner)
	}
	if !wkutil.ArrayContainsUint64(slot.Replicas, slot.MigrateTo) {
		slot.Replicas = append(slot.Replicas, slot.MigrateTo)
	}
	if slot.MigrateFrom != slot.MigrateTo {
		slot.Replicas = wkutil.RemoveUint64(slot.Replicas, slot.MigrateFrom)
	}

	if slot.Leader == slot.MigrateFrom {
		slot.Leader = learnerId
	}

	slot.MigrateFrom = 0
	slot.MigrateTo = 0

	err := s.s.clusterEventServer.ProposeSlots([]*pb.Slot{slot})
	if err != nil {
		s.Error("learnerTo: propose slot error", zap.Error(err))
		return err
	}

	return nil

}

func (s *slot) SaveConfig(cfg replica.Config) error {
	return nil
}

func (s *slot) SetSpeedLevel(level replica.SpeedLevel) {
	s.rc.SetSpeedLevel(level)
}

func (s *slot) SpeedLevel() replica.SpeedLevel {
	return s.rc.SpeedLevel()
}

func (s *slot) SetLeaderTermStartIndex(term uint32, index uint64) error {

	return s.opts.SlotLogStorage.SetLeaderTermStartIndex(s.key, term, index)
}

func (s *slot) LeaderTermStartIndex(term uint32) (uint64, error) {
	return s.opts.SlotLogStorage.LeaderTermStartIndex(s.key, term)
}

func (s *slot) LeaderLastTerm() (uint32, error) {
	return s.opts.SlotLogStorage.LeaderLastTerm(s.key)
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

func (s *slot) DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error {
	return s.opts.SlotLogStorage.DeleteLeaderTermStartIndexGreaterThanTerm(s.key, term)
}

func (s *slot) TruncateLogTo(index uint64) error {
	return s.opts.SlotLogStorage.TruncateLogTo(s.key, index)
}

func (s *slot) DetailLogOn(on bool) {
	s.rc.DetailLogOn(on)
}
