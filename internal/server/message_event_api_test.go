package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/require"
)

// ---------- helpers ----------

// sendAnchorMessage sends an anchor message and waits until it's persisted.
func sendAnchorMessage(t *testing.T, apiBaseURL, clientMsgNo, channelID, fromUID string) {
	t.Helper()
	status, body := postJSON(t, apiBaseURL+"/message/send", map[string]interface{}{
		"header": map[string]interface{}{
			"no_persist": 0,
			"red_dot":    0,
			"sync_once":  0,
		},
		"client_msg_no": clientMsgNo,
		"is_stream":     1,
		"from_uid":      fromUID,
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"payload":       "aGVsbG8=",
	})
	require.Equal(t, http.StatusOK, status, "send anchor message should succeed: %+v", body)

	fakeChannelID := options.GetFakeChannelIDWith(fromUID, channelID)
	require.Eventually(t, func() bool {
		msg, err := service.Store.LoadMsgByClientMsgNo(fakeChannelID, wkproto.ChannelTypePerson, clientMsgNo)
		if err != nil {
			return false
		}
		return !wkdb.IsEmptyMessage(msg)
	}, 8*time.Second, 80*time.Millisecond)
}

// eventAppend is a shorthand for calling /message/eventappend.
func eventAppend(t *testing.T, apiBaseURL string, req map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	return postJSON(t, apiBaseURL+"/message/eventappend", req)
}

// eventAppendData calls eventAppend and returns the data map directly. Fails if status != 200.
func eventAppendData(t *testing.T, apiBaseURL string, req map[string]interface{}) map[string]interface{} {
	t.Helper()
	status, body := eventAppend(t, apiBaseURL, req)
	require.Equal(t, http.StatusOK, status, "eventappend should succeed: %+v", body)
	return body["data"].(map[string]interface{})
}

// ---------- tests ----------

func TestMessageEventAPI_AppendSyncAndMessageSyncMeta(t *testing.T) {
	s, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-event-api-001"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_1",
		"event_type":    "stream.open",
		"lane_id":       "main",
		"visibility":    "public",
		"payload":       map[string]interface{}{"kind": "text"},
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_2",
		"event_type":    "stream.delta",
		"lane_id":       "main",
		"visibility":    "public",
		"payload":       map[string]interface{}{"kind": "text", "delta": "你好，事件流"},
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_3",
		"event_type":    "stream.close",
		"lane_id":       "main",
		"visibility":    "public",
		"payload":       map[string]interface{}{"end_reason": 0},
	})

	// eventsync
	status, body := postJSON(t, apiBaseURL+"/message/eventsync", map[string]interface{}{
		"channel_id":         channelID,
		"channel_type":       wkproto.ChannelTypePerson,
		"from_uid":           fromUID,
		"client_msg_no":      clientMsgNo,
		"lane_id":            "main",
		"from_msg_event_seq": 0,
		"limit":              10,
	})
	require.Equal(t, http.StatusOK, status, "eventsync should succeed: %+v", body)
	data := body["data"].(map[string]interface{})
	events := data["events"].([]interface{})
	require.Len(t, events, 1)
	require.Equal(t, float64(2), events[0].(map[string]interface{})["msg_event_seq"])

	// messagesync
	status, syncBodyBytes := postJSONRaw(t, apiBaseURL+"/channel/messagesync", map[string]interface{}{
		"login_uid":          fromUID,
		"channel_id":         channelID,
		"channel_type":       wkproto.ChannelTypePerson,
		"start_message_seq":  0,
		"end_message_seq":    0,
		"limit":              20,
		"pull_mode":          0,
		"stream_v2":          1,
		"include_event_meta": 1,
	})
	require.Equal(t, http.StatusOK, status)

	var syncResp map[string]interface{}
	err := json.Unmarshal(syncBodyBytes, &syncResp)
	require.NoError(t, err)
	messages := syncResp["messages"].([]interface{})

	target := findMessageByClientMsgNo(t, messages, clientMsgNo)
	eventMeta := target["event_meta"].(map[string]interface{})
	require.Equal(t, true, eventMeta["has_events"])
	require.Equal(t, float64(1), eventMeta["lane_count"])
	lanes := eventMeta["lanes"].([]interface{})
	require.Len(t, lanes, 1)
	lane := lanes[0].(map[string]interface{})
	require.Equal(t, "main", lane["lane_id"])
	require.Equal(t, "closed", lane["status"])
	streamDataBase64, ok := target["stream_data"].(string)
	require.True(t, ok)
	streamData, err := base64.StdEncoding.DecodeString(streamDataBase64)
	require.NoError(t, err)
	require.Equal(t, "你好，事件流", string(streamData))

	_ = s
}

func TestMessageEventAPI_OpenIdempotent(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-open-idempotent"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	d1 := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_open",
		"event_type":    "stream.open",
		"lane_id":       "main",
	})
	firstSeq := d1["msg_event_seq"]

	// retry same event_id → should be idempotent (DB path handles this)
	d2 := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_open",
		"event_type":    "stream.open",
		"lane_id":       "main",
	})
	require.Equal(t, firstSeq, d2["msg_event_seq"])
}

func TestMessageEventAPI_DefaultLane(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-default-lane"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	// omit lane_id → should default to "main"
	d := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_open",
		"event_type":    "stream.open",
	})
	require.Equal(t, "main", d["lane_id"])
}

func TestMessageEventAPI_DeltaWithoutOpenFails(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-delta-no-open"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	// stream.delta without prior stream.open → cache has no session → error
	status, _ := eventAppend(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_delta",
		"event_type":    "stream.delta",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "x"},
	})
	require.Equal(t, http.StatusBadRequest, status)
}

func TestMessageEventAPI_ResponseFields(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-resp-fields"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	// stream.open response (DB path)
	openData := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_open",
		"event_type":    "stream.open",
		"lane_id":       "main",
	})

	// verify expected fields present
	require.Equal(t, clientMsgNo, openData["client_msg_no"])
	require.Equal(t, "main", openData["lane_id"])
	require.Equal(t, "evt_open", openData["event_id"])
	require.NotNil(t, openData["msg_event_seq"])
	require.NotNil(t, openData["stream_status"])
	require.Equal(t, channelID, openData["channel_id"])
	require.Equal(t, float64(wkproto.ChannelTypePerson), openData["channel_type"])
	require.Equal(t, fromUID, openData["from_uid"])
	// message_id and message_seq should NOT be present
	_, hasMessageID := openData["message_id"]
	_, hasMessageSeq := openData["message_seq"]
	require.False(t, hasMessageID, "response should not contain message_id")
	require.False(t, hasMessageSeq, "response should not contain message_seq")

	// stream.delta response (cache path)
	deltaData := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_delta",
		"event_type":    "stream.delta",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "x"},
	})
	require.Equal(t, clientMsgNo, deltaData["client_msg_no"])
	require.Equal(t, "main", deltaData["lane_id"])
	_, hasMessageID = deltaData["message_id"]
	_, hasMessageSeq = deltaData["message_seq"]
	require.False(t, hasMessageID, "delta response should not contain message_id")
	require.False(t, hasMessageSeq, "delta response should not contain message_seq")
}

func TestMessageEventAPI_MultiDeltaAccumulation(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-multi-delta"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_open",
		"event_type":    "stream.open",
		"lane_id":       "main",
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_d1",
		"event_type":    "stream.delta",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "hello "},
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_d2",
		"event_type":    "stream.delta",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "world"},
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_close",
		"event_type":    "stream.close",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"end_reason": 0},
	})

	// eventsync → verify merged snapshot contains accumulated text
	status, body := postJSON(t, apiBaseURL+"/message/eventsync", map[string]interface{}{
		"channel_id":         channelID,
		"channel_type":       wkproto.ChannelTypePerson,
		"from_uid":           fromUID,
		"client_msg_no":      clientMsgNo,
		"from_msg_event_seq": 0,
		"limit":              10,
	})
	require.Equal(t, http.StatusOK, status)
	events := body["data"].(map[string]interface{})["events"].([]interface{})
	require.Len(t, events, 1)

	evt := events[0].(map[string]interface{})
	payload := evt["payload"].(map[string]interface{})
	// buildProjectedEvent uses SnapshotPayload directly as payload
	require.Equal(t, "text", payload["kind"])
	require.Equal(t, "hello world", payload["text"])
}

func TestMessageEventAPI_NonTextDelta(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-nontext-delta"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_open",
		"event_type":    "stream.open",
		"lane_id":       "main",
	})

	// non-text delta
	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_tool",
		"event_type":    "stream.delta",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"kind": "tool_call", "tool": "weather"},
	})

	// close with merged non-text snapshot
	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_close",
		"event_type":    "stream.close",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"end_reason": 0},
	})

	status, body := postJSON(t, apiBaseURL+"/message/eventsync", map[string]interface{}{
		"channel_id":         channelID,
		"channel_type":       wkproto.ChannelTypePerson,
		"from_uid":           fromUID,
		"client_msg_no":      clientMsgNo,
		"from_msg_event_seq": 0,
		"limit":              10,
	})
	require.Equal(t, http.StatusOK, status)
	events := body["data"].(map[string]interface{})["events"].([]interface{})
	require.Len(t, events, 1)

	evt := events[0].(map[string]interface{})
	payload := evt["payload"].(map[string]interface{})
	// buildProjectedEvent uses SnapshotPayload directly as payload
	require.Equal(t, "tool_call", payload["kind"])
	require.Equal(t, "weather", payload["tool"])
}

func TestMessageEventAPI_StreamError(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-stream-error"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_open",
		"event_type":    "stream.open",
		"lane_id":       "main",
	})

	d := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_error",
		"event_type":    "stream.error",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"error": "timeout"},
	})
	require.Equal(t, "error", d["stream_status"])
}

func TestMessageEventAPI_StreamCancel(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-stream-cancel"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_open",
		"event_type":    "stream.open",
		"lane_id":       "main",
	})

	d := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_cancel",
		"event_type":    "stream.cancel",
		"lane_id":       "main",
	})
	require.Equal(t, "cancelled", d["stream_status"])
}

func TestMessageEventAPI_EventTypeCaseInsensitive(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-case-insensitive"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	// uppercase event_type should work
	d := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_open",
		"event_type":    "STREAM.OPEN",
		"lane_id":       "main",
	})
	require.Equal(t, "open", d["stream_status"])

	// mixed case delta
	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_delta",
		"event_type":    "Stream.Delta",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "x"},
	})

	// mixed case close
	d = eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_close",
		"event_type":    "STREAM.CLOSE",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"end_reason": 0},
	})
	require.Equal(t, "closed", d["stream_status"])
}

func TestMessageEventAPI_MissingRequiredFields(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	// missing channel_id
	status, _ := eventAppend(t, apiBaseURL, map[string]interface{}{
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      "u1",
		"client_msg_no": "cmn_1",
		"event_id":      "evt_1",
		"event_type":    "stream.open",
	})
	require.Equal(t, http.StatusBadRequest, status)

	// missing client_msg_no
	status, _ = eventAppend(t, apiBaseURL, map[string]interface{}{
		"channel_id":   "u2",
		"channel_type": wkproto.ChannelTypePerson,
		"from_uid":     "u1",
		"event_id":     "evt_1",
		"event_type":   "stream.open",
	})
	require.Equal(t, http.StatusBadRequest, status)

	// missing event_id
	status, _ = eventAppend(t, apiBaseURL, map[string]interface{}{
		"channel_id":    "u2",
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      "u1",
		"client_msg_no": "cmn_1",
		"event_type":    "stream.open",
	})
	require.Equal(t, http.StatusBadRequest, status)

	// missing event_type
	status, _ = eventAppend(t, apiBaseURL, map[string]interface{}{
		"channel_id":    "u2",
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      "u1",
		"client_msg_no": "cmn_1",
		"event_id":      "evt_1",
	})
	require.Equal(t, http.StatusBadRequest, status)
}

func TestMessageEventAPI_MessageSyncEventSummaryMode(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-summary-mode-001"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_open",
		"event_type":    "stream.open",
		"lane_id":       "main",
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_summary_1",
		"event_type":    "stream.delta",
		"lane_id":       "main",
		"visibility":    "public",
		"payload":       map[string]interface{}{"kind": "text", "delta": "summary-check"},
	})

	// close to persist snapshot to DB
	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_close",
		"event_type":    "stream.close",
		"lane_id":       "main",
		"payload":       map[string]interface{}{"end_reason": 0},
	})

	// basic: no snapshot field in lane summary
	status, syncRaw := postJSONRaw(t, apiBaseURL+"/channel/messagesync", map[string]interface{}{
		"login_uid":          fromUID,
		"channel_id":         channelID,
		"channel_type":       wkproto.ChannelTypePerson,
		"start_message_seq":  0,
		"end_message_seq":    0,
		"limit":              20,
		"pull_mode":          0,
		"stream_v2":          1,
		"include_event_meta": 1,
		"event_summary_mode": "basic",
	})
	require.Equal(t, http.StatusOK, status)

	var basicResp map[string]interface{}
	err := json.Unmarshal(syncRaw, &basicResp)
	require.NoError(t, err)
	basicMsg := findMessageByClientMsgNo(t, basicResp["messages"].([]interface{}), clientMsgNo)
	basicLane := basicMsg["event_meta"].(map[string]interface{})["lanes"].([]interface{})[0].(map[string]interface{})
	_, basicHasSnapshot := basicLane["snapshot"]
	require.False(t, basicHasSnapshot)

	// full: snapshot field present
	status, syncRaw = postJSONRaw(t, apiBaseURL+"/channel/messagesync", map[string]interface{}{
		"login_uid":          fromUID,
		"channel_id":         channelID,
		"channel_type":       wkproto.ChannelTypePerson,
		"start_message_seq":  0,
		"end_message_seq":    0,
		"limit":              20,
		"pull_mode":          0,
		"stream_v2":          1,
		"include_event_meta": 1,
		"event_summary_mode": "full",
	})
	require.Equal(t, http.StatusOK, status)

	var fullResp map[string]interface{}
	err = json.Unmarshal(syncRaw, &fullResp)
	require.NoError(t, err)
	fullMsg := findMessageByClientMsgNo(t, fullResp["messages"].([]interface{}), clientMsgNo)
	fullLane := fullMsg["event_meta"].(map[string]interface{})["lanes"].([]interface{})[0].(map[string]interface{})
	_, fullHasSnapshot := fullLane["snapshot"]
	require.True(t, fullHasSnapshot)
}

// ---------- shared helpers ----------

func postJSON(t *testing.T, url string, body map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	status, raw := postJSONRaw(t, url, body)
	var resp map[string]interface{}
	err := json.Unmarshal(raw, &resp)
	require.NoError(t, err)
	return status, resp
}

func postJSONRaw(t *testing.T, url string, body map[string]interface{}) (int, []byte) {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(httpReq)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBytes := make([]byte, 0)
	buf := bytes.NewBuffer(respBytes)
	_, err = buf.ReadFrom(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, buf.Bytes()
}

func findMessageByClientMsgNo(t *testing.T, messages []interface{}, clientMsgNo string) map[string]interface{} {
	t.Helper()
	for _, m := range messages {
		msg := m.(map[string]interface{})
		if msg["client_msg_no"] == clientMsgNo {
			return msg
		}
	}
	require.FailNowf(t, "message not found", "client_msg_no=%s", clientMsgNo)
	return nil
}
