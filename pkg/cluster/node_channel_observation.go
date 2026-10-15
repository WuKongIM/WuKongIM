package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

// ObserveLocalChannels returns bounded, non-activating local Channel evidence.
func (n *Node) ObserveLocalChannels(ctx context.Context, targets []channels.RuntimeObservationTarget, includeMaxMessageSeq bool) ([]channels.RuntimeObservation, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	observer, ok := n.channels.(interface {
		ObserveLocalChannels(context.Context, []channels.RuntimeObservationTarget, bool) ([]channels.RuntimeObservation, error)
	})
	if !ok {
		return nil, ErrNotStarted
	}
	return observer.ObserveLocalChannels(ctx, targets, includeMaxMessageSeq)
}
