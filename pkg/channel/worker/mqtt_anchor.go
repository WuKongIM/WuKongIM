package worker

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// QuorumMQTTAnchorTask owns a bounded current-copy admission request.
type QuorumMQTTAnchorTask struct{ Request ch.MQTTReplayAnchorRequest }

// QuorumMQTTAnchorResult contains a new or previously committed exact proof.
type QuorumMQTTAnchorResult struct{ Proof ch.MQTTReplayAnchorProof }

func runQuorumMQTTAnchor(ctx context.Context, deps Deps, t Task) Result {
	committer, ok := deps.QuorumLog.(ch.MQTTReplayAnchorCommitter)
	if !ok || t.QuorumMQTTAnchor == nil {
		return invalidResult(t)
	}
	proof, err := committer.CommitMQTTReplayAnchor(ctx, t.QuorumMQTTAnchor.Request)
	return Result{Kind: t.Kind, Fence: t.Fence, Err: err, QuorumMQTTAnchor: &QuorumMQTTAnchorResult{Proof: proof}}
}
