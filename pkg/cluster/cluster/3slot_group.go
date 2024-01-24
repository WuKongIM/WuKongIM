package cluster

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type slotGroup struct {
	stopper *syncutil.Stopper
	opts    *Options
	wklog.Log

	listener *slotListener
	stopped  bool
}

func newSlotGroup(opts *Options) *slotGroup {

	return &slotGroup{
		stopper:  syncutil.NewStopper(),
		opts:     opts,
		Log:      wklog.NewWKLog(fmt.Sprintf("slotGroup[%d]", opts.NodeID)),
		listener: newSlotListener(opts),
	}
}

func (s *slotGroup) start() error {
	s.stopper.RunWorker(s.listen)
	return s.listener.start()
}

func (s *slotGroup) stop() {
	s.stopped = true
	s.listener.stop()
	s.stopper.Stop()
}

func (s *slotGroup) listen() {
	for !s.stopped {
		ready := s.listener.wait()
		if ready.slot == nil {
			continue
		}
		s.handleReady(ready)

	}
}

func (s *slotGroup) handleReady(rd slotReady) {
	var (
		slot    = rd.slot
		shardNo = GetSlotShardNo(slot.slotId)
	)
	for _, msg := range rd.Messages {
		// g.Info("recv msg", zap.String("msgType", msg.MsgType.String()), zap.String("channelID", channel.channelID), zap.Uint8("channelType", channel.channelType), zap.Uint64("to", msg.To))
		if msg.To == s.opts.NodeID {
			slot.handleLocalMsg(msg)
			continue
		}
		if msg.To == 0 {
			s.Error("msg.To is 0", zap.String("shardNo", shardNo))
			continue
		}
		protMsg, err := NewMessage(shardNo, msg)
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

func (s *slotGroup) slot(slotId uint32) *slot {
	return s.listener.slot(slotId)
}

func (s *slotGroup) addSlot(st *slot) {
	s.listener.addSlot(st)
}

func (s *slotGroup) exist(slotId uint32) bool {
	return s.listener.exist(slotId)
}
