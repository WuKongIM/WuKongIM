package cluster

import (
	"context"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ChannelRuntimeSnapshot returns local Channel runtime state for benchmark controllers.
func (n *Node) ChannelRuntimeSnapshot(ctx context.Context) (channelruntime.RuntimeSnapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return channelruntime.RuntimeSnapshot{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelruntime.RuntimeSnapshot{}, err
	}
	if n.channels == nil {
		return channelruntime.RuntimeSnapshot{}, ErrNotStarted
	}
	out, err := n.channels.RuntimeSnapshot(ctx)
	if err != nil {
		return channelruntime.RuntimeSnapshot{}, err
	}
	if out.NodeID == 0 {
		out.NodeID = channelruntime.NodeID(n.cfg.NodeID)
	}
	return out, nil
}

// ChannelRuntimeProbe checks selected local Channel runtimes for benchmark controllers.
func (n *Node) ChannelRuntimeProbe(ctx context.Context, selector channelruntime.RuntimeSelector) (channelruntime.RuntimeProbeResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelruntime.RuntimeProbeResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelruntime.RuntimeProbeResult{}, err
	}
	if n.channels == nil {
		return channelruntime.RuntimeProbeResult{}, ErrNotStarted
	}
	return n.channels.RuntimeProbe(ctx, selector)
}

// ChannelRuntimeEvict evicts selected local Channel runtimes for benchmark controllers.
func (n *Node) ChannelRuntimeEvict(ctx context.Context, selector channelruntime.RuntimeSelector) (channelruntime.RuntimeEvictResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelruntime.RuntimeEvictResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelruntime.RuntimeEvictResult{}, err
	}
	if n.channels == nil {
		return channelruntime.RuntimeEvictResult{}, ErrNotStarted
	}
	return n.channels.RuntimeEvict(ctx, selector)
}
