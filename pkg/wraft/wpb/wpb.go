package wpb

import "google.golang.org/protobuf/proto"

func (c *ClusterConfig) Marshal() ([]byte, error) {

	return proto.Marshal(c)
}

func (c *ClusterConfig) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *ClusterConfig) Clone() *ClusterConfig {
	return proto.Clone(c).(*ClusterConfig)
}

func (p *Peer) Clone() *Peer {
	return proto.Clone(p).(*Peer)
}

func NewPeer(id uint64, addr string) *Peer {
	return &Peer{
		Id:   id,
		Addr: addr,
	}

}

func (p *Peer) Marshal() ([]byte, error) {

	return proto.Marshal(p)
}

func (p *Peer) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, p)
}
