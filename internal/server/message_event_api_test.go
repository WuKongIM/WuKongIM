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

// eventAppend is a shorthand for calling /message/event.
func eventAppend(t *testing.T, apiBaseURL string, req map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	return postJSON(t, apiBaseURL+"/message/event", req)
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
		"event_type":    "stream.delta",
		"event_key":     "main",
		"visibility":    "public",
		"payload":       map[string]interface{}{"kind": "text", "delta": "你好，事件流"},
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_2",
		"event_type":    "stream.close",
		"event_key":     "main",
		"visibility":    "public",
		"payload":       map[string]interface{}{"end_reason": 0},
	})

	// eventsync
	status, body := postJSON(t, apiBaseURL+"/message/eventsync", map[string]interface{}{
		"channel_id":         channelID,
		"channel_type":       wkproto.ChannelTypePerson,
		"from_uid":           fromUID,
		"client_msg_no":      clientMsgNo,
		"event_key":          "main",
		"from_msg_event_seq": 0,
		"limit":              10,
	})
	require.Equal(t, http.StatusOK, status, "eventsync should succeed: %+v", body)
	data := body["data"].(map[string]interface{})
	events := data["events"].([]interface{})
	require.Len(t, events, 1)
	require.Equal(t, float64(1), events[0].(map[string]interface{})["msg_event_seq"])

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
	require.Equal(t, float64(1), eventMeta["event_count"])
	eventKeys :=eventMeta["events"].([]interface{})
	require.Len(t, eventKeys, 1)
	ek := eventKeys[0].(map[string]interface{})
	require.Equal(t, "main", ek["event_key"])
	require.Equal(t, "closed", ek["status"])
	streamDataBase64, ok := target["stream_data"].(string)
	require.True(t, ok)
	streamData, err := base64.StdEncoding.DecodeString(streamDataBase64)
	require.NoError(t, err)
	require.Equal(t, "你好，事件流", string(streamData))

	_ = s
}

func TestMessageEventAPI_DefaultEventKey(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-default-key"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	// omit event_key → should default to "main"
	d := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_delta",
		"event_type":    "stream.delta",
		"payload":       map[string]interface{}{"kind": "text", "delta": "x"},
	})
	require.Equal(t, "main", d["event_key"])
}

func TestMessageEventAPI_DeltaCloseWithoutOpen(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-delta-close-no-open"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	// send → delta("hello ") → delta("world") → close, all without stream.open
	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_d1",
		"event_type":    "stream.delta",
		"event_key":     "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "hello "},
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_d2",
		"event_type":    "stream.delta",
		"event_key":     "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "world"},
	})

	closeData := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_close",
		"event_type":    "stream.close",
		"event_key":     "main",
		"payload":       map[string]interface{}{"end_reason": 0},
	})
	require.Equal(t, "closed", closeData["stream_status"])

	// messagesync: verify snapshot and status
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
		"event_summary_mode": "full",
	})
	require.Equal(t, http.StatusOK, status)

	var syncResp map[string]interface{}
	err := json.Unmarshal(syncRaw, &syncResp)
	require.NoError(t, err)
	target := findMessageByClientMsgNo(t, syncResp["messages"].([]interface{}), clientMsgNo)
	eventMeta := target["event_meta"].(map[string]interface{})
	eventKeys := eventMeta["events"].([]interface{})
	require.Len(t, eventKeys, 1)
	ek := eventKeys[0].(map[string]interface{})
	require.Equal(t, "closed", ek["status"])
	snapshot := ek["snapshot"].(map[string]interface{})
	require.Equal(t, "hello world", snapshot["text"])
}

func TestMessageEventAPI_ResponseFields(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-resp-fields"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	// stream.delta response (cache path)
	deltaData := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_delta",
		"event_type":    "stream.delta",
		"event_key":     "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "x"},
	})

	// verify expected fields present
	require.Equal(t, clientMsgNo, deltaData["client_msg_no"])
	require.Equal(t, "main", deltaData["event_key"])
	require.Equal(t, "evt_delta", deltaData["event_id"])
	require.NotNil(t, deltaData["msg_event_seq"])
	require.NotNil(t, deltaData["stream_status"])
	require.Equal(t, channelID, deltaData["channel_id"])
	require.Equal(t, float64(wkproto.ChannelTypePerson), deltaData["channel_type"])
	require.Equal(t, fromUID, deltaData["from_uid"])
	// message_id and message_seq should NOT be present
	_, hasMessageID := deltaData["message_id"]
	_, hasMessageSeq := deltaData["message_seq"]
	require.False(t, hasMessageID, "response should not contain message_id")
	require.False(t, hasMessageSeq, "response should not contain message_seq")
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
		"event_id":      "evt_d1",
		"event_type":    "stream.delta",
		"event_key":     "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "hello "},
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_d2",
		"event_type":    "stream.delta",
		"event_key":     "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "world"},
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_close",
		"event_type":    "stream.close",
		"event_key":     "main",
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

	// non-text delta
	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_tool",
		"event_type":    "stream.delta",
		"event_key":     "main",
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
		"event_key":     "main",
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

	d := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_error",
		"event_type":    "stream.error",
		"event_key":     "main",
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

	d := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_cancel",
		"event_type":    "stream.cancel",
		"event_key":     "main",
	})
	require.Equal(t, "cancelled", d["stream_status"])
}

func TestMessageEventAPI_EventTypeCaseInsensitive(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-case-insensitive"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	// mixed case delta
	d := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_delta",
		"event_type":    "Stream.Delta",
		"event_key":     "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "x"},
	})
	require.Equal(t, "open", d["stream_status"])

	// mixed case close
	d = eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_close",
		"event_type":    "STREAM.CLOSE",
		"event_key":     "main",
		"payload":       map[string]interface{}{"end_reason": 0},
	})
	require.Equal(t, "closed", d["stream_status"])
}

func TestMessageEventAPI_StreamFinish(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	clientMsgNo := "cmn-stream-finish"
	channelID := "u2"
	fromUID := "u1"

	sendAnchorMessage(t, apiBaseURL, clientMsgNo, channelID, fromUID)

	// delta → close on "main" key
	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_d1",
		"event_type":    "stream.delta",
		"event_key":     "main",
		"payload":       map[string]interface{}{"kind": "text", "delta": "hello"},
	})

	eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_close",
		"event_type":    "stream.close",
		"event_key":     "main",
		"payload":       map[string]interface{}{"end_reason": 0},
	})

	// send stream.finish
	finishData := eventAppendData(t, apiBaseURL, map[string]interface{}{
		"channel_id":    channelID,
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt_finish",
		"event_type":    "stream.finish",
	})
	require.Equal(t, "closed", finishData["stream_status"])

	// messagesync: verify event_meta.completed == true and events array does not contain __finish__
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
	})
	require.Equal(t, http.StatusOK, status)

	var syncResp map[string]interface{}
	err := json.Unmarshal(syncRaw, &syncResp)
	require.NoError(t, err)
	target := findMessageByClientMsgNo(t, syncResp["messages"].([]interface{}), clientMsgNo)
	eventMeta := target["event_meta"].(map[string]interface{})

	// completed should be true
	require.Equal(t, true, eventMeta["completed"])

	// events array should not contain __finish__
	eventKeys := eventMeta["events"].([]interface{})
	for _, ek := range eventKeys {
		ekMap := ek.(map[string]interface{})
		require.NotEqual(t, "__finish__", ekMap["event_key"], "events should not contain __finish__ entry")
	}

	// events should only have "main"
	require.Len(t, eventKeys, 1)
	require.Equal(t, "main", eventKeys[0].(map[string]interface{})["event_key"])
}

func TestMessageEventAPI_MissingRequiredFields(t *testing.T) {
	_, apiBaseURL := newEventAPITestServer(t)

	// missing channel_id
	status, _ := eventAppend(t, apiBaseURL, map[string]interface{}{
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      "u1",
		"client_msg_no": "cmn_1",
		"event_id":      "evt_1",
		"event_type":    "stream.delta",
		"payload":       map[string]interface{}{"kind": "text", "delta": "x"},
	})
	require.Equal(t, http.StatusBadRequest, status)

	// missing client_msg_no
	status, _ = eventAppend(t, apiBaseURL, map[string]interface{}{
		"channel_id":   "u2",
		"channel_type": wkproto.ChannelTypePerson,
		"from_uid":     "u1",
		"event_id":     "evt_1",
		"event_type":   "stream.delta",
		"payload":      map[string]interface{}{"kind": "text", "delta": "x"},
	})
	require.Equal(t, http.StatusBadRequest, status)

	// missing event_id
	status, _ = eventAppend(t, apiBaseURL, map[string]interface{}{
		"channel_id":    "u2",
		"channel_type":  wkproto.ChannelTypePerson,
		"from_uid":      "u1",
		"client_msg_no": "cmn_1",
		"event_type":    "stream.delta",
		"payload":       map[string]interface{}{"kind": "text", "delta": "x"},
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
		"event_id":      "evt_summary_1",
		"event_type":    "stream.delta",
		"event_key":     "main",
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
		"event_key":     "main",
		"payload":       map[string]interface{}{"end_reason": 0},
	})

	// basic: no snapshot field in event key summary
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
	basicEK := basicMsg["event_meta"].(map[string]interface{})["events"].([]interface{})[0].(map[string]interface{})
	_, basicHasSnapshot := basicEK["snapshot"]
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
	fullEK := fullMsg["event_meta"].(map[string]interface{})["events"].([]interface{})[0].(map[string]interface{})
	_, fullHasSnapshot := fullEK["snapshot"]
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
