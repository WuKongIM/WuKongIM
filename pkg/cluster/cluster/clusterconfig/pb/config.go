package pb

import "google.golang.org/protobuf/proto"

func (c *Config) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}
func (c *Config) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}
