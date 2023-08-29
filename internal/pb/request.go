package pb

import "google.golang.org/protobuf/proto"

func (a *AuthReq) Marshal() ([]byte, error) {
	return proto.Marshal(a)
}

func (a *AuthReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, a)
}

func (a *AuthReq) Conn() *Conn {
	return &Conn{
		Id:           a.ConnID,
		Uid:          a.Uid,
		DeviceFlag:   a.DeviceFlag,
		DeviceID:     a.DeviceID,
		GatewayID:    a.GatewayID,
		ProtoVersion: a.ProtoVersion,
	}
}

func (a *AuthResp) Marshal() ([]byte, error) {
	return proto.Marshal(a)
}

func (a *AuthResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, a)
}

func (c *ClientCloseReq) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ClientCloseReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (r *ResetReq) Marshal() ([]byte, error) {
	return proto.Marshal(r)
}

func (r *ResetReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, r)
}

func (c *Conn) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *Conn) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}
