package channels

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

const (
	persistedConversationMaxChannels = 200
	persistedConversationReadBatches = 16
	persistedConversationReadTimeout = 5 * time.Second
)

// readSelectedConversationHeads keeps persisted previews independent of quorum recovery.
// Admission is per serving node, shared by ingress and forwarded requests, with no waiting queue.
func (s *Service) readSelectedConversationHeads(ctx context.Context, uid string, requests []ConversationHeadRequest, persisted bool) []ConversationHeadResult {
	if !persisted {
		return s.readLocalConversationHeads(ctx, uid, requests)
	}
	results := make([]ConversationHeadResult, len(requests))
	if len(requests) == 0 {
		return results
	}
	fail := func(err error) []ConversationHeadResult {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	select {
	case s.persistedReads <- struct{}{}:
		defer func() { <-s.persistedReads }()
	default:
		return fail(ch.ErrBackpressured)
	}
	for i, req := range requests {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		results[i].Head, _, results[i].Err = s.readStoredConversationHead(ctx, req.ChannelID, uid, req.RetentionThroughSeq, req.ExpectedMinISR, 0, false, true, req.Badge)
		// Only metadata can prove deletion. Missing storage under valid metadata is a read failure.
		if errors.Is(results[i].Err, ch.ErrChannelNotFound) {
			results[i].Err = ch.ErrNotReady
		}
	}
	return results
}
