package message

import (
	"context"
	"errors"
	"testing"
)

func TestPageReaderDoesNotInventMoreWithoutVisibleLookahead(t *testing.T) {
	reader := &historyScanFixture{rows: []SyncedMessage{{MessageSeq: 17}, {MessageSeq: 18}, {MessageSeq: 19}, {MessageSeq: 20, Flags: MessageFlags{SyncOnce: true}}}}
	page, err := NewPageReader(reader).SyncMessages(context.Background(), ChannelMessageQuery{Limit: 3, PullMode: PullModeUp})
	if err != nil || page.HasMore || len(page.Messages) != 3 || page.Messages[0].MessageSeq != 17 || page.Messages[2].MessageSeq != 19 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if len(reader.queries) != 2 || reader.queries[0].Limit != 4 || !reader.queries[0].Reverse || reader.queries[1].FromSeq != 16 {
		t.Fatalf("queries=%+v, want bounded continuation below the raw frontier", reader.queries)
	}
}

func TestPageReaderKeepsBatchAlignmentAndErrorCauses(t *testing.T) {
	cause := errors.New("unavailable channel")
	fixture := &pageReadFixture{results: []MessageScanResult{{Messages: []SyncedMessage{{MessageSeq: 7}}}, {Err: cause}}}
	reader := NewPageReader(fixture)
	queries := []ChannelMessageQuery{{Limit: 1}, {Limit: 1}}
	results, err := reader.SyncMessagesBatch(context.Background(), queries)
	if err != nil || len(results) != 2 || results[0].Err != nil || results[0].Page.Messages[0].MessageSeq != 7 || results[1].Err != cause {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	fixture.results = fixture.results[1:]
	if _, err := reader.SyncMessages(context.Background(), queries[0]); err != cause {
		t.Fatalf("single error=%v, want original item error", err)
	}
	for _, global := range []error{context.Canceled, context.DeadlineExceeded, cause} {
		fixture.err = global
		if _, err := reader.SyncMessagesBatch(context.Background(), queries); err != global {
			t.Fatalf("global error=%v, want %v", err, global)
		}
	}
	fixture.err = nil
	if _, err := reader.SyncMessagesBatch(context.Background(), queries); !errors.Is(err, ErrSyncBatchResultMismatch) {
		t.Fatalf("cardinality error=%v", err)
	}
	fixture.results = nil
	results, err = reader.SyncMessagesBatch(context.Background(), nil)
	if err != nil || len(results) != 0 {
		t.Fatalf("empty batch=%+v err=%v", results, err)
	}
}

func TestPageReaderRequiresCommittedReader(t *testing.T) {
	for _, reader := range []*PageReader{nil, NewPageReader(nil)} {
		if _, err := reader.SyncMessages(context.Background(), ChannelMessageQuery{}); !errors.Is(err, ErrMessageReaderRequired) {
			t.Fatalf("missing reader error=%v", err)
		}
	}
}

type pageReadFixture struct {
	results []MessageScanResult
	queries []MessageScanQuery
	err     error
	calls   int
}

func (f *pageReadFixture) ReadCommittedMessages(_ context.Context, queries []MessageScanQuery) ([]MessageScanResult, error) {
	f.calls++
	f.queries = queries
	return f.results, f.err
}
