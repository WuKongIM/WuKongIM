package reactor

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type IRequest interface {
	// GetConfig 获取配置
	GetConfig(req ConfigReq) (ConfigResp, error)

	// GetLeaderTermStartIndex 从领导者获取指定任期开始的第一条日志索引
	GetLeaderTermStartIndex(req LeaderTermStartIndexReq) (uint64, error)

	// AppendLogBatch 批量追加日志
	// AppendLogBatch(reqs []AppendLogReq) error
	Append(req AppendLogReq) error

	// // GetLeaderTermStartIndex 获取领导任期开始的第一条日志索引
	// GetLeaderTermStartIndex(handleKey []string) ([]LeaderTermStartIndexResp, error)

	// // GetConfigFromLeader 从领导者获取配置
	// GetConfigFromLeader(handleKey string) (ConfigResp, error)
}

type ConfigReq struct {
	HandlerKey string
}

var EmptyConfigResp = ConfigResp{}

func IsEmptyConfigResp(resp ConfigResp) bool {
	return resp.HandlerKey == ""
}

type ConfigResp struct {
	HandlerKey string
	Config     replica.Config
}

type LeaderTermStartIndexReq struct {
	HandlerKey string
	LeaderId   uint64
	Term       uint32
}

func (l LeaderTermStartIndexReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteString(l.HandlerKey)
	enc.WriteUint64(l.LeaderId)
	enc.WriteUint32(l.Term)

	return enc.Bytes(), nil
}

func (l *LeaderTermStartIndexReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if l.HandlerKey, err = dec.String(); err != nil {
		return err

	}
	if l.LeaderId, err = dec.Uint64(); err != nil {
		return err
	}
	if l.Term, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}
