package gatewaycommon

import (
	"sync"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// type Client interface {
// 	// ListenFrames(fnc func(conn pb.Conn, frames []wkproto.Frame)) error

// 	WriteToGateway(conn *pb.Conn, data []byte) error

// 	GetConnManager() ConnManager
// }

type Conn interface {
	ID() int64
	UID() string
	DeviceLevel() uint8
	DeviceFlag() uint8
	DeviceID() string
	Value(key string) interface{}
	SetValue(key string, value interface{})
	ProtoVersion() int
}

type ConnManager interface {
	AddConn(conn Conn)
	GetConn(id int64) Conn
	RemoveConn(conn Conn)
	RemoveConnWithID(id int64)
	GetConnsWithUID(uid string) []Conn
	ExistConnsWithUID(uid string) bool
	GetConnsWith(uid string, deviceFlag wkproto.DeviceFlag) []Conn
}

type DefaultConnManager struct {
	userConnMap map[string][]int64
	connMap     map[int64]Conn
	sync.RWMutex
}

func NewDefaultConnManager() *DefaultConnManager {

	return &DefaultConnManager{
		userConnMap: make(map[string][]int64),
		connMap:     make(map[int64]Conn),
	}
}

func (d *DefaultConnManager) AddConn(conn Conn) {
	d.Lock()
	defer d.Unlock()
	connIDs := d.userConnMap[conn.UID()]
	if connIDs == nil {
		connIDs = make([]int64, 0, 10)
	}
	connIDs = append(connIDs, conn.ID())
	d.userConnMap[conn.UID()] = connIDs
	d.connMap[conn.ID()] = conn
}

func (d *DefaultConnManager) GetConn(id int64) Conn {
	d.RLock()
	defer d.RUnlock()
	return d.connMap[id]
}

func (d *DefaultConnManager) RemoveConn(conn Conn) {
	d.RemoveConnWithID(conn.ID())
}

func (d *DefaultConnManager) RemoveConnWithID(id int64) {
	d.Lock()
	defer d.Unlock()
	conn := d.connMap[id]
	delete(d.connMap, id)
	if conn == nil {
		return
	}
	connIDs := d.userConnMap[conn.UID()]
	if len(connIDs) > 0 {
		for index, connID := range connIDs {
			if connID == conn.ID() {
				connIDs = append(connIDs[:index], connIDs[index+1:]...)
				d.userConnMap[conn.UID()] = connIDs
			}
		}
	}
}

func (d *DefaultConnManager) GetConnsWithUID(uid string) []Conn {
	d.RLock()
	defer d.RUnlock()
	connIDs := d.userConnMap[uid]
	if len(connIDs) == 0 {
		return nil
	}
	conns := make([]Conn, 0, len(connIDs))
	for _, id := range connIDs {
		conn := d.connMap[id]
		if conn != nil {
			conns = append(conns, conn)
		}
	}
	return conns
}

func (d *DefaultConnManager) ExistConnsWithUID(uid string) bool {
	d.RLock()
	defer d.RUnlock()
	return len(d.userConnMap[uid]) > 0
}

func (d *DefaultConnManager) GetConnsWith(uid string, deviceFlag wkproto.DeviceFlag) []Conn {
	conns := d.GetConnsWithUID(uid)
	if len(conns) == 0 {
		return nil
	}
	deviceConns := make([]Conn, 0, len(conns))
	for _, conn := range conns {
		if conn.DeviceFlag() == deviceFlag.ToUint8() {
			deviceConns = append(deviceConns, conn)
		}
	}
	return deviceConns
}

// GetConnCountWith 获取设备的在线数量和用户所有设备的在线数量
func (d *DefaultConnManager) GetConnCountWith(uid string, deviceFlag wkproto.DeviceFlag) (int, int) {
	conns := d.GetConnsWithUID(uid)
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
func (d *DefaultConnManager) GetOnlineConns(uids []string) []Conn {
	if len(uids) == 0 {
		return make([]Conn, 0)
	}
	d.Lock()
	defer d.Unlock()
	var onlineConns = make([]Conn, 0, len(uids))
	for _, uid := range uids {
		connIDs := d.userConnMap[uid]
		for _, connID := range connIDs {
			conn := d.connMap[connID]
			if conn != nil {
				onlineConns = append(onlineConns, conn)
			}
		}
	}
	return onlineConns
}
