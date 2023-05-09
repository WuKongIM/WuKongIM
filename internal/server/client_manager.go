package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/lmproto"
)

// ClientManager 客户端管理
type ClientManager struct {
	userClientMap map[string][]uint32
	clientMap     map[uint32]*client // TODO: 这里需要做gc优化
	sync.RWMutex
	s          *Server
	clientPool sync.Pool
}

// NewClientManager 创建一个默认的客户端管理者
func NewClientManager(s *Server) *ClientManager {
	return &ClientManager{userClientMap: make(map[string][]uint32), clientMap: make(map[uint32]*client), s: s, clientPool: sync.Pool{
		New: func() any {
			return &client{}
		},
	}}
}

// Add 添加客户端
func (m *ClientManager) Add(cli *client) {
	m.Lock()
	defer m.Unlock()

	m.clientMap[cli.ID()] = cli
	ids := m.userClientMap[cli.uid]
	if ids == nil {
		ids = make([]uint32, 0)
	}
	ids = append(ids, cli.ID())
	m.userClientMap[cli.uid] = ids
}

func (m *ClientManager) GetFromPool() *client {
	return m.clientPool.Get().(*client)
}

func (m *ClientManager) PutToPool(cli *client) {
	m.clientPool.Put(cli)
}

// Get 获取客户端
func (m *ClientManager) Get(id uint32) *client {
	m.Lock()
	defer m.Unlock()

	return m.clientMap[id]
}

// Remove 移除客户端
func (m *ClientManager) Remove(id uint32) {
	m.Lock()
	defer m.Unlock()

	cli := m.clientMap[id]
	if cli == nil {
		return
	}
	delete(m.clientMap, id)

	ids := m.userClientMap[cli.uid]
	if len(ids) > 0 {
		for i, id := range ids {
			if id == cli.ID() {
				ids = append(ids[:i], ids[i+1:]...)
				m.userClientMap[cli.uid] = ids
				break
			}
		}
	}
}

// GetClientsWithUID 通过用户uid获取客户端集合
func (m *ClientManager) GetClientsWithUID(uid string) []*client {

	m.Lock()
	defer m.Unlock()

	ids := m.userClientMap[uid]
	if ids == nil {
		return nil
	}

	clients := make([]*client, 0, len(ids))
	for _, id := range ids {
		cli := m.clientMap[id]
		if cli != nil {
			clients = append(clients, cli)
		}
	}
	return clients
}

// GetOnlineUIDs 传一批uids 返回在线的uids
func (m *ClientManager) GetOnlineClients(uids []string) []*client {

	m.Lock()
	defer m.Unlock()

	if len(uids) == 0 {
		return make([]*client, 0)
	}
	var onlineClients = make([]*client, 0, len(uids))
	for _, uid := range uids {
		ids := m.userClientMap[uid]
		if len(ids) > 0 {
			for _, clientID := range ids {
				cli := m.clientMap[clientID]
				if cli != nil {
					onlineClients = append(onlineClients, cli)
				}
			}
		}

	}
	return onlineClients
}

// GetClientsWith 查询设备
func (m *ClientManager) GetClientsWith(uid string, deviceFlag lmproto.DeviceFlag) []*client {
	clients := m.GetClientsWithUID(uid)
	if len(clients) == 0 {
		return nil
	}
	deviceClients := make([]*client, 0, len(clients))
	for _, cli := range clients {
		if cli.deviceFlag == deviceFlag {
			deviceClients = append(deviceClients, cli)
		}
	}
	return deviceClients
}

// GetClientCountWith 获取设备的在线数量和用户所有设备的在线数量
func (m *ClientManager) GetClientCountWith(uid string, deviceFlag lmproto.DeviceFlag) (int, int) {
	clients := m.GetClientsWithUID(uid)
	if len(clients) == 0 {
		return 0, 0
	}
	deviceOnlineCount := 0
	for _, cli := range clients {
		if cli.deviceFlag == deviceFlag {
			deviceOnlineCount++
		}
	}
	return deviceOnlineCount, len(clients)
}

func (m *ClientManager) GetAllClient() Clients {
	m.Lock()
	defer m.Unlock()

	clients := make([]*client, 0)
	for _, cli := range m.clientMap {
		clients = append(clients, cli)
	}
	return clients
}

func (m *ClientManager) GetAllClientCount() int {
	m.Lock()
	defer m.Unlock()
	return len(m.clientMap)
}

type Clients []*client

func (cs Clients) Len() int { return len(cs) }

func (cs Clients) Swap(i, j int) { cs[i], cs[j] = cs[j], cs[i] }
