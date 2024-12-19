package manager

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
)

type ConnManager struct {
	bluckets []*connBlucket
}

func NewConnManager(blucketCount int) *ConnManager {
	connBluckets := make([]*connBlucket, blucketCount)
	for i := 0; i < blucketCount; i++ {
		connBluckets[i] = &connBlucket{
			connMap: make(map[int64]wknet.Conn),
		}
	}
	return &ConnManager{
		bluckets: connBluckets,
	}
}

func (m *ConnManager) AddConn(conn wknet.Conn) {
	m.blucket(conn.ID()).addConn(conn)
}

func (m *ConnManager) RemoveConn(conn wknet.Conn) {
	m.blucket(conn.ID()).removeConn(conn)
}

func (m *ConnManager) GetConn(connID int64) wknet.Conn {
	return m.blucket(connID).getConn(connID)
}

func (m *ConnManager) blucket(connId int64) *connBlucket {
	blucketIndex := connId % int64(len(m.bluckets))
	return m.bluckets[blucketIndex]
}

func (m *ConnManager) ConnCount() int {
	connCount := 0
	for _, b := range m.bluckets {
		connCount += b.connCount()
	}
	return connCount
}

func (m *ConnManager) GetAllConn() []wknet.Conn {
	var conns []wknet.Conn
	for _, b := range m.bluckets {
		b.RLock()
		for _, conn := range b.connMap {
			conns = append(conns, conn)
		}
		b.RUnlock()
	}
	return conns
}

type connBlucket struct {
	connMap map[int64]wknet.Conn
	sync.RWMutex
}

func (c *connBlucket) getConn(connId int64) wknet.Conn {
	c.RLock()
	defer c.RUnlock()
	return c.connMap[connId]
}

func (c *connBlucket) addConn(conn wknet.Conn) {
	c.Lock()
	defer c.Unlock()
	c.connMap[conn.ID()] = conn
}

func (c *connBlucket) removeConn(conn wknet.Conn) {
	c.Lock()
	defer c.Unlock()
	delete(c.connMap, conn.ID())
}

func (c *connBlucket) connCount() int {
	c.RLock()
	defer c.RUnlock()
	return len(c.connMap)
}
