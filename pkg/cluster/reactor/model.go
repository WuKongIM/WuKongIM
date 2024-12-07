package reactor

import "github.com/WuKongIM/WuKongIM/pkg/cluster/replica"

// 追加日志请求
type AppendLogReq struct {
	HandleKey string
	Logs      []replica.Log
	WaitC     chan error

	sub     *ReactorSub
	handler *handler
}
