package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
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
