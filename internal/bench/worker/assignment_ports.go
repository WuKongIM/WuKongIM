package worker

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
)

// WorkloadRunner receives worker lifecycle hooks for assigned benchmark shards.
type WorkloadRunner interface {
	// Prepare prepares target-side benchmark data for the active assignment.
	Prepare(ctx context.Context, assignment Assignment) error
	// Connect establishes workload connections for the active assignment.
	Connect(ctx context.Context, assignment Assignment) error
	// Warmup runs warmup traffic for the active assignment.
	Warmup(ctx context.Context, assignment Assignment) error
	// Run runs measured traffic for the active assignment.
	Run(ctx context.Context, assignment Assignment) error
	// Cooldown drains workload state after measured traffic.
	Cooldown(ctx context.Context, assignment Assignment) error
}

// MetricsReporter exposes worker-local metrics collected by a workload runner.
type MetricsReporter interface {
	// MetricsSnapshot returns a JSON-friendly worker-local metrics snapshot.
	MetricsSnapshot() metrics.SnapshotData
}

// ConnectionStatusReporter exposes live online connection state for bounded diagnostics.
type ConnectionStatusReporter interface {
	// ConnectionStatus returns the latest active connection count and reconnect churn.
	ConnectionStatus() (activeUsers int, reconnectedUsers uint64)
}

// LifecycleStatusReporter exposes one bounded worker-wide status projection
// for periodic local baseline evidence. Implementations must not include
// per-connection or per-message identities.
type LifecycleStatusReporter interface {
	// LifecycleStatus returns one coherent connection and traffic lifecycle snapshot.
	LifecycleStatus() LifecycleStatus
}

// AssignmentStarter receives a hook when the control plane accepts a fresh run assignment.
type AssignmentStarter interface {
	// BeginAssignment resets per-run runner state before any phase hook executes.
	BeginAssignment(assignment Assignment)
}

// AssignmentStopper releases resources owned by a terminal worker assignment.
type AssignmentStopper interface {
	// EndAssignment closes assignment-scoped connections and background work.
	// Implementations must be idempotent because stop requests may be retried.
	EndAssignment(assignment Assignment) error
}

// TerminalReceiveSealer closes only the assignment's receive readers after an
// acknowledged external terminal cut and proves that the live drained cut did
// not change across that stop boundary. Product sessions remain connected.
type TerminalReceiveSealer interface {
	SealTerminalReceive(ctx context.Context, assignment Assignment) error
}

// assignmentIdentity identifies one immutable worker assignment generation.
type assignmentIdentity struct {
	// runID identifies the parent benchmark run.
	runID string
	// assignmentID identifies one generation within runID.
	assignmentID string
}

func requiredAssignmentIdentity(runID, assignmentID string) (assignmentIdentity, error) {
	identity := assignmentIdentity{
		runID:        strings.TrimSpace(runID),
		assignmentID: strings.TrimSpace(assignmentID),
	}
	if identity.runID == "" {
		return assignmentIdentity{}, fmt.Errorf("run_id is required")
	}
	if identity.assignmentID == "" {
		return assignmentIdentity{}, fmt.Errorf("assignment_id is required")
	}
	return identity, nil
}

func (i assignmentIdentity) matches(assignment Assignment) bool {
	return assignmentIdentityMatches(assignment, i.runID, i.assignmentID)
}

func (i assignmentIdentity) isZero() bool {
	return i.runID == "" && i.assignmentID == ""
}

// TrafficResetter rebuilds traffic executors for an assignment without reconnecting sessions.
type TrafficResetter interface {
	// ResetTraffic applies assignment traffic changes while preserving existing connections.
	ResetTraffic(assignment Assignment) error
}

// TrafficRecoverer repairs failed sessions and rebuilds traffic executors.
type TrafficRecoverer interface {
	// RecoverTraffic applies recovery for cause while preserving healthy connections.
	RecoverTraffic(ctx context.Context, assignment Assignment, cause error) error
}
