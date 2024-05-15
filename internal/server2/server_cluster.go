package server

import "github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"

func (s *Server) handleClusterMessage(msg *proto.Message) {
	// switch ClusterMsgType(msg.MsgType) {
	// case ClusterMsgTypeConnWrite: // 远程连接写入
	// 	p.handleConnWrite(msg)
	// case ClusterMsgTypeConnClose:
	// 	p.handleConnClose(msg)
	// }
}

func (s *Server) setClusterRoutes() {
	// s.cluster.Route("/wk/connect", p.handleConnectReq)
	// s.cluster.Route("/wk/recvPacket", p.handleOnRecvPacketReq)
	// s.cluster.Route("/wk/sendPacket", p.handleOnSendPacketReq)
	// s.cluster.Route("/wk/connPing", p.handleOnConnPingReq)
	// s.cluster.Route("/wk/recvackPacket", p.handleOnRecvackPacketReq)
}
