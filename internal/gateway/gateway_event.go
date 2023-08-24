package gateway

import (
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 数据统一入口
func (g *Gateway) onData(conn wknet.Conn) error {
	buff, err := conn.Peek(-1)
	if err != nil {
		return err
	}
	if len(buff) == 0 {
		return nil
	}
	data, _ := gnetUnpacket(buff)
	if len(data) == 0 {
		return nil
	}
	if !conn.IsAuthed() { // conn is not authed must be connect packet
		packet, _, err := g.opts.Proto.DecodeFrame(data, wkproto.LatestVersion)
		if err != nil {
			g.Warn("Failed to decode the message", zap.Error(err))
			conn.Close()
			return nil
		}
		if packet == nil {
			g.Warn("message is nil", zap.ByteString("data", data))
			return nil
		}
		if packet.GetFrameType() != wkproto.CONNECT {
			g.Warn("请先进行连接！")
			conn.Close()
			return nil
		}
		//  process conn auth
		_, err = conn.Discard(len(data))
		if err != nil {
			g.Warn("discard error", zap.Error(err))
		}
		g.processor.auth(conn, packet.(*wkproto.ConnectPacket))
	} else { // authed
		offset := 0
		for len(data) > offset {
			frame, size, err := g.opts.Proto.DecodeFrame(data[offset:], uint8(conn.ProtoVersion()))
			if err != nil { //
				g.Warn("Failed to decode the message", zap.Error(err))
				conn.Close()
				return err
			}
			if frame == nil {
				break
			}

			// 统计
			g.monitor.UpstreamPackageAdd(1)
			g.monitor.UpstreamTrafficAdd(size)
			g.monitor.InMsgsInc()
			g.monitor.InBytesAdd(int64(size))
			// context
			// connCtx := conn.Context().(*connContext)
			// connCtx.putFrame(frame)
			offset += size
		}
		// process frames
		_, err = conn.Discard(offset)
		if err != nil {
			g.Warn("discard error", zap.Error(err))
		}

		// d.processor.process(conn)
	}
	return nil
}

func (g *Gateway) onConnect(conn wknet.Conn) error {
	return nil
}

func (g *Gateway) onClose(conn wknet.Conn) {
}
