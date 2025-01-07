package clusterconfig

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type raftTransport struct {
	s *Server
}

func newRaftTransport(s *Server) *raftTransport {
	return &raftTransport{
		s: s,
	}
}

func (t *raftTransport) Send(event types.Event) {
	t.s.opts.Transport.Send(event)
}
