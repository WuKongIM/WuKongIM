package message

import (
	"bytes"
	"reflect"
	"testing"
)

func TestCommitCoordinatorConfigDoesNotExposeNoSync(t *testing.T) {
	if _, ok := reflect.TypeOf(CommitCoordinatorConfig{}).FieldByName("NoSync"); ok {
		t.Fatal("CommitCoordinatorConfig exposes NoSync, want durable sync fixed on")
	}
}

func TestAppendValidationSeenAllocatesIdempotencyMapOnlyWhenNeeded(t *testing.T) {
	seen := newAppendValidationSeen(2)
	if seen.idempotencyKeys != nil {
		t.Fatal("new append validation state allocated idempotency map before seeing an idempotency key")
	}
	if duplicate := seen.rememberMessageID(1); duplicate {
		t.Fatal("first message ID marked duplicate")
	}
	if seen.idempotencyKeys != nil {
		t.Fatal("message ID validation allocated idempotency map")
	}
	if duplicate := seen.rememberIdempotencyKey(IdempotencyKey{FromUID: "u1", ClientMsgNo: "c1"}); duplicate {
		t.Fatal("first idempotency key marked duplicate")
	}
	if seen.idempotencyKeys != nil {
		t.Fatal("first idempotency key allocated idempotency map")
	}
	if duplicate := seen.rememberIdempotencyKey(IdempotencyKey{FromUID: "u1", ClientMsgNo: "c1"}); !duplicate {
		t.Fatal("duplicate first idempotency key was not detected")
	}
	if seen.idempotencyKeys != nil {
		t.Fatal("duplicate first idempotency key allocated idempotency map")
	}
	if duplicate := seen.rememberIdempotencyKey(IdempotencyKey{FromUID: "u2", ClientMsgNo: "c2"}); duplicate {
		t.Fatal("second distinct idempotency key marked duplicate")
	}
	if seen.idempotencyKeys == nil {
		t.Fatal("second distinct idempotency key did not allocate idempotency map")
	}
}

func TestIdempotencyIndexValueDirectCodecMatchesEncoder(t *testing.T) {
	row := normalizeMessageRow(messageRow{
		MessageSeq:        3,
		MessageID:         73,
		ClientMsgNo:       "same",
		FromUID:           "u1",
		ChannelID:         "ch",
		ChannelType:       1,
		Payload:           []byte("payload"),
		ServerTimestampMS: 99,
	})
	value := make([]byte, idempotencyIndexValueLen)
	if err := writeIdempotencyIndexValue(value, row); err != nil {
		t.Fatalf("writeIdempotencyIndexValue(): %v", err)
	}

	want, err := encodeIdempotencyIndexValue(row)
	if err != nil {
		t.Fatalf("encodeIdempotencyIndexValue(): %v", err)
	}
	if !bytes.Equal(value, want) {
		t.Fatalf("direct idempotency index value = %x, want %x", value, want)
	}
}

func TestMessageIDIndexValueDirectCodecMatchesEncoder(t *testing.T) {
	value := make([]byte, messageIDIndexValueLen)
	writeMessageIDIndexValue(value, 42)

	want := encodeMessageIDIndexValue(42)
	if !bytes.Equal(value, want) {
		t.Fatalf("direct message ID index value = %x, want %x", value, want)
	}
}

func TestLatestMessageIndexProgressCodecRoundTrips(t *testing.T) {
	want := latestMessageIndexProgress{afterChannel: "channel-a", currentChannel: "channel-b", lastMessageID: 401}
	got, err := decodeLatestMessageIndexProgress(encodeLatestMessageIndexProgress(want))
	if err != nil {
		t.Fatalf("decodeLatestMessageIndexProgress(): %v", err)
	}
	if got != want {
		t.Fatalf("progress = %#v, want %#v", got, want)
	}
}
