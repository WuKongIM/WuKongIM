package pb

import "google.golang.org/protobuf/proto"

func (a *AuthReq) Marshal() ([]byte, error) {
	return proto.Marshal(a)
}

func (a *AuthReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, a)
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
