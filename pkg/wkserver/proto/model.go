package proto

import "google.golang.org/protobuf/proto"

func (r *Request) Marshal() ([]byte, error) {
	return proto.Marshal(r)
}

func (r *Request) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, r)
}

func (r *Response) Marshal() ([]byte, error) {
	return proto.Marshal(r)
}

func (r *Response) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, r)
}
