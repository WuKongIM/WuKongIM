package reactor

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type proposeReq struct {
	logs    []replica.Log
	handler *handler
	waitKey string
}

func newProposeReq(handler *handler, waitKey string, logs []replica.Log) proposeReq {
	return proposeReq{
		logs:    logs,
		handler: handler,
		waitKey: waitKey,
	}
}

type ProposeResult struct {
	Id        uint64
	Index     uint64
	committed bool
}

func (p ProposeResult) LogId() uint64 {
	return p.Id
}

func (p ProposeResult) LogIndex() uint64 {
	return p.Index
}
