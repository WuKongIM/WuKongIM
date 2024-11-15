package cluster

import (
	"context"
	"time"
)

func (s *Server) MustWaitAllSlotsReady(timeout time.Duration) {
	tk := time.NewTicker(time.Millisecond * 10)
	defer tk.Stop()
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
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
	defer tk.Stop()
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

func (s *Server) MustWaitAllNodeOnline() {
	tk := time.NewTicker(time.Millisecond * 10)
	defer tk.Stop()
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-tk.C:
			nodes := s.GetConfig().Nodes
			if len(nodes) > 0 {
				notOnline := false
				for _, node := range nodes {
					if !node.Online {
						notOnline = true
						break
					}
				}
				if !notOnline {
					return
				}

			}
		case <-timeoutCtx.Done():
			s.Panic("wait all node online timeout")
			return
		}
	}
}
