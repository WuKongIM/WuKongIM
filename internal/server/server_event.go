package server

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/lmproto"
	"go.uber.org/zap"
)

func (s *Server) OnConnect(c conn) {

}

// OnPacket 收到数据包
func (s *Server) OnPacket(c conn, data []byte) []byte {

	s.inBytes.Add(int64(len(data))) // 入口流量统计

	cli := s.clientManager.Get(c.GetID())
	if cli != nil {
		start := time.Now().UnixMilli()
		cli.InboundAppend(data)
		fmt.Println("InboundAppend---->", time.Now().UnixMilli()-start, "dataSize", len(data))
		// slot := s.inboundManager.AddClientID(cli.ID())
		// s.inboundManager.Notify(slot)
		return nil
	}

	packet, size, err := s.opts.Proto.DecodeFrame(data, lmproto.LatestVersion)
	if err != nil {
		fmt.Println("decode err---->", err)
		s.Warn("Failed to decode the message", zap.Error(err))
		c.Close()
		return nil
	}
	fmt.Println("packet----->", packet.GetFrameType())
	if packet.GetFrameType() != lmproto.CONNECT {
		s.Warn("请先进行连接！")
		c.Close()
		return nil
	}

	s.packetHandler.handleConnect2(c, packet.(*lmproto.ConnectPacket))

	return data[size:]

	// // 处理包
	// offset := 0
	// for len(data) > offset {
	// 	packet, size, err := s.opts.Proto.DecodePacket(data[offset:], lmproto.LatestVersion)
	// 	if err != nil { //
	// 		s.Warn("Failed to decode the message", zap.Error(err))
	// 		c.Close()
	// 		return
	// 	}
	// 	s.handlePacket(c, packet, size) // 处理包

	// 	offset += size
	// 	if !c.Authed() && packet.GetPacketType() != lmproto.CONNECT {
	// 		s.Warn("请先进行连接！")
	// 		c.Close()
	// 		return
	// 	}
	// }
}

func (s *Server) OnClose(c conn) {
	cli := s.clientManager.Get(c.GetID())
	if cli != nil {
		cli.Stop()
		s.clientManager.Remove(c.GetID())
		s.clientManager.PutToPool(cli)
	}
}
