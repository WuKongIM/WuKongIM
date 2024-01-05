package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type ServerRole uint8

const (
	// 未知状态
	ServerRoleUnknown ServerRole = iota
	// 表示实例还没有加入集群,(也就是还没有分配slot)
	ServerRoleMySelf
	// 从节点 表示实例已经加入集群,作为从节点运行
	ServerRoleFollow
	// 主节点 表示实例已经加入集群,作为主节点运行
	ServerRoleLeader
	// 学习者 (只负责复制数据，不参与选举)
	ServerRoleLearner
)

type MessageType uint32

const (
	MessageTypeUnknown                MessageType = iota
	MessageTypePing                               // master节点发送ping
	MessageTypeVoteRequest                        // 投票请求
	MessageTypeVoteResponse                       // 投票返回
	MessageTypePong                               // pong
	MessageTypeSlotInfoReportRequest              // slot信息上报请求
	MessageTypeSlotInfoReportResponse             // slot信息上报返回
	MessageTypeSlotLogSyncNotify                  // slot日志同步通知
	MessageTypeChannelLogSyncNotify               // channel日志同步通知
)

func (m MessageType) Uint32() uint32 {

	return uint32(m)
}

type PingRequest struct {
	Epoch                uint32 // 选举周期
	ClusterConfigVersion uint32 // 集群配置版本
}

func (p *PingRequest) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(p.Epoch)
	enc.WriteUint32(p.ClusterConfigVersion)
	return enc.Bytes(), nil
}

func (p *PingRequest) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if p.Epoch, err = dec.Uint32(); err != nil {
		return err
	}
	if p.ClusterConfigVersion, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}

type PongResponse struct {
	ClusterConfigVersion uint32 // 集群配置版本
}

func (p *PongResponse) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(p.ClusterConfigVersion)
	return enc.Bytes(), nil
}

func (p *PongResponse) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if p.ClusterConfigVersion, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}

// VoteRequest 投票请求
type VoteRequest struct {
	Epoch                uint32 // 选举周期
	ClusterConfigVersion uint32 // 配置版本
}

func (v *VoteRequest) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(v.Epoch)
	enc.WriteUint32(v.ClusterConfigVersion)
	return enc.Bytes(), nil
}

func (f *VoteRequest) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if f.Epoch, err = dec.Uint32(); err != nil {
		return err
	}
	if f.ClusterConfigVersion, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}

// VoteRespose 投票请求
type VoteResponse struct {
	Epoch  uint32 // 选举周期
	Reject bool   // 是否拒绝 如果是拒绝，则表示参选者的数据不是最新，需要放弃参选
}

func (v *VoteResponse) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(v.Epoch)
	enc.WriteUint8(uint8(wkutil.BoolToInt(v.Reject)))
	return enc.Bytes(), nil
}

func (f *VoteResponse) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if f.Epoch, err = dec.Uint32(); err != nil {
		return err
	}
	var rejectI uint8
	if rejectI, err = dec.Uint8(); err != nil {
		return err
	}
	f.Reject = rejectI == 1
	return nil
}

type SlotAppendLogRequest struct {
	ReqID    uint64 // 请求ID （无需编码）
	SlotID   uint32 // slotID
	LogIndex uint64 // 日志序号
	Data     []byte // 日志数据
}

func (s *SlotAppendLogRequest) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(s.SlotID)
	enc.WriteUint64(s.LogIndex)
	enc.WriteBinary(s.Data)
	return enc.Bytes(), nil
}

func (s *SlotAppendLogRequest) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.SlotID, err = dec.Uint32(); err != nil {
		return err
	}
	if s.LogIndex, err = dec.Uint64(); err != nil {
		return err
	}
	if s.Data, err = dec.Binary(); err != nil {
		return err
	}
	return nil
}

type SlotAppendLogResponse struct {
	ReqID    uint64 // 请求ID，（无需编码）
	SlotID   uint32 // slotID
	LogIndex uint64 // 日志序号
}

func (s *SlotAppendLogResponse) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(s.SlotID)
	enc.WriteUint64(s.LogIndex)
	return enc.Bytes(), nil
}

func (s *SlotAppendLogResponse) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.SlotID, err = dec.Uint32(); err != nil {
		return err
	}
	if s.LogIndex, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type SlotInfoReportRequest struct {
	SlotIDs []uint32 // 需要上报的槽id集合
}

func (s *SlotInfoReportRequest) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(uint16(len(s.SlotIDs)))
	if len(s.SlotIDs) > 0 {
		for _, slotID := range s.SlotIDs {
			enc.WriteUint32(slotID)
		}
	}
	return enc.Bytes(), nil
}

func (s *SlotInfoReportRequest) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var slotIDCount uint16
	var err error
	if slotIDCount, err = dec.Uint16(); err != nil {
		return err
	}
	if slotIDCount == 0 {
		return nil
	}
	for i := 0; i < int(slotIDCount); i++ {
		if slotID, err := dec.Uint32(); err != nil {
			s.SlotIDs = append(s.SlotIDs, slotID)
		}
	}
	return nil
}

type SlotInfoReportResponse struct {
	NodeID    uint64
	SlotInfos []*SlotInfo
}

func (s *SlotInfoReportResponse) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(s.NodeID)
	enc.WriteUint16(uint16(len(s.SlotInfos)))
	if len(s.SlotInfos) > 0 {
		for _, slotInfo := range s.SlotInfos {
			enc.WriteUint32(slotInfo.SlotID)
			enc.WriteUint64(slotInfo.LogIndex)
		}
	}
	return enc.Bytes(), nil
}

func (s *SlotInfoReportResponse) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var count uint16
	var err error
	if s.NodeID, err = dec.Uint64(); err != nil {
		return err
	}
	if count, err = dec.Uint16(); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	for i := 0; i < int(count); i++ {
		slotInfo := &SlotInfo{}
		var slotID uint32
		var logIndex uint64
		if slotID, err = dec.Uint32(); err != nil {
			return err
		}
		if logIndex, err = dec.Uint64(); err != nil {
			return err
		}
		slotInfo.SlotID = slotID
		slotInfo.LogIndex = logIndex
		s.SlotInfos = append(s.SlotInfos, slotInfo)
	}
	return nil
}

type SlotInfo struct {
	SlotID   uint32
	LogIndex uint64
}

type ProposeRequest struct {
	SlotID uint32 // slotID
	Data   []byte
}

func (a *ProposeRequest) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(a.SlotID)
	enc.WriteBytes(a.Data)
	return enc.Bytes(), nil
}

func (a *ProposeRequest) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if a.SlotID, err = dec.Uint32(); err != nil {
		return err
	}
	if a.Data, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}

type ChannelInfo struct {
	ChannelID   string   // 频道id
	ChannelType uint8    // 频道类型
	LeaderID    uint64   // 领导节点ID
	Replicas    []uint64 // 副本节点ID

	version uint16 // 数据协议版本

}

func (c *ChannelInfo) Marshal() ([]byte, error) {
	c.version = 1
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(c.version)
	enc.WriteString(c.ChannelID)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint64(c.LeaderID)
	enc.WriteUint16(uint16(len(c.Replicas)))
	if len(c.Replicas) > 0 {
		for _, replica := range c.Replicas {
			enc.WriteUint64(replica)
		}
	}
	return enc.Bytes(), nil
}

func (c *ChannelInfo) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.version, err = dec.Uint16(); err != nil {
		return err
	}
	if c.ChannelID, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	if c.LeaderID, err = dec.Uint64(); err != nil {
		return err
	}
	var count uint16
	if count, err = dec.Uint16(); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	for i := 0; i < int(count); i++ {
		var replica uint64
		if replica, err = dec.Uint64(); err != nil {
			return err
		}
		c.Replicas = append(c.Replicas, replica)
	}
	return nil
}

type CmdType uint16

const (
	CmdTypeUnknown CmdType = iota
	CmdTypeSetChannelInfo
)

func (c CmdType) Uint16() uint16 {
	return uint16(c)
}

type CMD struct {
	CmdType CmdType
	Data    []byte
	version uint16 // 数据协议版本

}

func (c *CMD) Marshal() ([]byte, error) {
	c.version = 1
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(c.version)
	enc.WriteUint16(c.CmdType.Uint16())
	enc.WriteBytes(c.Data)
	return enc.Bytes(), nil

}

func (c *CMD) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.version, err = dec.Uint16(); err != nil {
		return err
	}
	var cmdType uint16
	if cmdType, err = dec.Uint16(); err != nil {
		return err
	}
	c.CmdType = CmdType(cmdType)
	if c.Data, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}

type ReplicaInfo struct {
	NodeID       uint64 // 节点ID
	LastLogIndex uint64 // 最后一条日志的索引
	LastSyncTime uint64 // 最后一次同步时间

	version uint16 // 数据版本
}

func (r *ReplicaInfo) Marshal() ([]byte, error) {
	r.version = 1
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(r.version)
	enc.WriteUint64(r.NodeID)
	enc.WriteUint64(r.LastLogIndex)
	enc.WriteUint64(r.LastSyncTime)
	return enc.Bytes(), nil
}

func (r *ReplicaInfo) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if r.version, err = dec.Uint16(); err != nil {
		return err
	}
	if r.NodeID, err = dec.Uint64(); err != nil {
		return err
	}
	if r.LastLogIndex, err = dec.Uint64(); err != nil {
		return err
	}
	if r.LastSyncTime, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}
