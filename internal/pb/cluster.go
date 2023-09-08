package pb

import "google.golang.org/protobuf/proto"

func (c *ClusterConfigSet) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *ClusterConfigSet) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (c *PartitionConfig) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}

func (c *PartitionConfig) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (r *JoinReq) Marshal() ([]byte, error) {
	return proto.Marshal(r)
}

func (r *JoinReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, r)
}

func (c *SyncClustersetConfigReq) Marshal() ([]byte, error) {
	return proto.Marshal(c)
}
func (c *SyncClustersetConfigReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, c)
}

func (p *Ping) Marshal() ([]byte, error) {
	return proto.Marshal(p)
}
func (p *Ping) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, p)
}

func (d *DiceResult) Marshal() ([]byte, error) {
	return proto.Marshal(d)
}

func (d *DiceResult) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, d)
}

func (v *VoteReq) Marshal() ([]byte, error) {
	return proto.Marshal(v)
}
func (v *VoteReq) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, v)
}

func (v *VoteResp) Marshal() ([]byte, error) {
	return proto.Marshal(v)
}

func (v *VoteResp) Unmarshal(data []byte) error {
	return proto.Unmarshal(data, v)
}
