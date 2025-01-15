package types

import (
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"google.golang.org/protobuf/proto"
)

func (c *Config) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}
func (c *Config) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *Config) Clone() *Config {
	return proto.Clone(c).(*Config)
}

func (n *Node) Marshal() ([]byte, error) {
	return proto.Marshal(n)
}

func (n *Node) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, n)
}

func (n *Node) Clone() *Node {
	return proto.Clone(n).(*Node)
}

func (s *Slot) Clone() *Slot {
	return proto.Clone(s).(*Slot)
}

func (s *Slot) Marshal() ([]byte, error) {
	return proto.Marshal(s)
}

func (s *Slot) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, s)
}

// func (s *SlotMigrate) Equal(v *SlotMigrate) bool {
// 	if s.From != v.From {
// 		return false
// 	}
// 	if s.To != v.To {
// 		return false
// 	}
// 	if s.Slot != v.Slot {
// 		return false
// 	}
// 	if s.Status != v.Status {
// 		return false
// 	}
// 	return true

// }

func (s *Slot) Equal(v *Slot) bool {
	if s.Id != v.Id {
		return false
	}
	if len(s.Replicas) != len(v.Replicas) {
		return false
	}

	if len(s.Learners) != len(v.Learners) {
		return false
	}

	if s.MigrateFrom != v.MigrateFrom {
		return false
	}

	if s.MigrateTo != v.MigrateTo {
		return false
	}

	if s.Leader != v.Leader {
		return false
	}

	if s.ExpectLeader != v.ExpectLeader {
		return false
	}

	if s.Term != v.Term {
		return false
	}

	if s.Status != v.Status {
		return false
	}

	for _, replicaId := range s.Replicas {
		exist := false
		for _, rId := range v.Replicas {
			if replicaId == rId {
				exist = true
				break
			}
		}
		if !exist {
			return false
		}
	}

	for _, learnerId := range s.Learners {
		exist := false
		for _, lId := range v.Learners {
			if learnerId == lId {
				exist = true
				break
			}
		}
		if !exist {
			return false
		}
	}

	return true

}

func (n *Node) Equal(v *Node) bool {
	if n.Id != v.Id {
		return false
	}
	if n.ApiServerAddr != v.ApiServerAddr {
		return false
	}
	if n.Status != v.Status {
		return false
	}

	if n.Online != v.Online {
		return false
	}

	if n.AllowVote != v.AllowVote {
		return false
	}

	if n.ClusterAddr != v.ClusterAddr {
		return false
	}
	return true
}

type SlotSet []*Slot

func (s SlotSet) Marshal() ([]byte, error) {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	for _, slot := range s {
		slotData, err := slot.Marshal()
		if err != nil {
			return nil, err
		}
		encoder.WriteBinary(slotData)
	}

	return encoder.Bytes(), nil
}

func (s *SlotSet) Unmarshal(data []byte) error {
	decoder := wkproto.NewDecoder(data)
	for decoder.Len() > 0 {
		slotData, err := decoder.Binary()
		if err != nil {
			return err
		}
		slot := &Slot{}
		if err := slot.Unmarshal(slotData); err != nil {
			return err
		}
		*s = append(*s, slot)
	}
	return nil
}
