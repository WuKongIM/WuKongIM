package service

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
)

var ConnManager *ConnMgr

type ConnMgr struct {
	connBlucket []map[int64]wknet.Conn
	sync.RWMutex
}

func NewConnManager(blucketCount int) *ConnMgr {
	connBlucket := make([]map[int64]wknet.Conn, blucketCount)
	for i := 0; i < blucketCount; i++ {
		connBlucket[i] = make(map[int64]wknet.Conn)
	}
	return &ConnMgr{connBlucket: connBlucket}
}

func (m *ConnMgr) AddConn(conn wknet.Conn) {
	m.Lock()
	defer m.Unlock()
	blucketIndex := conn.ID() % int64(len(m.connBlucket))
	m.connBlucket[blucketIndex][conn.ID()] = conn
}

func (m *ConnMgr) RemoveConn(conn wknet.Conn) {
	m.Lock()
	defer m.Unlock()
	blucketIndex := conn.ID() % int64(len(m.connBlucket))
	delete(m.connBlucket[blucketIndex], conn.ID())
}

func (m *ConnMgr) GetConn(connID int64) wknet.Conn {
	m.RLock()
	defer m.RUnlock()
	blucketIndex := connID % int64(len(m.connBlucket))
	return m.connBlucket[blucketIndex][connID]
}
