package cluster

import (
	"fmt"
	"time"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type slotListener struct {
	opts  *Options
	slots *slotQueue
	// 已准备的槽
	readySlots *readySlotQueue
	stopper    *syncutil.Stopper
	readyCh    chan struct{}
	wklog.Log
}

func newSlotListener(opts *Options) *slotListener {
	return &slotListener{
		opts:       opts,
		slots:      newSlotQueue(),
		readySlots: newReadySlotQueue(),
		stopper:    syncutil.NewStopper(),
		readyCh:    make(chan struct{}, 100),
		Log:        wklog.NewWKLog(fmt.Sprintf("slotListener[%d]", opts.NodeID)),
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
	if s.readySlots.len() > 0 {
		return s.readySlots.pop()
	}
	select {
	case <-s.readyCh:
	case <-s.stopper.ShouldStop():
		return emptySlotReady
	}
	if s.readySlots.len() > 0 {
		return s.readySlots.pop()
	}
	return emptySlotReady
}

func (s *slotListener) loopEvent() {
	tick := time.NewTicker(time.Millisecond * 1)

	for {
		select {
		case <-tick.C:
			s.slots.foreach(func(st *slot) {
				if st.isDestroy() {
					return
				}
				if st.hasReady() {
					rd := st.ready()
					if replica.IsEmptyReady(rd) {
						return
					}
					s.readySlots.add(slotReady{
						slot:  st,
						Ready: rd,
					})

					select {
					case s.readyCh <- struct{}{}:
					case <-s.stopper.ShouldStop():
						return
					}
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
