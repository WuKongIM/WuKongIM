package channelappend

import (
	"testing"
	"time"
)

func TestDefaultAppendInflightBatchesPerChannelIsTen(t *testing.T) {
	opts := applyDefaults(Options{})

	if opts.AppendInflightBatchesPerChannel != 10 {
		t.Fatalf("AppendInflightBatchesPerChannel = %d, want 10", opts.AppendInflightBatchesPerChannel)
	}
}

func TestDefaultPostCommitHandoffCapacityUsesLargerSafetyBound(t *testing.T) {
	fromPool := applyDefaults(Options{EffectPoolSize: 2000, ChannelBacklogHighWatermark: 1024})
	if fromPool.PostCommitHandoffCapacity != 2000*commitBatchMaxEvents {
		t.Fatalf("pool-derived PostCommitHandoffCapacity = %d, want %d", fromPool.PostCommitHandoffCapacity, 2000*commitBatchMaxEvents)
	}

	fromBacklog := applyDefaults(Options{EffectPoolSize: 1, ChannelBacklogHighWatermark: 4096})
	if fromBacklog.PostCommitHandoffCapacity != 4096 {
		t.Fatalf("backlog-derived PostCommitHandoffCapacity = %d, want 4096", fromBacklog.PostCommitHandoffCapacity)
	}
}

func TestDefaultInboxCoalesceOptionsAreConservative(t *testing.T) {
	opts := applyDefaults(Options{})

	if opts.InboxCoalesceWindow != 250*time.Microsecond {
		t.Fatalf("InboxCoalesceWindow = %s, want 250µs", opts.InboxCoalesceWindow)
	}
	if opts.InboxCoalesceMaxItems != 16 {
		t.Fatalf("InboxCoalesceMaxItems = %d, want 16", opts.InboxCoalesceMaxItems)
	}
}

func TestNegativeInboxCoalesceOptionsDisableCoalescing(t *testing.T) {
	opts := applyDefaults(Options{InboxCoalesceWindow: -time.Nanosecond})

	if opts.InboxCoalesceWindow != 0 {
		t.Fatalf("InboxCoalesceWindow = %s, want disabled zero", opts.InboxCoalesceWindow)
	}
	if opts.InboxCoalesceMaxItems != 0 {
		t.Fatalf("InboxCoalesceMaxItems = %d, want disabled zero", opts.InboxCoalesceMaxItems)
	}

	opts = applyDefaults(Options{InboxCoalesceMaxItems: -1})
	if opts.InboxCoalesceWindow != 0 {
		t.Fatalf("InboxCoalesceWindow with negative max = %s, want disabled zero", opts.InboxCoalesceWindow)
	}
	if opts.InboxCoalesceMaxItems != 0 {
		t.Fatalf("InboxCoalesceMaxItems with negative max = %d, want disabled zero", opts.InboxCoalesceMaxItems)
	}
}
