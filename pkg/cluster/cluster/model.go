package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/store"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	rafttype "github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

var (
	ErrStopped                            = errors.New("cluster stopped")
	ErrStepChannelFull                    = errors.New("step channel full")
	ErrProposeChannelFull                 = errors.New("propose channel full")
	ErrRecvChannelFull                    = errors.New("recv channel full")
	ErrSlotNotFound                       = errors.New("slot not found")
	ErrNodeNotFound                       = errors.New("node not found")
	ErrNotLeader                          = errors.New("not leader")
	ErrNotIsLeader                        = errors.New("not is leader")
	ErrSlotNotExist                       = errors.New("slot not exist")
	ErrSlotNotIsLeader                    = errors.New("slot not is leader")
	ErrTermZero                           = errors.New("term is zero")
	ErrChannelNotFound                    = errors.New("channel not found")
	ErrOldChannelClusterConfig            = errors.New("old channel cluster config")
	ErrNodeAlreadyExists                  = errors.New("node already exists")
	ErrProposeFailed                      = errors.New("propose failed")
	ErrNotEnoughReplicas                  = errors.New("not enough replicas")
	ErrEmptyChannelClusterConfig          = errors.New("empty channel cluster config")
	ErrNoLeader                           = errors.New("no leader")
	ErrChannelElectionCIsFull             = errors.New("channel election c is full")
	ErrNoAllowVoteNode                    = errors.New("no allow vote node")
	ErrNodeNotExist                       = errors.New("node not exist")
	ErrSlotLeaderNotFound                 = errors.New("slot leader not found")
	ErrEmptyRequest                       = errors.New("empty request")
	ErrChannelClusterConfigNotFound       = errors.New("channel cluster config not found")
	ErrCircuitBreakerNotReady       error = fmt.Errorf("circuit breaker not ready")
	ErrRateLimited                        = fmt.Errorf("rate limited")
	ErrChanIsFull                         = fmt.Errorf("channel is full")
)

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
	LogIndex uint64 // 频道最新日志索引
	LogTerm  uint32 // 频道最新日志任期
	Term     uint32 // 频道领导最新任期
}

func (c *ChannelLastLogInfoResponse) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(c.LogIndex)
	enc.WriteUint32(c.LogTerm)
	enc.WriteUint32(c.Term)
	return enc.Bytes(), nil
}

func (c *ChannelLastLogInfoResponse) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.LogIndex, err = dec.Uint64(); err != nil {
		return err
	}
	if c.LogTerm, err = dec.Uint32(); err != nil {
		return err
	}
	if c.Term, err = dec.Uint32(); err != nil {
		return err
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
	From        uint64 `json:"from"`         // 请求节点
}

func (c *ChannelClusterConfigReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint64(c.From)
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
	if c.From, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type ChannelProposeReq struct {
	ChannelId   string         // 频道id
	ChannelType uint8          // 频道类型
	Logs        []rafttype.Log // 数据
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
		c.Logs = make([]rafttype.Log, dataLen)
		for i := uint16(0); i < dataLen; i++ {
			data, err := dec.Binary()
			if err != nil {
				return err
			}
			log := &rafttype.Log{}
			if err = log.Unmarshal(data); err != nil {
				return err
			}
			c.Logs[i] = *log
		}
	}
	return nil
}

// type ChannelProposeResp struct {
// 	ClusterConfigOld bool                    // 请求的节点的集群配置是否是旧的
// 	ProposeResults   []reactor.ProposeResult // 提案索引
// }

// func (c *ChannelProposeResp) Marshal() ([]byte, error) {
// 	enc := wkproto.NewEncoder()
// 	defer enc.End()
// 	enc.WriteUint8(uint8(wkutil.BoolToInt(c.ClusterConfigOld)))
// 	enc.WriteUint16(uint16(len(c.ProposeResults)))
// 	for _, proposeResult := range c.ProposeResults {
// 		enc.WriteUint64(proposeResult.Id)
// 		enc.WriteUint64(proposeResult.Index)
// 	}
// 	return enc.Bytes(), nil
// }

// func (c *ChannelProposeResp) Unmarshal(data []byte) error {
// 	dec := wkproto.NewDecoder(data)
// 	var err error

// 	var clusterConfigOld uint8
// 	if clusterConfigOld, err = dec.Uint8(); err != nil {
// 		return err
// 	}
// 	c.ClusterConfigOld = wkutil.IntToBool(int(clusterConfigOld))

// 	var itemLen uint16
// 	if itemLen, err = dec.Uint16(); err != nil {
// 		return err
// 	}
// 	if itemLen > 0 {
// 		c.ProposeResults = make([]reactor.ProposeResult, itemLen)
// 		for i := uint16(0); i < itemLen; i++ {
// 			item := reactor.ProposeResult{}
// 			if item.Id, err = dec.Uint64(); err != nil {
// 				return err
// 			}
// 			if item.Index, err = dec.Uint64(); err != nil {
// 				return err
// 			}
// 			c.ProposeResults[i] = item

// 		}
// 	}
// 	return nil
// }

type SlotProposeReq struct {
	SlotId uint32
	Logs   []rafttype.Log // 数据
}

func (s *SlotProposeReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(s.SlotId)
	enc.WriteUint32(uint32(len(s.Logs)))
	for _, lg := range s.Logs {
		logData, err := lg.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteUint32(uint32(len(logData)))
		enc.WriteBytes(logData)
	}

	return enc.Bytes(), nil
}

func (s *SlotProposeReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.SlotId, err = dec.Uint32(); err != nil {
		return err
	}
	var dataLen uint32
	if dataLen, err = dec.Uint32(); err != nil {
		return err
	}
	if dataLen > 0 {
		s.Logs = make([]rafttype.Log, dataLen)
		for i := uint32(0); i < dataLen; i++ {
			logDataLen, err := dec.Uint32()
			if err != nil {
				return err
			}
			data, err := dec.Bytes(int(logDataLen))
			if err != nil {
				return err
			}

			log := &rafttype.Log{}
			if err = log.Unmarshal(data); err != nil {
				return err
			}
			s.Logs[i] = *log
		}
	}

	return nil
}

// type SlotProposeResp struct {
// 	ClusterConfigOld bool                    // 请求的节点的集群配置是否是旧的
// 	ProposeResults   []reactor.ProposeResult // 提案索引
// }

// func (s *SlotProposeResp) Marshal() ([]byte, error) {
// 	enc := wkproto.NewEncoder()
// 	defer enc.End()
// 	enc.WriteUint8(uint8(wkutil.BoolToInt(s.ClusterConfigOld)))
// 	enc.WriteUint16(uint16(len(s.ProposeResults)))
// 	for _, proposeResult := range s.ProposeResults {
// 		enc.WriteUint64(proposeResult.Id)
// 		enc.WriteUint64(proposeResult.Index)
// 	}
// 	return enc.Bytes(), nil
// }

// func (s *SlotProposeResp) Unmarshal(data []byte) error {
// 	dec := wkproto.NewDecoder(data)
// 	var err error

// 	var clusterConfigOld uint8
// 	if clusterConfigOld, err = dec.Uint8(); err != nil {
// 		return err
// 	}
// 	s.ClusterConfigOld = wkutil.IntToBool(int(clusterConfigOld))

// 	var itemLen uint16
// 	if itemLen, err = dec.Uint16(); err != nil {
// 		return err
// 	}
// 	if itemLen > 0 {
// 		s.ProposeResults = make([]reactor.ProposeResult, itemLen)
// 		for i := uint16(0); i < itemLen; i++ {
// 			if s.ProposeResults[i].Id, err = dec.Uint64(); err != nil {
// 				return err
// 			}
// 			if s.ProposeResults[i].Index, err = dec.Uint64(); err != nil {
// 				return err
// 			}
// 		}
// 	}
// 	return nil
// }

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
	LogIndex uint64 // 最后一条日志下标
	LogTerm  uint32 // 最后一条日志任期
	Term     uint32 // 领导任期
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
		enc.WriteUint32(slot.Term)
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
			if s.Slots[i].Term, err = dec.Uint32(); err != nil {
				return err
			}
		}
	}
	return nil
}

type ClusterJoinReq struct {
	NodeId     uint64
	ServerAddr string
	Role       types.NodeRole
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
	c.Role = types.NodeRole(role)
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
	Role    rafttype.Role
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
	c.Role = rafttype.Role(role)
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
	// Delete(channelId string, channelType uint8) error
	// 获取分布式配置
	Get(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error)

	// 获取频道分布式配置版本
	GetVersion(channelId string, channelType uint8) (uint64, error)
	// // 获取某个槽位的频道分布式数量
	GetCountWithSlotId(slotId uint32) (int, error)
	// // 获取某个槽位的频道分布式配置
	GetWithSlotId(slotId uint32) ([]wkdb.ChannelClusterConfig, error)

	GetAll(offsetId uint64, limit int) ([]wkdb.ChannelClusterConfig, error)
	// 提案配置
	Propose(cfg wkdb.ChannelClusterConfig) error
	// // 获取所有槽位的频道分布式配置
	// GetWithAllSlot() ([]*wkstore.ChannelClusterConfig, error)
}

type ChannelClusterConfigRespTotal struct {
	Total   int                         `json:"total"`   // 总数
	More    int                         `json:"more"`    // 是否还有更多
	Running int                         `json:"running"` // 运行中数量
	Data    []*ChannelClusterConfigResp `json:"data"`
}

type ChannelClusterConfigResp struct {
	ChannelId         string                    `json:"channel_id"`          // 频道ID
	ChannelType       uint8                     `json:"channel_type"`        // 频道类型
	ChannelTypeFormat string                    `json:"channel_type_format"` // 频道类型格式化
	ReplicaCount      uint16                    `json:"replica_count"`       // 副本数量
	Replicas          []uint64                  `json:"replicas"`            // 副本节点ID集合
	LeaderId          uint64                    `json:"leader_id"`           // 领导者ID
	Term              uint32                    `json:"term"`                // 任期
	SlotId            uint32                    `json:"slot_id"`             // 槽位ID
	SlotLeaderId      uint64                    `json:"slot_leader_id"`      // 槽位领导者ID
	LastMessageSeq    uint64                    `json:"last_message_seq"`    // 最大消息序号
	LastAppendTime    string                    `json:"last_append_time"`    // 最后一次追加时间
	Active            int                       `json:"active"`              // 是否激活
	ActiveFormat      string                    `json:"active_format"`       // 状态格式化
	Status            wkdb.ChannelClusterStatus `json:"status"`              // 状态
	StatusFormat      string                    `json:"status_format"`       // 状态格式化
	MigrateFrom       uint64                    `json:"migrate_from"`        // 迁移来源
	MigrateTo         uint64                    `json:"migrate_to"`          // 迁移目标
	CreatedAt         int64                     `json:"created_at"`          // 创建时间
	UpdatedAt         int64                     `json:"updated_at"`          // 更新时间
	CreatedAtFormat   string                    `json:"created_at_format"`   // 创建时间格式化
	UpdatedAtFormat   string                    `json:"updated_at_format"`   // 更新时间格式化
}

func NewChannelClusterConfigRespFromClusterConfig(slotLeaderId uint64, slotId uint32, cfg wkdb.ChannelClusterConfig) *ChannelClusterConfigResp {

	channelTypeFormat := formatChannelType(cfg.ChannelType)

	statusFormat := ""
	if cfg.Status == wkdb.ChannelClusterStatusNormal {
		statusFormat = "正常"
	} else if cfg.Status == wkdb.ChannelClusterStatusCandidate {
		statusFormat = "选举中"
	} else {
		statusFormat = fmt.Sprintf("未知(%d)", cfg.Status)
	}

	createdAt := int64(0)
	createdAtFormat := ""
	updatedAtFormat := ""

	if cfg.CreatedAt != nil {
		createdAt = cfg.CreatedAt.UnixNano()
		createdAtFormat = wkutil.ToyyyyMMddHHmmss(*cfg.CreatedAt)
	}

	updatedAt := int64(0)
	if cfg.UpdatedAt != nil {
		updatedAt = cfg.UpdatedAt.UnixNano()
		updatedAtFormat = wkutil.ToyyyyMMddHHmmss(*cfg.UpdatedAt)
	}

	return &ChannelClusterConfigResp{
		ChannelId:         cfg.ChannelId,
		ChannelType:       cfg.ChannelType,
		ChannelTypeFormat: channelTypeFormat,
		ReplicaCount:      cfg.ReplicaMaxCount,
		Replicas:          cfg.Replicas,
		LeaderId:          cfg.LeaderId,
		Term:              cfg.Term,
		SlotId:            slotId,
		SlotLeaderId:      slotLeaderId,
		Status:            cfg.Status,
		StatusFormat:      statusFormat,
		MigrateTo:         cfg.MigrateTo,
		MigrateFrom:       cfg.MigrateFrom,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		CreatedAtFormat:   createdAtFormat,
		UpdatedAtFormat:   updatedAtFormat,
	}
}

func formatChannelType(channelType uint8) string {
	channelTypeFormat := ""
	switch channelType {
	case wkproto.ChannelTypeGroup:
		channelTypeFormat = "群组"
	case wkproto.ChannelTypePerson:
		channelTypeFormat = "个人"
	case wkproto.ChannelTypeCommunity:
		channelTypeFormat = "社区"
	case wkproto.ChannelTypeCustomerService:
		channelTypeFormat = "客服"
	case wkproto.ChannelTypeInfo:
		channelTypeFormat = "资讯"
	case wkproto.ChannelTypeData:
		channelTypeFormat = "数据"
	default:
		channelTypeFormat = fmt.Sprintf("未知(%d)", channelType)
	}
	return channelTypeFormat
}

type SlotResp struct {
	Id           uint32           `json:"id"`
	LeaderId     uint64           `json:"leader_id"`
	Term         uint32           `json:"term"`
	Replicas     []uint64         `json:"replicas"`
	ChannelCount int              `json:"channel_count"`
	LogIndex     uint64           `json:"log_index"`
	Status       types.SlotStatus `json:"status"`
	StatusFormat string           `json:"status_format"`
}

func NewSlotResp(st *types.Slot, channelCount int) *SlotResp {
	statusFormat := ""
	switch st.Status {
	case types.SlotStatus_SlotStatusNormal:
		statusFormat = "正常"
	case types.SlotStatus_SlotStatusCandidate:
		statusFormat = "候选中"
	case types.SlotStatus_SlotStatusLeaderTransfer:
		statusFormat = "领导者转移中"

	}
	return &SlotResp{
		Id:           st.Id,
		LeaderId:     st.Leader,
		Term:         st.Term,
		Replicas:     st.Replicas,
		ChannelCount: channelCount,
		Status:       st.Status,
		StatusFormat: statusFormat,
	}
}

type SlotRespTotal struct {
	Total int         `json:"total"` // 总数
	Data  []*SlotResp `json:"data"`  // 槽位信息
}

func (s *Server) requestSlotInfo(nodeId uint64, slotIds []uint32, headers map[string]string) ([]*SlotResp, error) {
	node := s.cfgServer.Node(nodeId)
	if node == nil {
		s.Error("node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	resp, err := network.Get(fmt.Sprintf("%s%s", node.ApiServerAddr, s.formatPath("/slots")), map[string]string{
		"ids": strings.Join(wkutil.Uint32ArrayToStringArray(slotIds), ","),
	}, headers)
	if err != nil {
		return nil, err
	}
	slotResps := make([]*SlotResp, 0)
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &slotResps)
	return slotResps, err
}

// 获取槽领导离线的槽位信息
func (s *Server) getSlotInfoForLeaderOffline(slotIds []uint32) ([]*SlotResp, error) {
	slotResps := make([]*SlotResp, 0)
	for _, slotId := range slotIds {
		slot := s.cfgServer.Slot(slotId)
		if slot == nil {
			s.Error("slot not found", zap.Uint32("slotId", slotId))
			continue
		}
		slotResp := NewSlotResp(slot, 0)
		slotResps = append(slotResps, slotResp)
	}
	return slotResps, nil
}

type SlotClusterConfigResp struct {
	Id                uint32   `json:"id"`                   // 槽位ID
	LeaderId          uint64   `json:"leader_id"`            // 领导者ID
	Term              uint32   `json:"term"`                 // 任期
	Replicas          []uint64 `json:"replicas"`             // 副本节点ID集合
	LogMaxIndex       uint64   `json:"log_max_index"`        // 本地日志最大索引
	LeaderLogMaxIndex uint64   `json:"leader_log_max_index"` // 领导者日志最大索引
	AppliedIndex      uint64   `json:"applied_index"`        // 已应用索引
}

func NewSlotClusterConfigRespFromClusterConfig(appliedIdx, logMaxIndex uint64, leaderLogMaxIndex uint64, slot *types.Slot) *SlotClusterConfigResp {
	return &SlotClusterConfigResp{
		Id:                slot.Id,
		LeaderId:          slot.Leader,
		Term:              slot.Term,
		Replicas:          slot.Replicas,
		LogMaxIndex:       logMaxIndex,
		LeaderLogMaxIndex: leaderLogMaxIndex,
		AppliedIndex:      appliedIdx,
	}
}

type messageRespTotal struct {
	Total int            `json:"total"` // 总数
	Data  []*messageResp `json:"data"`
}

type messageResp struct {
	MessageId       string `json:"message_id"`       // 服务端的消息ID(全局唯一)
	MessageSeq      uint32 `json:"message_seq"`      // 消息序列号 （用户唯一，有序递增）
	ClientMsgNo     string `json:"client_msg_no"`    // 客户端唯一标示
	Timestamp       int32  `json:"timestamp"`        // 服务器消息时间戳(10位，到秒)
	TimestampForamt string `json:"timestamp_format"` // 服务器消息时间戳格式化
	ChannelId       string `json:"channel_id"`       // 频道ID
	ChannelType     uint8  `json:"channel_type"`     // 频道类型
	Topic           string `json:"topic"`            // 话题ID
	FromUid         string `json:"from_uid"`         // 发送者UID
	Payload         []byte `json:"payload"`          // 消息内容
	Expire          uint32 `json:"expire"`           // 消息过期时间 0 表示永不过期
	Term            uint64 `json:"term"`             // 消息任期
}

func newMessageResp(m wkdb.Message) *messageResp {

	timestampFormat := wkutil.ToyyyyMMddHHmm(time.Unix(int64(m.Timestamp), 0))

	return &messageResp{
		MessageId:       strconv.FormatInt(m.MessageID, 10),
		MessageSeq:      m.MessageSeq,
		ClientMsgNo:     m.ClientMsgNo,
		Timestamp:       m.Timestamp,
		TimestampForamt: timestampFormat,
		ChannelId:       m.ChannelID,
		ChannelType:     m.ChannelType,
		Topic:           m.Topic,
		FromUid:         m.FromUID,
		Payload:         m.Payload,
		Expire:          m.Expire,
		Term:            m.Term,
	}
}

type NodeConfig struct {
	Id              uint64           `json:"id"`                          // 节点ID
	IsLeader        int              `json:"is_leader,omitempty"`         // 是否是leader
	Role            types.NodeRole   `json:"role"`                        // 节点角色
	ClusterAddr     string           `json:"cluster_addr"`                // 集群地址
	ApiServerAddr   string           `json:"api_server_addr,omitempty"`   // API服务地址
	Online          int              `json:"online,omitempty"`            // 是否在线
	OfflineCount    int              `json:"offline_count,omitempty"`     // 下线次数
	LastOffline     string           `json:"last_offline,omitempty"`      // 最后一次下线时间
	AllowVote       int              `json:"allow_vote"`                  // 是否允许投票
	SlotCount       int              `json:"slot_count,omitempty"`        // 槽位数量
	Term            uint32           `json:"term,omitempty"`              // 任期
	SlotLeaderCount int              `json:"slot_leader_count,omitempty"` // 槽位领导者数量
	ExportCount     int              `json:"export_count,omitempty"`      // 迁出槽位数量
	Exports         []*SlotMigrate   `json:"exports,omitempty"`           // 迁移槽位
	ImportCount     int              `json:"import_count,omitempty"`      // 迁入槽位数量
	Imports         []*SlotMigrate   `json:"imports,omitempty"`           // 迁入槽位
	Uptime          string           `json:"uptime,omitempty"`            // 运行时间
	AppVersion      string           `json:"app_version,omitempty"`       // 应用版本
	ConfigVersion   uint64           `json:"config_version,omitempty"`    // 配置版本
	Status          types.NodeStatus `json:"status,omitempty"`            // 状态
	StatusFormat    string           `json:"status_format,omitempty"`     // 状态格式化
}

func NewNodeConfigFromNode(n *types.Node) *NodeConfig {
	// lastOffline format string
	lastOffline := ""
	if n.LastOffline != 0 {
		lastOffline = wkutil.ToyyyyMMddHHmm(time.Unix(n.LastOffline, 0))
	}
	status := ""
	if n.Status == types.NodeStatus_NodeStatusJoined {
		status = "已加入"

	} else if n.Status == types.NodeStatus_NodeStatusJoining {
		status = "加入中"
	} else if n.Status == types.NodeStatus_NodeStatusWillJoin {
		status = "将加入"
	}
	return &NodeConfig{
		Id:            n.Id,
		Role:          n.Role,
		ClusterAddr:   n.ClusterAddr,
		ApiServerAddr: n.ApiServerAddr,
		Online:        wkutil.BoolToInt(n.Online),
		OfflineCount:  int(n.OfflineCount),
		LastOffline:   lastOffline,
		AllowVote:     wkutil.BoolToInt(n.AllowVote),
		Status:        n.Status,
		StatusFormat:  status,
	}
}

type SlotMigrate struct {
	Slot   uint32              `json:"slot_id"`
	From   uint64              `json:"from"`
	To     uint64              `json:"to"`
	Status types.MigrateStatus `json:"status"`
}

type NodeConfigTotal struct {
	Total int           `json:"total"` // 总数
	Data  []*NodeConfig `json:"data"`
}

type channelInfoResp struct {
	Id                uint64 `json:"id"`                   // 主键
	Slot              uint32 `json:"slot"`                 // 槽位ID
	ChannelId         string `json:"channel_id"`           // 频道ID
	ChannelType       uint8  `json:"channel_type"`         // 频道类型
	Ban               int    `json:"ban"`                  // 是否禁言
	Disband           int    `json:"disband"`              // 是否解散
	SubscriberCount   int    `json:"subscriber_count"`     // 订阅者数量
	AllowlistCount    int    `json:"allowlist_count"`      // 白名单数量
	DenylistCount     int    `json:"denylist_count"`       // 黑名单数量
	LastMsgSeq        uint64 `json:"last_msg_seq"`         // 频道最新消息序号
	LastMsgTime       uint64 `json:"last_msg_time"`        // 频道最新消息时间
	LastMsgTimeFormat string `json:"last_msg_time_format"` // 频道最新消息时间格式化
	StatusFormat      string `json:"status_format"`        // 状态格式化
	CreatedAt         int64  `json:"created_at"`           // 创建时间
	UpdatedAt         int64  `json:"updated_at"`           // 更新时间
	CreatedAtFormat   string `json:"created_at_format"`    // 创建时间格式化
	UpdatedAtFormat   string `json:"updated_at_format"`    // 更新时间格式化

}

func newChannelInfoResp(ch wkdb.ChannelInfo, slotId uint32) *channelInfoResp {

	lastMsgTimeFormat := ""
	if ch.LastMsgTime != 0 {
		lastMsgTimeFormat = wkutil.ToyyyyMMddHHmm(time.Unix(int64(ch.LastMsgTime/1e9), 0))
	}

	statusFormat := "正常"
	if ch.Disband {
		statusFormat = "解散"
	}
	if ch.Ban {
		statusFormat = "封禁"
	}

	createdAt := int64(0)
	updatedAt := int64(0)
	if ch.CreatedAt != nil {
		createdAt = ch.CreatedAt.UnixNano()
	}
	if ch.UpdatedAt != nil {
		updatedAt = ch.UpdatedAt.UnixNano()
	}

	createdAtFormat := ""
	if ch.CreatedAt != nil {
		createdAtFormat = wkutil.ToyyyyMMddHHmm(*ch.CreatedAt)
	}

	updatedAtFormat := ""
	if ch.UpdatedAt != nil {
		updatedAtFormat = wkutil.ToyyyyMMddHHmm(*ch.UpdatedAt)
	}

	return &channelInfoResp{
		Id:                ch.Id,
		Slot:              slotId,
		ChannelId:         ch.ChannelId,
		ChannelType:       ch.ChannelType,
		Ban:               wkutil.BoolToInt(ch.Ban),
		Disband:           wkutil.BoolToInt(ch.Disband),
		SubscriberCount:   ch.SubscriberCount,
		AllowlistCount:    ch.AllowlistCount,
		DenylistCount:     ch.DenylistCount,
		LastMsgSeq:        ch.LastMsgSeq,
		LastMsgTime:       ch.LastMsgTime,
		LastMsgTimeFormat: lastMsgTimeFormat,
		StatusFormat:      statusFormat,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		CreatedAtFormat:   createdAtFormat,
		UpdatedAtFormat:   updatedAtFormat,
	}
}

type channelInfoRespTotal struct {
	Total int                `json:"total"` // 总数
	More  int                `json:"more"`  // 是否还有更多
	Data  []*channelInfoResp `json:"data"`
}

type userResp struct {
	Id                uint64 `json:"id"`                  // 主键
	Uid               string `json:"uid"`                 // 用户ID
	DeviceCount       uint32 `json:"device_count"`        // 设备数量
	OnlineDeviceCount uint32 `json:"online_device_count"` // 在线设备数量
	ConnCount         uint32 `json:"conn_count"`          // 连接数量
	SendMsgCount      uint64 `json:"send_msg_count"`      // 发送消息数量
	RecvMsgCount      uint64 `json:"recv_msg_count"`      // 接收消息数量
	SendMsgBytes      uint64 `json:"send_msg_bytes"`      // 发送消息字节数
	RecvMsgBytes      uint64 `json:"recv_msg_bytes"`      // 接收消息字节数
	CreatedAt         int64  `json:"created_at"`          // 创建时间
	UpdatedAt         int64  `json:"updated_at"`          // 更新时间
	CreatedAtFormat   string `json:"created_at_format"`   // 创建时间格式化
	UpdatedAtFormat   string `json:"updated_at_format"`   // 更新时间格式化
	Slot              uint32 `json:"slot"`                // 槽位ID
}

func newUserResp(u wkdb.User) *userResp {

	var createdAtFormat string
	var updatedAtFormat string
	if u.CreatedAt != nil {
		createdAtFormat = wkutil.ToyyyyMMddHHmm(*u.CreatedAt)
	}
	if u.UpdatedAt != nil {
		updatedAtFormat = wkutil.ToyyyyMMddHHmm(*u.UpdatedAt)
	}
	return &userResp{
		Id:                u.Id,
		Uid:               u.Uid,
		DeviceCount:       u.DeviceCount,
		OnlineDeviceCount: u.OnlineDeviceCount,
		ConnCount:         u.ConnCount,
		SendMsgCount:      u.SendMsgCount,
		RecvMsgCount:      u.RecvMsgCount,
		SendMsgBytes:      u.SendMsgBytes,
		RecvMsgBytes:      u.RecvMsgBytes,
		CreatedAt:         u.CreatedAt.UnixNano(),
		UpdatedAt:         u.UpdatedAt.UnixNano(),
		CreatedAtFormat:   createdAtFormat,
		UpdatedAtFormat:   updatedAtFormat,
	}
}

type userRespTotal struct {
	Total int         `json:"total"` // 总数
	More  int         `json:"more"`  // 是否还有更多
	Data  []*userResp `json:"data"`
}

type deviceResp struct {
	Id                uint64 `json:"id"`                  // 主键
	Uid               string `json:"uid"`                 // 用户唯一uid
	Token             string `json:"token"`               // 设备token
	DeviceFlag        uint64 `json:"device_flag"`         // 设备标记 (TODO: 这里deviceFlag弄成uint64是为了以后扩展)
	DeviceFlagFormat  string `json:"device_flag_format"`  // 设备标记格式化
	DeviceLevel       uint8  `json:"device_level"`        // 设备等级
	DeviceLevelFormat string `json:"device_level_format"` // 设备等级格式化
	ConnCount         uint32 `json:"conn_count"`          // 连接数量
	SendMsgCount      uint64 `json:"send_msg_count"`      // 发送消息数量
	RecvMsgCount      uint64 `json:"recv_msg_count"`      // 接收消息数量
	SendMsgBytes      uint64 `json:"send_msg_bytes"`      // 发送消息字节数
	RecvMsgBytes      uint64 `json:"recv_msg_bytes"`      // 接收消息字节数
	CreatedAt         int64  `json:"created_at"`          // 创建时间
	UpdatedAt         int64  `json:"updated_at"`          // 更新时间
	CreatedAtFormat   string `json:"created_at_format"`   // 创建时间格式化
	UpdatedAtFormat   string `json:"updated_at_format"`   // 更新时间格式化
}

func newDeviceResp(d wkdb.Device) *deviceResp {

	deviceFlagFormat := ""
	switch d.DeviceFlag {
	case uint64(wkproto.WEB):
		deviceFlagFormat = "WEB"
	case uint64(wkproto.APP):
		deviceFlagFormat = "APP"
	case uint64(wkproto.PC):
		deviceFlagFormat = "PC"
	default:
		deviceFlagFormat = fmt.Sprintf("未知(%d)", d.DeviceFlag)
	}

	deviceLevelFormat := ""
	switch d.DeviceLevel {
	case uint8(wkproto.DeviceLevelMaster):
		deviceLevelFormat = "主设备"
	case uint8(wkproto.DeviceLevelSlave):
		deviceLevelFormat = "从设备"
	default:
		deviceLevelFormat = fmt.Sprintf("未知(%d)", d.DeviceLevel)
	}

	var createdAtFormat string
	var updatedAtFormat string
	if d.CreatedAt != nil {
		createdAtFormat = wkutil.ToyyyyMMddHHmm(*d.CreatedAt)
	}
	if d.UpdatedAt != nil {
		updatedAtFormat = wkutil.ToyyyyMMddHHmm(*d.UpdatedAt)
	}

	return &deviceResp{
		Id:                d.Id,
		Uid:               d.Uid,
		Token:             d.Token,
		DeviceFlag:        d.DeviceFlag,
		DeviceFlagFormat:  deviceFlagFormat,
		DeviceLevel:       d.DeviceLevel,
		DeviceLevelFormat: deviceLevelFormat,
		ConnCount:         d.ConnCount,
		SendMsgCount:      d.SendMsgCount,
		RecvMsgCount:      d.RecvMsgCount,
		SendMsgBytes:      d.SendMsgBytes,
		RecvMsgBytes:      d.RecvMsgBytes,
		CreatedAt:         d.CreatedAt.UnixNano(),
		UpdatedAt:         d.UpdatedAt.UnixNano(),
		CreatedAtFormat:   createdAtFormat,
		UpdatedAtFormat:   updatedAtFormat,
	}
}

type deviceRespTotal struct {
	Total int           `json:"total"` // 总数
	More  int           `json:"more"`  // 是否还有更多
	Data  []*deviceResp `json:"data"`
}

type conversationResp struct {
	Id                uint64                `json:"id"`                  // 主键
	Uid               string                `json:"uid"`                 // 用户唯一uid
	Type              wkdb.ConversationType `json:"type"`                // 会话类型
	TypeFormat        string                `json:"type_format"`         // 会话类型格式化
	ChannelId         string                `json:"channel_id"`          // 频道ID
	ChannelType       uint8                 `json:"channel_type"`        // 频道类型
	ChannelTypeFormat string                `json:"channel_type_format"` // 频道类型格式化
	UnreadCount       uint32                `json:"unread_count"`        // 未读消息数量（这个可以用户自己设置）
	LastMsgSeq        uint64                `json:"last_msg_seq"`        // 最新消息序号
	ReadedToMsgSeq    uint64                `json:"readed_to_msg_seq"`   // 已经读至的消息序号
	CreatedAt         int64                 `json:"created_at"`          // 创建时间
	UpdatedAt         int64                 `json:"updated_at"`          // 更新时间
	CreatedAtFormat   string                `json:"created_at_format"`   // 创建时间格式化
	UpdatedAtFormat   string                `json:"updated_at_format"`   // 更新时间格式化
}

func newConversationResp(c wkdb.Conversation) *conversationResp {
	typeFormat := "聊天"
	if c.Type == wkdb.ConversationTypeCMD {
		typeFormat = "命令"
	}
	var createdAtFormat string
	var updatedAtFormat string
	var createdAt int64
	var updatedAt int64
	if c.CreatedAt != nil {
		createdAtFormat = wkutil.ToyyyyMMddHHmm(*c.CreatedAt)
		createdAt = c.CreatedAt.Unix()
	}
	if c.UpdatedAt != nil {
		updatedAtFormat = wkutil.ToyyyyMMddHHmm(*c.UpdatedAt)
		updatedAt = c.UpdatedAt.Unix()
	}
	return &conversationResp{
		Id:                c.Id,
		Uid:               c.Uid,
		ChannelId:         c.ChannelId,
		ChannelType:       c.ChannelType,
		Type:              c.Type,
		TypeFormat:        typeFormat,
		ChannelTypeFormat: formatChannelType(c.ChannelType),
		UnreadCount:       c.UnreadCount,
		ReadedToMsgSeq:    c.ReadToMsgSeq,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		CreatedAtFormat:   createdAtFormat,
		UpdatedAtFormat:   updatedAtFormat,
	}
}

type conversationRespTotal struct {
	Total int                 `json:"total"` // 总数
	Data  []*conversationResp `json:"data"`  // 会话信息
}

type LogType int

const (
	LogTypeUnknown LogType = iota
	LogTypeConfig          // 节点配置日志
	LogTypeSlot            // 槽日志
	LogTypeChannel         // 频道日志
)

type LogResp struct {
	Id         string `json:"id"`          // 日志ID
	Index      uint64 `json:"index"`       // 日志下标
	Term       uint32 `json:"term"`        // 数据任期
	Cmd        string `json:"cmd"`         // 命令
	Content    string `json:"data"`        // 命令数据
	TimeFormat string `json:"time_format"` // 时间格式化
}

func NewLogRespFromLog(log rafttype.Log, logType LogType) (*LogResp, error) {

	cmdStr := ""
	cmdContent := ""
	if logType == LogTypeConfig {
		cmd := &clusterconfig.CMD{}
		err := cmd.Unmarshal(log.Data)
		if err != nil {
			wklog.Error("config: cmd unmarshal error", zap.Error(err), zap.Uint64("index", log.Index), zap.Uint32("term", log.Term), zap.Binary("data", log.Data))
			return nil, err
		}
		cmdStr = cmd.CmdType.String()
		cmdContent, err = cmd.CMDContent()
		if err != nil {
			wklog.Error("config: cmd content error", zap.Error(err), zap.String("cmd", cmdStr), zap.Uint64("index", log.Index), zap.Uint32("term", log.Term), zap.Binary("data", cmd.Data))
			return nil, err
		}
	} else if logType == LogTypeSlot {
		cmd := &store.CMD{}
		err := cmd.Unmarshal(log.Data)
		if err != nil {
			wklog.Error("slot: cmd unmarshal error", zap.Error(err), zap.Uint64("index", log.Index), zap.Uint32("term", log.Term), zap.Binary("data", log.Data))
			return nil, err
		}
		cmdStr = cmd.CmdType.String()
		cmdContent, err = cmd.CMDContent()
		if err != nil {
			wklog.Error("slot: cmd content error", zap.Error(err), zap.String("cmd", cmdStr), zap.Uint64("index", log.Index), zap.Uint32("term", log.Term), zap.Binary("data", cmd.Data))
			return nil, err
		}
	}
	timeFormat := wkutil.ToyyyyMMddHHmmss(log.Time)

	return &LogResp{
		Id:         strconv.FormatUint(log.Id, 10),
		Index:      log.Index,
		Term:       log.Term,
		Cmd:        cmdStr,
		Content:    cmdContent,
		TimeFormat: timeFormat,
	}, nil
}

type LogRespTotal struct {
	Next    uint64     `json:"next"`    // 下一个查询日志ID
	Pre     uint64     `json:"pre"`     // 上一个查询日志ID
	Applied uint64     `json:"applied"` // 已应用日志下标
	Last    uint64     `json:"last"`    // 最后一个日志下标
	More    int        `json:"more"`    // 是否有更多
	Logs    []*LogResp `json:"logs"`    // 日志信息
}

type SpanNode struct {
	Id          string                 `json:"id"`          // ID
	Shape       string                 `json:"shape"`       // 形状
	Name        string                 `json:"name"`        // 名称
	NodeId      uint64                 `json:"nodeId"`      // 节点ID
	Time        string                 `json:"time"`        // 时间
	Duration    int64                  `json:"duration"`    // 单位纳秒
	Icon        string                 `json:"icon"`        // 图标
	Width       float64                `json:"width"`       // 宽度
	Height      float64                `json:"height"`      // 高度
	X           float64                `json:"x"`           // X
	Y           float64                `json:"y"`           // Y
	Description string                 `json:"description"` // 描述
	Data        map[string]interface{} `json:"data"`        // 数据
}

func (s *SpanNode) genDescription() {
	s.Description = fmt.Sprintf("节点:%d", s.NodeId)
}

type SpanEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Shape  string `json:"shape"`
	Label  string `json:"label"`
}

type Trace struct {
	Nodes []*SpanNode `json:"nodes"`
	Edges []*SpanEdge `json:"edges"`
}

type Stream map[string]interface{}

func (s Stream) getString(key string) string {
	if v, ok := s[key]; ok {
		if str, ok := v.(string); ok {
			return str
		}
	}
	return ""
}

func (s Stream) getInt64(key string) int64 {
	if v, ok := s[key]; ok {
		switch tv := v.(type) {
		case json.Number:
			num, _ := tv.Int64()
			return num
		case string:
			num, _ := strconv.ParseInt(tv, 10, 64)
			return num
		}
	}
	return 0
}

func (s Stream) getObjects(key string) []interface{} {
	if v, ok := s[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

func (s Stream) getAction() string {
	return s.getString("action")
}

func (s Stream) getName() string {
	action := s.getAction()

	return getSpanNodeName(action)
}

func (s Stream) getNodeId() uint64 {
	return s.getUint64("nodeId")
}

func (s Stream) getTraceTime() time.Time {
	return s.getTime("time")
}

func (s Stream) getIcon() string {
	action := s.getAction()
	switch action {
	case "processMessage":
		return `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-circle-play"><circle cx="12" cy="12" r="10"/><polygon points="10 8 16 12 10 16 10 8"/></svg>`
	case "processPayloadDecrypt":
		return `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-shield"><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/></svg>`
	case "processPermission":
		return `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-person-standing"><circle cx="12" cy="5" r="1"/><path d="m9 20 3-6 3 6"/><path d="m6 8 6 2 6-2"/><path d="M12 10v4"/></svg>`
	case "processStorage":
		return `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-cylinder"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14a9 3 0 0 0 18 0V5"/></svg>`
	case "processSendack":
		return ``
	case "makeReceiverTag":
		return ``
	case "forwardDeliver":
		return ``
	case "deliverMessage":
		return `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-package-2"><path d="M3 9h18v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V9Z"/><path d="m3 9 2.45-4.9A2 2 0 0 1 7.24 3h9.52a2 2 0 0 1 1.8 1.1L21 9"/><path d="M12 3v6"/></svg>`
	case "requestReceiverTag":
		return ``
	default:
		return ``
	}
}

func (s Stream) getTime(key string) time.Time {
	if v, ok := s[key]; ok {
		tm, _ := time.Parse(time.RFC3339Nano, v.(string))
		return tm
	}
	return time.Time{}
}

func (s Stream) getUint64(key string) uint64 {
	if v, ok := s[key]; ok {
		switch tv := v.(type) {

		case json.Number:
			num, _ := tv.Int64()
			return uint64(num)
		case string:
			num, _ := strconv.ParseUint(tv, 10, 64)
			return num
		}
	}
	return 0
}

func (s Stream) getInt(key string) int {
	if v, ok := s[key]; ok {
		switch tv := v.(type) {
		case json.Number:
			num, _ := tv.Int64()
			return int(num)
		case string:
			num, _ := strconv.Atoi(tv)
			return num
		}
	}
	return 0
}

func getSpanNodeName(id string) string {
	switch id {
	case "processMessage":
		return "收到消息"
	case "processPayloadDecrypt":
		return "解密消息"
	case "processPermission":
		return "权限验证"
	case "processStorage":
		return "存储消息"
	case "processSendack":
		return "回应客户端"
	case "processDeliver":
		return "投递消息"
	case "processRecvack":
		return "收到投递"
	case "deliverNode":
		return "投递节点"
	case "deliverOnline":
		return "在线通知"
	case "deliverOffline":
		return "离线通知"
	default:
		return id
	}
}

type StreamGroup struct {
	Action    string
	Streams   []Stream
	SubGroups []*StreamGroup
}

func (s *StreamGroup) First() Stream {
	if len(s.Streams) > 0 {
		return s.Streams[0]
	}
	return nil
}

type Template struct {
	Nodes []*SpanNode
	Edges []*SpanEdge
}

func newMessageTraceTemple(containerWidth float64) *Template {

	nodeWidth := float64(180)
	nodeHeight := float64(70)

	centerX := (containerWidth - float64(nodeWidth)) / 2
	topSpace := float64(60)
	leftSpace := float64(80)

	groupSpace := float64(40)

	nodes := []*SpanNode{
		{
			Id:     "processMessage",
			Name:   getSpanNodeName("processMessage"),
			Shape:  "textNode",
			Width:  nodeWidth,
			Height: nodeHeight,
			X:      centerX - nodeWidth - groupSpace - topSpace,
			Y:      topSpace,
		},
		{
			Id:     "processPayloadDecrypt",
			Name:   getSpanNodeName("processPayloadDecrypt"),
			Shape:  "textNode",
			Width:  nodeWidth,
			Height: nodeHeight,
			X:      centerX,
			Y:      topSpace,
		},
		{
			Id:     "processPermission",
			Name:   getSpanNodeName("processPermission"),
			Shape:  "textNode",
			Width:  nodeWidth,
			Height: nodeHeight,
			X:      centerX,
			Y:      (topSpace+nodeHeight)*1 + topSpace,
		},
		{
			Id:     "processStorage",
			Name:   getSpanNodeName("processStorage"),
			Shape:  "textNode",
			Width:  nodeWidth,
			Height: nodeHeight,
			X:      centerX,
			Y:      (topSpace+nodeHeight)*2 + topSpace,
		},
		{
			Id:     "processSendack",
			Name:   getSpanNodeName("processSendack"),
			Shape:  "textNode",
			Width:  nodeWidth,
			Height: nodeHeight,
			X:      centerX + nodeWidth + leftSpace,
			Y:      (topSpace+nodeHeight)*2 + topSpace,
		},
		{
			Id:     "processDeliver",
			Name:   getSpanNodeName("processDeliver"),
			Shape:  "textNode",
			Width:  nodeWidth,
			Height: nodeHeight,
			X:      centerX,
			Y:      (topSpace+nodeHeight)*3 + topSpace + groupSpace,
		},
	}

	edges := []*SpanEdge{
		{
			Source: "processMessage",
			Target: "processPayloadDecrypt",
		},
		{
			Source: "processPayloadDecrypt",
			Target: "processPermission",
		},
		{
			Source: "processPermission",
			Target: "processStorage",
		},
		{
			Source: "processStorage",
			Target: "processSendack",
			Shape:  "dashed",
		},
		{
			Source: "processStorage",
			Target: "processDeliver",
			Shape:  "dashed",
		},
	}

	return &Template{
		Nodes: nodes,
		Edges: edges,
	}
}

type channelBase struct {
	ChannelId   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

type channelStatusResp struct {
	channelBase
	Running     int    `json:"running"`       // 是否运行中
	LastMsgSeq  uint64 `json:"last_msg_seq"`  // 最新消息序号
	LastMsgTime uint64 `json:"last_msg_time"` // 最新消息时间
}

type channelReplicaResp struct {
	ReplicaId   uint64 `json:"replica_id"`    // 副本节点id
	Running     int    `json:"running"`       // 是否运行中
	LastMsgSeq  uint64 `json:"last_msg_seq"`  // 最新消息序号
	LastMsgTime uint64 `json:"last_msg_time"` // 最新消息时间
	Term        uint32 `json:"term"`          // 任期
}

type channelReplicaDetailResp struct {
	channelReplicaResp
	Role              int    `json:"role"`                 // 角色
	RoleFormat        string `json:"role_format"`          // 角色格式化
	LastMsgTimeFormat string `json:"last_msg_time_format"` // 最新消息时间格式化

}

type ping struct {
	no        string // ping唯一编号
	from      uint64 // 发起节点
	to        uint64
	startMill int64 // 开始时间
	costMill  int64 // 耗时毫秒
	waitC     chan *pong
	err       error // 错误信息
}

func (p *ping) marshal() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(p.no)
	enc.WriteUint64(p.from)
	enc.WriteInt64(p.startMill)
	return enc.Bytes()
}

func unmarshalPing(data []byte) (*ping, error) {
	dec := wkproto.NewDecoder(data)
	no, err := dec.String()
	if err != nil {
		return nil, err
	}
	from, err := dec.Uint64()
	if err != nil {
		return nil, err
	}
	startMill, err := dec.Int64()
	if err != nil {
		return nil, err
	}
	return &ping{
		no:        no,
		from:      from,
		startMill: startMill,
	}, nil
}

type pong struct {
	no        string // ping唯一编号
	from      uint64 // 发起节点
	startMill int64  // 开始时间
}

func (p *pong) marshal() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(p.no)
	enc.WriteUint64(p.from)
	enc.WriteInt64(p.startMill)
	return enc.Bytes()
}

func unmarshalPong(data []byte) (*pong, error) {
	dec := wkproto.NewDecoder(data)
	no, err := dec.String()
	if err != nil {
		return nil, err
	}
	from, err := dec.Uint64()
	if err != nil {
		return nil, err
	}
	startMill, err := dec.Int64()
	if err != nil {
		return nil, err
	}
	return &pong{
		no:        no,
		from:      from,
		startMill: startMill,
	}, nil
}
