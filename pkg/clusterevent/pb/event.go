package pb

import "google.golang.org/protobuf/proto"

func (c *Cluster) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}
func (c *Cluster) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (n *Node) Marshal() ([]byte, error) {
	return proto.Marshal(n)
}
func (n *Node) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, n)
}

type NodeSlice []*Node

func (n NodeSlice) Len() int {
	return len(n)
}

func (n NodeSlice) Less(i, j int) bool {
	return n[i].Id < n[j].Id
}

func (n NodeSlice) Swap(i, j int) {
	n[i], n[j] = n[j], n[i]
}
