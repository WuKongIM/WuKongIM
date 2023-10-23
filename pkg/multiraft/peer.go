package multiraft

import (
	"sync"
)

type Peer struct {
	ID   uint64
	Addr string
}

func NewPeer(id uint64, addr string) Peer {
	return Peer{
		ID:   id,
		Addr: addr,
	}
}

type PeerClientManager struct {
	peerClients     map[uint64]*PeerClient
	peerClientsLock sync.RWMutex
}

func NewPeerClientManager() *PeerClientManager {
	return &PeerClientManager{
		peerClients: make(map[uint64]*PeerClient),
	}
}

func (p *PeerClientManager) GetPeerClient(id uint64) *PeerClient {
	p.peerClientsLock.Lock()
	defer p.peerClientsLock.Unlock()
	return p.peerClients[id]
}

func (p *PeerClientManager) AddPeerClient(id uint64, peerClient *PeerClient) {
	p.peerClientsLock.Lock()
	defer p.peerClientsLock.Unlock()
	p.peerClients[id] = peerClient
}

func (p *PeerClientManager) RemovePeerClient(id uint64) {
	p.peerClientsLock.Lock()
	defer p.peerClientsLock.Unlock()
	delete(p.peerClients, id)
}
