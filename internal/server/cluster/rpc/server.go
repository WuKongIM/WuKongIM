package rpc

import (
	context "context"
	"log"
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/proto"
)

const (
	// CMDTypeForwardSendPacket 转发发送包
	CMDTypeForwardSendPacket = 1001
)

// CMDEvent cmd事件
type CMDEvent interface {
	// 收到发送包
	OnSendPacket(req *ForwardSendPacketReq) (*ForwardSendPacketResp, error)
	// 收到接受包
	OnRecvPacket(req *ForwardRecvPacketReq) error
	// 获取频道订阅者
	OnGetSubscribers(channelID string, channelType uint8) ([]string, error)
	// 连接请求
	OnConnectReq(req *ConnectReq) (*ConnectResp, error)
	// 连接写入
	OnConnectWriteReq(req *ConnectWriteReq) (Status, error)
	// 连接ping
	OnConnPingReq(req *ConnPingReq) (Status, error)
	// 发送同步提议请求
	OnSendSyncProposeReq(req *SendSyncProposeReq) (*SendSyncProposeResp, error)
}

// Server rpc服务
type Server struct {
	event CMDEvent
	proto wkproto.Protocol
	wklog.Log
	addr string
}

// NewServer NewServer
func NewServer(proto wkproto.Protocol, event CMDEvent, addr string) *Server {
	return &Server{
		event: event,
		proto: proto,
		Log:   wklog.NewWKLog("NodeServer"),
		addr:  addr,
	}
}

// Start 开启rpc服务
func (s *Server) Start() {
	grpcServer := grpc.NewServer(grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
		// MinTime is the minimum amount of time a client should wait before sending
		// a keepalive ping.
		MinTime: 5 * time.Second,
		// If true, server allows keepalive pings even when there are no active
		// streams(RPCs). If false, and client sends ping when there are no active
		// streams, server will send GOAWAY and close the connection.
		PermitWithoutStream: true,
	}))
	RegisterNodeServiceServer(grpcServer, &nodeServiceImp{s: s})

	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		err = grpcServer.Serve(lis)
		if err != nil {
			s.Panic("grpcServer.Serve(lis) error", zap.Error(err))
		}
	}()
}

type nodeServiceImp struct {
	s *Server
	UnimplementedNodeServiceServer
}

func (n *nodeServiceImp) SendCMD(ctx context.Context, req *CMDReq) (*CMDResp, error) {
	n.s.Debug("收到命令", zap.String("cmd", req.Cmd.String()))
	if req.Cmd == CMDType_ForwardSendPacket { //收到转发发送包
		forwardSendPacketReq := &ForwardSendPacketReq{}
		err := proto.Unmarshal(req.Data, forwardSendPacketReq)
		if err != nil {
			return nil, err
		}
		resp, err := n.s.event.OnSendPacket(forwardSendPacketReq)
		if err != nil {
			return nil, err
		}
		respData, err := proto.Marshal(resp)
		if err != nil {
			return nil, err
		}
		return &CMDResp{
			Status: Status_Success,
			Data:   respData,
		}, nil
	} else if req.Cmd == CMDType_ForwardRecvPacket { // 收到转发接受包
		forwardRecvPacketReq := &ForwardRecvPacketReq{}
		err := proto.Unmarshal(req.Data, forwardRecvPacketReq)
		if err != nil {
			n.s.Error("解码转发接受包数据失败！", zap.Error(err))
			return nil, err
		}
		err = n.s.event.OnRecvPacket(forwardRecvPacketReq)
		if err != nil {
			return nil, err
		}
		return &CMDResp{
			Status: Status_Success,
		}, nil
	} else if req.Cmd == CMDType_GetSubscribers { // 获取订阅者
		getSubscribersReq := &GetSubscribersReq{}
		err := proto.Unmarshal(req.Data, getSubscribersReq)
		if err != nil {
			n.s.Error("解码获取订阅者请求数据失败！", zap.Error(err))
			return nil, err
		}
		subscribers, err := n.s.event.OnGetSubscribers(getSubscribersReq.ChannelID, uint8(getSubscribersReq.ChannelType))
		if err != nil {
			return nil, err
		}
		respData, err := proto.Marshal(&GetSubscribersResp{
			Subscribers: subscribers,
		})
		if err != nil {
			return nil, err
		}
		return &CMDResp{
			Status: Status_Success,
			Data:   respData,
		}, nil
	} else if req.Cmd == CMDType_SendConnectReq { // 连接请求
		connectReq := &ConnectReq{}
		err := proto.Unmarshal(req.Data, connectReq)
		if err != nil {
			n.s.Error("解码连接请求数据失败！", zap.Error(err))
			return nil, err
		}
		connectResp, err := n.s.event.OnConnectReq(connectReq)
		if err != nil {
			return nil, err
		}
		respData, err := proto.Marshal(connectResp)
		if err != nil {
			return nil, err
		}
		return &CMDResp{
			Status: Status_Success,
			Data:   respData,
		}, nil
	} else if req.Cmd == CMDType_ConnWrite { // 连接写入
		connectWrite := &ConnectWriteReq{}
		err := proto.Unmarshal(req.Data, connectWrite)
		if err != nil {
			n.s.Error("解码连接写入数据失败！", zap.Error(err))
			return nil, err
		}
		status, err := n.s.event.OnConnectWriteReq(connectWrite)
		if err != nil {
			return nil, err
		}
		return &CMDResp{
			Status: status,
		}, nil
	} else if req.Cmd == CMDType_ConnPing { // 连接ping
		connPing := &ConnPingReq{}
		err := proto.Unmarshal(req.Data, connPing)
		if err != nil {
			n.s.Error("解码连接ping数据失败！", zap.Error(err))
			return nil, err
		}
		status, err := n.s.event.OnConnPingReq(connPing)
		if err != nil {
			return nil, err
		}
		return &CMDResp{
			Status: status,
		}, nil
	} else if req.Cmd == CMDType_SendSyncPropose { // 发送同步提议请求
		sendSyncProposeReq := &SendSyncProposeReq{}
		err := proto.Unmarshal(req.Data, sendSyncProposeReq)
		if err != nil {
			n.s.Error("解码发送同步提议请求数据失败！", zap.Error(err))
			return nil, err
		}
		sendSyncProposeResp, err := n.s.event.OnSendSyncProposeReq(sendSyncProposeReq)
		if err != nil {
			return nil, err
		}
		respData, err := proto.Marshal(sendSyncProposeResp)
		if err != nil {
			return nil, err
		}
		return &CMDResp{
			Status: Status_Success,
			Data:   respData,
		}, nil
	} else {
		n.s.Error("不支持的RPC CMD", zap.String("cmd", req.Cmd.String()))
		return &CMDResp{
			Status: Status_Error,
		}, nil
	}
}
