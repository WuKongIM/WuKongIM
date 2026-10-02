package worker

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// QuorumMQTTRetirementTask owns a bounded consumer-floor admission request.
type QuorumMQTTRetirementTask struct {
	Request ch.MQTTReplayRetirementRequest
}

// QuorumMQTTRetirementResult contains a new or previously committed exact proof.
type QuorumMQTTRetirementResult struct{ Proof ch.MQTTReplayRetirementProof }

func runQuorumMQTTRetirement(ctx context.Context, deps Deps, t Task) Result {
	committer, ok := deps.QuorumLog.(ch.MQTTReplayRetirementCommitter)
	if !ok || t.QuorumMQTTRetirement == nil {
		return invalidResult(t)
	}
	proof, err := committer.CommitMQTTReplayRetirement(ctx, t.QuorumMQTTRetirement.Request)
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, QuorumMQTTRetirement: &QuorumMQTTRetirementResult{Proof: proof}}
}
