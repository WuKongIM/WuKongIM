package planner

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// Planner produces the next durable ControllerV2 command for a state snapshot.
type Planner interface {
	Next(context.Context, View) (Decision, error)
}

// View is the immutable input used by planners to make one decision.
type View struct {
	State        state.ClusterState
	Observations RuntimeObservations
	Constraints  Constraints
	Now          time.Time
}

// RuntimeObservations is reserved for volatile runtime signals not stored in cluster-state.
type RuntimeObservations struct{}

// Constraints is reserved for caller-provided planning limits.
type Constraints struct{}

// DecisionKind identifies the result of one planner tick.
type DecisionKind string

const (
	// DecisionKindNone means the planner found no work to propose.
	DecisionKindNone DecisionKind = "none"
	// DecisionKindBlocked means work exists but cannot safely be planned yet.
	DecisionKindBlocked DecisionKind = "blocked"
	// DecisionKindCommand means Command contains a durable command to propose.
	DecisionKindCommand DecisionKind = "command"
)

// Decision is the planner output for one state snapshot.
type Decision struct {
	Kind    DecisionKind
	Reason  string
	Command command.Command
}
