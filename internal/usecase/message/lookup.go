package message

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

const maxLookupSelectors = 128
const maxLookupMessages = 1024
const maxLookupBytes = 16 << 20

// ErrLookupBounds reports invalid selectors or a result exceeding the bounded
// exact-query contract. Such failures never return a partial result.
var ErrLookupBounds = errors.New("invalid or excessive message lookup")

// LookupMessagesQuery selects exact messages within one user's visible channel.
// Selectors form a union; duplicates are returned only once in sequence order.
type LookupMessagesQuery struct {
	LoginUID     string
	ChannelID    string
	ChannelType  uint8
	MessageSeqs  []uint64
	MessageIDs   []uint64
	ClientMsgNos []string
}

// LookupMessages uses authority-routed indexes with the same membership and
// deleted/join boundaries as history reads. It never scans recent history as a
// substitute for a missing index and never returns an unchecked partial result.
func (a *App) LookupMessages(ctx context.Context, q LookupMessagesQuery) (SyncChannelMessagesResult, error) {
	count := len(q.MessageSeqs) + len(q.MessageIDs) + len(q.ClientMsgNos)
	if count == 0 || count > maxLookupSelectors {
		return SyncChannelMessagesResult{}, ErrLookupBounds
	}
	for _, s := range q.MessageSeqs {
		if s == 0 {
			return SyncChannelMessagesResult{}, ErrLookupBounds
		}
	}
	for _, id := range q.MessageIDs {
		if id == 0 {
			return SyncChannelMessagesResult{}, ErrLookupBounds
		}
	}
	for _, key := range q.ClientMsgNos {
		if strings.TrimSpace(key) == "" || len(key) > 1024 {
			return SyncChannelMessagesResult{}, ErrLookupBounds
		}
	}
	prepared, err := a.prepareSyncChannelMessages(ctx, SyncChannelMessagesQuery{LoginUID: q.LoginUID, ChannelID: q.ChannelID, ChannelType: q.ChannelType})
	if err != nil {
		return SyncChannelMessagesResult{}, err
	}
	out := SyncChannelMessagesResult{Messages: []SyncedMessage{}}
	if prepared.empty {
		return out, nil
	}
	if a.lookupReader == nil {
		return out, ErrMessageReaderRequired
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	reads := make([]CommittedMessageQuery, 0, count)
	base := CommittedMessageQuery{ChannelID: prepared.query.ChannelID, MinSeq: prepared.query.MinSeq, Limit: maxLookupMessages, MaxBytes: maxLookupBytes}
	for _, seq := range q.MessageSeqs {
		r := base
		r.FromSeq = seq
		r.MinSeq = max(r.MinSeq, seq)
		r.MaxSeq = seq
		r.Limit = 1
		if r.MinSeq <= seq {
			reads = append(reads, r)
		}
	}
	for _, id := range q.MessageIDs {
		r := base
		r.MessageID = id
		reads = append(reads, r)
	}
	for _, key := range q.ClientMsgNos {
		r := base
		r.ClientMsgNo = key
		reads = append(reads, r)
	}
	seen := make(map[uint64]struct{})
	used := 0
	// Serial bounded index reads prevent selector count from multiplying peak
	// payload allocation. Routing stays in the same committed-read cluster seam.
	for _, r := range reads {
		got, err := a.lookupReader.ReadCommittedMessages(ctx, []CommittedMessageQuery{r})
		if err != nil {
			return SyncChannelMessagesResult{}, err
		}
		if len(got) != 1 {
			return SyncChannelMessagesResult{}, ErrSyncBatchResultMismatch
		}
		if got[0].Err != nil {
			return SyncChannelMessagesResult{}, got[0].Err
		}
		for _, m := range got[0].Messages {
			if m.ChannelID != r.ChannelID.ID || m.ChannelType != r.ChannelID.Type || m.MessageSeq < r.MinSeq || r.MaxSeq != 0 && m.MessageSeq > r.MaxSeq || r.MessageID != 0 && m.MessageID != r.MessageID || r.ClientMsgNo != "" && m.ClientMsgNo != r.ClientMsgNo {
				return SyncChannelMessagesResult{}, ErrSyncBatchResultMismatch
			}
			if m.Flags.SyncOnce {
				continue
			}
			if _, ok := seen[m.MessageSeq]; ok {
				continue
			}
			seen[m.MessageSeq] = struct{}{}
			used += len(m.Payload)
			if len(out.Messages) >= maxLookupMessages || used > maxLookupBytes {
				return SyncChannelMessagesResult{}, ErrLookupBounds
			}
			out.Messages = append(out.Messages, m)
		}
	}
	sort.Slice(out.Messages, func(i, j int) bool { return out.Messages[i].MessageSeq < out.Messages[j].MessageSeq })
	return out, nil
}
