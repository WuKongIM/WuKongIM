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
	advanceCh chan struct{}
}

func newSlotListener(opts *Options) *slotListener {
	return &slotListener{
		opts:      opts,
		slots:     newSlotQueue(),
		stopper:   syncutil.NewStopper(),
		readyCh:   make(chan slotReady, 100),
		Log:       wklog.NewWKLog(fmt.Sprintf("slotListener[%d]", opts.NodeID)),
		advanceCh: make(chan struct{}, 1),
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
	tick := time.NewTicker(time.Millisecond * 150)
	for {
		s.ready()
		select {
		case <-tick.C:
		case <-s.advanceCh:
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *slotListener) advance() {
	select {
	case s.advanceCh <- struct{}{}:
	case <-s.stopper.ShouldStop():
	default:
	}
}

func (s *slotListener) ready() {
	hasEvent := true
	var err error
	for hasEvent {
		hasEvent = false
		batchStart := time.Now()
		s.slots.foreach(func(st *slot) {
			if st.isDestroy() {
				return
			}
			// start1 := time.Now()
			event := false
			if event, err = st.handleEvents(); err != nil {
				s.Warn("loopEvent: handleReceivedMessages error", zap.Uint32("slotId", st.slotId), zap.Error(err))
			}
			if event {
				hasEvent = true
			}
			event = s.handleReady(st)
			if event {
				hasEvent = true
			}

		})
		if time.Since(batchStart) > time.Millisecond*10 {
			s.Info("ready batch delay high", zap.Duration("cost", time.Since(batchStart)))

		}
	}
}

func (s *slotListener) handleReady(st *slot) bool {
	if st.hasReady() {
		rd := st.ready()
		if replica.IsEmptyReady(rd) {
			return false
		}
		s.triggerReady(slotReady{
			slot:  st,
			Ready: rd,
		})
		return true
	}
	return false
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
