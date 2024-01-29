package cluster

import (
	"fmt"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type slotManager struct {
	opts    *Options
	stopper *syncutil.Stopper
	wklog.Log

	listener *slotListener
	stopped  bool
}

func newSlotManager(opts *Options) *slotManager {
	return &slotManager{
		stopper:  syncutil.NewStopper(),
		opts:     opts,
		Log:      wklog.NewWKLog(fmt.Sprintf("slotGroupManager[%d]", opts.NodeID)),
		listener: newSlotListener(opts),
	}
}

func (s *slotManager) start() error {
	s.stopper.RunWorker(s.listen)
	return s.listener.start()
}

func (s *slotManager) stop() {
	s.stopped = true
	s.listener.stop()
	s.stopper.Stop()

}

func (s *slotManager) listen() {
	for !s.stopped {
		ready := s.listener.wait()
		if ready.slot == nil {
			continue
		}
		s.Info("slot recv ready")
		s.handleReady(ready)

	}
}

func (s *slotManager) handleReady(rd slotReady) {
	var (
		slot    = rd.slot
		shardNo = GetSlotShardNo(slot.slotId)
	)
	for _, msg := range rd.Messages {
		s.Info("recv msg", zap.String("msgType", msg.MsgType.String()), zap.String("shardNo", shardNo))
		if msg.To == s.opts.NodeID {
			slot.handleLocalMsg(msg)
			continue
		}
		if msg.To == 0 {
			s.Error("msg.To is 0", zap.String("shardNo", shardNo))
			continue
		}
		protMsg, err := NewMessage(shardNo, msg, MsgSlotMsg)
		if err != nil {
			s.Error("new message error", zap.String("shardNo", shardNo), zap.Error(err))
			continue
		}
		err = s.opts.Transport.Send(msg.To, protMsg)
		if err != nil {
			s.Warn("send msg error", zap.String("shardNo", shardNo), zap.Error(err))
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
		return ErrSlotNotFound
	}
	if !st.isLeader() {
		return ErrNotLeader
	}
	return st.proposeAndWaitCommit(data, s.opts.ProposeTimeout)
}

func (s *slotManager) handleMessage(slotId uint32, m replica.Message) {
	st := s.slot(slotId)
	if st == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId))
		return
	}
	err := st.handleMessage(m)
	if err != nil {
		s.Error("handle message error", zap.Uint32("slotId", slotId), zap.Error(err))
	}
}
