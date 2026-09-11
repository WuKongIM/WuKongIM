package message

import (
	"context"
	"time"
)

const (
	messagePageScanChunk   = 1024
	messagePageScanWaves   = 64
	messagePageScanTimeout = 5 * time.Second
)

// CommittedMessageQuery describes one bounded scan after page semantics have
// been resolved. The adapter must preserve these bounds and scan ordering.
type CommittedMessageQuery struct {
	// MessageID and ClientMsgNo select one exact identity index, not a range.
	MessageID   uint64
	ClientMsgNo string
	// ChannelID identifies the canonical Channel whose committed log is read.
	ChannelID ChannelID
	// FromSeq is the inclusive scan starting sequence.
	FromSeq uint64
	// MinSeq and MaxSeq are inclusive scan bounds; zero leaves that bound unset.
	MinSeq uint64
	MaxSeq uint64
	// Limit and MaxBytes bound returned records and payload bytes.
	Limit    int
	MaxBytes int
	// Reverse selects descending storage iteration, not response ordering.
	Reverse bool
}

// CommittedMessageResult owns one scan's messages and payload bytes. The page
// reader may filter and reorder Messages; adapters must not retain mutable aliases.
// Flags.SyncOnce is preserved until page construction excludes command records.
type CommittedMessageResult struct {
	Messages []SyncedMessage
	// Err is scoped to this scan and preserves its underlying cause.
	Err error
}

// CommittedMessageReader reads committed records in request-aligned batches.
// It enforces cluster authority and retention without interpreting page intent.
type CommittedMessageReader interface {
	ReadCommittedMessages(context.Context, []CommittedMessageQuery) ([]CommittedMessageResult, error)
}

// PageReader owns compatible message-page semantics for ordinary sync and
// plugin reads. Authorization and response-specific enrichment belong to callers.
// It is stateless and retains no requests or messages between calls.
type PageReader struct {
	// committed executes bounded scans and transfers ownership of their results.
	committed CommittedMessageReader
}

// NewPageReader constructs page reads over an injected committed-record adapter.
func NewPageReader(committed CommittedMessageReader) *PageReader {
	return &PageReader{committed: committed}
}

var _ ChannelMessageReader = (*PageReader)(nil)
var _ ChannelMessageBatchReader = (*PageReader)(nil)

// SyncMessages uses the same bounded scan and page construction as batch reads.
func (r *PageReader) SyncMessages(ctx context.Context, query ChannelMessageQuery) (ChannelMessagePage, error) {
	results, err := r.SyncMessagesBatch(ctx, []ChannelMessageQuery{query})
	if err != nil {
		return ChannelMessagePage{}, err
	}
	if results[0].Err != nil {
		return ChannelMessagePage{}, results[0].Err
	}
	return results[0].Page, nil
}

// SyncMessagesBatch fills visible pages in bounded aligned read waves. Hidden
// records advance the raw cursor but never consume the caller's visible limit.
func (r *PageReader) SyncMessagesBatch(ctx context.Context, queries []ChannelMessageQuery) ([]ChannelMessageReadResult, error) {
	if r == nil || r.committed == nil {
		return nil, ErrMessageReaderRequired
	}
	ctx, cancel := context.WithTimeout(ctx, messagePageScanTimeout)
	defer cancel()
	results := make([]ChannelMessageReadResult, len(queries))
	states := make([]messagePageScan, len(queries))
	pending := make([]int, len(queries))
	for i, q := range queries {
		states[i].plan = planMessagePage(q)
		states[i].next = states[i].plan.scan
		pending[i] = i
	}
	for wave := 0; len(pending) > 0 && wave < messagePageScanWaves; wave++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reads := make([]CommittedMessageQuery, len(pending))
		for i, index := range pending {
			reads[i] = states[index].next
		}
		got, err := r.committed.ReadCommittedMessages(ctx, reads)
		if err != nil {
			return nil, err
		}
		if len(got) != len(reads) {
			return nil, ErrSyncBatchResultMismatch
		}
		remaining := pending[:0]
		for i, index := range pending {
			state := &states[index]
			if got[i].Err != nil {
				results[index].Err = got[i].Err
				state.kept = nil
				continue
			}
			done, err := state.consume(got[i].Messages)
			if err != nil {
				results[index].Err = err
				state.kept = nil
				continue
			}
			if done {
				results[index].Page = state.plan.page(state.kept)
				state.kept = nil
				continue
			}
			remaining = append(remaining, index)
		}
		pending = remaining
	}
	for _, index := range pending {
		results[index].Err = ErrSyncPageScanBudget
	}
	return results, nil
}

// messagePageScan retains at most limit+1 visible records and one raw cursor.
// Every continuation stays inside the original visibility and sequence bounds.
type messagePageScan struct {
	plan messagePagePlan
	next CommittedMessageQuery
	kept []SyncedMessage
}

func (s *messagePageScan) consume(rows []SyncedMessage) (bool, error) {
	q := s.next
	if len(rows) > q.Limit {
		return false, ErrSyncPageScanInvalid
	}
	if len(rows) == 0 {
		return true, nil
	}
	var previous uint64
	for i, m := range rows {
		seq := m.MessageSeq
		if seq == 0 || seq < q.MinSeq || (q.MaxSeq > 0 && seq > q.MaxSeq) ||
			(q.Reverse && (seq > q.FromSeq || (i > 0 && seq >= previous))) ||
			(!q.Reverse && (seq < q.FromSeq || (i > 0 && seq <= previous))) {
			return false, ErrSyncPageScanInvalid
		}
		previous = seq
		if (s.plan.excludeThroughSeq > 0 && seq <= s.plan.excludeThroughSeq) || (s.plan.excludeFromSeq > 0 && seq >= s.plan.excludeFromSeq) {
			return true, nil
		}
		if !m.Flags.SyncOnce {
			s.kept = append(s.kept, m)
		}
		if len(s.kept) > s.plan.limit {
			return true, nil
		}
	}
	if len(rows) < q.Limit {
		return true, nil
	}
	last := rows[len(rows)-1].MessageSeq
	if q.Reverse {
		if last <= 1 || last <= q.MinSeq || last-1 <= s.plan.excludeThroughSeq && s.plan.excludeThroughSeq > 0 {
			return true, nil
		}
		s.next.FromSeq = last - 1
		s.next.MaxSeq = last - 1
	} else {
		if last == ^uint64(0) || (q.MaxSeq > 0 && last >= q.MaxSeq) {
			return true, nil
		}
		s.next.FromSeq = last + 1
	}
	// Keep remote responses proportional to the remaining visible demand. A
	// hidden record must not amplify a small page into a full raw-history chunk.
	s.next.Limit = min(messagePageScanChunk, s.plan.limit+1-len(s.kept))
	return false, nil
}

// messagePagePlan keeps scan selection and response construction together so
// applying a visibility floor cannot change the meaning of a latest-page request.
type messagePagePlan struct {
	scan  CommittedMessageQuery
	limit int
	// Exclusive end bounds retain filtering after the bounded storage read.
	excludeThroughSeq uint64
	excludeFromSeq    uint64
}

// planMessagePage resolves page intent and visibility into one bounded scan
// while retaining the exclusive end needed for compatibility filtering.
func planMessagePage(query ChannelMessageQuery) messagePagePlan {
	limit := query.Limit
	if limit <= 0 {
		limit = 1
	}
	if limit > maxSyncMessagesLimit {
		limit = maxSyncMessagesLimit
	}
	latest := query.StartSeq == 0 && query.EndSeq == 0
	plan := messagePagePlan{limit: limit, scan: CommittedMessageQuery{
		ChannelID: query.ChannelID,
		FromSeq:   query.StartSeq,
		MinSeq:    query.MinSeq,
		MaxSeq:    ^uint64(0),
		Limit:     min(limit+1, messagePageScanChunk),
		MaxBytes:  int(^uint(0) >> 1),
		Reverse:   query.PullMode == PullModeDown || latest,
	}}
	if !latest && query.PullMode == PullModeUp && query.MinSeq > plan.scan.FromSeq {
		plan.scan.FromSeq = query.MinSeq
	}
	if query.PullMode == PullModeUp && query.EndSeq > 0 {
		plan.scan.MaxSeq = query.EndSeq - 1
		plan.excludeFromSeq = query.EndSeq
	} else if query.PullMode == PullModeDown {
		plan.excludeThroughSeq = query.EndSeq
		if query.StartSeq > 0 {
			plan.scan.MaxSeq = query.StartSeq
		}
	}
	if plan.scan.Reverse && plan.scan.FromSeq == 0 {
		plan.scan.FromSeq = ^uint64(0)
		plan.scan.MaxSeq = ^uint64(0)
	} else if !plan.scan.Reverse && plan.scan.FromSeq == 0 {
		plan.scan.FromSeq = 1
	}
	return plan
}

// page consumes owned scan results and applies the same direction and bounds
// that selected the scan, returning ordinary messages in ascending order.
func (p messagePagePlan) page(messages []SyncedMessage) ChannelMessagePage {
	if messages == nil {
		messages = []SyncedMessage{}
	}
	kept := messages[:0]
	for _, msg := range messages {
		if msg.Flags.SyncOnce ||
			(p.excludeThroughSeq > 0 && msg.MessageSeq <= p.excludeThroughSeq) ||
			(p.excludeFromSeq > 0 && msg.MessageSeq >= p.excludeFromSeq) {
			continue
		}
		kept = append(kept, msg)
	}
	// Only an additional visible record proves another page. Continuation has
	// already crossed any intervening hidden records before this projection.
	hasMore := len(kept) > p.limit
	if hasMore {
		kept = kept[:p.limit]
	}
	if p.scan.Reverse {
		for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
			kept[left], kept[right] = kept[right], kept[left]
		}
	}
	return ChannelMessagePage{Messages: kept, HasMore: hasMore}
}
