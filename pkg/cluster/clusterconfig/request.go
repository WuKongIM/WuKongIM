package clusterconfig

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

var _ reactor.IRequest = &Request{}

type Request struct {
	s *Server
}

func NewRequest(s *Server) *Request {
	return &Request{
		s: s,
	}
}

func (r *Request) GetConfig(req reactor.ConfigReq) (reactor.ConfigResp, error) {
	nodes := r.s.cfg.allowVoteNodes()
	replicas := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		replicas = append(replicas, node.Id)
	}

	for replicaId := range r.s.opts.InitNodes {
		exist := false
		for _, replica := range replicas {
			if replicaId == replica {
				exist = true
				break
			}
		}
		if !exist {
			replicas = append(replicas, replicaId)
		}
	}
	_, term, err := r.s.storage.LastIndexAndTerm()
	if err != nil {
		r.s.Error("Request: get last index and term error", zap.Error(err))
		return reactor.EmptyConfigResp, err
	}

	return reactor.ConfigResp{
		HandlerKey: "config",
		Config: replica.Config{
			Replicas: replicas,
			Term:     term,
		},
	}, nil
}

func (r *Request) GetLeaderTermStartIndex(req reactor.LeaderTermStartIndexReq) (uint64, error) {

	reqBytes, err := req.Marshal()
	if err != nil {
		return 0, err
	}
	resp, err := r.request(req.LeaderId, "/clusterconfig/leaderTermStartIndex", reqBytes)
	if err != nil {
		return 0, err
	}
	if resp.Status != proto.StatusOK {
		return 0, fmt.Errorf("get leader term start index failed, status: %v", resp.Status)
	}
	if len(resp.Body) > 0 {
		return binary.BigEndian.Uint64(resp.Body), nil
	}
	return 0, nil
}

func (r *Request) Append(req reactor.AppendLogReq) error {
	if err := r.s.storage.AppendLog(req.Logs); err != nil {
		return err
	}
	return nil
}

func (r *Request) request(toNodeId uint64, path string, body []byte) (*proto.Response, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), r.s.opts.ReqTimeout)
	defer cancel()
	return r.s.opts.Cluster.RequestWithContext(timeoutCtx, toNodeId, path, body)
}
