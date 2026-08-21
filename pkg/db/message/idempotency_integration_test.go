//go:build integration

package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestIdempotencyLookup(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	payload := []byte("one")
	if _, err := log.Append(context.Background(), []Record{{ID: 61, ClientMsgNo: "same", FromUID: "u1", Payload: payload}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	hit, ok, err := log.LookupIdempotency(context.Background(), IdempotencyKey{FromUID: "u1", ClientMsgNo: "same"})
	if err != nil {
		t.Fatalf("LookupIdempotency(): %v", err)
	}
	if !ok {
		t.Fatal("LookupIdempotency() ok = false, want true")
	}
	if hit.MessageSeq != 1 || hit.Offset != 0 || hit.MessageID != 61 || hit.PayloadHash != hashPayload(payload) {
		t.Fatalf("hit = %#v, want seq=1 offset=0 id=61 payload hash", hit)
	}

	_, ok, err = log.LookupIdempotency(context.Background(), IdempotencyKey{FromUID: "u2", ClientMsgNo: "missing"})
	if err != nil {
		t.Fatalf("LookupIdempotency() missing: %v", err)
	}
	if ok {
		t.Fatal("LookupIdempotency() missing ok = true, want false")
	}
}

func TestAppendStrictRejectsDuplicateIdempotency(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{{ID: 71, ClientMsgNo: "same", FromUID: "u1", Payload: []byte("one")}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	_, err := log.Append(context.Background(), []Record{{ID: 72, ClientMsgNo: "same", FromUID: "u1", Payload: []byte("two")}}, AppendOptions{})
	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("Append() err = %v, want conflict", err)
	}
}

func TestAppendServerAllocatedUsesMembershipFilterForNegativeIdempotencyLookups(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	defer log.Close()
	for i, clientMsgNo := range []string{"client-1", "client-2"} {
		_, err := log.Append(context.Background(), []Record{{
			ID:          uint64(81 + i),
			ClientMsgNo: clientMsgNo,
			FromUID:     "u1",
			Payload:     []byte(clientMsgNo),
		}}, AppendOptions{Mode: AppendServerAllocatedMessageID})
		if err != nil {
			t.Fatalf("Append(%q): %v", clientMsgNo, err)
		}
	}
	if got := store.db.idempotencyNegativeFilterSkips.Load(); got != 2 {
		t.Fatalf("negative filter skips = %d, want 2", got)
	}
	if got := store.db.idempotencyPointReads.Load(); got != 0 {
		t.Fatalf("idempotency point reads = %d, want 0", got)
	}

	_, err := log.Append(context.Background(), []Record{{
		ID:          83,
		ClientMsgNo: "client-1",
		FromUID:     "u1",
		Payload:     []byte("duplicate"),
	}}, AppendOptions{Mode: AppendServerAllocatedMessageID})
	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("duplicate Append() err = %v, want conflict", err)
	}
	if got := store.db.idempotencyPointReads.Load(); got != 1 {
		t.Fatalf("idempotency point reads = %d, want 1 for the possible hit", got)
	}
	metrics := store.db.MetricsSnapshot()
	if metrics.IdempotencyNegativeFilterSkips != 2 || metrics.IdempotencyPointReads != 1 {
		t.Fatalf("idempotency metrics = %+v, want skips=2 point_reads=1", metrics)
	}
}

func TestStoreAppendBatchExactFreshRecordValidatesIdempotencyOnce(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer engine.Close()
	const channelID = "exact-idempotency-once"
	store, err := engine.ForChannel(channel.ChannelKey(channelID+":1"), channel.ChannelID{ID: channelID, Type: 1})
	if err != nil {
		t.Fatalf("ForChannel(): %v", err)
	}
	defer store.Close()
	record := compatExactTestRecord(t, 5, 201, channelID, "client-201")
	manifest := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{2, 0, 1}, BaseOffset: 0, LastOffset: 1,
	}, []channel.Record{record})

	results := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{record}, ExactBaseOffset: true,
		ExpectedBaseOffset: 0, Proposal: manifest, ServerAllocatedMessageIDs: true,
	}})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("StoreAppendBatch() = %+v, want durable append", results)
	}
	metrics := engine.MetricsSnapshot()
	if metrics.IdempotencyNegativeFilterSkips != 1 || metrics.IdempotencyPointReads != 0 {
		t.Fatalf("idempotency metrics = %+v, want one negative skip and no point read for one fresh record", metrics)
	}
}

func TestIdempotencyMembershipFilterRebuildsFromDurableIndex(t *testing.T) {
	path := t.TempDir()
	store := openTestMessageStoreAt(t, path)
	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{{
		ID:          91,
		ClientMsgNo: "durable-client",
		FromUID:     "u1",
		Payload:     []byte("original"),
	}}, AppendOptions{Mode: AppendServerAllocatedMessageID}); err != nil {
		t.Fatalf("seed Append(): %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("seed Close(): %v", err)
	}
	store.close(t)

	reopened := openTestMessageStoreAt(t, path)
	defer reopened.close(t)
	reopenedLog := testChannelLog(reopened)
	defer reopenedLog.Close()
	_, err := reopenedLog.Append(context.Background(), []Record{{
		ID:          92,
		ClientMsgNo: "durable-client",
		FromUID:     "u1",
		Payload:     []byte("duplicate"),
	}}, AppendOptions{Mode: AppendServerAllocatedMessageID})
	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("duplicate after reopen err = %v, want conflict", err)
	}
	if got := reopened.db.idempotencyPointReads.Load(); got != 1 {
		t.Fatalf("idempotency point reads after rebuild = %d, want 1", got)
	}
	if got := reopened.db.idempotencyNegativeFilterSkips.Load(); got != 0 {
		t.Fatalf("negative skips after rebuild = %d, want 0 for durable hit", got)
	}
}

func TestTrustedFollowerAppendKeepsLoadedIdempotencyMembershipCurrent(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	defer log.Close()
	if _, err := log.Append(context.Background(), []Record{{
		ID:          101,
		ClientMsgNo: "leader-client",
		FromUID:     "u1",
		Payload:     []byte("leader"),
	}}, AppendOptions{Mode: AppendServerAllocatedMessageID}); err != nil {
		t.Fatalf("leader Append(): %v", err)
	}
	if _, err := log.Append(context.Background(), []Record{{
		ID:          102,
		ClientMsgNo: "follower-client",
		FromUID:     "u1",
		Payload:     []byte("follower"),
	}}, AppendOptions{Mode: AppendTrustedContiguous}); err != nil {
		t.Fatalf("trusted follower Append(): %v", err)
	}

	_, err := log.Append(context.Background(), []Record{{
		ID:          103,
		ClientMsgNo: "follower-client",
		FromUID:     "u1",
		Payload:     []byte("duplicate-after-role-change"),
	}}, AppendOptions{Mode: AppendServerAllocatedMessageID})
	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("duplicate after trusted append err = %v, want conflict", err)
	}
	if got := store.db.idempotencyPointReads.Load(); got != 1 {
		t.Fatalf("idempotency point reads = %d, want 1 for follower-written key", got)
	}
}
