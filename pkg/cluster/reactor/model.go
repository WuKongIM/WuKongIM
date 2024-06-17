package reactor

import replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"

// 追加日志请求
type AppendLogReq struct {
	HandleKey string
	Logs      []replica.Log
}
