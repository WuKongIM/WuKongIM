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

// 等待所有节点提交apiServerAddr
func (s *Server) MustWaitAllApiServerAddrReady() {
	tk := time.NewTicker(time.Millisecond * 10)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-tk.C:
			nodes := s.GetConfig().Nodes
			if len(nodes) == 0 {
				continue
			}
			exist := false
			for _, node := range nodes {
				if node.ApiServerAddr == "" {
					exist = true
					break
				}
			}
			if !exist {
				return
			}
		case <-timeoutCtx.Done():
			s.Panic("wait all api server addr ready timeout")
			return
		}
	}
}
