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
	// fromUID:发送设备的uid
	//  deviceFlag：发送设备的deviceFlag
	//  deviceID：发送设备的deviceID
	OnSendPacket(messageID int64, fromUID string, deviceFlag wkproto.DeviceFlag, deviceID string, sendPacket *wkproto.SendPacket, traceInfo *TracerInfo) (messageSeq uint32, reasonCode wkproto.ReasonCode, err error)
	// 收到接受包
	OnRecvPacket(deviceFlag wkproto.DeviceFlag, deviceID string, recvPacket *wkproto.RecvPacket, large bool, users []string, traceInfo *TracerInfo) error
	// 获取频道订阅者
	OnGetSubscribers(channelID string, channelType uint8) ([]string, error)
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
		sendPacket, _, err := n.s.proto.DecodeFrame(forwardSendPacketReq.SendPacket, wkproto.LatestVersion)
		if err != nil {
			return nil, err
		}
		messageSeq, reasonCode, err := n.s.event.OnSendPacket(forwardSendPacketReq.MessageID, forwardSendPacketReq.FromUID, wkproto.DeviceFlag(forwardSendPacketReq.FromDeviceFlag), forwardSendPacketReq.DeviceID, sendPacket.(*wkproto.SendPacket), forwardSendPacketReq.TracerInfo)
		if err != nil {
			return nil, err
		}
		resp := &ForwardSendPacketResp{
			MessageSeq: int32(messageSeq),
			ReasonCode: int32(reasonCode),
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
		recvPacket, _, err := n.s.proto.DecodeFrame(forwardRecvPacketReq.Message, wkproto.LatestVersion)
		if err != nil {
			n.s.Error("解码接受包数据失败！", zap.Error(err))
			return nil, err
		}
		err = n.s.event.OnRecvPacket(wkproto.DeviceFlag(forwardRecvPacketReq.FromDeviceFlag), forwardRecvPacketReq.DeviceID, recvPacket.(*wkproto.RecvPacket), forwardRecvPacketReq.Large, forwardRecvPacketReq.Users, forwardRecvPacketReq.TracerInfo)
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
	} else {
		n.s.Error("不支持的RPC CMD", zap.String("cmd", req.Cmd.String()))
		return &CMDResp{
			Status: Status_Error,
		}, nil
	}
}
