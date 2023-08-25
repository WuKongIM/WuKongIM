package gateway

import (
	"github.com/WuKongIM/WuKongIM/internal/gateway/proto"
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

	if !conn.IsAuthed() { // conn is not authed must be connect packet
		data, _ := gnetUnpacket(buff)
		if len(data) == 0 {
			return nil
		}
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

		blockData, err := g.blockProto.Encode(&proto.Block{
			Seq:        g.blockSeq.Inc(),
			ConnID:     conn.ID(),
			UID:        conn.UID(),
			DeviceFlag: wkproto.DeviceFlag(conn.DeviceFlag()),
			Data:       buff,
		})
		if err != nil {
			g.Warn("Failed to encode the message", zap.Error(err))
			return nil
		}
		if len(blockData) == 0 {
			return nil
		}
		g.monitor.UpstreamTrafficAdd(len(buff))
		g.monitor.InBytesAdd(int64(len(buff)))

		g.deliverData(conn.UID(), blockData)
		_, err = conn.Discard(len(buff))
		if err != nil {
			g.Warn("discard error", zap.Error(err))
		}

	}
	return nil
}

func (g *Gateway) onConnect(conn wknet.Conn) error {
	return nil
}

func (g *Gateway) onClose(conn wknet.Conn) {
}

func (g *Gateway) deliverData(uid string, blockData []byte) {

}
