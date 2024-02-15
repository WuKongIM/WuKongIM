package server

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type ConnManager struct {
	userConnMap map[string][]string
	connMap     map[string]int

	proxyConnMap map[string]*ProxyClientConn

	sync.RWMutex
	s *Server
}

func NewConnManager(s *Server) *ConnManager {

	return &ConnManager{
		userConnMap:  make(map[string][]string),
		connMap:      make(map[string]int),
		proxyConnMap: make(map[string]*ProxyClientConn),
		s:            s,
	}
}

func (c *ConnManager) AddConn(conn wknet.Conn) {
	c.Lock()
	defer c.Unlock()
	devices := c.userConnMap[conn.UID()]
	if devices == nil {
		devices = make([]string, 0, 10)
	}
	devices = append(devices, conn.DeviceID())
	c.userConnMap[conn.UID()] = devices

	proxyConn, ok := conn.(*ProxyClientConn)
	if ok {
		c.addProxyConn(proxyConn)
	} else {
		c.connMap[c.userDeviceID(conn.UID(), conn.DeviceID())] = conn.Fd().Fd()
	}

}

func (c *ConnManager) userDeviceID(uid string, deviceId string) string {
	return fmt.Sprintf("%s:%s", uid, deviceId)
}

func (c *ConnManager) addProxyConn(conn *ProxyClientConn) {
	c.proxyConnMap[c.userDeviceID(conn.UID(), conn.DeviceID())] = conn
}

// func (c *ConnManager) removeProxyConn(conn *ProxyClientConn) {
// 	peerConnMap := c.peerProxyConnMap[conn.belongNodeID]
// 	if peerConnMap == nil {
// 		return
// 	}
// 	delete(peerConnMap, conn.orgID)
// 	delete(c.proxyConnMap, conn.ID())
// }

// func (c *ConnManager) GetProxyConn(nodeId uint64, orgID int64) *ProxyClientConn {
// 	c.Lock()
// 	defer c.Unlock()
// 	peerConnMap := c.peerProxyConnMap[nodeId]
// 	if peerConnMap == nil {
// 		return nil
// 	}
// 	connID := peerConnMap[orgID]
// 	if connID == 0 {
// 		return nil
// 	}
// 	return c.proxyConnMap[connID]
// }

// func (c *ConnManager) getProxyConnByID(id int64) *ProxyClientConn {
// 	return c.proxyConnMap[id]
// }

func (c *ConnManager) GetConn(uid string, deviceId string) wknet.Conn {
	c.Lock()
	defer c.Unlock()
	fd, ok := c.connMap[c.userDeviceID(uid, deviceId)]
	if !ok {
		proxyConn := c.proxyConnMap[c.userDeviceID(uid, deviceId)]
		return proxyConn
	}
	return c.s.dispatch.engine.GetConn(fd)
}

func (c *ConnManager) RemoveConn(conn wknet.Conn) {

	c.RemoveConnWithID(conn.UID(), conn.DeviceID())
}

func (c *ConnManager) RemoveConnWithID(uid string, deviceId string) {
	c.Lock()
	defer c.Unlock()

	userFlag := c.userDeviceID(uid, deviceId)
	fd := c.connMap[userFlag]
	fmt.Println("RemoveConnWithID---->", uid, deviceId, fd)
	conn := c.s.dispatch.engine.GetConn(fd)
	delete(c.connMap, userFlag)
	if conn == nil {
		delete(c.proxyConnMap, userFlag)
		return
	}

	devices := c.userConnMap[conn.UID()]
	if len(devices) > 0 {
		for index, deviceId := range devices {
			if deviceId == conn.DeviceID() {
				devices = append(devices[:index], devices[index+1:]...)
				c.userConnMap[conn.UID()] = devices
				fmt.Println("RemoveConnWithID----2>", uid, deviceId, devices)
			}
		}
	}
}

func (c *ConnManager) GetConnsWithUID(uid string) []wknet.Conn {
	c.Lock()
	defer c.Unlock()

	devices := c.userConnMap[uid]
	if len(devices) == 0 {
		return nil
	}
	conns := make([]wknet.Conn, 0, len(devices))
	var conn wknet.Conn
	for _, deviceId := range devices {
		userFlag := c.userDeviceID(uid, deviceId)
		conn = c.s.dispatch.engine.GetConn(c.connMap[userFlag])
		if conn != nil {
			conns = append(conns, conn)
		} else {
			proxyConn := c.proxyConnMap[userFlag]
			if proxyConn != nil {
				conns = append(conns, proxyConn)
			}
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
		devices := c.userConnMap[uid]
		var conn wknet.Conn
		for _, deviceId := range devices {
			userFlag := c.userDeviceID(uid, deviceId)
			conn = c.s.dispatch.engine.GetConn(c.connMap[userFlag])
			if conn != nil {
				onlineConns = append(onlineConns, conn)
			} else {
				proxyConn := c.proxyConnMap[userFlag]
				if proxyConn != nil {
					onlineConns = append(onlineConns, proxyConn)
				}
			}
		}
	}
	return onlineConns
}
