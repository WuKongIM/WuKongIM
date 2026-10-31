package chatlifecycle

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

// workerFullSyncConcurrency keeps the validated three-worker cohort at at most
// twelve complete HTTP syncs, below one target node's sixteen-page admission.
// CONNECT and SEND retain their independent original concurrency and clocks.
const workerFullSyncConcurrency = 4

// workerConversationSyncer owns only generation-local HTTP admission. Waiting
// callers already occupy the SessionPool's bounded Starting capacity; this
// adapter creates no queue, goroutine, retry, transport or retained UID history.
type workerConversationSyncer struct {
	next ConversationSyncer
	// admission remains held through every page and existing endpoint fallback.
	admission chan struct{}
}

func (s *workerConversationSyncer) ConversationSync(ctx context.Context, req target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.admission <- struct{}{}:
		defer func() { <-s.admission }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Cancellation and a newly released slot can become ready together. Never
	// dispatch canceled work merely because select chose the admission branch.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.next.ConversationSync(ctx, req)
}
