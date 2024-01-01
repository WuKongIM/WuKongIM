package cluster

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
)

func (s *Server) MustWaitLeader(timeout time.Duration) {
	err := s.WaitLeader(timeout)
	if err != nil {
		s.Panic("WaitLeader failed!", zap.Error(err))
	}
}

func (s *Server) WaitLeader(timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	tick := time.NewTicker(time.Millisecond * 100)

	for {
		select {
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		case <-tick.C:
			if s.leaderID.Load() != 0 {
				return nil
			}
		case <-s.stopper.ShouldStop():
			return errors.New("server stopped")
		}
	}
}

func (s *Server) MustWaitAllSlotLeaderIs(leaderID uint64, timeout time.Duration) {
	err := s.WaitAllSlotLeaderIs(leaderID, timeout)
	if err != nil {
		s.Panic("WaitAllSlotLeaderIs failed!", zap.Error(err))
	}
}

// 等待所有槽的领导为指定的领导ID
func (s *Server) WaitAllSlotLeaderIs(leaderID uint64, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tick := time.NewTicker(time.Millisecond * 100)
	for {
		select {
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		case <-tick.C:
			if len(s.clusterEventManager.GetSlots()) == 0 {
				continue
			}
			all := true
			for _, slot := range s.clusterEventManager.GetSlots() {
				if slot.Leader != leaderID {
					all = false
					break
				}
			}
			if all {
				return nil
			}
		case <-s.stopper.ShouldStop():
			return errors.New("server stopped")
		}
	}
}

func (s *Server) MustWaitSlotLeaderNotIs(leaderID uint64, timeout time.Duration) {
	err := s.WaitSlotLeaderNotIs(leaderID, timeout)
	if err != nil {
		s.Panic("WaitSlotLeaderNotIs failed!", zap.Error(err))
	}
}

func (s *Server) WaitSlotLeaderNotIs(leaderID uint64, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tick := time.NewTicker(time.Millisecond * 100)
	for {
		select {
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		case <-tick.C:
			if len(s.clusterEventManager.GetSlots()) == 0 {
				continue
			}
			exist := false
			for _, slot := range s.clusterEventManager.GetSlots() {
				if slot.Leader == leaderID {
					exist = true
					break
				}
			}
			if !exist {
				return nil
			}

		case <-s.stopper.ShouldStop():
			return errors.New("server stopped")
		}
	}
}
