package replica

type ITransport interface {
	// 发送通知,(需要快速处理，不要阻塞，这个方法会重试调用)
	SendSyncNotify(toNodeIDs []uint64, r *SyncNotify)
	// 同步日志
	SyncLog(leaderID uint64, r *SyncReq) (*SyncRsp, error)
}

type proxyTransport struct {
	trans ITransport
}

func newProxyTransport(trans ITransport) ITransport {

	return &proxyTransport{
		trans: trans,
	}
}

// 发送通知
func (p *proxyTransport) SendSyncNotify(toNodeIDs []uint64, r *SyncNotify) {
	p.trans.SendSyncNotify(toNodeIDs, r)
}

// 同步日志
func (p *proxyTransport) SyncLog(leaderID uint64, r *SyncReq) (*SyncRsp, error) {

	resp, err := p.trans.SyncLog(leaderID, r)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
