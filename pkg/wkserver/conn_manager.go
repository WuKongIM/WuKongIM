package wkserver

import (
	"sync"

	"github.com/panjf2000/gnet/v2"
)

type ConnManager struct {
	connMapLock sync.RWMutex
	connMap     map[string]gnet.Conn
}

func NewConnManager() *ConnManager {
	return &ConnManager{
		connMap: make(map[string]gnet.Conn),
	}
}

func (c *ConnManager) AddConn(uid string, conn gnet.Conn) {
	c.connMapLock.Lock()
	defer c.connMapLock.Unlock()
	c.connMap[uid] = conn
}

func (c *ConnManager) GetConn(uid string) gnet.Conn {
	c.connMapLock.RLock()
	defer c.connMapLock.RUnlock()
	return c.connMap[uid]
}

func (c *ConnManager) RemoveConn(uid string) {
	c.connMapLock.Lock()
	defer c.connMapLock.Unlock()
	delete(c.connMap, uid)
}
