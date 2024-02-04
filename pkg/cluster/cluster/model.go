package cluster

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

var (
	ErrStopped                 = errors.New("cluster stopped")
	ErrStepChannelFull         = errors.New("step channel full")
	ErrProposeChannelFull      = errors.New("propose channel full")
	ErrRecvChannelFull         = errors.New("recv channel full")
	ErrSlotNotFound            = errors.New("slot not found")
	ErrNodeNotFound            = errors.New("node not found")
	ErrNotLeader               = errors.New("not leader")
	ErrNotIsLeader             = errors.New("not is leader")
	ErrSlotNotExist            = errors.New("slot not exist")
	ErrSlotNotIsLeader         = errors.New("slot not is leader")
	ErrTermZero                = errors.New("term is zero")
	ErrChannelNotFound         = errors.New("channel not found")
	ErrClusterConfigNotFound   = errors.New("clusterConfig not found")
	ErrOldChannelClusterConfig = errors.New("old channel cluster config")
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

func ChannelKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%d-%s", channelType, channelID)
}

func ChannelFromChannelKey(channelKey string) (channelID string, channelType uint8) {
	channels := strings.Split(channelKey, "-")
	if len(channels) == 2 {
		channelTypeI, _ := strconv.Atoi(channels[0])
		return channels[1], uint8(channelTypeI)
	}
	return "", 0
}

// 频道分布式配置
type ChannelClusterConfig struct {
	ChannelID    string   // 频道ID
	ChannelType  uint8    // 频道类型
	ReplicaCount uint16   // 副本数量
	Replicas     []uint64 // 副本节点ID集合
	LeaderId     uint64   // 领导者ID
	Term         uint32   // 任期

	version uint16 // 数据协议版本
}

func (c *ChannelClusterConfig) Marshal() ([]byte, error) {
	c.version = 1
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(c.version)
	enc.WriteString(c.ChannelID)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint16(c.ReplicaCount)
	enc.WriteUint16(uint16(len(c.Replicas)))
	if len(c.Replicas) > 0 {
		for _, replica := range c.Replicas {
			enc.WriteUint64(replica)
		}
	}
	enc.WriteUint64(c.LeaderId)
	enc.WriteUint32(c.Term)
	return enc.Bytes(), nil
}

func (c *ChannelClusterConfig) Unmarshal(data []byte) error {
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
	if c.ReplicaCount, err = dec.Uint16(); err != nil {
		return err
	}
	var replicasLen uint16
	if replicasLen, err = dec.Uint16(); err != nil {
		return err
	}
	if replicasLen > 0 {
		c.Replicas = make([]uint64, replicasLen)
		for i := uint16(0); i < replicasLen; i++ {
			if c.Replicas[i], err = dec.Uint64(); err != nil {
				return err
			}
		}
	}
	if c.LeaderId, err = dec.Uint64(); err != nil {
		return err
	}
	if c.Term, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}

func (c *ChannelClusterConfig) String() string {
	return fmt.Sprintf("ChannelID: %s, ChannelType: %d, ReplicaCount: %d, Replicas: %v, LeaderId: %d, Term: %d",
		c.ChannelID, c.ChannelType, c.ReplicaCount, c.Replicas, c.LeaderId, c.Term)
}

type ChannelLastLogInfoReq struct {
	ChannelID   string
	ChannelType uint8
}

func (c *ChannelLastLogInfoReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelID)
	enc.WriteUint8(c.ChannelType)
	return enc.Bytes(), nil
}

func (c *ChannelLastLogInfoReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.ChannelID, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	return nil
}

type ChannelLastLogInfoResponse struct {
	LogIndex uint64 // 频道最新日志索引
}

func (c *ChannelLastLogInfoResponse) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(c.LogIndex)
	return enc.Bytes(), nil
}

func (c *ChannelLastLogInfoResponse) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.LogIndex, err = dec.Uint64(); err != nil {
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
	ChannelID   string `json:"channel_id"`   // 频道id
	ChannelType uint8  `json:"channel_type"` // 频道类型
}

func (c *ChannelClusterConfigReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelID)
	enc.WriteUint8(c.ChannelType)
	return enc.Bytes(), nil
}

func (c *ChannelClusterConfigReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.ChannelID, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	return nil
}

type ChannelProposeReq struct {
	ChannelId   string   // 频道id
	ChannelType uint8    // 频道类型
	Data        [][]byte // 数据
}

func (c *ChannelProposeReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint16(uint16(len(c.Data)))
	for _, data := range c.Data {
		enc.WriteBinary(data)
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
		c.Data = make([][]byte, dataLen)
		for i := uint16(0); i < dataLen; i++ {
			if c.Data[i], err = dec.Binary(); err != nil {
				return err
			}
		}
	}
	return nil
}

type ChannelProposeResp struct {
	ClusterConfigOld bool     // 请求的节点的集群配置是否是旧的
	Indexs           []uint64 // 提案索引
}

func (c *ChannelProposeResp) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint8(uint8(wkutil.BoolToInt(c.ClusterConfigOld)))
	enc.WriteUint16(uint16(len(c.Indexs)))
	for _, index := range c.Indexs {
		enc.WriteUint64(index)
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

	var indexsLen uint16
	if indexsLen, err = dec.Uint16(); err != nil {
		return err
	}
	if indexsLen > 0 {
		c.Indexs = make([]uint64, indexsLen)
		for i := uint16(0); i < indexsLen; i++ {
			if c.Indexs[i], err = dec.Uint64(); err != nil {
				return err
			}
		}
	}
	return nil
}

type UpdateNodeApiServerAddrReq struct {
	NodeId        uint64 // 节点ID
	ApiServerAddr string // API服务地址
}

func (u *UpdateNodeApiServerAddrReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(u.NodeId)
	enc.WriteString(u.ApiServerAddr)
	return enc.Bytes(), nil
}

func (u *UpdateNodeApiServerAddrReq) Unmarshal(data []byte) error {
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

type SlotProposeReq struct {
	SlotId uint32
	Data   []byte
}

func (s *SlotProposeReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(s.SlotId)
	enc.WriteBinary(s.Data)
	return enc.Bytes(), nil
}

func (s *SlotProposeReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.SlotId, err = dec.Uint32(); err != nil {
		return err
	}
	if s.Data, err = dec.Binary(); err != nil {
		return err
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
	SlotId   uint32
	LogIndex uint64
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
		}
	}
	return nil
}

type ChannelClusterStorage interface {
	// 保存分布式配置
	Save(channelId string, channelType uint8, clusterCfg *ChannelClusterConfig) error
	// 删除频道分布式配置
	Delete(channelId string, channelType uint8) error
	// 获取分布式配置
	Get(channelId string, channelType uint8) (*ChannelClusterConfig, error)
	// 提案频道分布式数据
	ProposeSave(channelId string, channelType uint8, clusterCfg *ChannelClusterConfig) error
}
