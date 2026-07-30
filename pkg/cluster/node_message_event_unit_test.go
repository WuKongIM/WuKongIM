package cluster

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestMergeMessageEventTerminalPayloadKeepsCachedSnapshot(t *testing.T) {
	snapshot := []byte(`{"kind":"text","text":"cached"}`)

	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{name: "missing snapshot", payload: []byte(`{"end_reason":2}`)},
		{name: "null snapshot", payload: []byte(`{"snapshot":null,"end_reason":2}`)},
		{name: "invalid json", payload: []byte(`not-json`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeMessageEventTerminalPayload(tc.payload, snapshot)
			var body map[string]json.RawMessage
			if err := json.Unmarshal(got, &body); err != nil {
				t.Fatalf("merged payload is invalid JSON: %s: %v", got, err)
			}
			if !strings.Contains(string(body["snapshot"]), "cached") {
				t.Fatalf("snapshot = %s, want cached snapshot in payload %s", body["snapshot"], got)
			}
			if tc.name == "invalid json" && !strings.Contains(string(body["raw_payload"]), "not-json") {
				t.Fatalf("raw_payload = %s, want original invalid payload preserved", body["raw_payload"])
			}
		})
	}
}

func TestMessageEventStreamCacheRejectsNewActiveSessionWhenFull(t *testing.T) {
	cache := newMessageEventStreamCache(1)
	first := metadb.MessageEventAppend{
		ChannelID:   "g1",
		ChannelType: 2,
		ClientMsgNo: "cmn-1",
		EventID:     "evt-1",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		Payload:     []byte(`{"kind":"text","delta":"a"}`),
	}
	if _, err := cache.appendCached(first); err != nil {
		t.Fatalf("appendCached(first) error = %v", err)
	}
	second := first
	second.ClientMsgNo = "cmn-2"
	second.EventID = "evt-2"
	if _, err := cache.appendCached(second); !errors.Is(err, ErrBackpressured) {
		t.Fatalf("appendCached(second) error = %v, want ErrBackpressured", err)
	}

	closeResult := metadb.MessageEventAppendResult{
		ChannelID:   first.ChannelID,
		ChannelType: first.ChannelType,
		ClientMsgNo: first.ClientMsgNo,
		EventID:     "evt-close",
		EventKey:    "main",
		MsgEventSeq: 1,
		Status:      metadb.EventStatusClosed,
		State: metadb.MessageEventState{
			ChannelID:   first.ChannelID,
			ChannelType: first.ChannelType,
			ClientMsgNo: first.ClientMsgNo,
			EventKey:    "main",
			Status:      metadb.EventStatusClosed,
		},
	}
	cache.markTerminalPersisted(metadb.MessageEventAppend{
		ChannelID:   first.ChannelID,
		ChannelType: first.ChannelType,
		ClientMsgNo: first.ClientMsgNo,
		EventID:     "evt-close",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamClose,
	}, closeResult)
	if _, err := cache.appendCached(second); err != nil {
		t.Fatalf("appendCached(second after terminal eviction) error = %v", err)
	}
}
