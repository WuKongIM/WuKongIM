package channelwrite

import (
	"sync"
	"sync/atomic"
)

// channelWriter is the single-writer state machine for one locally authoritative channel.
// Invariant: at most one goroutine advances a writer at a time (guarded by scheduled).
type channelWriter struct {
	key string

	// ports are the dependencies used to advance this writer's state machine.
	ports writerPorts

	// scheduled reports whether a worker is already queued to advance this writer.
	scheduled atomic.Bool

	// mu guards state, inbox, and the phase transitions inside advance.
	mu    sync.Mutex
	state *channelState
	// inbox holds submitted batches not yet drained into the channelState pending queue.
	inbox []submittedBatch
}

// submittedBatch is one admitted SubmitLocal call awaiting prepare+append.
type submittedBatch struct {
	target AuthorityTarget
	items  []SendBatchItem
	future *Future
}

// writerPorts are the dependencies a writer needs to advance its state machine.
type writerPorts struct {
	prepare  preparePorts
	append   appendPorts
	commit   commitPorts
	pool     *workerPool
	schedule func(*channelWriter)
}

func newChannelWriter(target AuthorityTarget, limits channelStateLimits) *channelWriter {
	return &channelWriter{
		key:   targetKey(target),
		state: newChannelState(target, limits),
	}
}
