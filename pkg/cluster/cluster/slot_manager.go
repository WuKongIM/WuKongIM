package cluster

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type slotManager struct {
	opts    *Options
	stopper *syncutil.Stopper
	wklog.Log

	listener *slotListener
	stopped  atomic.Bool
}

func newSlotManager(opts *Options) *slotManager {
	return &slotManager{
		stopper:  syncutil.NewStopper(),
		opts:     opts,
		Log:      wklog.NewWKLog(fmt.Sprintf("slotManager[%d]", opts.NodeID)),
		listener: newSlotListener(opts),
	}
}

func (s *slotManager) start() error {
	s.stopper.RunWorker(s.listen)
	return s.listener.start()
}

func (s *slotManager) stop() {
	s.stopped.Store(true)
	s.listener.stop()
	s.stopper.Stop()

}

func (s *slotManager) listen() {
	for !s.stopped.Load() {
		ready := s.listener.wait()
		if ready.slot == nil {
			continue
		}
		s.handleReady(ready)
	}
}

func (s *slotManager) handleReady(rd slotReady) {
	var (
		slot    = rd.slot
		shardNo = GetSlotShardNo(slot.slotId)
	)
	for _, msg := range rd.Messages {
		if msg.To == s.opts.NodeID {
			slot.handleLocalMsg(msg)
			continue
		}
		if msg.To == 0 {
			s.Error("msg.To is 0", zap.String("shardNo", shardNo), zap.String("msg", msg.MsgType.String()))
			continue
		}

		if msg.MsgType != replica.MsgSync && msg.MsgType != replica.MsgSyncResp && msg.MsgType != replica.MsgPing && msg.MsgType != replica.MsgPong {
			s.Info("send message", zap.String("msgType", msg.MsgType.String()), zap.Uint64("committedIdx", msg.CommittedIndex), zap.Uint32("term", msg.Term), zap.Uint64("lastLogIndex", slot.rc.LastLogIndex()), zap.String("shardNo", shardNo), zap.Uint64("to", msg.To), zap.Uint64("from", msg.From))
		}

		protMsg, err := NewMessage(shardNo, msg, MsgSlotMsg)
		if err != nil {
			s.Error("new message error", zap.String("shardNo", shardNo), zap.Error(err))
			continue
		}

		traceOutgoingMessage(trace.ClusterKindSlot, msg)

		err = s.opts.Transport.Send(msg.To, protMsg, func() {
			for _, resp := range msg.Responses {
				err = slot.stepLock(resp)
				if err != nil {
					s.Error("step error", zap.Error(err), zap.String("shardNo", shardNo))
					continue
				}
			}
		})
		if err != nil {
			s.Warn("send msg error", zap.String("msgType", msg.MsgType.String()), zap.Uint64("to", msg.To), zap.String("shardNo", shardNo), zap.Error(err))
		}

	}

}
func (s *slotManager) exist(slotId uint32) bool {
	return s.listener.exist(slotId)
}

func (s *slotManager) addSlot(st *slot) {
	s.listener.addSlot(st)
}

func (s *slotManager) slot(slotId uint32) *slot {

	return s.listener.slot(slotId)
}

func (s *slotManager) proposeAndWaitCommit(slotId uint32, data []byte) error {
	st := s.slot(slotId)
	if st == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId))
		return ErrSlotNotFound
	}
	if !st.isLeader() {
		s.Error("not leader", zap.Uint32("slotId", slotId), zap.Uint64("leader", st.rc.LeaderId()))
		return ErrNotLeader
	}
	return st.proposeAndWaitCommit(data, s.opts.ProposeTimeout)
}

func (s *slotManager) handleMessage(slotId uint32, m replica.Message) {
	st := s.slot(slotId)
	if st == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId), zap.String("msgType", m.MsgType.String()), zap.Uint64("from", m.From))
		return
	}
	err := st.handleMessage(m)
	if err != nil {
		s.Error("handle message error", zap.Uint32("slotId", slotId), zap.Error(err))
	}
}
