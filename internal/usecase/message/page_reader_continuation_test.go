package message

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPageReaderLooksPastHiddenRecordsBeforeDeclaringEnd(t *testing.T) {
	f := &historyScanFixture{rows: []SyncedMessage{{MessageSeq: 16}, {MessageSeq: 17}, {MessageSeq: 18}, {MessageSeq: 19}, {MessageSeq: 20, Flags: MessageFlags{SyncOnce: true}}}}
	page, err := NewPageReader(f).SyncMessages(context.Background(), ChannelMessageQuery{Limit: 3, PullMode: PullModeDown})
	require.NoError(t, err)
	require.True(t, page.HasMore, "visible history exists beyond the filtered raw page")
	require.Equal(t, []uint64{17, 18, 19}, pageSequences(page))
}

func TestPageReaderCrossesEntireControlPages(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "ascending", true: "descending"}[reverse], func(t *testing.T) {
			f := &historyScanFixture{}
			for seq := uint64(1); seq <= 130; seq++ {
				hidden := seq != 130
				if reverse {
					hidden = seq != 1
				}
				f.rows = append(f.rows, SyncedMessage{MessageSeq: seq, Flags: MessageFlags{SyncOnce: hidden}})
			}
			q := ChannelMessageQuery{StartSeq: 1, Limit: 3, PullMode: PullModeUp}
			want := uint64(130)
			if reverse {
				q.StartSeq = 0
				q.PullMode = PullModeDown
				want = 1
			}
			page, err := NewPageReader(f).SyncMessages(context.Background(), q)
			require.NoError(t, err)
			require.False(t, page.HasMore)
			require.Equal(t, []uint64{want}, pageSequences(page))
			require.Greater(t, len(f.queries), 1)
		})
	}
}

func TestPageReaderLegacyCountPaginationDoesNotOmitOrRepeatHistory(t *testing.T) {
	f := &historyScanFixture{}
	expected := map[uint64]bool{}
	for seq := uint64(1); seq <= 219; seq++ {
		hidden := seq%7 == 0
		f.rows = append(f.rows, SyncedMessage{MessageSeq: seq, Flags: MessageFlags{SyncOnce: hidden}})
		if !hidden {
			expected[seq] = true
		}
	}
	seen := map[uint64]bool{}
	cursor := uint64(0)
	for pageNo := 0; pageNo < 30; pageNo++ {
		page, err := NewPageReader(f).SyncMessages(context.Background(), ChannelMessageQuery{StartSeq: cursor, Limit: 15, PullMode: PullModeDown})
		require.NoError(t, err)
		for _, m := range page.Messages {
			require.False(t, m.Flags.SyncOnce)
			require.False(t, seen[m.MessageSeq])
			seen[m.MessageSeq] = true
		}
		if len(page.Messages) < 15 {
			require.False(t, page.HasMore)
			break
		}
		require.NotEmpty(t, page.Messages)
		cursor = page.Messages[0].MessageSeq - 1
		if cursor == 0 {
			require.False(t, page.HasMore)
			break
		}
	}
	require.Equal(t, expected, seen)
}

func pageSequences(p ChannelMessagePage) []uint64 {
	out := make([]uint64, len(p.Messages))
	for i, m := range p.Messages {
		out[i] = m.MessageSeq
	}
	return out
}

// historyScanFixture models raw bounded storage scans; hidden entries consume
// the scan limit exactly as ordinary records do in the real adapter.
type historyScanFixture struct {
	rows    []SyncedMessage
	queries []MessageScanQuery
}

func (f *historyScanFixture) ReadCommittedMessages(ctx context.Context, queries []MessageScanQuery) ([]MessageScanResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.queries = append(f.queries, queries...)
	results := make([]MessageScanResult, len(queries))
	for i, q := range queries {
		visit := func(m SyncedMessage) {
			if m.MessageSeq < q.MinSeq || (q.MaxSeq > 0 && m.MessageSeq > q.MaxSeq) || (q.Reverse && m.MessageSeq > q.FromSeq) || (!q.Reverse && m.MessageSeq < q.FromSeq) || len(results[i].Messages) >= q.Limit {
				return
			}
			results[i].Messages = append(results[i].Messages, m)
		}
		if q.Reverse {
			for j := len(f.rows) - 1; j >= 0; j-- {
				visit(f.rows[j])
			}
		} else {
			for _, m := range f.rows {
				visit(m)
			}
		}
	}
	return results, nil
}

func TestPageReaderStopsAtVisibilityAndExclusiveRangeBounds(t *testing.T) {
	for _, mode := range []PullMode{PullModeUp, PullModeDown} {
		t.Run(map[PullMode]string{PullModeUp: "ascending", PullModeDown: "descending"}[mode], func(t *testing.T) {
			f := &historyScanFixture{}
			for seq := uint64(1); seq <= 140; seq++ {
				f.rows = append(f.rows, SyncedMessage{MessageSeq: seq, Flags: MessageFlags{SyncOnce: seq >= 10 && seq <= 130}})
			}
			q := ChannelMessageQuery{StartSeq: 10, EndSeq: 131, Limit: 3, PullMode: mode, MinSeq: 10}
			if mode == PullModeDown {
				q.StartSeq = 130
				q.EndSeq = 9
			}
			page, err := NewPageReader(f).SyncMessages(context.Background(), q)
			require.NoError(t, err)
			require.Empty(t, page.Messages)
			require.False(t, page.HasMore)
		})
	}
}

func TestPageReaderBoundsAllControlWorkWithoutFalseEnd(t *testing.T) {
	calls := 0
	f := scanFunction(func(ctx context.Context, qs []MessageScanQuery) ([]MessageScanResult, error) {
		calls++
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), messagePageScanTimeout)
		require.Len(t, qs, 1)
		q := qs[0]
		require.LessOrEqual(t, q.Limit, messagePageScanChunk)
		rows := make([]SyncedMessage, q.Limit)
		for i := range rows {
			rows[i] = SyncedMessage{MessageSeq: q.FromSeq + uint64(i), Flags: MessageFlags{SyncOnce: true}}
		}
		return []MessageScanResult{{Messages: rows}}, nil
	})
	_, err := NewPageReader(f).SyncMessages(context.Background(), ChannelMessageQuery{StartSeq: 1, Limit: 3, PullMode: PullModeUp})
	require.ErrorIs(t, err, ErrSyncPageScanBudget)
	require.Equal(t, messagePageScanWaves, calls)
}

func TestPageReaderContinuationPreservesBatchAlignmentAndFailures(t *testing.T) {
	calls := 0
	cause := errors.New("continuation read failed")
	f := scanFunction(func(_ context.Context, qs []MessageScanQuery) ([]MessageScanResult, error) {
		calls++
		if calls == 1 {
			require.Len(t, qs, 2)
			return []MessageScanResult{{Messages: []SyncedMessage{{MessageSeq: 1}}}, {Messages: []SyncedMessage{{MessageSeq: 3, Flags: MessageFlags{SyncOnce: true}}, {MessageSeq: 2, Flags: MessageFlags{SyncOnce: true}}}}}, nil
		}
		require.Len(t, qs, 1)
		require.Equal(t, "second", qs[0].ChannelID.ID)
		return []MessageScanResult{{Err: cause}}, nil
	})
	got, err := NewPageReader(f).SyncMessagesBatch(context.Background(), []ChannelMessageQuery{{ChannelID: ChannelID{ID: "first"}, Limit: 1}, {ChannelID: ChannelID{ID: "second"}, Limit: 1}})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.NoError(t, got[0].Err)
	require.Equal(t, []uint64{1}, pageSequences(got[0].Page))
	require.ErrorIs(t, got[1].Err, cause)
	require.Empty(t, got[1].Page.Messages)
}

func TestPageReaderRejectsRepeatedRawCursorAndHonorsCancellation(t *testing.T) {
	rows := []SyncedMessage{{MessageSeq: 4, Flags: MessageFlags{SyncOnce: true}}, {MessageSeq: 3, Flags: MessageFlags{SyncOnce: true}}}
	_, err := NewPageReader(scanFunction(func(context.Context, []MessageScanQuery) ([]MessageScanResult, error) {
		return []MessageScanResult{{Messages: rows}}, nil
	})).SyncMessages(context.Background(), ChannelMessageQuery{Limit: 1})
	require.ErrorIs(t, err, ErrSyncPageScanInvalid)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err = NewPageReader(scanFunction(func(context.Context, []MessageScanQuery) ([]MessageScanResult, error) {
		calls++
		cancel()
		return []MessageScanResult{{Messages: rows}}, nil
	})).SyncMessages(ctx, ChannelMessageQuery{Limit: 1})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}

type scanFunction func(context.Context, []MessageScanQuery) ([]MessageScanResult, error)

func (f scanFunction) ReadCommittedMessages(ctx context.Context, q []MessageScanQuery) ([]MessageScanResult, error) {
	return f(ctx, q)
}

// A remote frame budget applies before PageReader can discard surplus records.
// Simulate the default 64 MiB frame with 128 KiB records without allocating it.
func TestPageReaderDoesNotAmplifySmallPagePastRemoteFrameBudget(t *testing.T) {
	calls := 0
	frameTooLarge := errors.New("remote frame body exceeds 64 MiB")
	reader := scanFunction(func(_ context.Context, qs []MessageScanQuery) ([]MessageScanResult, error) {
		calls++
		q := qs[0]
		if q.Limit*(128<<10) > 64<<20 {
			return nil, frameTooLarge
		}
		rows := make([]SyncedMessage, q.Limit)
		for i := range rows {
			rows[i] = SyncedMessage{MessageSeq: q.FromSeq + uint64(i)}
		}
		if calls == 1 {
			rows[0].Flags.SyncOnce = true
		}
		return []MessageScanResult{{Messages: rows}}, nil
	})
	page, err := NewPageReader(reader).SyncMessages(context.Background(), ChannelMessageQuery{StartSeq: 1, Limit: 15, PullMode: PullModeUp})
	require.NoError(t, err)
	require.Len(t, page.Messages, 15)
	require.True(t, page.HasMore)
	require.Equal(t, 2, calls)
}
