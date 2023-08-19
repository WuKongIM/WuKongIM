package wraft

import (
	"errors"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.etcd.io/raft/v3/raftpb"
)

// CMDType CMDType
type CMDType uint32

const (
	// CMDUnknown unknown
	CMDUnknown CMDType = iota
	// CMDRaftMessage CMDRaftMessage
	CMDRaftMessage CMDType = 100

	// CMDGetClusterConfig get cluster config
	CMDGetClusterConfig CMDType = 101
	CMDJoinCluster      CMDType = 102
)

// Int32 Int32
func (c CMDType) Uint32() uint32 {
	return uint32(c)
}

func (c CMDType) String() string {
	switch c {
	case CMDRaftMessage:
		return "CMDRaftMessage"
	case CMDGetClusterConfig:
		return "CMDGetClusterConfig"
	case CMDJoinCluster:
		return "CMDJoinCluster"
	default:
		return "CMDUnknown"
	}
}

type CMDReq struct {
	Id      uint64
	Type    uint32
	Version uint8
	Param   []byte

	To uint64 // 不编码
}

func NewCMDReq(id uint64, typ uint32) *CMDReq {
	return &CMDReq{
		Id:   id,
		Type: typ,
	}
}

// Encode Encode
func (c *CMDReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteUint64(c.Id)
	enc.WriteUint32(c.Type)
	enc.WriteUint8(c.Version)
	enc.WriteBytes(c.Param)
	return enc.Bytes(), nil
}

func (c *CMDReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.Id, err = dec.Uint64(); err != nil {
		return err
	}
	var cmdType uint32
	if cmdType, err = dec.Uint32(); err != nil {
		return err
	}
	c.Type = cmdType
	if c.Version, err = dec.Uint8(); err != nil {
		return err
	}
	if c.Param, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}

type CMDRespStatus uint32

const (
	// CMDRespStatusOK ok
	CMDRespStatusOK CMDRespStatus = 0
	// CMDRespStatusError error
	CMDRespStatusError CMDRespStatus = 1
)

var (
	ErrCMDRespStatus error = errors.New("CMDRespStatusError")
)

func (c CMDRespStatus) Uint32() uint32 {
	return uint32(c)
}

type CMDResp struct {
	Id     uint64
	Status CMDRespStatus
	Param  []byte
}

func NewCMDResp(id uint64) *CMDResp {
	return &CMDResp{
		Id: id,
	}
}

func NewCMDRespWithStatus(id uint64, status CMDRespStatus) *CMDResp {
	return &CMDResp{
		Id:     id,
		Status: status,
	}
}

func (c *CMDResp) ID() uint64 {
	return c.Id
}

// Encode Encode
func (c *CMDResp) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteUint64(c.Id)
	enc.WriteUint32(c.Status.Uint32())
	enc.WriteBytes(c.Param)
	return enc.Bytes(), nil
}

func (c *CMDResp) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.Id, err = dec.Uint64(); err != nil {
		return err
	}
	var status uint32
	if status, err = dec.Uint32(); err != nil {
		return err
	}
	c.Status = CMDRespStatus(status)
	if c.Param, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}

type Storage interface {
	// Save function saves ents and state to the underlying stable storage.
	// Save MUST block until st and ents are on stable storage.
	Save(st raftpb.HardState, ents []raftpb.Entry) error
	// SaveSnap function saves snapshot to the underlying stable storage.
	SaveSnap(snap raftpb.Snapshot) error
	// Close closes the Storage and performs finalization.
	Close() error
	// Release releases the locked wal files older than the provided snapshot.
	Release(snap raftpb.Snapshot) error
	// Sync WAL
	Sync() error
}

type RaftReadyHandler struct {
	GetLead              func() (lead uint64)
	UpdateLead           func(lead uint64)
	UpdateLeadership     func(newLeader bool)
	UpdateCommittedIndex func(uint64)
}

// ToApply contains entries, snapshot to be applied. Once
// an toApply is consumed, the entries will be persisted to
// raft storage concurrently; the application must read
// notifyc before assuming the raft messages are stable.
type ToApply struct {
	Entries  []raftpb.Entry
	Snapshot raftpb.Snapshot
	// notifyc synchronizes etcd server applies with the raft node
	Notifyc chan struct{}
	// raftAdvancedC notifies EtcdServer.apply that
	// 'raftLog.applied' has advanced by r.Advance
	// it should be used only when entries contain raftpb.EntryConfChange
	RaftAdvancedC <-chan struct{}
}

func (r *RaftNode) Applyc() chan ToApply {

	return r.applyc
}

type Nil struct {
}
