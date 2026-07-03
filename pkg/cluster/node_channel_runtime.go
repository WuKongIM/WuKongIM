package cluster

import (
	"context"

	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ChannelRuntimeSnapshot returns local Channel runtime state for benchmark controllers.
func (n *Node) ChannelRuntimeSnapshot(ctx context.Context) (channelv2.RuntimeSnapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.RuntimeSnapshot{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.RuntimeSnapshot{}, err
	}
	if n.channels == nil {
		return channelv2.RuntimeSnapshot{}, ErrNotStarted
	}
	out, err := n.channels.RuntimeSnapshot(ctx)
	if err != nil {
		return channelv2.RuntimeSnapshot{}, err
	}
	if out.NodeID == 0 {
		out.NodeID = channelv2.NodeID(n.cfg.NodeID)
	}
	return out, nil
}

// ChannelRuntimeProbe checks selected local Channel runtimes for benchmark controllers.
func (n *Node) ChannelRuntimeProbe(ctx context.Context, selector channelv2.RuntimeSelector) (channelv2.RuntimeProbeResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.RuntimeProbeResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.RuntimeProbeResult{}, err
	}
	if n.channels == nil {
		return channelv2.RuntimeProbeResult{}, ErrNotStarted
	}
	return n.channels.RuntimeProbe(ctx, selector)
}

// ChannelRuntimeEvict evicts selected local Channel runtimes for benchmark controllers.
func (n *Node) ChannelRuntimeEvict(ctx context.Context, selector channelv2.RuntimeSelector) (channelv2.RuntimeEvictResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.RuntimeEvictResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.RuntimeEvictResult{}, err
	}
	if n.channels == nil {
		return channelv2.RuntimeEvictResult{}, ErrNotStarted
	}
	return n.channels.RuntimeEvict(ctx, selector)
}
