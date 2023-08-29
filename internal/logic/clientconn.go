package logic

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type clientConn struct {
	conn *pb.Conn
	wklog.Log
	aesKey string
	aesIV  string

	valueMap  map[string]interface{}
	valueLock sync.RWMutex

	tmpBuffer     []byte
	tmpBufferLock sync.RWMutex
}

func newClientConn(conn *pb.Conn) *clientConn {
	return &clientConn{
		conn:      conn,
		Log:       wklog.NewWKLog(fmt.Sprintf("clientConn[%s-%s-%d]", conn.GatewayID, conn.Uid, conn.Id)),
		valueMap:  map[string]interface{}{},
		tmpBuffer: make([]byte, 0),
	}
}

func (c *clientConn) ID() int64 {
	return c.conn.Id
}

func (c *clientConn) UID() string {
	return c.conn.Uid
}

func (c *clientConn) DeviceLevel() uint8 {
	return uint8(c.conn.DeviceLevel)
}

func (c *clientConn) DeviceFlag() uint8 {
	return uint8(c.conn.DeviceFlag)
}

func (c *clientConn) DeviceID() string {
	return c.conn.DeviceID
}

func (c *clientConn) Value(key string) interface{} {
	c.valueLock.RLock()
	defer c.valueLock.RUnlock()
	return c.valueMap[key]
}
func (c *clientConn) SetValue(key string, value interface{}) {
	c.valueLock.Lock()
	defer c.valueLock.Unlock()
	c.valueMap[key] = value
}

func (c *clientConn) ProtoVersion() int {
	return int(c.conn.ProtoVersion)
}
