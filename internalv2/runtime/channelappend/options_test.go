package channelappend

import "testing"

func TestDefaultAppendInflightBatchesPerChannelIsTen(t *testing.T) {
	opts := applyDefaults(Options{})

	if opts.AppendInflightBatchesPerChannel != 10 {
		t.Fatalf("AppendInflightBatchesPerChannel = %d, want 10", opts.AppendInflightBatchesPerChannel)
	}
}
