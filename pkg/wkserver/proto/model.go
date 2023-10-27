package proto

import "google.golang.org/protobuf/proto"

func (c *Connect) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *Connect) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *Connack) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *Connack) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

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

func (m *Message) Marshal() ([]byte, error) {
	return proto.Marshal(m)
}

func (m *Message) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, m)
}
