package reactor

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type testConn struct {
	connId     int64
	uid        string
	from       uint64
	auth       bool
	deviceFlag wkproto.DeviceFlag
}

func (t *testConn) ConnId() int64 {
	return t.connId
}
func (t *testConn) Uid() string {
	return t.uid
}
func (t *testConn) FromNode() uint64 {
	return t.from
}

func (t *testConn) SetAuth(auth bool) {
	t.auth = auth
}

func (t *testConn) IsAuth() bool {
	return t.auth
}

func (t *testConn) DeviceFlag() wkproto.DeviceFlag {
	return t.deviceFlag
}

type testMessage struct {
	index uint64
	conn  *testConn
}

func (t *testMessage) Conn() reactor.Conn {
	return t.conn
}
func (t *testMessage) Frame() wkproto.Frame {
	return nil
}
func (t *testMessage) Size() uint64 {
	return 10
}

func (t *testMessage) SetIndex(index uint64) {
	t.index = index
}

func (t *testMessage) Index() uint64 {
	return t.index
}
