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

func TestReadJSONLDecodesChannelNumericFlags(t *testing.T) {
	input := strings.NewReader(
		"{\"hash_slot\":1,\"channel_id\":\"g1\",\"channel_type\":2,\"ban\":1,\"disband\":0,\"send_ban\":1,\"allow_stranger\":1,\"large\":1,\"subscriber_mutation_version\":\"7\"}\n",
	)
	var got []ChannelRecord
	err := readJSONL(context.Background(), input, FileKindMetaChannels, func(record any) error {
		got = append(got, record.(ChannelRecord))
		return nil
	})
	if err != nil {
		t.Fatalf("readJSONL() error = %v", err)
	}
	assertInt64Field(t, "Ban", got[0].Ban, 1)
	assertInt64Field(t, "Disband", got[0].Disband, 0)
	assertInt64Field(t, "SendBan", got[0].SendBan, 1)
	assertInt64Field(t, "AllowStranger", got[0].AllowStranger, 1)
	assertInt64Field(t, "Large", got[0].Large, 1)
	if uint64(got[0].SubscriberMutationVersion) != 7 {
		t.Fatalf("SubscriberMutationVersion = %d, want 7", got[0].SubscriberMutationVersion)
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

func TestReadJSONLRejectsMissingMessagePayloadField(t *testing.T) {
	input := strings.NewReader("{\"channel_key\":\"g1:2\",\"message_seq\":1,\"message_id\":1}\n")
	err := readJSONL(context.Background(), input, FileKindMessageMessages, func(any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "payload_b64") {
		t.Fatalf("readJSONL() error = %v, want payload_b64 error", err)
	}
}

func TestReadJSONLRejectsMissingChannelLatestPayloadField(t *testing.T) {
	input := strings.NewReader("{\"hash_slot\":1,\"channel_id\":\"g1\",\"channel_type\":2,\"last_message_id\":1,\"last_message_seq\":1}\n")
	err := readJSONL(context.Background(), input, FileKindMetaChannelLatest, func(any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "last_payload_b64") {
		t.Fatalf("readJSONL() error = %v, want last_payload_b64 error", err)
	}
}

func TestReadJSONLAcceptsEmptyPayload(t *testing.T) {
	input := strings.NewReader("{\"channel_key\":\"g1:2\",\"message_seq\":1,\"message_id\":1,\"payload_b64\":\"\"}\n")
	var got []MessageRecord
	err := readJSONL(context.Background(), input, FileKindMessageMessages, func(record any) error {
		got = append(got, record.(MessageRecord))
		return nil
	})
	if err != nil {
		t.Fatalf("readJSONL() error = %v", err)
	}
	if got[0].PayloadB64 != "" || len(got[0].Payload) != 0 {
		t.Fatalf("record = %+v payload_len=%d, want explicit empty payload", got[0], len(got[0].Payload))
	}
}

func assertInt64Field(t *testing.T, name string, got any, want int64) {
	t.Helper()
	value, ok := got.(int64)
	if !ok {
		t.Fatalf("%s = %T(%v), want int64(%d)", name, got, got, want)
	}
	if value != want {
		t.Fatalf("%s = %d, want %d", name, value, want)
	}
}
