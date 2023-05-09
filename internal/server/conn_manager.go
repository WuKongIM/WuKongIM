package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
)

type ConnManager struct {
	connMap     map[int64]wknet.Conn
	userConnMap map[string][]int64
	sync.RWMutex
}

func NewConnManager() *ConnManager {
	return &ConnManager{}
}

func (c *ConnManager) Add(conn wknet.Conn) {
	c.Lock()
	defer c.Unlock()
	c.connMap[conn.ID()] = conn
	ids := c.userConnMap[conn.UID()]
	if ids == nil {
		ids = make([]int64, 0)
	}
	ids = append(ids, conn.ID())
	c.userConnMap[conn.UID()] = ids
}
