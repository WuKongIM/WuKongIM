package message

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

func willServerKey(marker string) string { return "mqtt-will-v1:" + strings.Repeat(marker, 64) }

func willRecord(t *testing.T, id uint64, marker, client string) Record {
	t.Helper()
	b, err := publication.Encode(publication.Metadata{Source: publication.SourceWill, QoS: 1,
		PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t", ServerWillKey: willServerKey(marker)})
	if err != nil {
		t.Fatal(err)
	}
	return Record{ID: id, FromUID: "sender", ClientMsgNo: client, Payload: []byte("body"), PublicationMetadata: b, ServerTimestampMS: 1000}
}

func TestWillIdempotencySeparateDomainsSurviveReopenBackupAndDeletion(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer func() { source.close(t) }()
	id, key := ChannelID{ID: "will", Type: 2}, ChannelKey("will:2")
	log := mustAcquireChannel(t, source.db, key, id)
	client := willServerKey("a") // A prefix cannot establish a separate domain.
	native := Record{ID: 1, FromUID: "sender", ClientMsgNo: client, Payload: []byte("native")}
	if _, err := log.Append(ctx, []Record{native, willRecord(t, 2, "a", client), willRecord(t, 3, "b", client)}, AppendOptions{}); err != nil {
		t.Fatalf("different identities with one client number: %v", err)
	}
	if err := log.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 3}); err != nil {
		t.Fatal(err)
	}
	check := func(l *ChannelLog) {
		t.Helper()
		for _, entry := range []struct {
			key IdempotencyKey
			id  uint64
		}{
			{IdempotencyKey{FromUID: "sender", ClientMsgNo: client}, 1},
			{IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")}, 2},
			{IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("b")}, 3},
		} {
			hit, ok, err := l.LookupIdempotency(ctx, entry.key)
			if err != nil || !ok || hit.MessageID != entry.id {
				t.Fatalf("lookup %+v: %+v %v %v", entry.key, hit, ok, err)
			}
		}
		page, err := l.ListByClientMsgNo(ctx, client, 0, 10)
		if err != nil || len(page.Messages) != 3 {
			t.Fatalf("history omitted Will: %+v %v", page, err)
		}
		if _, err := l.Append(ctx, []Record{willRecord(t, 99, "a", "changed-client")}, AppendOptions{}); !errors.Is(err, dberrors.ErrConflict) {
			t.Fatalf("server identity changed with client number: %v", err)
		}
	}
	check(log)
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	source.close(t)
	source = openTestMessageStoreAt(t, source.path)
	log = mustAcquireChannel(t, source.db, key, id)
	defer log.Close()
	check(log)
	body := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: key, ID: id, Checkpoint: Checkpoint{Epoch: 1, HW: 3}}}})
	target := openTestMessageStore(t)
	defer target.close(t)
	if _, err := target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	restored := mustAcquireChannel(t, target.db, key, id)
	defer restored.Close()
	check(restored)
	// Suffix deletion must remove only Will B, keeping the native and Will A keys.
	if err := restored.TruncateFrom(ctx, 3); err != nil {
		t.Fatal(err)
	}
	for _, k := range []IdempotencyKey{{FromUID: "sender", ClientMsgNo: client}, {FromUID: "sender", ServerWillKey: willServerKey("a")}} {
		if _, ok, err := restored.LookupIdempotency(ctx, k); err != nil || !ok {
			t.Fatalf("truncate removed other domain: %v", err)
		}
	}
	if _, ok, err := restored.LookupIdempotency(ctx, IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("b")}); err != nil || ok {
		t.Fatalf("truncate left Will B index: %v", err)
	}
	// Prefix deletion must preserve Will A even though its client number is equal.
	if _, err := restored.TrimPrefixThrough(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := restored.LookupIdempotency(ctx, IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")}); err != nil || !ok {
		t.Fatalf("native trim removed Will: %v", err)
	}
	if _, err := restored.TrimPrefixThrough(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := restored.LookupIdempotency(ctx, IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")}); err != nil || ok {
		t.Fatalf("trim left Will index: %v", err)
	}
	page, err := restored.ListByClientMsgNo(ctx, client, 0, 10)
	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("cleanup left client-number entries: %+v %v", page, err)
	}
}

func TestWillIdempotencyBatchFollowerAndInvalidIdentity(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []AppendMode{AppendStrict, AppendServerAllocatedMessageID, AppendTrustedContiguous} {
		s := openTestMessageStore(t)
		l := testChannelLog(s)
		// An invalid second row must roll back the entire batch.
		for _, mutate := range []func(*Record){func(r *Record) { r.FromUID = "" }, func(r *Record) { r.ClientMsgNo = "" }} {
			bad := willRecord(t, 2, "b", "client")
			mutate(&bad)
			if _, err := l.Append(ctx, []Record{willRecord(t, 1, "a", "client"), bad}, AppendOptions{Mode: mode}); !errors.Is(err, dberrors.ErrInvalidArgument) {
				t.Fatalf("invalid keyed Will: %v", err)
			}
		}
		if _, err := l.Append(ctx, []Record{willRecord(t, 1, "a", "one"), willRecord(t, 2, "a", "two")}, AppendOptions{Mode: mode}); !errors.Is(err, dberrors.ErrConflict) {
			t.Fatalf("same-batch duplicate %v: %v", mode, err)
		}
		if leo, err := l.LEO(ctx); err != nil || leo != 0 {
			t.Fatalf("rejected batch advanced log: %d %v", leo, err)
		}
		if _, err := l.Append(ctx, []Record{willRecord(t, 1, "a", "one")}, AppendOptions{Mode: mode}); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Append(ctx, []Record{willRecord(t, 2, "a", "two")}, AppendOptions{Mode: AppendServerAllocatedMessageID}); !errors.Is(err, dberrors.ErrConflict) {
			t.Fatalf("leader lost follower identity: %v", err)
		}
		for _, k := range []IdempotencyKey{{FromUID: "sender", ClientMsgNo: "one", ServerWillKey: willServerKey("a")}, {FromUID: "sender", ServerWillKey: "bad"}, {ServerWillKey: willServerKey("a")}} {
			if _, _, err := l.LookupIdempotency(ctx, k); !errors.Is(err, dberrors.ErrInvalidArgument) {
				t.Fatalf("invalid lookup accepted: %+v %v", k, err)
			}
		}
		if _, ok, err := l.LookupIdempotency(ctx, IdempotencyKey{FromUID: "other", ServerWillKey: willServerKey("a")}); err != nil || ok {
			t.Fatalf("identity not sender scoped: %v", err)
		}
		l.Close()
		s.close(t)
	}
}

func TestWillIdempotencyRecoveryRetainedPrefixAndCorruptIndex(t *testing.T) {
	ctx := context.Background()
	e := openCompatEngine(t)
	s := mustForChannel(t, e, "will:2", channel.ChannelID{ID: "will", Type: 2})
	defer s.Close()
	if _, err := s.log.Append(ctx, []Record{willRecord(t, 1, "a", "client"), willRecord(t, 2, "b", "client")}, AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	row := normalizeMessageRow(s.log.recordToRow(3, willRecord(t, 3, "a", "new-client"), 1000))
	seen := newAppendValidationSeen(1)
	if err := s.validateRecoveryRows(ctx, []messageRow{row}, 1, &seen); !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("reused retained identity: %v", err)
	}
	row = normalizeMessageRow(s.log.recordToRow(3, willRecord(t, 3, "b", "new-client"), 1000))
	seen = newAppendValidationSeen(1)
	if err := s.validateRecoveryRows(ctx, []messageRow{row}, 1, &seen); err != nil {
		t.Fatalf("discarded suffix identity not reusable: %v", err)
	}
	other := normalizeMessageRow(s.log.recordToRow(4, willRecord(t, 4, "b", "other-client"), 1000))
	seen = newAppendValidationSeen(2)
	if err := s.validateRecoveryRows(ctx, []messageRow{row, other}, 1, &seen); !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("recovery accepted duplicate intents: %v", err)
	}
	// A valid tuple pointing to another Will must not authorize a hit.
	value, err := encodeIdempotencyIndexValue(normalizeMessageRow(s.log.recordToRow(2, willRecord(t, 2, "b", "client"), 1000)))
	if err != nil {
		t.Fatal(err)
	}
	batch := e.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeMessageWillIdempotencyIndexKey(s.log.key, "sender", willServerKey("a")), value); err != nil {
		t.Fatal(err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.log.LookupIdempotency(ctx, IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")}); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("unverified Will index: %v", err)
	}
}
