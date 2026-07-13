package app

import (
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

const (
	channelRuntimeLeaderReported uint8 = 1 << iota
	channelRuntimeFollowerReported
	channelRuntimeRolesReported = channelRuntimeLeaderReported | channelRuntimeFollowerReported
)

// channelRuntimeSummary is the low-cardinality active Channel runtime view for
// one node.
type channelRuntimeSummary struct {
	// ActiveTotal is the sum of loaded leader and follower runtimes.
	ActiveTotal int
	// ActiveLeader is the number of loaded leader runtimes.
	ActiveLeader int
	// ActiveFollower is the number of loaded follower runtimes.
	ActiveFollower int
	// Unknown means at least one expected reactor role count is unavailable.
	Unknown bool
}

// channelRuntimeReactorCounts stores the latest low-cardinality report from one reactor.
type channelRuntimeReactorCounts struct {
	// leader is the latest active leader runtime count reported by the reactor.
	leader int
	// follower is the latest active follower runtime count reported by the reactor.
	follower int
	// reported records which role counts have been observed at least once.
	reported uint8
}

// channelRuntimeSummaryCollector aggregates per-reactor counts without
// scanning loaded Channel runtimes during manager reads.
type channelRuntimeSummaryCollector struct {
	// mu protects topology and per-reactor reports across reactor and manager goroutines.
	mu sync.RWMutex
	// reactorCount is the fixed number of reactor partitions expected in a complete snapshot.
	reactorCount int
	// reactors stores only one low-cardinality count pair per reactor partition.
	reactors map[int]channelRuntimeReactorCounts
}

// newChannelRuntimeSummaryCollector creates an empty collector whose snapshot
// remains unknown until the reactor topology and all role counts arrive.
func newChannelRuntimeSummaryCollector() *channelRuntimeSummaryCollector {
	return &channelRuntimeSummaryCollector{reactors: make(map[int]channelRuntimeReactorCounts)}
}

// SetChannelRuntimeReactorCount resets the expected topology before per-reactor
// runtime counts are reported.
func (c *channelRuntimeSummaryCollector) SetChannelRuntimeReactorCount(count int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reactorCount = count
	c.reactors = make(map[int]channelRuntimeReactorCounts, max(count, 0))
}

// SetChannelRuntimeCount records the latest count for one reactor and role.
func (c *channelRuntimeSummaryCollector) SetChannelRuntimeCount(reactorID int, role ch.Role, count int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if reactorID < 0 || reactorID >= c.reactorCount {
		return
	}
	current := c.reactors[reactorID]
	switch role {
	case ch.RoleLeader:
		current.leader = count
		current.reported |= channelRuntimeLeaderReported
	case ch.RoleFollower:
		current.follower = count
		current.reported |= channelRuntimeFollowerReported
	default:
		return
	}
	c.reactors[reactorID] = current
}

// Snapshot returns a known aggregate only after every reactor has reported both
// leader and follower counts.
func (c *channelRuntimeSummaryCollector) Snapshot() channelRuntimeSummary {
	if c == nil {
		return channelRuntimeSummary{Unknown: true}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.reactorCount <= 0 || len(c.reactors) < c.reactorCount {
		return channelRuntimeSummary{Unknown: true}
	}
	summary := channelRuntimeSummary{}
	for reactorID := 0; reactorID < c.reactorCount; reactorID++ {
		current, ok := c.reactors[reactorID]
		if !ok || current.reported != channelRuntimeRolesReported {
			return channelRuntimeSummary{Unknown: true}
		}
		summary.ActiveLeader += current.leader
		summary.ActiveFollower += current.follower
	}
	summary.ActiveTotal = summary.ActiveLeader + summary.ActiveFollower
	return summary
}

func (c *channelRuntimeSummaryCollector) SetReactorMailboxDepth(int, string, int) {}

func (c *channelRuntimeSummaryCollector) SetWorkerQueueDepth(string, int) {}

func (c *channelRuntimeSummaryCollector) ObserveAppendBatch(int, int, time.Duration) {}

func (c *channelRuntimeSummaryCollector) ObserveAppendLatency(ch.CommitMode, time.Duration) {}

func (c *channelRuntimeSummaryCollector) ObserveWorkerResult(worker.TaskKind, error, time.Duration) {}

func (c *channelRuntimeSummaryCollector) ObserveChannelActivationRejected(string) {}

var _ reactor.Observer = (*channelRuntimeSummaryCollector)(nil)
var _ reactor.RuntimeObserver = (*channelRuntimeSummaryCollector)(nil)
var _ reactor.RuntimeTopologyObserver = (*channelRuntimeSummaryCollector)(nil)
