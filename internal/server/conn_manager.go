package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
)

type ConnManager struct {
	userConnMap map[string][]int64
	connMap     map[int64]int

	sync.RWMutex
	s *Server
}

func NewConnManager(s *Server) *ConnManager {

	return &ConnManager{
		userConnMap: make(map[string][]int64),
		connMap:     make(map[int64]int),
		s:           s,
	}
}

func (c *ConnManager) AddConn(conn wknet.Conn) {
	c.Lock()
	defer c.Unlock()
	connIDs := c.userConnMap[conn.UID()]
	if connIDs == nil {
		connIDs = make([]int64, 0, 10)
	}
	connIDs = append(connIDs, conn.ID())
	c.userConnMap[conn.UID()] = connIDs
	c.connMap[conn.ID()] = conn.Fd().Fd()
}

func (c *ConnManager) GetConn(id int64) wknet.Conn {
	c.RLock()
	defer c.RUnlock()
	return c.s.dispatch.engine.GetConn(c.connMap[id])
}

func (c *ConnManager) RemoveConn(conn wknet.Conn) {
	c.RemoveConnWithID(conn.ID())
}

func (c *ConnManager) RemoveConnWithID(id int64) {
	c.Lock()
	defer c.Unlock()
	conn := c.s.dispatch.engine.GetConn(c.connMap[id])
	delete(c.connMap, id)
	if conn == nil {
		return
	}
	connIDs := c.userConnMap[conn.UID()]
	if len(connIDs) > 0 {
		for index, connID := range connIDs {
			if connID == conn.ID() {
				connIDs = append(connIDs[:index], connIDs[index+1:]...)
				c.userConnMap[conn.UID()] = connIDs
			}
		}
	}
}

func (c *ConnManager) GetConnsWithUID(uid string) []wknet.Conn {
	c.RLock()
	defer c.RUnlock()
	connIDs := c.userConnMap[uid]
	if len(connIDs) == 0 {
		return nil
	}
	conns := make([]wknet.Conn, 0, len(connIDs))
	for _, id := range connIDs {
		conn := c.s.dispatch.engine.GetConn(c.connMap[id])
		if conn != nil {
			conns = append(conns, conn)
		}
	}
	return conns
}

func (c *ConnManager) ExistConnsWithUID(uid string) bool {
	c.RLock()
	defer c.RUnlock()
	return len(c.userConnMap[uid]) > 0
}

func (c *ConnManager) GetConnsWith(uid string, deviceFlag wkproto.DeviceFlag) []wknet.Conn {
	conns := c.GetConnsWithUID(uid)
	if len(conns) == 0 {
		return nil
	}
	deviceConns := make([]wknet.Conn, 0, len(conns))
	for _, conn := range conns {
		if conn.DeviceFlag() == deviceFlag.ToUint8() {
			deviceConns = append(deviceConns, conn)
		}
	}
	return deviceConns
}

// GetConnCountWith 获取设备的在线数量和用户所有设备的在线数量
func (c *ConnManager) GetConnCountWith(uid string, deviceFlag wkproto.DeviceFlag) (int, int) {
	conns := c.GetConnsWithUID(uid)
	if len(conns) == 0 {
		return 0, 0
	}
	deviceOnlineCount := 0
	for _, conn := range conns {
		if wkproto.DeviceFlag(conn.DeviceFlag()) == deviceFlag {
			deviceOnlineCount++
		}
	}
	return deviceOnlineCount, len(conns)
}

// GetOnlineConns 传一批uids 返回在线的uids
func (c *ConnManager) GetOnlineConns(uids []string) []wknet.Conn {
	if len(uids) == 0 {
		return make([]wknet.Conn, 0)
	}
	c.Lock()
	defer c.Unlock()
	var onlineConns = make([]wknet.Conn, 0, len(uids))
	for _, uid := range uids {
		connIDs := c.userConnMap[uid]
		for _, connID := range connIDs {
			conn := c.s.dispatch.engine.GetConn(c.connMap[connID])
			if conn != nil {
				onlineConns = append(onlineConns, conn)
			}
		}
	}
	return onlineConns
}
