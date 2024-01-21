package replica

import (
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// SyncNotify 通知同步，由主节点发起
type SyncNotify struct {
	ShardNo        string
	LeaderID       uint64
	CommittedIndex uint64 // 领导已提交日志下标
}

func (s *SyncNotify) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(s.ShardNo)
	enc.WriteUint64(s.LeaderID)
	enc.WriteUint64(s.CommittedIndex)
	return enc.Bytes(), nil
}

func (s *SyncNotify) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.ShardNo, err = dec.String(); err != nil {
		return err
	}
	if s.LeaderID, err = dec.Uint64(); err != nil {
		return err
	}
	if s.CommittedIndex, err = dec.Uint64(); err != nil {
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

// 同步信息
type SyncInfo struct {
	NodeID           uint64 // 节点ID
	LastSyncLogIndex uint64 // 最后一次来同步日志的下标（一般最新日志 + 1）
	LastSyncTime     uint64 // 最后一次同步时间

	version uint16 // 数据版本
}

func (r *SyncInfo) Marshal() ([]byte, error) {
	r.version = 1
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(r.version)
	enc.WriteUint64(r.NodeID)
	enc.WriteUint64(r.LastSyncLogIndex)
	enc.WriteUint64(r.LastSyncTime)
	return enc.Bytes(), nil
}

func (r *SyncInfo) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if r.version, err = dec.Uint16(); err != nil {
		return err
	}
	if r.NodeID, err = dec.Uint64(); err != nil {
		return err
	}
	if r.LastSyncLogIndex, err = dec.Uint64(); err != nil {
		return err
	}
	if r.LastSyncTime, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}
