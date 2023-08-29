package pb

import "google.golang.org/protobuf/proto"

func (c *ClusterConfigSet) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ClusterConfigSet) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ClusterConfig) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ClusterConfig) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}
