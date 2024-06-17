package clusterconfig

import (
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"go.uber.org/zap"
)

func (s *Server) setRoutes() {
	s.opts.Cluster.Route("/clusterconfig/leaderTermStartIndex", s.handleLeaderTermStartIndex)
}

func (s *Server) handleLeaderTermStartIndex(c *wkserver.Context) {
	req := &reactor.LeaderTermStartIndexReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		s.Error("unmarshal request error", zap.Error(err))
		c.WriteErr(err)
		return
	}

	resultBytes := make([]byte, 8)

	lastIndex, term := s.handler.LastLogIndexAndTerm()

	fmt.Println("handleLeaderTermStartIndex-1---->", lastIndex, term, req.Term)

	if term == req.Term {
		binary.BigEndian.PutUint64(resultBytes, lastIndex+1)
	} else {
		syncTerm := req.Term + 1
		syncTerm, err = s.storage.LeaderLastTermGreaterThan(syncTerm)
		if err != nil {
			s.Error("get leader last term error", zap.Error(err))
			c.WriteErr(err)
			return
		}
		lastIndex, err = s.storage.LeaderTermStartIndex(syncTerm)
		if err != nil {
			s.Error("get leader term start index error", zap.Error(err))
			c.WriteErr(err)
			return
		}
		fmt.Println("handleLeaderTermStartIndex-2---->", lastIndex, syncTerm)
		binary.BigEndian.PutUint64(resultBytes, lastIndex)
	}
	c.Write(resultBytes)

}
