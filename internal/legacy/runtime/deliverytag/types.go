package deliverytag

import "time"

// DeliveryTag is a node-local subscriber snapshot for one tag incarnation.
type DeliveryTag struct {
	Key                             string
	ChannelKey                      string
	TagVersion                      uint64
	SubscriberMutationVersion       uint64
	SourceChannelKey                string
	SourceSubscriberMutationVersion uint64
	Topology                        PartitionTopologyVersion
	Partitions                      []NodePartition
	CreatedAt                       time.Time
	LastAccess                      time.Time
}

// PartitionForNode returns the subscriber partition assigned to nodeID.
func (t DeliveryTag) PartitionForNode(nodeID uint64) NodePartition {
	for _, partition := range t.Partitions {
		if partition.NodeID == nodeID {
			return partition.Clone()
		}
	}
	return NodePartition{NodeID: nodeID}
}

func (t DeliveryTag) clone() DeliveryTag {
	out := t
	out.Topology = t.Topology.Clone()
	out.Partitions = clonePartitions(t.Partitions)
	return out
}

// NodePartition stores the subscriber UIDs one node should process.
type NodePartition struct {
	NodeID uint64
	UIDs   []string
}

// Clone returns a deep copy of a node partition.
func (p NodePartition) Clone() NodePartition {
	out := p
	out.UIDs = append([]string(nil), p.UIDs...)
	return out
}

// TagRef is the channel-level fence used to validate a cached tag body.
type TagRef struct {
	ChannelKey                      string
	TagKey                          string
	TagVersion                      uint64
	SubscriberMutationVersion       uint64
	SourceChannelKey                string
	SourceSubscriberMutationVersion uint64
	Topology                        PartitionTopologyVersion
}

func refFromTag(tag DeliveryTag) TagRef {
	return TagRef{
		ChannelKey:                      tag.ChannelKey,
		TagKey:                          tag.Key,
		TagVersion:                      tag.TagVersion,
		SubscriberMutationVersion:       tag.SubscriberMutationVersion,
		SourceChannelKey:                tag.SourceChannelKey,
		SourceSubscriberMutationVersion: tag.SourceSubscriberMutationVersion,
		Topology:                        tag.Topology.Clone(),
	}
}

func (r TagRef) clone() TagRef {
	out := r
	out.Topology = r.Topology.Clone()
	return out
}

// SourceVersion is the current durable subscriber version of a derived source channel.
type SourceVersion struct {
	ChannelKey                string
	SubscriberMutationVersion uint64
}

// BuildRequest describes the authoritative material used to build a leader tag.
type BuildRequest struct {
	ChannelKey                      string
	SubscriberMutationVersion       uint64
	SourceChannelKey                string
	SourceSubscriberMutationVersion uint64
	Topology                        PartitionTopologyVersion
	Partitions                      []NodePartition
	MintFreshKey                    bool
}

func clonePartitions(in []NodePartition) []NodePartition {
	if len(in) == 0 {
		return nil
	}
	out := make([]NodePartition, 0, len(in))
	for _, partition := range in {
		out = append(out, partition.Clone())
	}
	return out
}
