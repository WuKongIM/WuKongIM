package cluster

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// StepChannelMQTTReplayRecovery preserves foreground gates before one bounded
// target-owned step, including explicit source release and application of an
// already committed retirement. It grants no new retirement or migration authority.
func (n *Node) StepChannelMQTTReplayRecovery(ctx context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.MQTTReplayRecoveryResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.MQTTReplayRecoveryResult{}, err
	}
	if n.channels == nil {
		return ch.MQTTReplayRecoveryResult{}, ErrNotStarted
	}
	recovery, ok := n.channels.(ch.MQTTReplayRecoveryStepper)
	if !ok {
		return ch.MQTTReplayRecoveryResult{}, ch.ErrInvalidConfig
	}
	return recovery.StepMQTTReplayRecovery(ctx, q)
}
