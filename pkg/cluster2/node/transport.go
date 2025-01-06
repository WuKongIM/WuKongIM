package node

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type Transport interface {
	// Send 发送事件
	Send(event Event)
}

type raftTransport struct {
	s *Server
}

func newRaftTransport(s *Server) *raftTransport {
	return &raftTransport{
		s: s,
	}
}

func (t *raftTransport) Send(event types.Event) {
	t.s.opts.Transport.Send(Event{
		Type:  RaftEvent,
		Event: event,
	})
}
