package clusterconfig

import (
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"go.uber.org/zap"
)

func (s *Server) setRoutes() {
	s.opts.Cluster.Route("/clusterconfig/leaderTermStartIndex", s.handleLeaderTermStartIndex)

	s.opts.Cluster.Route("/clusterconfig/propose", s.handlePropose) // 处理提案
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
		binary.BigEndian.PutUint64(resultBytes, lastIndex)
	}
	c.Write(resultBytes)

}

func (s *Server) handlePropose(c *wkserver.Context) {
	var logset replica.LogSet
	err := logset.Unmarshal(c.Body())
	if err != nil {
		s.Error("unmarshal request error", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if !s.IsLeader() {
		s.Error("not leader", zap.Uint64("nodeId", s.opts.NodeId))
		c.WriteErr(fmt.Errorf("not leader"))
		return
	}

	if len(logset) == 0 {
		s.Error("empty logset")
		c.WriteErr(fmt.Errorf("empty logset"))
		return
	}

	err = s.proposeAndWait(logset)
	if err != nil {
		s.Error("proposeAndWait error", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}
