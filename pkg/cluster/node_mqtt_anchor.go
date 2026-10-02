package cluster

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// CommitChannelMQTTReplayAnchor preserves foreground admission before fresh Slot
// routing and reactor-owned control commit. Its proof alone cannot release source.
func (n *Node) CommitChannelMQTTReplayAnchor(ctx context.Context, q ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.MQTTReplayAnchorProof{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.MQTTReplayAnchorProof{}, err
	}
	if n.channels == nil {
		return ch.MQTTReplayAnchorProof{}, ErrNotStarted
	}
	committer, ok := n.channels.(ch.MQTTReplayAnchorCommitter)
	if !ok {
		return ch.MQTTReplayAnchorProof{}, ch.ErrInvalidConfig
	}
	return committer.CommitMQTTReplayAnchor(ctx, q)
}
