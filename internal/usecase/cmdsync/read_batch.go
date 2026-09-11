package cmdsync

import (
	"context"
	"fmt"
)

// loadCommandBatch preserves the same per-channel boundaries while allowing
// the infrastructure adapter to group reads by current Slot and Channel owners.
func (a *App) loadCommandBatch(ctx context.Context, candidates []syncChannelCandidate, limit int) ([][]SyncedMessage, error) {
	if batch, ok := a.messages.(MessageBatchStore); ok {
		queries := make([]CommandMessageRead, len(candidates))
		for i, c := range candidates {
			queries[i] = CommandMessageRead{Key: c.key, FromSeq: c.fromSeq, Limit: limit}
		}
		result, err := batch.LoadCommandMessagesBatch(ctx, queries)
		if err != nil {
			return nil, err
		}
		if len(result) != len(queries) {
			return nil, fmt.Errorf("CMD batch returned %d results for %d channels", len(result), len(queries))
		}
		return result, nil
	}
	result := make([][]SyncedMessage, len(candidates))
	for i, c := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		messages, err := a.messages.LoadCommandMessages(ctx, c.key, c.fromSeq, limit)
		if err != nil {
			return nil, err
		}
		result[i] = messages
	}
	return result, nil
}
