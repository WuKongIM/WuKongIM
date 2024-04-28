package cluster

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

var (
	ErrStopped                      = errors.New("cluster stopped")
	ErrStepChannelFull              = errors.New("step channel full")
	ErrProposeChannelFull           = errors.New("propose channel full")
	ErrRecvChannelFull              = errors.New("recv channel full")
	ErrSlotNotFound                 = errors.New("slot not found")
	ErrNodeNotFound                 = errors.New("node not found")
	ErrNotLeader                    = errors.New("not leader")
	ErrNotIsLeader                  = errors.New("not is leader")
	ErrSlotNotExist                 = errors.New("slot not exist")
	ErrSlotNotIsLeader              = errors.New("slot not is leader")
	ErrTermZero                     = errors.New("term is zero")
	ErrChannelNotFound              = errors.New("channel not found")
	ErrClusterConfigNotFound        = errors.New("clusterConfig not found")
	ErrOldChannelClusterConfig      = errors.New("old channel cluster config")
	ErrNodeAlreadyExists            = errors.New("node already exists")
	ErrProposeFailed                = errors.New("propose failed")
	ErrNotEnoughReplicas            = errors.New("not enough replicas")
	ErrEmptyChannelClusterConfig    = errors.New("empty channel cluster config")
	ErrNoLeader                     = errors.New("no leader")
	ErrChannelElectionCIsFull       = errors.New("channel election c is full")
	ErrNoAllowVoteNode              = errors.New("no allow vote node")
	ErrNodeNotExist                 = errors.New("node not exist")
	ErrSlotLeaderNotFound           = errors.New("slot leader not found")
	ErrEmptyRequest                 = errors.New("empty request")
	ErrChannelClusterConfigNotFound = errors.New("channel cluster config not found")
)

const (
	MsgUnknown          = iota
	MsgSlotMsg          // 槽消息
	MsgChannelMsg       // 频道消息
	MsgClusterConfigMsg // 集群配置消息
)

var globalRand = &lockedRand{}

type lockedRand struct {
	mu sync.Mutex
}

func (r *lockedRand) Intn(n int) int {
	r.mu.Lock()
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	r.mu.Unlock()
	return int(v.Int64())
}

// // 频道分布式配置
// type ChannelClusterConfig struct {
// 	ChannelID    string   // 频道ID
// 	ChannelType  uint8    // 频道类型
// 	ReplicaCount uint16   // 副本数量
// 	Replicas     []uint64 // 副本节点ID集合
// 	LeaderId     uint64   // 领导者ID
// 	Term         uint32   // 任期

// 	version uint16 // 数据协议版本
// }

// func (c *ChannelClusterConfig) Marshal() ([]byte, error) {
// 	c.version = 1
// 	enc := wkproto.NewEncoder()
// 	defer enc.End()
// 	enc.WriteUint16(c.version)
// 	enc.WriteString(c.ChannelID)
// 	enc.WriteUint8(c.ChannelType)
// 	enc.WriteUint16(c.ReplicaCount)
// 	enc.WriteUint16(uint16(len(c.Replicas)))
// 	if len(c.Replicas) > 0 {
// 		for _, replica := range c.Replicas {
// 			enc.WriteUint64(replica)
// 		}
// 	}
// 	enc.WriteUint64(c.LeaderId)
// 	enc.WriteUint32(c.Term)
// 	return enc.Bytes(), nil
// }

// func (c *ChannelClusterConfig) Unmarshal(data []byte) error {
// 	dec := wkproto.NewDecoder(data)
// 	var err error
// 	if c.version, err = dec.Uint16(); err != nil {
// 		return err
// 	}
// 	if c.ChannelID, err = dec.String(); err != nil {
// 		return err
// 	}
// 	if c.ChannelType, err = dec.Uint8(); err != nil {
// 		return err
// 	}
// 	if c.ReplicaCount, err = dec.Uint16(); err != nil {
// 		return err
// 	}
// 	var replicasLen uint16
// 	if replicasLen, err = dec.Uint16(); err != nil {
// 		return err
// 	}
// 	if replicasLen > 0 {
// 		c.Replicas = make([]uint64, replicasLen)
// 		for i := uint16(0); i < replicasLen; i++ {
// 			if c.Replicas[i], err = dec.Uint64(); err != nil {
// 				return err
// 			}
// 		}
// 	}
// 	if c.LeaderId, err = dec.Uint64(); err != nil {
// 		return err
// 	}
// 	if c.Term, err = dec.Uint32(); err != nil {
// 		return err
// 	}
// 	return nil
// }

// func (c *ChannelClusterConfig) String() string {
// 	return fmt.Sprintf("ChannelID: %s, ChannelType: %d, ReplicaCount: %d, Replicas: %v, LeaderId: %d, Term: %d",
// 		c.ChannelID, c.ChannelType, c.ReplicaCount, c.Replicas, c.LeaderId, c.Term)
// }

type ChannelLastLogInfoReq struct {
	ChannelId   string
	ChannelType uint8
}

func (c *ChannelLastLogInfoReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	return enc.Bytes(), nil
}

func (c *ChannelLastLogInfoReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	return nil
}

type ChannelLastLogInfoReqSet []*ChannelLastLogInfoReq

func (c ChannelLastLogInfoReqSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(uint16(len(c)))
	for _, req := range c {
		enc.WriteString(req.ChannelId)
		enc.WriteUint8(req.ChannelType)
	}
	return enc.Bytes(), nil
}

func (c *ChannelLastLogInfoReqSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var reqLen uint16
	if reqLen, err = dec.Uint16(); err != nil {
		return err
	}
	if reqLen > 0 {
		*c = make([]*ChannelLastLogInfoReq, reqLen)
		for i := uint16(0); i < reqLen; i++ {
			req := &ChannelLastLogInfoReq{}
			if req.ChannelId, err = dec.String(); err != nil {
				return err
			}
			if req.ChannelType, err = dec.Uint8(); err != nil {
				return err
			}
			(*c)[i] = req
		}
	}
	return nil

}

type ChannelLastLogInfoResponse struct {
	LogIndex    uint64 // 频道最新日志索引
	Term        uint32 // 频道最新日志任期
	ChannelId   string
	ChannelType uint8
}

type ChannelLastLogInfoResponseSet []*ChannelLastLogInfoResponse

func (c ChannelLastLogInfoResponseSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(uint16(len(c)))
	for _, resp := range c {
		enc.WriteString(resp.ChannelId)
		enc.WriteUint8(resp.ChannelType)
		enc.WriteUint64(resp.LogIndex)
		enc.WriteUint32(resp.Term)
	}
	return enc.Bytes(), nil
}

func (c *ChannelLastLogInfoResponseSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var respLen uint16
	if respLen, err = dec.Uint16(); err != nil {
		return err
	}
	if respLen > 0 {
		*c = make([]*ChannelLastLogInfoResponse, respLen)
		for i := uint16(0); i < respLen; i++ {
			resp := &ChannelLastLogInfoResponse{}
			if resp.ChannelId, err = dec.String(); err != nil {
				return err
			}
			if resp.ChannelType, err = dec.Uint8(); err != nil {
				return err
			}
			if resp.LogIndex, err = dec.Uint64(); err != nil {
				return err
			}
			if resp.Term, err = dec.Uint32(); err != nil {
				return err
			}
			(*c)[i] = resp
		}
	}
	return nil
}

type AppointLeaderReq struct {
	ChannelId   string
	ChannelType uint8
	LeaderId    uint64
	Term        uint32
}

func (c *AppointLeaderReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint64(c.LeaderId)
	enc.WriteUint32(c.Term)
	return enc.Bytes(), nil
}

func (c *AppointLeaderReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}

	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}

	if c.LeaderId, err = dec.Uint64(); err != nil {
		return err
	}
	if c.Term, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}

// 同步信息
type SyncInfo struct {
	NodeId           uint64 // 节点ID
	LastSyncLogIndex uint64 // 最后一次来同步日志的下标（一般最新日志 + 1）
	LastSyncTime     uint64 // 最后一次同步时间

	version uint16 // 数据版本
}

func (c *SyncInfo) Marshal() ([]byte, error) {
	c.version = 1
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(c.version)
	enc.WriteUint64(c.NodeId)
	enc.WriteUint64(c.LastSyncLogIndex)
	enc.WriteUint64(c.LastSyncTime)
	return enc.Bytes(), nil
}

func (c *SyncInfo) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.version, err = dec.Uint16(); err != nil {
		return err
	}
	if c.NodeId, err = dec.Uint64(); err != nil {
		return err
	}
	if c.LastSyncLogIndex, err = dec.Uint64(); err != nil {
		return err
	}
	if c.LastSyncTime, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type ChannelClusterConfigReq struct {
	ChannelId   string `json:"channel_id"`   // 频道id
	ChannelType uint8  `json:"channel_type"` // 频道类型
}

func (c *ChannelClusterConfigReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	return enc.Bytes(), nil
}

func (c *ChannelClusterConfigReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	return nil
}

type ChannelProposeReq struct {
	ChannelId   string        // 频道id
	ChannelType uint8         // 频道类型
	Logs        []replica.Log // 数据
	TraceID     trace.TraceID
	SpanID      trace.SpanID
}

func (c *ChannelProposeReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint16(uint16(len(c.Logs)))
	for _, lg := range c.Logs {
		logData, err := lg.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteBinary(logData)
	}
	enc.WriteBytes(c.TraceID[:])
	enc.WriteBytes(c.SpanID[:])
	return enc.Bytes(), nil
}

func (c *ChannelProposeReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	var dataLen uint16
	if dataLen, err = dec.Uint16(); err != nil {
		return err
	}
	if dataLen > 0 {
		c.Logs = make([]replica.Log, dataLen)
		for i := uint16(0); i < dataLen; i++ {
			data, err := dec.Binary()
			if err != nil {
				return err
			}
			log := &replica.Log{}
			if err = log.Unmarshal(data); err != nil {
				return err
			}
			c.Logs[i] = *log
		}
	}
	var traceIDBytes []byte
	if traceIDBytes, err = dec.Bytes(len(c.TraceID)); err != nil {
		return err
	}
	copy(c.TraceID[:], traceIDBytes)

	var spanIDBytes []byte
	if spanIDBytes, err = dec.Bytes(len(c.SpanID)); err != nil {
		return err
	}
	copy(c.SpanID[:], spanIDBytes)
	return nil
}

type ChannelProposeResp struct {
	ClusterConfigOld bool                    // 请求的节点的集群配置是否是旧的
	ProposeResults   []reactor.ProposeResult // 提案索引
}

func (c *ChannelProposeResp) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint8(uint8(wkutil.BoolToInt(c.ClusterConfigOld)))
	enc.WriteUint16(uint16(len(c.ProposeResults)))
	for _, proposeResult := range c.ProposeResults {
		enc.WriteUint64(proposeResult.Id)
		enc.WriteUint64(proposeResult.Index)
	}
	return enc.Bytes(), nil
}

func (c *ChannelProposeResp) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	var clusterConfigOld uint8
	if clusterConfigOld, err = dec.Uint8(); err != nil {
		return err
	}
	c.ClusterConfigOld = wkutil.IntToBool(int(clusterConfigOld))

	var itemLen uint16
	if itemLen, err = dec.Uint16(); err != nil {
		return err
	}
	if itemLen > 0 {
		c.ProposeResults = make([]reactor.ProposeResult, itemLen)
		for i := uint16(0); i < itemLen; i++ {
			item := reactor.ProposeResult{}
			if item.Id, err = dec.Uint64(); err != nil {
				return err
			}
			if item.Index, err = dec.Uint64(); err != nil {
				return err
			}
			c.ProposeResults[i] = item

		}
	}
	return nil
}

type SlotProposeReq struct {
	SlotId uint32
	Logs   []replica.Log // 数据
}

func (s *SlotProposeReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(s.SlotId)
	enc.WriteUint16(uint16(len(s.Logs)))
	for _, lg := range s.Logs {
		logData, err := lg.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteBinary(logData)
	}

	return enc.Bytes(), nil
}

func (s *SlotProposeReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.SlotId, err = dec.Uint32(); err != nil {
		return err
	}
	var dataLen uint16
	if dataLen, err = dec.Uint16(); err != nil {
		return err
	}
	if dataLen > 0 {
		s.Logs = make([]replica.Log, dataLen)
		for i := uint16(0); i < dataLen; i++ {
			data, err := dec.Binary()
			if err != nil {
				return err
			}
			log := &replica.Log{}
			if err = log.Unmarshal(data); err != nil {
				return err
			}
			s.Logs[i] = *log
		}
	}

	return nil
}

type SlotProposeResp struct {
	ClusterConfigOld bool                    // 请求的节点的集群配置是否是旧的
	ProposeResults   []reactor.ProposeResult // 提案索引
}

func (s *SlotProposeResp) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint8(uint8(wkutil.BoolToInt(s.ClusterConfigOld)))
	enc.WriteUint16(uint16(len(s.ProposeResults)))
	for _, proposeResult := range s.ProposeResults {
		enc.WriteUint64(proposeResult.Id)
		enc.WriteUint64(proposeResult.Index)
	}
	return enc.Bytes(), nil
}

func (s *SlotProposeResp) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	var clusterConfigOld uint8
	if clusterConfigOld, err = dec.Uint8(); err != nil {
		return err
	}
	s.ClusterConfigOld = wkutil.IntToBool(int(clusterConfigOld))

	var itemLen uint16
	if itemLen, err = dec.Uint16(); err != nil {
		return err
	}
	if itemLen > 0 {
		s.ProposeResults = make([]reactor.ProposeResult, itemLen)
		for i := uint16(0); i < itemLen; i++ {
			if s.ProposeResults[i].Id, err = dec.Uint64(); err != nil {
				return err
			}
			if s.ProposeResults[i].Index, err = dec.Uint64(); err != nil {
				return err
			}
		}
	}
	return nil
}

type SlotLogInfoReq struct {
	SlotIds []uint32
}

func (s *SlotLogInfoReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(uint16(len(s.SlotIds)))
	for _, slotId := range s.SlotIds {
		enc.WriteUint32(slotId)
	}
	return enc.Bytes(), nil
}

func (s *SlotLogInfoReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var slotIdsLen uint16
	if slotIdsLen, err = dec.Uint16(); err != nil {
		return err
	}
	if slotIdsLen > 0 {
		s.SlotIds = make([]uint32, slotIdsLen)
		for i := uint16(0); i < slotIdsLen; i++ {
			if s.SlotIds[i], err = dec.Uint32(); err != nil {
				return err
			}
		}
	}
	return nil
}

type SlotInfo struct {
	SlotId   uint32 // 槽Id
	LogIndex uint64 // 日志下标
	LogTerm  uint32 // 日期任期
}

type SlotLogInfoResp struct {
	NodeId uint64
	Slots  []SlotInfo
}

func (s *SlotLogInfoResp) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(s.NodeId)
	enc.WriteUint16(uint16(len(s.Slots)))
	for _, slot := range s.Slots {
		enc.WriteUint32(slot.SlotId)
		enc.WriteUint64(slot.LogIndex)
		enc.WriteUint32(slot.LogTerm)
	}
	return enc.Bytes(), nil
}

func (s *SlotLogInfoResp) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.NodeId, err = dec.Uint64(); err != nil {
		return err
	}
	var slotsLen uint16
	if slotsLen, err = dec.Uint16(); err != nil {
		return err
	}
	if slotsLen > 0 {
		s.Slots = make([]SlotInfo, slotsLen)
		for i := uint16(0); i < slotsLen; i++ {
			if s.Slots[i].SlotId, err = dec.Uint32(); err != nil {
				return err
			}
			if s.Slots[i].LogIndex, err = dec.Uint64(); err != nil {
				return err
			}
			if s.Slots[i].LogTerm, err = dec.Uint32(); err != nil {
				return err
			}
		}
	}
	return nil
}

type ClusterJoinReq struct {
	NodeId     uint64
	ServerAddr string
	Role       pb.NodeRole
}

func (c *ClusterJoinReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(c.NodeId)
	enc.WriteString(c.ServerAddr)
	enc.WriteUint32(uint32(c.Role))
	return enc.Bytes(), nil

}

func (c *ClusterJoinReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.NodeId, err = dec.Uint64(); err != nil {
		return err
	}
	if c.ServerAddr, err = dec.String(); err != nil {
		return err
	}
	var role uint32
	if role, err = dec.Uint32(); err != nil {
		return err
	}
	c.Role = pb.NodeRole(role)
	return nil
}

type ClusterJoinResp struct {
	Nodes []*NodeInfo
}

func (c *ClusterJoinResp) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(uint16(len(c.Nodes)))
	for _, node := range c.Nodes {
		enc.WriteUint64(node.NodeId)
		enc.WriteString(node.ServerAddr)
	}
	return enc.Bytes(), nil
}

func (c *ClusterJoinResp) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var nodesLen uint16
	if nodesLen, err = dec.Uint16(); err != nil {
		return err
	}
	if nodesLen > 0 {
		c.Nodes = make([]*NodeInfo, nodesLen)
		for i := uint16(0); i < nodesLen; i++ {
			node := &NodeInfo{}
			if node.NodeId, err = dec.Uint64(); err != nil {
				return err
			}
			if node.ServerAddr, err = dec.String(); err != nil {
				return err
			}
			c.Nodes[i] = node
		}
	}
	return nil
}

type SlotMigrateFinishReq struct {
	SlotId uint32
	From   uint64
	To     uint64
}

func (s *SlotMigrateFinishReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(s.SlotId)
	enc.WriteUint64(s.From)
	enc.WriteUint64(s.To)
	return enc.Bytes(), nil
}

func (s *SlotMigrateFinishReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.SlotId, err = dec.Uint32(); err != nil {
		return err
	}
	if s.From, err = dec.Uint64(); err != nil {
		return err
	}
	if s.To, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type UpdateApiServerAddrReq struct {
	NodeId        uint64
	ApiServerAddr string
}

func (u *UpdateApiServerAddrReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(u.NodeId)
	enc.WriteString(u.ApiServerAddr)
	return enc.Bytes(), nil
}

func (u *UpdateApiServerAddrReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if u.NodeId, err = dec.Uint64(); err != nil {
		return err
	}
	if u.ApiServerAddr, err = dec.String(); err != nil {
		return err
	}
	return nil
}

type ChangeSlotRoleReq struct {
	Role    replica.Role
	SlotIds []uint32
}

func (c *ChangeSlotRoleReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint8(uint8(c.Role))
	enc.WriteUint16(uint16(len(c.SlotIds)))
	for _, slotId := range c.SlotIds {
		enc.WriteUint32(slotId)
	}
	return enc.Bytes(), nil
}

func (c *ChangeSlotRoleReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var role uint8
	if role, err = dec.Uint8(); err != nil {
		return err
	}
	c.Role = replica.Role(role)
	var slotIdsLen uint16
	if slotIdsLen, err = dec.Uint16(); err != nil {
		return err
	}
	if slotIdsLen > 0 {
		c.SlotIds = make([]uint32, slotIdsLen)
		for i := uint16(0); i < slotIdsLen; i++ {
			if c.SlotIds[i], err = dec.Uint32(); err != nil {
				return err
			}
		}
	}
	return nil

}

type NodeInfo struct {
	NodeId     uint64
	ServerAddr string
}

type ChannelClusterStorage interface {
	// 保存分布式配置
	Save(clusterCfg wkdb.ChannelClusterConfig) error
	// 删除频道分布式配置
	Delete(channelId string, channelType uint8) error
	// 获取分布式配置
	Get(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error)
	// // 获取某个槽位的频道分布式数量
	GetCountWithSlotId(slotId uint32) (int, error)
	// // 获取某个槽位的频道分布式配置
	GetWithSlotId(slotId uint32) ([]wkdb.ChannelClusterConfig, error)

	GetAll(offsetId uint64, limit int) ([]wkdb.ChannelClusterConfig, error)
	// 提案配置
	Propose(ctx context.Context, cfg wkdb.ChannelClusterConfig) error
	// // 获取所有槽位的频道分布式配置
	// GetWithAllSlot() ([]*wkstore.ChannelClusterConfig, error)
}

type syncStatus int

const (
	syncStatusNone    syncStatus = iota // 无状态
	syncStatusSyncing                   // 同步中
	syncStatusSynced                    // 已同步
)
