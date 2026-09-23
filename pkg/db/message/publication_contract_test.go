package message

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func publicationFixture(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPublicationStorageSurvivesReopenAndPortableBackup(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer func() { source.close(t) }()
	id, key := ChannelID{ID: "publication", Type: 2}, ChannelKey("publication:2")
	log := mustAcquireChannel(t, source.db, key, id)
	want := publicationFixture(t)
	input := bytes.Clone(want)
	if _, err := log.Append(ctx, []Record{
		{ID: 101, Payload: []byte("body"), PublicationMetadata: input, ServerTimestampMS: 2000},
		{ID: 102, Payload: []byte("native")},
	}, AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	clear(input)
	if err := log.StoreCheckpoint(ctx, Checkpoint{Epoch: 3, HW: 2}); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	source.close(t)
	source = openTestMessageStoreAt(t, source.path)
	log = mustAcquireChannel(t, source.db, key, id)
	defer log.Close()
	assert := func(l *ChannelLog) {
		t.Helper()
		m, present, err := l.GetBySeq(ctx, 1)
		if err != nil || !present || !bytes.Equal(m.PublicationMetadata, want) || string(m.Payload) != "body" || m.ServerTimestampMS != 2000 {
			t.Fatalf("stored metadata lost: %+v %v", m, err)
		}
		clear(m.PublicationMetadata)
		m, present, err = l.GetBySeq(ctx, 1)
		if err != nil || !present || !bytes.Equal(m.PublicationMetadata, want) {
			t.Fatal("read result aliases durable metadata")
		}
		m, present, err = l.GetBySeq(ctx, 2)
		if err != nil || !present || len(m.PublicationMetadata) != 0 {
			t.Fatal("native metadata must stay absent")
		}
	}
	assert(log)
	body := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: key, ID: id, Checkpoint: Checkpoint{Epoch: 3, HW: 2}}}})
	visits := 0
	if _, err := ReplayBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)), func(BackupSnapshotBoundary) error { return nil }, func(r BackupSnapshotRecord) error {
		visits++
		if r.MessageID == 101 && !bytes.Equal(r.PublicationMetadata, want) {
			t.Fatal("restore visitor dropped metadata")
		}
		return nil
	}); err != nil || visits != 2 {
		t.Fatalf("visitor: %d %v", visits, err)
	}
	target := openTestMessageStore(t)
	defer target.close(t)
	if _, err := target.db.ImportBackupSnapshot(ctx, body); err != nil {
		t.Fatal(err)
	}
	restored := mustAcquireChannel(t, target.db, key, id)
	defer restored.Close()
	assert(restored)
}

func TestPublicationStorageRejectsInvalidBatchAndCountsMetadataInReadBudgets(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	for _, bad := range [][]byte{{1}, {2}, make([]byte, 32769)} {
		if _, err := l.Append(ctx, []Record{{ID: 1}, {ID: 2, PublicationMetadata: bad}}, AppendOptions{}); !errors.Is(err, dberrors.ErrInvalidArgument) {
			t.Fatalf("bad metadata: %v", err)
		}
		if leo, err := l.LEO(ctx); err != nil || leo != 0 {
			t.Fatalf("failed batch advanced to %d: %v", leo, err)
		}
	}
	metadata := publicationFixture(t)
	if _, err := l.ApplyFetch(ctx, ApplyFetchRequest{BaseSeq: 1, Records: []Record{{ID: 1, Payload: []byte("a"), PublicationMetadata: metadata}, {ID: 2, Payload: []byte("b"), PublicationMetadata: metadata}}, Checkpoint: &Checkpoint{Epoch: 1, HW: 2}}); err != nil {
		t.Fatal(err)
	}
	rows, err := l.Read(ctx, 1, ReadOptions{Limit: 10, MaxBytes: len(metadata) + 1})
	if err != nil || len(rows) != 1 || !bytes.Equal(rows[0].PublicationMetadata, metadata) {
		t.Fatalf("budget: %d %v", len(rows), err)
	}
}

func TestPublicationRecordAndProposalSurviveExactRetryAndBackup(t *testing.T) {
	ctx := context.Background()
	source := openCompatEngine(t)
	key, id := channel.ChannelKey("publication:2"), channel.ChannelID{ID: "publication", Type: 2}
	s := mustForChannel(t, source, key, id)
	defer s.Close()
	metadata := publicationFixture(t)
	m := channel.Message{MessageID: 101, MessageSeq: 1, ChannelID: id.ID, ChannelType: id.Type, FromUID: "sender", ClientMsgNo: "client", Payload: []byte("body"), ServerTimestampMS: 2000, PublicationMetadata: metadata}
	r, err := EncodeMessageRecord(m, 3)
	if err != nil || r.Payload[0] != 2 {
		t.Fatalf("publication requires record codec 2: %v", err)
	}
	decoded, err := DecodeMessageRecord(r)
	if err != nil || !bytes.Equal(decoded.PublicationMetadata, metadata) {
		t.Fatalf("record dropped metadata: %v", err)
	}
	clear(decoded.PublicationMetadata)
	decoded, err = DecodeMessageRecord(r)
	if err != nil || !bytes.Equal(decoded.PublicationMetadata, metadata) {
		t.Fatal("record result borrowed metadata")
	}
	for n := 0; n < len(r.Payload); n++ {
		partial := r
		partial.Payload = r.Payload[:n]
		if _, err := DecodeMessageRecord(partial); err == nil {
			t.Fatalf("partial record %d accepted", n)
		}
	}
	mf, _, ok := quorumlog.SealProposalManifest(quorumlog.ProposalManifest{Version: 3, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7, CommandID: quorumlog.CommandID{1}, LastOffset: 1}, []quorumlog.Record{{ID: 101, Index: 1, Epoch: 3, FromUID: "sender", ClientMsgNo: "client", ServerTimestampMS: 2000, Payload: []byte("body"), PublicationMetadata: metadata}})
	if !ok {
		t.Fatal("cannot seal proposal")
	}
	item := AppendBatchItem{Store: s, Records: []channel.Record{r}, ExactBaseOffset: true, ExpectedBaseOffset: 0, Proposal: mf}
	for _, outcome := range []quorumlog.AppendOutcome{quorumlog.AppendOutcomeDurable, quorumlog.AppendOutcomeAlreadyDurable} {
		results := StoreAppendBatch(ctx, []AppendBatchItem{item})
		if len(results) != 1 || results[0].Err != nil || results[0].Outcome != outcome {
			t.Fatalf("append: %+v", results)
		}
	}
	got, found, err := s.GetMessageBySeq(1)
	if err != nil || !found || !bytes.Equal(got.PublicationMetadata, metadata) {
		t.Fatalf("compat read lost metadata: %v", err)
	}
	records, err := s.Read(0, 1<<20)
	if err != nil || len(records) != 1 {
		t.Fatalf("read records: %d %v", len(records), err)
	}
	got, err = DecodeMessageRecord(records[0])
	if err != nil || !bytes.Equal(got.PublicationMetadata, metadata) {
		t.Fatalf("recovery record lost metadata: %v", err)
	}
	m.PublicationMetadata = bytes.Clone(metadata)
	m.PublicationMetadata[2] = 0
	changed, err := EncodeMessageRecord(m, 3)
	if err != nil {
		t.Fatal(err)
	}
	item.Records = []channel.Record{changed}
	results := StoreAppendBatch(ctx, []AppendBatchItem{item})
	if len(results) != 1 || results[0].Err == nil || results[0].Outcome.Durable() {
		t.Fatalf("changed metadata accepted original proof: %+v", results)
	}
	if err := s.StoreCheckpoint(channel.Checkpoint{Epoch: 3, HW: 1}); err != nil {
		t.Fatal(err)
	}
	body := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: ChannelKey(key), ID: ChannelID{ID: id.ID, Type: id.Type}, Checkpoint: Checkpoint{Epoch: 3, HW: 1}}}})
	target := openCompatEngine(t)
	if _, err := target.db.ImportBackupSnapshot(ctx, body); err != nil {
		t.Fatal(err)
	}
	restored := mustForChannel(t, target, key, id)
	defer restored.Close()
	item.Store, item.Records = restored, []channel.Record{r}
	results = StoreAppendBatch(ctx, []AppendBatchItem{item})
	if len(results) != 1 || results[0].Err != nil || results[0].Outcome != quorumlog.AppendOutcomeAlreadyDurable {
		t.Fatalf("restored exact retry: %+v", results)
	}
}

func TestPublicationStorageLookupAndRecoveryBudgets(t *testing.T) {
	ctx := context.Background()
	engine := openCompatEngine(t)
	s := mustForChannel(t, engine, "x:2", channel.ChannelID{ID: "x", Type: 2})
	defer s.Close()
	metadata := publicationFixture(t)
	m := channel.Message{MessageID: 1, MessageSeq: 1, ChannelID: "x", ChannelType: 2, FromUID: "a", ClientMsgNo: "b", Payload: []byte("c"), ServerTimestampMS: 2000, PublicationMetadata: metadata}
	r, err := EncodeMessageRecord(m, 1)
	if err != nil {
		t.Fatal(err)
	}
	mf, _, ok := quorumlog.SealProposalManifest(quorumlog.ProposalManifest{Version: 3, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: quorumlog.CommandID{1}, LastOffset: 1}, []quorumlog.Record{{ID: 1, Index: 1, Epoch: 1, FromUID: "a", ClientMsgNo: "b", ServerTimestampMS: 2000, Payload: []byte("c"), PublicationMetadata: metadata}})
	if !ok {
		t.Fatal("seal failed")
	}
	results := StoreAppendBatch(ctx, []AppendBatchItem{{Store: s, Records: []channel.Record{r}, ExactBaseOffset: true, Proposal: mf}})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("append: %+v", results)
	}
	if err := s.StoreCheckpoint(channel.Checkpoint{Epoch: 1, HW: 1}); err != nil {
		t.Fatal(err)
	}
	t.Run("index lookup", func(t *testing.T) {
		if _, err := s.LookupMessagesByClientMsgNo(ctx, "b", 1, 1, 1, len(metadata)); !errors.Is(err, channel.ErrInvalidArgument) {
			t.Fatalf("lookup exceeded budget: %v", err)
		}
	})
	t.Run("offline recovery", func(t *testing.T) {
		log := mustAcquireChannel(t, engine.db, "x:2", ChannelID{ID: "x", Type: 2})
		defer log.Close()
		if err := log.VerifyOfflineImportedLog(ctx, 1<<20); err != nil {
			t.Fatal(err)
		}
		// This record's compact native encoding fits, but the recovery envelope
		// plus metadata is larger; both independent budgets must be enforced.
		if err := log.VerifyOfflineImportedLog(ctx, r.SizeBytes); !errors.Is(err, dberrors.ErrInvalidArgument) {
			t.Fatalf("recovery exceeded budget: %v", err)
		}
	})
}
