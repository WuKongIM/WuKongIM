package wpb

import "google.golang.org/protobuf/proto"

func (r *CMDReq) Marshal() ([]byte, error) {
	return proto.Marshal(r)
}

func (r *CMDReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, r)
}

func (r *CMDResp) Marshal() ([]byte, error) {
	return proto.Marshal(r)
}

func (r *CMDResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, r)
}
