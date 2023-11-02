package pb

import "google.golang.org/protobuf/proto"

func (a *AllocateSlot) Marshal() ([]byte, error) {

	return proto.Marshal(a)
}

func (a *AllocateSlot) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, a)
}

func (a *AllocateSlotSet) Marshal() ([]byte, error) {
	return proto.Marshal(a)
}

func (a *AllocateSlotSet) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, a)
}

func (p *PeerSet) Marshal() ([]byte, error) {
	return proto.Marshal(p)
}

func (p *PeerSet) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, p)
}

func (c *Cluster) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}
func (c *Cluster) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (p *Peer) Marshal() ([]byte, error) {
	return proto.Marshal(p)
}
func (p *Peer) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, p)
}

func (s *SlotLeaderRelationSet) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *SlotLeaderRelationSet) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (c *Cluster) Clone() *Cluster {
	return proto.Clone(c).(*Cluster)
}
