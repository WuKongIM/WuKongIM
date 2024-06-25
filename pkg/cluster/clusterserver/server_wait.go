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
			slots := s.GetConfig().Slots
			if len(slots) > 0 {
				notReady := false
				for _, st := range slots {
					if st.Leader == 0 {
						notReady = true
						break
					}
				}
				if !notReady {
					return
				}

			}
		case <-timeoutCtx.Done():
			s.Panic("wait all slots ready timeout")
			return
		}
	}
}
