package wkdb_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/require"
)

const (
	testChannelId   = "test_channel"
	testChannelType = uint8(2)
)

func TestAppendMessageEventWithState_TextLifecycle(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_text_lifecycle"
	events := []*wkdb.MessageEvent{
		{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_1", EventKey: "main", EventType: "stream.delta", Payload: []byte(`{"kind":"text","delta":"你好"}`)},
		{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_2", EventKey: "main", EventType: "stream.delta", Payload: []byte(`{"kind":"text","delta":"，世界"}`)},
		{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_3", EventKey: "main", EventType: "stream.close", Payload: []byte(`{"end_reason":0}`)},
	}

	for idx, evt := range events {
		stored, state, err := d.AppendMessageEventWithState(evt)
		require.NoError(t, err)
		require.Equal(t, uint64(idx+1), stored.MsgEventSeq)
		require.Equal(t, uint64(idx+1), state.LastMsgEventSeq)
	}

	state, err := d.GetMessageEventState(testChannelId, testChannelType, clientMsgNo, "main")
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, "closed", state.Status)
	require.Equal(t, uint64(3), state.LastMsgEventSeq)
	require.JSONEq(t, `{"kind":"text","text":"你好，世界"}`, string(state.SnapshotPayload))

	list, err := d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, uint64(3), list[0].MsgEventSeq)
	require.Equal(t, "evt_3", list[0].EventID)
}

func TestAppendMessageEventWithState_IdempotentByEventID(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_idempotent"
	evt := &wkdb.MessageEvent{
		ChannelId:   testChannelId,
		ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo,
		EventID:     "evt_dup",
		EventKey:    "main",
		EventType:   "stream.delta",
		Payload:     []byte(`{"kind":"text","delta":"A"}`),
	}
	stored1, _, err := d.AppendMessageEventWithState(evt)
	require.NoError(t, err)
	require.Equal(t, uint64(1), stored1.MsgEventSeq)

	retry := &wkdb.MessageEvent{
		ChannelId:   testChannelId,
		ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo,
		EventID:     "evt_dup",
		EventKey:    "main",
		EventType:   "stream.delta",
		Payload:     []byte(`{"kind":"text","delta":"B"}`),
	}
	stored2, _, err := d.AppendMessageEventWithState(retry)
	require.NoError(t, err)
	require.Equal(t, uint64(1), stored2.MsgEventSeq)

	list, err := d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.JSONEq(t, `{"kind":"text","text":"A"}`, string(list[0].Payload))
}

func TestListMessageEvents_WithEventKeyFilter(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_multi_key"
	_, _, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_1", EventKey: "main", EventType: "stream.delta", Payload: []byte(`{"kind":"text","delta":"hi"}`)})
	require.NoError(t, err)
	_, _, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_2", EventKey: "tool_weather", EventType: "stream.delta", Payload: []byte(`{"kind":"tool_call","tool":"weather"}`)})
	require.NoError(t, err)
	_, _, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_3", EventKey: "tool_weather", EventType: "stream.close"})
	require.NoError(t, err)

	allEvents, err := d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "", 10)
	require.NoError(t, err)
	require.Len(t, allEvents, 2)

	toolEvents, err := d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "tool_weather", 10)
	require.NoError(t, err)
	require.Len(t, toolEvents, 1)
	require.Equal(t, uint64(3), toolEvents[0].MsgEventSeq)

	states, err := d.GetMessageEventStates(testChannelId, testChannelType, clientMsgNo)
	require.NoError(t, err)
	require.Len(t, states, 2)
}

func TestAppendMessageEventWithState_ChannelIsolation(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_same_client_no"

	// Channel A
	_, _, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: "channelA", ChannelType: 2,
		ClientMsgNo: clientMsgNo, EventID: "evt_a1", EventKey: "main", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"a"}`),
	})
	require.NoError(t, err)

	// Channel B with same clientMsgNo
	_, _, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: "channelB", ChannelType: 2,
		ClientMsgNo: clientMsgNo, EventID: "evt_b1", EventKey: "main", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"b"}`),
	})
	require.NoError(t, err)

	// Verify isolation
	statesA, err := d.GetMessageEventStates("channelA", 2, clientMsgNo)
	require.NoError(t, err)
	require.Len(t, statesA, 1)

	statesB, err := d.GetMessageEventStates("channelB", 2, clientMsgNo)
	require.NoError(t, err)
	require.Len(t, statesB, 1)

	// Different channels should have independent seq
	stateA, err := d.GetMessageEventState("channelA", 2, clientMsgNo, "main")
	require.NoError(t, err)
	require.Equal(t, uint64(1), stateA.LastMsgEventSeq)

	stateB, err := d.GetMessageEventState("channelB", 2, clientMsgNo, "main")
	require.NoError(t, err)
	require.Equal(t, uint64(1), stateB.LastMsgEventSeq)
}

// ---- New tests ----

func TestAppendMessageEventWithState_TerminalRejectsNewEvents(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_terminal_reject"

	_, state, err := d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", EventKey: "main", EventType: "stream.close",
	})
	require.NoError(t, err)
	require.Equal(t, "closed", state.Status)
	require.Equal(t, uint64(1), state.LastMsgEventSeq)

	// event after close should not allocate a new seq
	stored, state, err := d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", EventKey: "main", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"should be ignored"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "closed", state.Status)
	require.Equal(t, uint64(1), stored.MsgEventSeq) // same seq as close, not incremented
}

func TestAppendMessageEventWithState_StreamError(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_stream_error"

	_, evtState, err := d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", EventKey: "main", EventType: "stream.error",
		Payload: []byte(`{"error":"timeout"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "error", evtState.Status)
	require.Equal(t, "timeout", evtState.Error)
	require.Equal(t, uint64(1), evtState.LastMsgEventSeq)

	// verify terminal
	state, err := d.GetMessageEventState(testChannelId, testChannelType, clientMsgNo, "main")
	require.NoError(t, err)
	require.Equal(t, "error", state.Status)
	require.Equal(t, "timeout", state.Error)
}

func TestAppendMessageEventWithState_StreamCancel(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_stream_cancel"

	_, state, err := d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", EventKey: "main", EventType: "stream.cancel",
	})
	require.NoError(t, err)
	require.Equal(t, "cancelled", state.Status)
	require.Equal(t, uint64(1), state.LastMsgEventSeq)
}

func TestAppendMessageEventWithState_NonTextDelta(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_nontext_delta"

	toolPayload := []byte(`{"kind":"tool_call","tool":"weather","args":{"city":"北京"}}`)
	_, state, err := d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", EventKey: "main", EventType: "stream.delta",
		Payload: toolPayload,
	})
	require.NoError(t, err)
	require.Equal(t, "open", state.Status)
	// non-text delta replaces snapshot payload
	require.JSONEq(t, string(toolPayload), string(state.SnapshotPayload))
}

func TestAppendMessageEventWithState_EmptyEventKeyNormalization(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_empty_key"

	// send event with empty EventKey
	_, state, err := d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", EventKey: "", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"a"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "main", state.EventKey)

	// send another with whitespace EventKey
	_, state, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", EventKey: "  ", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"x"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "main", state.EventKey)

	// should only have 1 event key (both normalized to "main")
	states, err := d.GetMessageEventStates(testChannelId, testChannelType, clientMsgNo)
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, "main", states[0].EventKey)
}

func TestAppendMessageEventWithState_SnapshotOverride(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_snapshot_override"

	// text deltas accumulate
	_, _, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", EventKey: "main", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"hello"}`),
	})
	require.NoError(t, err)

	// stream.snapshot replaces everything
	_, state, err := d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", EventKey: "main", EventType: "stream.snapshot",
		Payload: []byte(`{"kind":"text","text":"full replacement"}`),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"kind":"text","text":"full replacement"}`, string(state.SnapshotPayload))
}

func TestGetMessageEventByEventID(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_get_by_event_id"

	_, _, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", EventKey: "main", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"x"}`),
	})
	require.NoError(t, err)

	// found
	evt, err := d.GetMessageEventByEventID(testChannelId, testChannelType, clientMsgNo, "evt_1")
	require.NoError(t, err)
	require.NotNil(t, evt)
	require.Equal(t, "evt_1", evt.EventID)
	require.Equal(t, "main", evt.EventKey)

	// not found
	evt, err = d.GetMessageEventByEventID(testChannelId, testChannelType, clientMsgNo, "no_such_event")
	require.NoError(t, err)
	require.Nil(t, evt)
}

func TestListMessageEvents_Pagination(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_pagination"

	// create 3 event keys, each with its own last_msg_event_seq
	_, _, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", EventKey: "key_a", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"a"}`),
	})
	require.NoError(t, err)
	_, _, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", EventKey: "key_b", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"b"}`),
	})
	require.NoError(t, err)
	_, _, err = d.AppendMessageEventWithState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_3", EventKey: "key_c", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"c"}`),
	})
	require.NoError(t, err)

	// fromSeq=0 → all 3
	list, err := d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 3)

	// fromSeq=1 → skip key_a (seq=1), return key_b (seq=2) and key_c (seq=3)
	list, err = d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 1, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, uint64(2), list[0].MsgEventSeq)
	require.Equal(t, uint64(3), list[1].MsgEventSeq)

	// limit=1 → only first result
	list, err = d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "", 1)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, uint64(1), list[0].MsgEventSeq)
}
