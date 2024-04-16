package cluster

import (
	"context"
	"time"
)

func (s *Server) MustWaitAllSlotsReady() {
	tk := time.NewTicker(time.Millisecond * 10)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-tk.C:
			if s.slotManager.slotLen() == int(s.opts.SlotCount) {
				return
			}
		case <-timeoutCtx.Done():
			s.Panic("wait all slots ready timeout")
			return
		}
	}
}
