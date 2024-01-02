package replica

import (
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// SyncNotify 通知同步，由主节点发起
type SyncNotify struct {
	ShardNo  string
	LogIndex uint64 // 通知同步的日志下标
}

func (s *SyncNotify) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(s.ShardNo)
	enc.WriteUint64(s.LogIndex)
	return enc.Bytes(), nil
}

func (s *SyncNotify) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.ShardNo, err = dec.String(); err != nil {
		return err
	}
	if s.LogIndex, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

// SyncReq 同步请求，由从节点发起
type SyncReq struct {
	ShardNo       string
	StartLogIndex uint64 // 开始日志下标（结果包含此下标数据）
	Limit         uint32 // 限制数量，每次最大同步数量
}

func (s *SyncReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(s.ShardNo)
	enc.WriteUint64(s.StartLogIndex)
	enc.WriteUint32(s.Limit)
	return enc.Bytes(), nil
}

func (s *SyncReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.ShardNo, err = dec.String(); err != nil {
		return err
	}
	if s.StartLogIndex, err = dec.Uint64(); err != nil {
		return err
	}
	if s.Limit, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}

// SyncRsp 同步响应，由主节点响应
type SyncRsp struct {
	Logs []Log // 日志列表
}

func (s *SyncRsp) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	for _, lg := range s.Logs {
		logData, err := lg.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteBinary(logData)
	}

	return enc.Bytes(), nil
}

func (s *SyncRsp) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)

	for dec.Len() > 0 {
		data, err := dec.Binary()
		if err != nil {
			return err
		}
		lg := &Log{}
		err = lg.Unmarshal(data)
		if err != nil {
			return err
		}
		s.Logs = append(s.Logs, *lg)
	}
	return nil
}
