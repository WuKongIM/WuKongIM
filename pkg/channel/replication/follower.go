// Package replication contains v0 pull/ack planning helpers.
package replication

import ch "github.com/WuKongIM/WuKongIM/pkg/channel"

// FollowerPlan describes the next follower pull request.
type FollowerPlan struct {
	ChannelKey ch.ChannelKey
	NextOffset uint64
}
