package webhook

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestBuildNotifyBodyMapsCommittedMessages(t *testing.T) {
	body, err := buildNotifyBody([]Message{{
		MessageID:         42,
		MessageSeq:        7,
		ChannelID:         "group-a",
		ChannelType:       2,
		Setting:           9,
		FromUID:           "alice",
		ClientMsgNo:       "client-1",
		ServerTimestampMS: time.Unix(10, 0).UnixMilli(),
		Payload:           []byte("hello"),
		RedDot:            true,
	}})
	if err != nil {
		t.Fatalf("buildNotifyBody() error = %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(body))
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	msg := got[0]
	if msg["message_id"] != float64(42) || msg["message_seq"] != float64(7) {
		t.Fatalf("message id/seq = %#v", msg)
	}
	if msg["setting"] != float64(9) {
		t.Fatalf("setting = %v, want 9", msg["setting"])
	}
	if msg["channel_id"] != "group-a" || msg["from_uid"] != "alice" {
		t.Fatalf("channel/from = %#v", msg)
	}
	if msg["payload"] != base64.StdEncoding.EncodeToString([]byte("hello")) {
		t.Fatalf("payload = %v", msg["payload"])
	}
	header, ok := msg["header"].(map[string]any)
	if !ok {
		t.Fatalf("header = %#v, want object", msg["header"])
	}
	if header["red_dot"] != float64(1) || header["sync_once"] != float64(0) || header["no_persist"] != float64(0) {
		t.Fatalf("header = %#v", header)
	}
}

func TestBuildOfflineBodyChunksAndCompressesUIDs(t *testing.T) {
	body, err := buildOfflineBody(OfflineMessage{
		Message: Message{
			MessageID:         10,
			MessageSeq:        11,
			ChannelID:         "group-a",
			ChannelType:       2,
			FromUID:           "alice",
			ServerTimestampMS: time.Unix(10, 0).UnixMilli(),
			Payload:           []byte("payload"),
		},
		ToUIDs: []string{"u1", "u2", "u3"},
	}, 2)
	if err != nil {
		t.Fatalf("buildOfflineBody() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(body))
	}
	if got["compress"] != "gzip" {
		t.Fatalf("compress = %v, want gzip", got["compress"])
	}
	if got["compress_to_uids"] == "" {
		t.Fatalf("compress_to_uids is empty")
	}
	compressed, ok := got["compress_to_uids"].(string)
	if !ok {
		t.Fatalf("compress_to_uids = %#v, want string", got["compress_to_uids"])
	}
	uids := decodeCompressedUIDs(t, compressed)
	if want := []string{"u1", "u2", "u3"}; !reflect.DeepEqual(uids, want) {
		t.Fatalf("compressed uids = %#v, want %#v", uids, want)
	}
	if _, exists := got["to_uids"]; exists {
		t.Fatalf("to_uids exists for compressed body: %#v", got)
	}
}

func TestBuildOfflineBodyIncludesUIDsBelowCompressThreshold(t *testing.T) {
	body, err := buildOfflineBody(OfflineMessage{
		Message: Message{
			MessageID:         10,
			MessageSeq:        11,
			ChannelID:         "group-a",
			ChannelType:       2,
			FromUID:           "alice",
			ServerTimestampMS: time.Unix(10, 0).UnixMilli(),
			Payload:           []byte("payload"),
		},
		ToUIDs: []string{"u1", "u2"},
	}, 3)
	if err != nil {
		t.Fatalf("buildOfflineBody() error = %v", err)
	}
	var got struct {
		ToUIDs         []string `json:"to_uids"`
		Compress       string   `json:"compress"`
		CompressToUIDs string   `json:"compress_to_uids"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(body))
	}
	if want := []string{"u1", "u2"}; !reflect.DeepEqual(got.ToUIDs, want) {
		t.Fatalf("to_uids = %#v, want %#v", got.ToUIDs, want)
	}
	if got.Compress != "" || got.CompressToUIDs != "" {
		t.Fatalf("compressed fields = %q/%q, want empty", got.Compress, got.CompressToUIDs)
	}
}

func TestBuildOnlineStatusBodyFiltersEmptyValues(t *testing.T) {
	body, err := buildOnlineStatusBody([]OnlineStatus{
		{Value: "u1-1"},
		{},
		{Value: "u2-0"},
	})
	if err != nil {
		t.Fatalf("buildOnlineStatusBody() error = %v", err)
	}
	var got []string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(body))
	}
	if want := []string{"u1-1", "u2-0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %#v, want %#v", got, want)
	}
}

func decodeCompressedUIDs(t *testing.T, encoded string) []string {
	t.Helper()

	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer zr.Close()
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	var got []string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(data))
	}
	return got
}
