package worker

import ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"

// Result is the common completion envelope for worker tasks.
type Result struct {
	Fence ch.Fence
	Err   error
	Value any
}

// CompletionSink receives worker completions for routing back to reactors.
type CompletionSink interface {
	Complete(Result)
}
