package server

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

func (s *Server) connectWrite(peerID uint64, req *rpc.ConnectWriteReq) (proto.Status, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.Cluster.ReqTimeout)
	data, _ := req.Marshal()
	resp, err := s.cluster.RequestWithContext(timeoutCtx, peerID, "/wk/connect/write", data)
	cancel()
	if err != nil {
		return proto.Status_ERROR, err
	}
	return resp.Status, nil
}

func (s *Server) sendConnectRequest(nodeID uint64, req *rpc.ConnectReq) (*rpc.ConnectResp, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.Cluster.ReqTimeout)
	defer cancel()
	data, _ := req.Marshal()
	resp, err := s.cluster.RequestWithContext(timeoutCtx, nodeID, "/wk/connect", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("send connect request fail")
	}
	connectResp := &rpc.ConnectResp{}
	err = connectResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return connectResp, nil
}

func (s *Server) connPing(peerID uint64, req *rpc.ConnPingReq) (proto.Status, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.Cluster.ReqTimeout)
	defer cancel()
	data, _ := req.Marshal()
	resp, err := s.cluster.RequestWithContext(timeoutCtx, peerID, "/wk/connPing", data)
	if err != nil {
		return 0, err
	}
	return resp.Status, nil
}

func (s *Server) forwardSendPacketReq(nodeID uint64, req *rpc.ForwardSendPacketReq) (*rpc.ForwardSendPacketResp, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.Cluster.ReqTimeout)
	defer cancel()
	data, _ := req.Marshal()
	resp, err := s.cluster.RequestWithContext(timeoutCtx, nodeID, "/wk/sendPacket", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("forward send packet fail")
	}
	forwardSendPacketResp := &rpc.ForwardSendPacketResp{}
	err = forwardSendPacketResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return forwardSendPacketResp, nil
}

func (s *Server) forwardRecvackPacketReq(nodeID uint64, req *rpc.RecvacksReq) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.Cluster.ReqTimeout)
	defer cancel()
	data, _ := req.Marshal()
	resp, err := s.cluster.RequestWithContext(timeoutCtx, nodeID, "/wk/recvackPacket", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("send forwardRecvackPacketReq fail")
	}
	return nil
}

func (s *Server) forwardRecvPacketReq(nodeID uint64, data []byte) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.Cluster.ReqTimeout)
	defer cancel()
	resp, err := s.cluster.RequestWithContext(timeoutCtx, nodeID, "/wk/recvPacket", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("send forwardRecvPacketReq fail")
	}
	return nil
}
