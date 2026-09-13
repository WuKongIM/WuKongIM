package channels

import (
	"context"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

const persistedMessageBatchBytes = 8 << 20

// ReadPersistedBatch reads current-Leader disk tails without loading Channel runtimes.
// The wire shape is shared with history, but a distinct request kind selects LEO.
func (s *Service) ReadPersistedBatch(ctx context.Context, reads []CommittedRead) ([]CommittedReadResult, error) {
	if len(reads) > persistedConversationMaxChannels {
		return nil, ch.ErrInvalidConfig
	}
	ctx, cancel := context.WithTimeout(ctx, persistedConversationReadTimeout)
	defer cancel()
	return s.readMessageBatch(ctx, reads, true)
}

// readSelectedMessageBatch shares serving-node admission with persisted heads.
// All admitted storage work runs serially; overflow never queues or starts recovery.
func (s *Service) readSelectedMessageBatch(ctx context.Context, requests []CommittedReadRequest, persisted bool) []CommittedReadResult {
	if !persisted {
		return s.readLocalCommittedBatch(ctx, requests)
	}
	results := make([]CommittedReadResult, len(requests))
	if len(requests) == 0 {
		return results
	}
	outcome := "ok"
	fail := func(err error) []CommittedReadResult {
		outcome = "error"
		for i := range results {
			results[i] = CommittedReadResult{Err: err}
		}
		return results
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	started, admitted := s.beginPersistedRead("recents")
	if !admitted {
		return fail(ch.ErrBackpressured)
	}
	defer func() { s.endPersistedRead("recents", outcome, len(requests), started) }()
	used := 0
	for i, req := range requests {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		q := req.Request
		if q.MessageID != 0 || q.ClientMsgNo != "" || q.Limit <= 0 || q.Limit > 1024 || q.MaxBytes <= 0 || q.MaxBytes > 1<<20 {
			return fail(ch.ErrInvalidConfig)
		}
		results[i].Read, results[i].Err = s.readStoredMessages(ctx, req.CommittedRead, req.RetentionThroughSeq, req.ExpectedMinISR, 0, false, true)
		if results[i].Err != nil {
			outcome = "error"
		}
		if errors.Is(results[i].Err, ch.ErrChannelNotFound) {
			results[i].Err = ch.ErrNotReady
		}
		for _, msg := range results[i].Read.Messages {
			used += len(msg.Payload)
			if used > persistedMessageBatchBytes {
				failed := fail(ch.ErrBackpressured)
				outcome = "byte_budget"
				return failed
			}
		}
	}
	return results
}
