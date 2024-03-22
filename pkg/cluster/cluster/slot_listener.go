package cluster

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type slotListener struct {
	opts  *Options
	slots *slotQueue
	// 已准备的槽
	stopper *syncutil.Stopper
	readyCh chan slotReady
	wklog.Log
}

func newSlotListener(opts *Options) *slotListener {
	return &slotListener{
		opts:    opts,
		slots:   newSlotQueue(),
		stopper: syncutil.NewStopper(),
		readyCh: make(chan slotReady),
		Log:     wklog.NewWKLog(fmt.Sprintf("slotListener[%d]", opts.NodeID)),
	}
}

func (s *slotListener) start() error {
	s.stopper.RunWorker(s.loopEvent)
	return nil
}

func (s *slotListener) stop() {
	s.stopper.Stop()
}

func (s *slotListener) wait() slotReady {

	select {
	case sr := <-s.readyCh:
		return sr
	case <-s.stopper.ShouldStop():
		return emptySlotReady
	}
}

func (s *slotListener) loopEvent() {
	tick := time.NewTicker(time.Millisecond * 51)
	for {
		select {
		case <-tick.C:
			s.slots.foreach(func(st *slot) {
				if st.isDestroy() {
					return
				}
				err := st.handleReceivedMessages()
				if err != nil {
					s.Error("loopEvent:handle received messages error", zap.Error(err), zap.Uint32("slotId", st.slotId))
				}
				if st.hasReady() {
					rd := st.ready()
					if replica.IsEmptyReady(rd) {
						return
					}
					s.triggerReady(slotReady{
						slot:  st,
						Ready: rd,
					})
				} else {
					if s.isInactiveSlot(st) { // 频道不活跃，移除，等待频道再此收到消息时，重新加入
						s.remove(st)
						s.Info("remove inactive slot", zap.Uint32("slotId", st.slotId))
					}
				}
			})
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *slotListener) triggerReady(ready slotReady) {
	select {
	case s.readyCh <- ready:
	case <-s.stopper.ShouldStop():
		return
	}
}

func (s *slotListener) remove(st *slot) {
	s.slots.remove(st)
}

func (s *slotListener) slot(slotId uint32) *slot {
	return s.slots.get(slotId)
}

func (s *slotListener) addSlot(st *slot) {
	s.slots.add(st)
}

func (s *slotListener) exist(slotId uint32) bool {
	return s.slots.exist(slotId)
}

// 判断是否是不活跃的频道
func (s *slotListener) isInactiveSlot(st *slot) bool {
	return st.isDestroy()
}
