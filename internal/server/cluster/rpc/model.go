package rpc

import "google.golang.org/protobuf/proto"

func (c *ConnectReq) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ConnectReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ConnectResp) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ConnectResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ConnectWriteReq) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ConnectWriteReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ConnPingReq) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ConnPingReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ForwardSendPacketReq) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ForwardSendPacketReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ForwardSendPacketResp) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ForwardSendPacketResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (s *SendSyncProposeReq) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *SendSyncProposeReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (s *SendSyncProposeResp) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *SendSyncProposeResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (f *ForwardRecvPacketReq) Marshal() ([]byte, error) {
	return proto.Marshal(f)
}

func (f *ForwardRecvPacketReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, f)
}

func (r *RecvacksReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, r)
}

func (r *RecvacksReq) Marshal() ([]byte, error) {
	return proto.Marshal(r)
}
