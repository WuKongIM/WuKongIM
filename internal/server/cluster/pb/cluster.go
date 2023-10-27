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

func (c *Cluster) Clonse() *Cluster {
	return proto.Clone(c).(*Cluster)
}
