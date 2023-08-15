package transporter

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
)

type ConnManager struct {
	userConnMap map[string]wknet.Conn
	sync.RWMutex
}

func NewConnManager() *ConnManager {

	return &ConnManager{
		userConnMap: map[string]wknet.Conn{},
	}
}

func (c *ConnManager) AddConn(conn wknet.Conn) {
	c.Lock()
	defer c.Unlock()

	c.userConnMap[conn.UID()] = conn
}

func (c *ConnManager) GetConn(uid string) wknet.Conn {
	c.RLock()
	defer c.RUnlock()

	return c.userConnMap[uid]
}

func (c *ConnManager) RemoveConn(conn wknet.Conn) {
	c.Lock()
	defer c.Unlock()
	delete(c.userConnMap, conn.UID())
}
