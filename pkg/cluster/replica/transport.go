package replica

import (
	"sync"
	"time"
)

type ITransport interface {
	// 发送通知
	SendSyncNotify(toNodeID uint64, r *SyncNotify) error
	// 同步日志
	SyncLog(fromNodeID uint64, r *SyncReq) (*SyncRsp, error)
}

type proxyTransport struct {
	trans ITransport

	lastSyncLogLock     sync.RWMutex
	lastSyncLogIndexMap map[uint64]uint64    // 副本最后一次来同步日志的下标
	lastSyncLogTimeMap  map[uint64]time.Time // 副本最后一次来同步日志的时间
}

func newProxyTransport(trans ITransport) ITransport {

	return &proxyTransport{
		trans:               trans,
		lastSyncLogIndexMap: map[uint64]uint64{},
		lastSyncLogTimeMap:  make(map[uint64]time.Time),
	}
}

// 发送通知
func (p *proxyTransport) SendSyncNotify(toNodeID uint64, r *SyncNotify) error {
	return p.trans.SendSyncNotify(toNodeID, r)
}

// 同步日志
func (p *proxyTransport) SyncLog(fromNodeID uint64, r *SyncReq) (*SyncRsp, error) {

	resp, err := p.trans.SyncLog(fromNodeID, r)
	if err != nil {
		return nil, err
	}
	p.lastSyncLogLock.Lock()
	p.lastSyncLogIndexMap[fromNodeID] = r.StartLogIndex
	p.lastSyncLogTimeMap[fromNodeID] = time.Now()
	p.lastSyncLogLock.Unlock()

	return resp, nil
}
