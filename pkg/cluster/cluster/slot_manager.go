package cluster

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
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
		st = rd.slot
	)
	st.handleReadyMessages(rd.Messages)

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

func (s *slotManager) proposeAndWaitCommit(ctx context.Context, slotId uint32, logs []replica.Log) ([]messageItem, error) {
	st := s.slot(slotId)
	if st == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId))
		return nil, ErrSlotNotFound
	}
	if !st.isLeader() {
		s.Error("not leader", zap.Uint32("slotId", slotId), zap.Uint64("leader", st.rc.LeaderId()))
		return nil, ErrNotLeader
	}

	return st.proposeAndWaitCommits(ctx, logs, s.opts.ProposeTimeout)
}

func (s *slotManager) handleMessage(slotId uint32, m replica.Message) {
	st := s.slot(slotId)
	if st == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId), zap.String("msgType", m.MsgType.String()), zap.Uint64("from", m.From))
		return
	}
	err := st.handleRecvMessage(m)
	if err != nil {
		s.Error("handle message error", zap.Uint32("slotId", slotId), zap.Error(err))
	}
}

func (s *slotManager) advanceHandler(slotId uint32) func() {

	return func() {
		s.listener.advance()
	}
}
