package logic

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/gatewaycommon"
	"github.com/WuKongIM/WuKongIM/internal/pb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 数据统一出口
func (l *Logic) dataOut(conn gatewaycommon.Conn, frames ...wkproto.Frame) {
	if len(frames) == 0 {
		return
	}

	fmt.Println("dataOut--->", frames)
	// 统计
	l.monitor.DownstreamPackageAdd(len(frames))

	framesData := make([]byte, 0)
	for _, frame := range frames {
		data, err := l.opts.Proto.EncodeFrame(frame, uint8(conn.ProtoVersion()))
		if err != nil {
			l.Warn("Failed to encode the message", zap.Error(err))
		} else {
			// 统计
			dataLen := len(data)
			l.monitor.DownstreamTrafficAdd(dataLen)

			framesData = append(framesData, data...)
		}
	}

	if len(framesData) > 0 {
		gatewayID := GetGatewayID(conn)
		gatewayCli := l.gatewayManager.get(gatewayID)
		if gatewayCli != nil {
			err := gatewayCli.WriteToGateway(&pb.Conn{
				Id:           conn.ID(),
				Uid:          conn.UID(),
				DeviceFlag:   uint32(conn.DeviceFlag()),
				DeviceID:     conn.DeviceID(),
				GatewayID:    gatewayID,
				ProtoVersion: uint32(conn.ProtoVersion()),
			}, framesData)
			if err != nil {
				l.Warn("Failed to write the message to the gateway", zap.Error(err))
			}
		}
	}

}
