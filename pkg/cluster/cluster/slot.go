package cluster

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type slot struct {
	rc      *replica.Replica
	slotId  uint32
	shardNo string
	wklog.Log
	opts         *Options
	destroy      bool        // 是否已经销毁
	lastActivity atomic.Time // 最后一次活跃时间
	commitWait   *commitWait
	localstorage *localStorage

	doneC chan struct{}
	sync.Mutex

	next *slot
	prev *slot

	messageQueue *ReplicaMessageQueue
}

func newSlot(st *pb.Slot, appliedIdx uint64, localstorage *localStorage, opts *Options) *slot {
	shardNo := GetSlotShardNo(st.Id)
	rc := replica.New(opts.NodeID, shardNo, replica.WithAppliedIndex(appliedIdx), replica.WithReplicas(st.Replicas), replica.WithStorage(newProxyReplicaStorage(shardNo, opts.ShardLogStorage, localstorage)))
	if st.Leader == opts.NodeID {
		rc.BecomeLeader(st.Term)
	} else {
		rc.BecomeFollower(st.Term, st.Leader)
	}
	stobj := &slot{
		slotId:       st.Id,
		shardNo:      shardNo,
		rc:           rc,
		Log:          wklog.NewWKLog(fmt.Sprintf("slot[%d:%d]", opts.NodeID, st.Id)),
		commitWait:   newCommitWait(),
		opts:         opts,
		doneC:        make(chan struct{}),
		localstorage: localstorage,
		messageQueue: NewReplicaMessageQueue(opts.ReceiveQueueLength, false, opts.LazyFreeCycle, opts.MaxReceiveQueueSize),
	}
	stobj.lastActivity.Store(time.Now())
	return stobj
}

func (s *slot) BecomeAny(term uint32, leaderId uint64) {
	s.Lock()
	defer s.Unlock()
	if leaderId == s.opts.NodeID {
		s.rc.BecomeLeader(term)
	} else {
		s.rc.BecomeFollower(term, leaderId)
	}
}

// 提案数据，并等待数据提交给大多数节点
func (s *slot) proposeAndWaitCommit(data []byte, timeout time.Duration) error {

	s.Lock()
	if s.destroy {
		s.Unlock()
		return errors.New("channel destroy, can not propose")
	}
	msg := s.rc.NewProposeMessage(data)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	waitC, err := s.commitWait.addWaitIndex(msg.Index)
	if err != nil {
		s.Error("add wait index failed", zap.Error(err))
		return err
	}
	err = s.step(msg)
	if err != nil {
		s.Unlock()
		return err
	}
	s.Unlock()

	select {
	case <-waitC:
		return nil
	case <-timeoutCtx.Done():
		return timeoutCtx.Err()
	case <-s.doneC:
		return ErrStopped
	}
}

func (s *slot) proposeAndWaitCommits(dataList [][]byte, timeout time.Duration) error {
	s.Lock()
	if s.destroy {
		s.Unlock()
		return errors.New("channel destroy, can not propose")
	}

	logs := make([]replica.Log, 0, len(dataList))
	for i, data := range dataList {
		logs = append(logs, replica.Log{
			Index: s.rc.LastLogIndex() + 1 + uint64(i),
			Term:  s.rc.Term(),
			Data:  data,
		})
	}
	msg := s.rc.NewProposeMessageWithLogs(logs)
	lastLog := logs[len(logs)-1]
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	waitC, err := s.commitWait.addWaitIndex(lastLog.Index)
	if err != nil {
		s.Unlock()
		s.Error("add wait index failed", zap.Error(err))
		return err
	}
	err = s.step(msg)
	if err != nil {
		s.Unlock()
		return err
	}
	s.Unlock()

	select {
	case <-waitC:
		return nil
	case <-timeoutCtx.Done():
		return timeoutCtx.Err()
	case <-s.doneC:
		return ErrStopped
	}
}

func (s *slot) hasReady() bool {
	if s.destroy {
		return false
	}
	s.Lock()
	defer s.Unlock()
	return s.rc.HasReady()
}

func (s *slot) ready() replica.Ready {
	if s.destroy {
		return replica.Ready{}
	}
	s.Lock()
	defer s.Unlock()
	return s.rc.Ready()
}

func (s *slot) isLeader() bool {
	return s.rc.IsLeader()
}

func (s *slot) leaderId() uint64 {
	return s.rc.LeaderId()
}

func (s *slot) stepLock(msg replica.Message) error {
	s.Lock()
	defer s.Unlock()
	return s.step(msg)
}

func (s *slot) step(msg replica.Message) error {
	if s.destroy {
		return errors.New("slot destroy, can not step")
	}
	s.lastActivity.Store(time.Now())
	return s.rc.Step(msg)
}

func (s *slot) isDestroy() bool {
	return s.destroy
}

func (s *slot) makeDestroy() {
	s.destroy = true
	close(s.doneC)
}

func (s *slot) handleMessage(msg replica.Message) error {
	if s.destroy {
		return errors.New("slot destroy, can not handle message")
	}
	if added, stopped := s.messageQueue.Add(msg); !added || stopped {
		return errors.New("message queue add failed")
	}
	return nil
}

func (s *slot) handleReceivedMessages() error {
	if s.destroy {
		return errors.New("slot destroy, can not handle message")
	}
	msgs := s.messageQueue.Get()
	var err error
	for _, msg := range msgs {
		err = s.stepLock(msg)
		if err != nil {
			s.Error("step message failed", zap.Error(err))
			return err
		}

	}
	return nil
}

func (s *slot) handleLocalMsg(msg replica.Message) {
	if s.destroy {
		s.Warn("handle local msg, but channel is destroy")
		return
	}
	if msg.To != s.opts.NodeID {
		s.Warn("handle local msg, but msg to is not self", zap.String("msgType", msg.MsgType.String()), zap.Uint64("to", msg.To), zap.Uint64("self", s.opts.NodeID))
		return
	}
	s.lastActivity.Store(time.Now())
	switch msg.MsgType {
	case replica.MsgStoreAppend: // 处理日志追加到存储内
		s.handleStoreAppend(msg)
	case replica.MsgApplyLogsReq: // 处理apply logs请求
		s.handleApplyLogsReq(msg)
	}
}

func (s *slot) handleStoreAppend(msg replica.Message) {
	if len(msg.Logs) == 0 {
		return
	}
	shardNo := GetSlotShardNo(s.slotId)
	err := s.opts.ShardLogStorage.AppendLog(shardNo, msg.Logs)
	if err != nil {
		s.Panic("append log error", zap.Error(err))
	}

	err = s.stepLock(s.rc.NewMsgStoreAppendResp(msg.Logs[len(msg.Logs)-1].Index))
	if err != nil {
		s.Panic("step store append resp failed", zap.Error(err))
	}
}

// 处理应用日志请求
func (s *slot) handleApplyLogsReq(msg replica.Message) {
	if msg.CommittedIndex <= 0 || msg.AppliedIndex >= msg.CommittedIndex {
		return
	}
	shardNo := GetSlotShardNo(s.slotId)
	var err error
	if len(msg.Logs) == 0 {
		s.Info("logs is empty", zap.Uint64("appliedIndex", msg.AppliedIndex), zap.Uint64("committedIndex", msg.CommittedIndex))
		return
	}
	if s.opts.OnSlotApply != nil {
		err = s.opts.OnSlotApply(s.slotId, msg.Logs)
		if err != nil {
			s.Panic("on slot apply error", zap.Error(err))
		}
		err = s.localstorage.setAppliedIndex(shardNo, msg.Logs[len(msg.Logs)-1].Index)
		if err != nil {
			s.Panic("set applied index error", zap.Error(err))
		}
	}
	lastLog := msg.Logs[len(msg.Logs)-1]
	s.Info("commit wait", zap.Uint64("lastLogIndex", lastLog.Index))
	s.commitWait.commitIndex(lastLog.Index)
	s.Info("commit wait done", zap.Uint64("lastLogIndex", lastLog.Index))

	err = s.stepLock(s.rc.NewMsgApplyLogsRespMessage(lastLog.Index))
	if err != nil {
		s.Error("step apply logs resp failed", zap.Error(err))
	}
}

func GetSlotShardNo(slotID uint32) string {
	return fmt.Sprintf("slot-%d", slotID)
}

func GetSlotId(shardNo string, lg wklog.Log) uint32 {
	var slotID uint64
	var err error
	strs := strings.Split(shardNo, "-")
	if len(strs) == 2 {
		slotID, err = strconv.ParseUint(strs[1], 10, 32)
		if err != nil {
			lg.Panic("parse slotID error", zap.Error(err))
		}
		return uint32(slotID)
	} else {
		lg.Panic("parse slotID error", zap.Error(err))
	}
	return 0
}
