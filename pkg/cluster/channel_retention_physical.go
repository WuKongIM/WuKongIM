package cluster

import (
	"context"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ChannelRetentionGCResult summarizes one bounded local physical retention pass.
type ChannelRetentionGCResult struct {
	// ScannedChannels is the number of local message catalog entries inspected.
	ScannedChannels int
	// AppliedChannels is the number of channels that received a retention apply request.
	AppliedChannels int
	// TrimmedChannels is the number of channels where at least one message row was removed.
	TrimmedChannels int
	// BlockedChannels is the number of channels whose logical boundary was adopted but physical trim was blocked.
	BlockedChannels int
	// DeletedMessages is the total number of message rows removed in this pass.
	DeletedMessages int
	// More reports whether the next pass should continue from the stored catalog cursor.
	More bool
	// Errors counts per-channel failures after the catalog page was read.
	Errors int
}

// ChannelRetentionView returns local Channel retention state.
func (n *Node) ChannelRetentionView(ctx context.Context, id channelruntime.ChannelID) (channelruntime.RetentionView, error) {
	if err := ctxErr(ctx); err != nil {
		return channelruntime.RetentionView{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelruntime.RetentionView{}, err
	}
	if n.channels == nil {
		return channelruntime.RetentionView{}, ErrNotStarted
	}
	return n.channels.RetentionView(ctx, id)
}

// ApplyChannelRetentionBoundary adopts a local logical boundary and attempts bounded physical cleanup.
func (n *Node) ApplyChannelRetentionBoundary(ctx context.Context, id channelruntime.ChannelID, throughSeq uint64, opts channelruntime.RetentionApplyOptions) (channelruntime.RetentionApplyResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelruntime.RetentionApplyResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelruntime.RetentionApplyResult{}, err
	}
	if n.channels == nil {
		return channelruntime.RetentionApplyResult{}, ErrNotStarted
	}
	return n.channels.ApplyRetentionBoundary(ctx, channelruntime.RetentionApplyRequest{
		ChannelID:  id,
		ThroughSeq: throughSeq,
		Options:    opts,
	})
}

// RunChannelRetentionGCOnce scans one local channel catalog page and applies committed retention boundaries.
func (n *Node) RunChannelRetentionGCOnce(ctx context.Context) (ChannelRetentionGCResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ChannelRetentionGCResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ChannelRetentionGCResult{}, err
	}
	if n.defaultChannelStore == nil || n.defaultSlotMetaDB == nil || n.channels == nil {
		return ChannelRetentionGCResult{}, ErrNotStarted
	}

	n.channelRetentionGCMu.Lock()
	defer n.channelRetentionGCMu.Unlock()

	entries, cursor, more, err := n.defaultChannelStore.ListChannelsPage(ctx, n.channelRetentionCursor, n.cfg.ChannelRetention.ChannelBatchSize)
	if err != nil {
		return ChannelRetentionGCResult{}, err
	}
	result := ChannelRetentionGCResult{ScannedChannels: len(entries), More: more}
	opts := channelruntime.RetentionApplyOptions{
		MaxTrimMessages: n.cfg.ChannelRetention.MaxTrimMessages,
		MaxTrimBytes:    n.cfg.ChannelRetention.MaxTrimBytes,
	}
	for _, entry := range entries {
		meta, err := n.GetChannelRuntimeMeta(ctx, entry.ID.ID, int64(entry.ID.Type))
		if err != nil {
			result.Errors++
			continue
		}
		if meta.RetentionThroughSeq == 0 {
			continue
		}
		applied, err := n.ApplyChannelRetentionBoundary(ctx, entry.ID, meta.RetentionThroughSeq, opts)
		if err != nil {
			result.Errors++
			continue
		}
		result.AppliedChannels++
		if applied.Deleted > 0 {
			result.TrimmedChannels++
			result.DeletedMessages += applied.Deleted
		}
		if applied.BlockedReason != "" {
			result.BlockedChannels++
		}
	}
	if more {
		n.channelRetentionCursor = cursor
	} else {
		n.channelRetentionCursor = ""
	}
	return result, nil
}
