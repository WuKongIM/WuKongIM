package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type ConnManager struct {
	userConnMap map[string][]int64
	connMap     map[int64]int

	peerProxyConnMap map[uint64]map[int64]int64
	proxyConnMap     map[int64]*ProxyClientConn

	sync.RWMutex
	s *Server
}

func NewConnManager(s *Server) *ConnManager {

	return &ConnManager{
		userConnMap:      make(map[string][]int64),
		connMap:          make(map[int64]int),
		peerProxyConnMap: make(map[uint64]map[int64]int64),
		proxyConnMap:     make(map[int64]*ProxyClientConn),
		s:                s,
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

	proxyConn, ok := conn.(*ProxyClientConn)
	if ok {
		c.addProxyConn(proxyConn)
	} else {
		c.connMap[conn.ID()] = conn.Fd().Fd()
	}

}

func (c *ConnManager) addProxyConn(conn *ProxyClientConn) {
	peerConnMap := c.peerProxyConnMap[conn.belongNodeID]
	if peerConnMap == nil {
		peerConnMap = make(map[int64]int64)
	}
	peerConnMap[conn.orgID] = conn.ID()
	c.peerProxyConnMap[conn.belongNodeID] = peerConnMap
	c.proxyConnMap[conn.ID()] = conn
}

func (c *ConnManager) removeProxyConn(conn *ProxyClientConn) {
	peerConnMap := c.peerProxyConnMap[conn.belongNodeID]
	if peerConnMap == nil {
		return
	}
	delete(peerConnMap, conn.orgID)
	delete(c.proxyConnMap, conn.ID())
}

func (c *ConnManager) GetProxyConn(peerID uint64, orgID int64) *ProxyClientConn {
	c.Lock()
	defer c.Unlock()
	peerConnMap := c.peerProxyConnMap[peerID]
	if peerConnMap == nil {
		return nil
	}
	connID := peerConnMap[orgID]
	if connID == 0 {
		return nil
	}
	return c.proxyConnMap[connID]
}

func (c *ConnManager) getProxyConnByID(id int64) *ProxyClientConn {
	return c.proxyConnMap[id]
}

func (c *ConnManager) GetConn(id int64) wknet.Conn {
	c.Lock()
	defer c.Unlock()
	proxyConn := c.getProxyConnByID(id)
	if proxyConn != nil {
		return proxyConn
	}
	return c.s.dispatch.engine.GetConn(c.connMap[id])
}

func (c *ConnManager) RemoveConn(conn wknet.Conn) {

	c.RemoveConnWithID(conn.ID())
}

func (c *ConnManager) RemoveConnWithID(id int64) {
	c.Lock()
	defer c.Unlock()

	proxyConn := c.getProxyConnByID(id)
	var conn wknet.Conn
	if proxyConn != nil {
		c.removeProxyConn(proxyConn)
		conn = proxyConn
	} else {
		conn = c.s.dispatch.engine.GetConn(c.connMap[id])
		delete(c.connMap, id)
		if conn == nil {
			return
		}
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
	c.Lock()
	defer c.Unlock()
	connIDs := c.userConnMap[uid]
	if len(connIDs) == 0 {
		return nil
	}
	conns := make([]wknet.Conn, 0, len(connIDs))
	var conn wknet.Conn
	for _, id := range connIDs {
		proxyConn := c.getProxyConnByID(id)
		if proxyConn == nil {
			conn = c.s.dispatch.engine.GetConn(c.connMap[id])
		} else {
			conn = proxyConn
		}
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

func (c *ConnManager) GetConnWithDeviceID(uid string, deviceID string) wknet.Conn {
	conns := c.GetConnsWithUID(uid)
	if len(conns) == 0 {
		return nil
	}
	for _, conn := range conns {
		if conn.DeviceID() == deviceID {
			return conn
		}
	}
	return nil
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
		var conn wknet.Conn
		for _, connID := range connIDs {
			proxyConn := c.getProxyConnByID(connID)
			if proxyConn == nil {
				conn = c.s.dispatch.engine.GetConn(c.connMap[connID])
			} else {
				conn = proxyConn
			}
			if conn != nil {
				onlineConns = append(onlineConns, conn)
			}
		}
	}
	return onlineConns
}
