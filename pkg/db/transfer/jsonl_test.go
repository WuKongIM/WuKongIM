package transfer

import (
	"context"
	"strings"
	"testing"
)

func TestReadJSONLAcceptsUint64NumberAndString(t *testing.T) {
	input := strings.NewReader(
		"{\"channel_key\":\"g1:2\",\"message_seq\":1,\"message_id\":\"18446744073709551615\",\"payload_b64\":\"aGk=\"}\n",
	)
	var got []MessageRecord
	err := readJSONL(context.Background(), input, FileKindMessageMessages, func(record any) error {
		got = append(got, record.(MessageRecord))
		return nil
	})
	if err != nil {
		t.Fatalf("readJSONL() error = %v", err)
	}
	if uint64(got[0].MessageID) != ^uint64(0) || string(got[0].Payload) != "hi" {
		t.Fatalf("record = %+v payload=%q", got[0], string(got[0].Payload))
	}
}

func TestReadJSONLRejectsMissingRequiredField(t *testing.T) {
	input := strings.NewReader("{\"hash_slot\":1,\"token\":\"t1\"}\n")
	err := readJSONL(context.Background(), input, FileKindMetaUsers, func(any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "uid") {
		t.Fatalf("readJSONL() error = %v, want missing uid", err)
	}
}

func TestReadJSONLRejectsUnknownConversationKind(t *testing.T) {
	input := strings.NewReader("{\"hash_slot\":1,\"uid\":\"u1\",\"kind\":\"other\",\"channel_id\":\"g1\",\"channel_type\":2}\n")
	err := readJSONL(context.Background(), input, FileKindMetaConversations, func(any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("readJSONL() error = %v, want kind error", err)
	}
}

func TestReadJSONLRejectsBadPayloadBase64(t *testing.T) {
	input := strings.NewReader("{\"channel_key\":\"g1:2\",\"message_seq\":1,\"message_id\":1,\"payload_b64\":\"@@\"}\n")
	err := readJSONL(context.Background(), input, FileKindMessageMessages, func(any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "payload_b64") {
		t.Fatalf("readJSONL() error = %v, want base64 error", err)
	}
}
