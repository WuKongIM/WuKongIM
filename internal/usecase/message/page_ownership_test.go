package message

import (
	"context"
	"testing"
)

// ownershipScan returns fresh storage-owned records on every read.
type ownershipScan struct {
	count int
	rich  bool
}

func (s ownershipScan) ReadCommittedMessages(_ context.Context, qs []MessageScanQuery) ([]MessageScanResult, error) {
	out := make([]MessageScanResult, len(qs))
	for i, q := range qs {
		rows := make([]SyncedMessage, s.count)
		for j := range rows {
			seq := uint64(j + 1)
			if q.Reverse {
				seq = uint64(s.count - j)
			}
			rows[j] = SyncedMessage{MessageID: seq, MessageSeq: seq, ChannelID: q.ChannelID.ID, ChannelType: q.ChannelID.Type, Payload: make([]byte, 256)}
			rows[j].Payload[0] = 'p'
			if s.rich {
				rows[j].StreamData = []byte("stream")
				rows[j].EventHint = &MessageEventSyncHint{ClientMsgNo: "original"}
				rows[j].EventMeta = &MessageEventMeta{Events: []MessageEventKeyMeta{{EventKey: "main", Snapshot: map[string]any{"value": []any{"original"}}}}}
			}
		}
		out[i].Messages = rows
	}
	return out, nil
}
func (s ownershipScan) ReadPersistedMessages(ctx context.Context, qs []MessageScanQuery) ([]MessageScanResult, error) {
	return s.ReadCommittedMessages(ctx, qs)
}

func ownershipQuery() SyncChannelMessagesQuery {
	return SyncChannelMessagesQuery{LoginUID: "reader", ChannelID: "group", ChannelType: 2, Limit: 100}
}

func BenchmarkPageReaderSyncOwnership(b *testing.B) {
	a := New(Options{Reader: NewPageReader(ownershipScan{count: 100}), Memberships: liveSyncMembershipStore()})
	q := ownershipQuery()
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		result, err := a.SyncChannelMessages(ctx, q)
		if err != nil || len(result.Messages) != 100 {
			b.Fatalf("result=%v error=%v", len(result.Messages), err)
		}
		benchmarkSyncResult = result
	}
}

func TestPageReaderSyncRepeatedResultsAreIndependent(t *testing.T) {
	for _, mode := range []string{"single", "batch", "persisted"} {
		t.Run(mode, func(t *testing.T) {
			a := New(Options{Reader: NewPageReader(ownershipScan{count: 1, rich: true}), PersistedReader: NewPersistedPageReader(ownershipScan{count: 1, rich: true}), Memberships: liveSyncMembershipStore()})
			read := func() SyncedMessage {
				t.Helper()
				var result SyncChannelMessagesResult
				var err error
				if mode == "single" {
					result, err = a.SyncChannelMessages(context.Background(), ownershipQuery())
				} else {
					q := SyncChannelMessagesBatchQuery{LoginUID: "reader", Items: []SyncChannelMessagesQuery{ownershipQuery()}}
					var batch SyncChannelMessagesBatchResult
					if mode == "batch" {
						batch, err = a.SyncChannelMessagesBatch(context.Background(), q)
					} else {
						batch, err = a.SyncPersistedChannelMessagesBatch(context.Background(), q)
					}
					if err == nil {
						if batch.Items[0].Err != nil {
							t.Fatal(batch.Items[0].Err)
						}
						result = batch.Items[0].Result
					}
				}
				if err != nil || len(result.Messages) != 1 {
					t.Fatalf("messages=%v err=%v", len(result.Messages), err)
				}
				return result.Messages[0]
			}
			first := read()
			second := read()
			mutateOwnershipMessage(&first)
			assertOwnershipMessage(t, second)
			assertOwnershipMessage(t, read())
		})
	}
}

func mutateOwnershipMessage(m *SyncedMessage) {
	m.Payload[0] = 'x'
	m.StreamData[0] = 'x'
	m.EventHint.ClientMsgNo = "changed"
	m.EventMeta.Events[0].Snapshot.(map[string]any)["value"].([]any)[0] = "changed"
}
func assertOwnershipMessage(t *testing.T, m SyncedMessage) {
	t.Helper()
	if m.Payload[0] != 'p' || string(m.StreamData) != "stream" || m.EventHint.ClientMsgNo != "original" || m.EventMeta.Events[0].Snapshot.(map[string]any)["value"].([]any)[0] != "original" {
		t.Fatalf("response aliases another caller: %+v", m)
	}
}

// The 100-record public sync path should allocate storage payloads once; this
// budget catches an accidental second deep copy at the usecase boundary.
func TestPageReaderSyncAllocationBudget(t *testing.T) {
	a := New(Options{Reader: NewPageReader(ownershipScan{count: 100}), Memberships: liveSyncMembershipStore()})
	allocations := testing.AllocsPerRun(30, func() {
		result, err := a.SyncChannelMessages(context.Background(), ownershipQuery())
		if err != nil || len(result.Messages) != 100 {
			t.Fatalf("read failed: %v", err)
		}
		benchmarkSyncResult = result
	})
	if allocations > 110 {
		t.Fatalf("sync allocated %.0f objects, budget 110 for 100 owned records", allocations)
	}
}

func TestCustomSyncReaderKeepsBorrowedMessagesIsolated(t *testing.T) {
	rows, err := (ownershipScan{count: 1, rich: true}).ReadCommittedMessages(context.Background(), []MessageScanQuery{{ChannelID: ChannelID{ID: "group", Type: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	page := ChannelMessagePage{Messages: rows[0].Messages}
	reader := &recordingChannelMessageReader{page: page, batchResults: []ChannelMessageReadResult{{Page: page}}}
	a := New(Options{Reader: reader, PersistedReader: reader, Memberships: liveSyncMembershipStore()})
	first, err := a.SyncChannelMessages(context.Background(), ownershipQuery())
	if err != nil {
		t.Fatal(err)
	}
	mutateOwnershipMessage(&first.Messages[0])
	assertOwnershipMessage(t, page.Messages[0])
	query := SyncChannelMessagesBatchQuery{LoginUID: "reader", Items: []SyncChannelMessagesQuery{ownershipQuery()}}
	for _, read := range []func(context.Context, SyncChannelMessagesBatchQuery) (SyncChannelMessagesBatchResult, error){a.SyncChannelMessagesBatch, a.SyncPersistedChannelMessagesBatch} {
		result, err := read(context.Background(), query)
		if err != nil || result.Items[0].Err != nil {
			t.Fatalf("read=%+v err=%v", result, err)
		}
		mutateOwnershipMessage(&result.Items[0].Result.Messages[0])
		assertOwnershipMessage(t, page.Messages[0])
	}
}

// Embedding PageReader does not transfer a wrapper's retained page ownership.
type retainedPageReader struct {
	*PageReader
	page ChannelMessagePage
}

func (r *retainedPageReader) SyncMessages(context.Context, ChannelMessageQuery) (ChannelMessagePage, error) {
	return r.page, nil
}

func (r *retainedPageReader) SyncMessagesBatch(_ context.Context, qs []ChannelMessageQuery) ([]ChannelMessageReadResult, error) {
	out := make([]ChannelMessageReadResult, len(qs))
	for i := range out {
		out[i].Page = r.page
	}
	return out, nil
}

func TestEmbeddedPageReaderDoesNotTransferRetainedPage(t *testing.T) {
	pageReader := NewPageReader(ownershipScan{count: 1, rich: true})
	page, err := pageReader.SyncMessages(context.Background(), ChannelMessageQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	reader := &retainedPageReader{PageReader: pageReader, page: page}
	a := New(Options{Reader: reader, PersistedReader: reader, Memberships: liveSyncMembershipStore()})
	single, err := a.SyncChannelMessages(context.Background(), ownershipQuery())
	if err != nil {
		t.Fatal(err)
	}
	mutateOwnershipMessage(&single.Messages[0])
	assertOwnershipMessage(t, page.Messages[0])
	query := SyncChannelMessagesBatchQuery{LoginUID: "reader", Items: []SyncChannelMessagesQuery{ownershipQuery()}}
	for _, read := range []func(context.Context, SyncChannelMessagesBatchQuery) (SyncChannelMessagesBatchResult, error){a.SyncChannelMessagesBatch, a.SyncPersistedChannelMessagesBatch} {
		result, err := read(context.Background(), query)
		if err != nil || result.Items[0].Err != nil {
			t.Fatalf("batch failed: %v", err)
		}
		mutateOwnershipMessage(&result.Items[0].Result.Messages[0])
		assertOwnershipMessage(t, page.Messages[0])
	}
}

func TestOwnedPageReleasesDiscardedRowsAndPreservesRawContinuation(t *testing.T) {
	var first []SyncedMessage
	calls := 0
	reader := NewPageReader(scanFunction(func(_ context.Context, qs []MessageScanQuery) ([]MessageScanResult, error) {
		calls++
		if calls == 1 {
			first = []SyncedMessage{{MessageSeq: 5, Payload: []byte("five")}, {MessageSeq: 4, Flags: MessageFlags{SyncOnce: true}, Payload: []byte("hidden")}, {MessageSeq: 3, Payload: []byte("three")}}
			return []MessageScanResult{{Messages: first}}, nil
		}
		if qs[0].FromSeq != 2 || qs[0].Limit != 1 {
			t.Fatalf("continuation lost raw cursor: %+v", qs[0])
		}
		return []MessageScanResult{{Messages: []SyncedMessage{{MessageSeq: 2, Payload: []byte("lookahead")}}}}, nil
	}))
	page, err := reader.SyncMessages(context.Background(), ChannelMessageQuery{Limit: 2})
	if err != nil || !page.HasMore || len(page.Messages) != 2 || page.Messages[0].MessageSeq != 3 || page.Messages[1].MessageSeq != 5 || calls != 2 {
		t.Fatalf("page=%+v calls=%d error=%v", page, calls, err)
	}
	// The first wave has room for lookahead; no hidden/lookahead payload may
	// remain reachable through its unused capacity after page construction.
	if first[2].MessageSeq != 0 || first[2].Payload != nil {
		t.Fatal("discarded row retained by response backing array")
	}
}
