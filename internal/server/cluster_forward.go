package server

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

func (s *Server) connectWrite(peerID uint64, req *rpc.ConnectWriteReq) (proto.Status, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.Cluster.ReqTimeout)
	defer cancel()
	data, _ := req.Marshal()
	resp, err := s.cluster.RequestWithContext(timeoutCtx, peerID, "/wk/connectWrite", data)
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

	startTime := time.Now().UnixMilli()
	s.Debug("开始转发\"发送包\"", zap.Uint64("nodeID", nodeID), zap.String("req", req.String()))
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
	s.Debug("转发\"发送包\"成功", zap.Int64("耗时(毫秒)", time.Now().UnixMilli()-startTime), zap.Uint64("nodeID", nodeID))
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
	startTime := time.Now().UnixMilli()
	s.Debug("开始转发\"接收包\"", zap.Uint64("nodeID", nodeID))
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.Cluster.ReqTimeout)
	defer cancel()
	resp, err := s.cluster.RequestWithContext(timeoutCtx, nodeID, "/wk/recvPacket", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("send forwardRecvPacketReq fail")
	}
	s.Debug("转发\"接收包\"成功", zap.Int64("耗时(毫秒)", time.Now().UnixMilli()-startTime), zap.Uint64("nodeID", nodeID))
	return nil
}
