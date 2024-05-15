package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type connInfo struct {
	id           int64
	uid          string
	deviceId     string
	deviceFlag   wkproto.DeviceFlag
	deviceLevel  wkproto.DeviceLevel
	aesKey       string
	aesIV        string
	protoVersion uint8
}

type connContext struct {
	connInfo
	conn wknet.Conn

	subReactor *userReactorSub
}

func newConnContext(connInfo connInfo, conn wknet.Conn, subReactor *userReactorSub) *connContext {
	return &connContext{
		connInfo:   connInfo,
		conn:       conn,
		subReactor: subReactor,
	}
}

func (c *connContext) addOtherPacket(packet wkproto.Frame) {
	_ = c.subReactor.step(c.uid, &UserAction{
		ActionType: UserActionProcess,
		Messages: []*ReactorUserMessage{
			{
				Uid:      c.uid,
				DeviceId: c.deviceId,
				InPacket: packet,
			},
		},
	})
}

func (c *connContext) addSendPacket(packet *wkproto.SendPacket) {

	_ = c.subReactor.proposeSend(c, packet)
}

func (c *connContext) write(d []byte) {
	_ = c.subReactor.step(c.uid, &UserAction{
		ActionType: UserActionProcess,
		Messages: []*ReactorUserMessage{
			{
				Uid:      c.uid,
				DeviceId: c.deviceId,
				OutBytes: d,
			},
		},
	})
}

func (c *connContext) close() {

}

func (c *connContext) isClosed() bool {
	return false
}
