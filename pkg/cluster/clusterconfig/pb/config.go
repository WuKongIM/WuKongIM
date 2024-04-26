package pb

import (
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

func (n *Node) Clone() *Node {
	return proto.Clone(n).(*Node)
}

func (s *Slot) Clone() *Slot {
	return proto.Clone(s).(*Slot)
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

	if s.Leader != v.Leader {
		return false
	}

	if s.LeaderTransferTo != v.LeaderTransferTo {
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
