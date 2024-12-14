package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
)

type connManager struct {
	connBlucket []map[int64]wknet.Conn
	sync.RWMutex
}

func newConnManager(blucketCount int) *connManager {
	connBlucket := make([]map[int64]wknet.Conn, blucketCount)
	for i := 0; i < blucketCount; i++ {
		connBlucket[i] = make(map[int64]wknet.Conn)
	}
	return &connManager{connBlucket: connBlucket}
}

func (m *connManager) addConn(conn wknet.Conn) {
	m.Lock()
	defer m.Unlock()
	blucketIndex := conn.ID() % int64(len(m.connBlucket))
	m.connBlucket[blucketIndex][conn.ID()] = conn
}

func (m *connManager) removeConn(conn wknet.Conn) {
	m.Lock()
	defer m.Unlock()
	blucketIndex := conn.ID() % int64(len(m.connBlucket))
	delete(m.connBlucket[blucketIndex], conn.ID())
}

func (m *connManager) getConn(connID int64) wknet.Conn {
	m.RLock()
	defer m.RUnlock()
	blucketIndex := connID % int64(len(m.connBlucket))
	return m.connBlucket[blucketIndex][connID]
}
