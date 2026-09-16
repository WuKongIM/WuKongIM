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
	// PersistedConversationReadTimeout bounds metadata routing and disk previews.
	PersistedConversationReadTimeout = 5 * time.Second
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
	outcome := "ok"
	fail := func(err error) []ConversationHeadResult {
		outcome = "error"
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	started, admitted := s.beginPersistedRead("heads")
	if !admitted {
		return fail(ch.ErrBackpressured)
	}
	defer func() { s.endPersistedRead("heads", outcome, len(requests), started) }()
	for i, req := range requests {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		results[i].Head, _, results[i].Err = s.readStoredConversationHead(ctx, req.ChannelID, uid, req.RetentionThroughSeq, req.ExpectedMinISR, 0, false, true, req.Badge)
		// Only metadata can prove deletion. Missing storage under valid metadata is a read failure.
		if results[i].Err != nil {
			outcome = "error"
		}
		if errors.Is(results[i].Err, ch.ErrChannelNotFound) {
			results[i].Err = ch.ErrNotReady
		}
	}
	return results
}
