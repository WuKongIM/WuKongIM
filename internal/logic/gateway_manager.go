package logic

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/gatewaycommon"
)

type gatewayManager struct {
	gatewayMap     map[string]*gatewayClient
	gatewayMapLock sync.RWMutex
}

func newGatewayManager() *gatewayManager {
	return &gatewayManager{
		gatewayMap: make(map[string]*gatewayClient),
	}
}

func (g *gatewayManager) add(gt *gatewayClient) {
	g.gatewayMapLock.Lock()
	defer g.gatewayMapLock.Unlock()
	g.gatewayMap[gt.gatewayID] = gt
}

func (g *gatewayManager) remove(gt *gatewayClient) {
	g.gatewayMapLock.Lock()
	defer g.gatewayMapLock.Unlock()
	delete(g.gatewayMap, gt.gatewayID)
}

func (g *gatewayManager) get(gatewayID string) *gatewayClient {
	g.gatewayMapLock.RLock()
	defer g.gatewayMapLock.RUnlock()
	return g.gatewayMap[gatewayID]
}

func (g *gatewayManager) getAllGateway() []*gatewayClient {
	g.gatewayMapLock.RLock()
	defer g.gatewayMapLock.RUnlock()
	gateways := make([]*gatewayClient, 0, len(g.gatewayMap))
	for _, gateway := range g.gatewayMap {
		gateways = append(gateways, gateway)
	}
	return gateways
}

func (g *gatewayManager) getConnsWithUID(uid string) []gatewaycommon.Conn {
	gatewayClients := g.getAllGateway()
	conns := make([]gatewaycommon.Conn, 0, 10)
	for _, gatewayClient := range gatewayClients {
		conns = append(conns, gatewayClient.GetConnsWithUID(uid)...)
	}
	return conns
}
