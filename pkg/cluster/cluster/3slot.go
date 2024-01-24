package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type slot struct {
	rc      *replica.Replica
	slotId  uint32
	shardNo string
	wklog.Log
	opts         *Options
	destroy      bool      // 是否已经销毁
	lastActivity time.Time // 最后一次活跃时间
	commitWait   *commitWait

	doneC chan struct{}
	sync.Mutex

	next *slot
	prev *slot
}

func newSlot(slotId uint32, appliedIdx uint64, replicas []uint64, opts *Options) *slot {
	shardNo := GetSlotShardNo(slotId)
	rc := replica.New(opts.NodeID, shardNo, replica.WithAppliedIndex(appliedIdx), replica.WithReplicas(replicas), replica.WithStorage(newProxyReplicaStorage(shardNo, opts.ShardLogStorage)))
	return &slot{
		slotId:       slotId,
		shardNo:      shardNo,
		rc:           rc,
		Log:          wklog.NewWKLog(fmt.Sprintf("slot[%d:%d]", opts.NodeID, slotId)),
		commitWait:   newCommitWait(),
		opts:         opts,
		doneC:        make(chan struct{}),
		lastActivity: time.Now(),
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

	waitC := s.commitWait.addWaitIndex(msg.Index)
	err := s.step(msg)
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

// 将当前节点任命为领导
func (s *slot) appointLeader(from uint64, term uint32) error {

	return s.stepLock(replica.Message{
		MsgType:           replica.MsgAppointLeaderReq,
		From:              from,
		AppointmentLeader: s.opts.NodeID,
		Term:              term,
	})

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
	s.lastActivity = time.Now()
	return s.rc.Step(msg)
}

func (s *slot) isDestroy() bool {
	return s.destroy
}

func (s *slot) makeDestroy() {
	s.destroy = true
	close(s.doneC)
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
	s.lastActivity = time.Now()
	switch msg.MsgType {
	case replica.MsgApplyLogsReq: // 处理apply logs请求
		s.handleApplyLogsReq(msg)
	}
}

// 处理应用日志请求
func (s *slot) handleApplyLogsReq(msg replica.Message) {
	if len(msg.Logs) == 0 {
		return
	}
	lastLog := msg.Logs[len(msg.Logs)-1]
	s.Debug("commit wait", zap.Uint64("lastLogIndex", lastLog.Index))
	s.commitWait.commitIndex(lastLog.Index)
	s.Debug("commit wait done", zap.Uint64("lastLogIndex", lastLog.Index))

	err := s.stepLock(s.rc.NewMsgApplyLogsRespMessage(lastLog.Index))
	if err != nil {
		s.Error("step apply logs resp failed", zap.Error(err))
	}
}

func GetSlotShardNo(slotID uint32) string {
	return fmt.Sprintf("slot-%d", slotID)
}
