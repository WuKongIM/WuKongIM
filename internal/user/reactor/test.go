package reactor

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type testConn struct {
	connId     int64
	uid        string
	from       uint64
	auth       bool
	deviceFlag wkproto.DeviceFlag
	deviceId   string
	valueLock  sync.RWMutex
	valueMap   map[string]string

	protoVersion uint8
	deviceLevel  wkproto.DeviceLevel
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

func (t *testConn) DeviceId() string {
	return t.deviceId
}

func (t *testConn) Equal(conn *reactor.Conn) bool {
	return t.connId == conn.ConnId && t.uid == conn.Uid && t.from == conn.FromNode
}

func (t *testConn) SetString(key string, value string) {
	t.valueLock.Lock()
	defer t.valueLock.Unlock()
	if t.valueMap == nil {
		t.valueMap = make(map[string]string)
	}
	t.valueMap[key] = value
}

func (t *testConn) GetString(key string) string {
	t.valueLock.RLock()
	defer t.valueLock.RUnlock()
	return t.valueMap[key]
}

func (t *testConn) SetProtoVersion(version uint8) {
	t.protoVersion = version
}

func (t *testConn) GetProtoVersion() uint8 {
	return t.protoVersion
}

func (t *testConn) DeviceLevel() wkproto.DeviceLevel {
	return t.deviceLevel
}

func (t *testConn) SetDeviceLevel(level wkproto.DeviceLevel) {
	t.deviceLevel = level
}

func (t *testConn) Encode() ([]byte, error) {

	return nil, nil
}

func (t *testConn) Decode(data []byte) error {
	return nil
}
