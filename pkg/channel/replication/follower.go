// Package replication owns Channel durable-quorum commit and bounded replica
// recovery. The existing pull planning helpers remain only during migration.
package replication

import ch "github.com/WuKongIM/WuKongIM/pkg/channel"

// FollowerPlan describes the next follower pull request.
type FollowerPlan struct {
	ChannelKey ch.ChannelKey
	NextOffset uint64
}
