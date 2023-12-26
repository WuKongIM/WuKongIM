package gossip

import (
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type CMDType uint32

const (
	CMDTypeUnknown             CMDType = iota
	CMDTypeFailoverAuthRequest         // 故障转移认证请求
	CMDTypeFailoverAuthRespose         // 故障转移认证响应
	CMDTypePingRquest                  // ping请求
)

func (c CMDType) Uint32() uint32 {
	return uint32(c)
}

type CMD struct {
	Type  CMDType
	Param []byte
}

func NewCMD(typ CMDType) *CMD {
	return &CMD{
		Type: typ,
	}
}
func NewCMDReqWithID(id uint64, typ CMDType) *CMD {
	return &CMD{
		Type: typ,
	}
}

// Encode Encode
func (c *CMD) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(c.Type.Uint32())
	enc.WriteBytes(c.Param)
	return enc.Bytes(), nil
}

func (c *CMD) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	var cmdType uint32
	if cmdType, err = dec.Uint32(); err != nil {
		return err
	}
	c.Type = CMDType(cmdType)
	if c.Param, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}

type FailoverAuthRequest struct {
	NodeID uint64 // 发起节点ID
	Epoch  uint32 // 选举周期
}

func (f *FailoverAuthRequest) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(f.NodeID)
	enc.WriteUint32(f.Epoch)
	return enc.Bytes(), nil
}

func (f *FailoverAuthRequest) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if f.NodeID, err = dec.Uint64(); err != nil {
		return err
	}
	if f.Epoch, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}

type FailoverAuthResponse struct {
	NodeID uint64 // 同意节点ID
}

func (f *FailoverAuthResponse) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(f.NodeID)
	return enc.Bytes(), nil
}

func (f *FailoverAuthResponse) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if f.NodeID, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type PingRequest struct {
	NodeID uint64
	Epoch  uint32
}

func (p *PingRequest) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(p.NodeID)
	enc.WriteUint32(p.Epoch)
	return enc.Bytes(), nil
}

func (p *PingRequest) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if p.NodeID, err = dec.Uint64(); err != nil {
		return err
	}
	if p.Epoch, err = dec.Uint32(); err != nil {
		return err
	}
	return nil
}
