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

func (p *Peer) Clone() *Peer {
	return proto.Clone(p).(*Peer)
}

func (s *SlotLeaderRelationSet) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *SlotLeaderRelationSet) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (j *JoinReq) Marshal() ([]byte, error) {
	return proto.Marshal(j)
}

func (j *JoinReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, j)
}

func (j *JoinDoneReq) Marshal() ([]byte, error) {
	return proto.Marshal(j)
}

func (j *JoinDoneReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, j)
}

func (s *SlotAddReplica) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

func (s *SlotAddReplica) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (c *Cluster) Clone() *Cluster {
	return proto.Clone(c).(*Cluster)
}
