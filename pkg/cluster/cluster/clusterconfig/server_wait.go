package clusterconfig

import (
	"time"

	"go.uber.org/zap"
)

func (s *Server) WaitLeader() error {
	tk := time.NewTicker(time.Millisecond * 20)
	for {
		select {
		case <-tk.C:
			if s.node.state.leader != None {
				return nil
			}
		case <-s.stopper.ShouldStop():
			return ErrStopped
		}
	}
}

func (s *Server) MustWaitLeader() {
	err := s.WaitLeader()
	if err != nil {
		s.Panic("wait leader error", zap.Error(err))
	}
}
