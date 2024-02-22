package pb

import "google.golang.org/protobuf/proto"

func (c *Config) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}
func (c *Config) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *Config) Clone() *Config {
	return proto.Clone(c).(*Config)
}

func (n *Node) Clone() *Node {
	return proto.Clone(n).(*Node)
}

func (s *Slot) Clone() *Slot {
	return proto.Clone(s).(*Slot)
}
