package replication

import ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"

// AckPlan describes follower progress reported to a leader.
type AckPlan struct {
	Follower ch.NodeID
	Match    uint64
}
