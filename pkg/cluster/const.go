package cluster

import (
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type ServerRole uint8

const (
	// 未知状态
	ServerRoleUnknown ServerRole = iota
	// 从节点
	ServerRoleSlave
	// 主节点
	ServerRoleMaster
)

type MessageType uint32

const (
	MessageTypeUnknown      MessageType = iota
	MessageTypePing                     // master节点发送ping
	MessageTypeVoteRequest              // 投票请求
	MessageTypeVoteResponse             // 投票返回
)

func (m MessageType) Uint32() uint32 {

	return uint32(m)
}

type PingRequest struct {
	Epoch uint32 // 选举周期
}

func (p *PingRequest) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(p.Epoch)
	return enc.Bytes(), nil
}

func (p *PingRequest) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if p.Epoch, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}

// VoteRequest 投票请求
type VoteRequest struct {
	Epoch uint32 // 选举周期
}

func (v *VoteRequest) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(v.Epoch)
	return enc.Bytes(), nil
}

func (f *VoteRequest) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if f.Epoch, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}

// VoteRespose 投票请求
type VoteRespose struct {
	Epoch uint32 // 选举周期
}

func (v *VoteRespose) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(v.Epoch)
	return enc.Bytes(), nil
}

func (f *VoteRespose) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if f.Epoch, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}
