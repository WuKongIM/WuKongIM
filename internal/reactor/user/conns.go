package reactor

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
)

type conns struct {
	conns []reactor.Conn // 一个用户有多个连接
	sync.RWMutex
}

func (c *conns) add(cn reactor.Conn) {
	c.Lock()
	defer c.Unlock()
	c.conns = append(c.conns, cn)
}

func (c *conns) remove(cn reactor.Conn) {
	c.Lock()
	defer c.Unlock()
	for i, conn := range c.conns {
		if conn == cn {
			c.conns = append(c.conns[:i], c.conns[i+1:]...)
			return
		}
	}
}

func (c *conns) connByConnId(nodeId uint64, connId int64) reactor.Conn {
	c.RLock()
	defer c.RUnlock()
	for _, conn := range c.conns {
		if conn.ConnId() == connId && conn.FromNode() == nodeId {
			return conn
		}
	}
	return nil
}

func (c *conns) updateConnAuth(nodeId uint64, connId int64, auth bool) {
	c.Lock()
	defer c.Unlock()
	for i, conn := range c.conns {
		if conn.ConnId() == connId && conn.FromNode() == nodeId {
			c.conns[i].SetAuth(auth)
			return
		}
	}
}

func (c *conns) updateConn(connId int64, nodeId uint64, newConn reactor.Conn) {
	c.Lock()
	defer c.Unlock()
	for i, conn := range c.conns {
		if conn.ConnId() == connId && conn.FromNode() == nodeId {
			c.conns[i] = newConn
			return
		}
	}
}

func (c *conns) len() int {
	c.RLock()
	defer c.RUnlock()
	return len(c.conns)
}

func (c *conns) allConns() []reactor.Conn {
	c.RLock()
	defer c.RUnlock()
	return c.conns
}

func (c *conns) connsByNodeId(nodeId uint64) []reactor.Conn {
	c.RLock()
	defer c.RUnlock()
	conns := make([]reactor.Conn, 0, len(c.conns))
	for _, conn := range c.conns {
		if conn.FromNode() == nodeId {
			conns = append(conns, conn)
		}
	}
	return conns
}

func (c *conns) nodeIds() []uint64 {
	c.RLock()
	defer c.RUnlock()
	nodeIds := make([]uint64, 0, len(c.conns))
	for _, conn := range c.conns {
		for _, nodeId := range nodeIds {
			if nodeId == conn.FromNode() {
				continue
			}
		}
		nodeIds = append(nodeIds, conn.FromNode())
	}
	return nodeIds
}

func (c *conns) reset() {
	c.Lock()
	defer c.Unlock()
	c.conns = nil
}
