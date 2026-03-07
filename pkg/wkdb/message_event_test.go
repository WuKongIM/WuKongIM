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

func TestAppendMessageEventWithLaneState_TextLifecycle(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_text_lifecycle"
	events := []*wkdb.MessageEvent{
		{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_1", LaneID: "main", EventType: "stream.open"},
		{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_2", LaneID: "main", EventType: "stream.delta", Payload: []byte(`{"kind":"text","delta":"你好"}`)},
		{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_3", LaneID: "main", EventType: "stream.delta", Payload: []byte(`{"kind":"text","delta":"，世界"}`)},
		{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_4", LaneID: "main", EventType: "stream.close", Payload: []byte(`{"end_reason":0}`)},
	}

	for idx, evt := range events {
		stored, lane, err := d.AppendMessageEventWithLaneState(evt)
		require.NoError(t, err)
		require.Equal(t, uint64(idx+1), stored.MsgEventSeq)
		require.Equal(t, uint64(idx+1), lane.LastMsgEventSeq)
	}

	lane, err := d.GetMessageLaneState(testChannelId, testChannelType, clientMsgNo, "main")
	require.NoError(t, err)
	require.NotNil(t, lane)
	require.Equal(t, "closed", lane.Status)
	require.Equal(t, uint64(4), lane.LastMsgEventSeq)
	require.JSONEq(t, `{"kind":"text","text":"你好，世界"}`, string(lane.SnapshotPayload))

	list, err := d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, uint64(4), list[0].MsgEventSeq)
	require.Equal(t, "evt_4", list[0].EventID)
}

func TestAppendMessageEventWithLaneState_IdempotentByEventID(t *testing.T) {
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
		LaneID:      "main",
		EventType:   "stream.delta",
		Payload:     []byte(`{"kind":"text","delta":"A"}`),
	}
	stored1, _, err := d.AppendMessageEventWithLaneState(evt)
	require.NoError(t, err)
	require.Equal(t, uint64(1), stored1.MsgEventSeq)

	retry := &wkdb.MessageEvent{
		ChannelId:   testChannelId,
		ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo,
		EventID:     "evt_dup",
		LaneID:      "main",
		EventType:   "stream.delta",
		Payload:     []byte(`{"kind":"text","delta":"B"}`),
	}
	stored2, _, err := d.AppendMessageEventWithLaneState(retry)
	require.NoError(t, err)
	require.Equal(t, uint64(1), stored2.MsgEventSeq)

	list, err := d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.JSONEq(t, `{"kind":"text","text":"A"}`, string(list[0].Payload))
}

func TestListMessageEvents_WithLaneFilter(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_multi_lane"
	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_1", LaneID: "main", EventType: "stream.open"})
	require.NoError(t, err)
	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_2", LaneID: "tool_weather", EventType: "stream.open"})
	require.NoError(t, err)
	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{ChannelId: testChannelId, ChannelType: testChannelType, ClientMsgNo: clientMsgNo, EventID: "evt_3", LaneID: "tool_weather", EventType: "stream.close"})
	require.NoError(t, err)

	allEvents, err := d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "", 10)
	require.NoError(t, err)
	require.Len(t, allEvents, 2)

	toolEvents, err := d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "tool_weather", 10)
	require.NoError(t, err)
	require.Len(t, toolEvents, 1)
	require.Equal(t, uint64(3), toolEvents[0].MsgEventSeq)

	lanes, err := d.GetMessageLaneStates(testChannelId, testChannelType, clientMsgNo)
	require.NoError(t, err)
	require.Len(t, lanes, 2)
}

func TestAppendMessageEventWithLaneState_ChannelIsolation(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_same_client_no"

	// Channel A
	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: "channelA", ChannelType: 2,
		ClientMsgNo: clientMsgNo, EventID: "evt_a1", LaneID: "main", EventType: "stream.open",
	})
	require.NoError(t, err)

	// Channel B with same clientMsgNo
	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: "channelB", ChannelType: 2,
		ClientMsgNo: clientMsgNo, EventID: "evt_b1", LaneID: "main", EventType: "stream.open",
	})
	require.NoError(t, err)

	// Verify isolation
	lanesA, err := d.GetMessageLaneStates("channelA", 2, clientMsgNo)
	require.NoError(t, err)
	require.Len(t, lanesA, 1)

	lanesB, err := d.GetMessageLaneStates("channelB", 2, clientMsgNo)
	require.NoError(t, err)
	require.Len(t, lanesB, 1)

	// Different channels should have independent seq
	stateA, err := d.GetMessageLaneState("channelA", 2, clientMsgNo, "main")
	require.NoError(t, err)
	require.Equal(t, uint64(1), stateA.LastMsgEventSeq)

	stateB, err := d.GetMessageLaneState("channelB", 2, clientMsgNo, "main")
	require.NoError(t, err)
	require.Equal(t, uint64(1), stateB.LastMsgEventSeq)
}

// ---- New tests ----

func TestAppendMessageEventWithLaneState_TerminalRejectsNewEvents(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_terminal_reject"

	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", LaneID: "main", EventType: "stream.open",
	})
	require.NoError(t, err)

	_, lane, err := d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", LaneID: "main", EventType: "stream.close",
	})
	require.NoError(t, err)
	require.Equal(t, "closed", lane.Status)
	require.Equal(t, uint64(2), lane.LastMsgEventSeq)

	// event after close should not allocate a new seq
	stored, lane, err := d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_3", LaneID: "main", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"should be ignored"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "closed", lane.Status)
	require.Equal(t, uint64(2), stored.MsgEventSeq) // same seq as close, not incremented
}

func TestAppendMessageEventWithLaneState_StreamError(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_stream_error"

	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", LaneID: "main", EventType: "stream.open",
	})
	require.NoError(t, err)

	_, lane, err := d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", LaneID: "main", EventType: "stream.error",
		Payload: []byte(`{"error":"timeout"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "error", lane.Status)
	require.Equal(t, "timeout", lane.Error)
	require.Equal(t, uint64(2), lane.LastMsgEventSeq)

	// verify terminal
	state, err := d.GetMessageLaneState(testChannelId, testChannelType, clientMsgNo, "main")
	require.NoError(t, err)
	require.Equal(t, "error", state.Status)
	require.Equal(t, "timeout", state.Error)
}

func TestAppendMessageEventWithLaneState_StreamCancel(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_stream_cancel"

	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", LaneID: "main", EventType: "stream.open",
	})
	require.NoError(t, err)

	_, lane, err := d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", LaneID: "main", EventType: "stream.cancel",
	})
	require.NoError(t, err)
	require.Equal(t, "cancelled", lane.Status)
	require.Equal(t, uint64(2), lane.LastMsgEventSeq)
}

func TestAppendMessageEventWithLaneState_NonTextDelta(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_nontext_delta"

	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", LaneID: "main", EventType: "stream.open",
	})
	require.NoError(t, err)

	toolPayload := []byte(`{"kind":"tool_call","tool":"weather","args":{"city":"北京"}}`)
	_, lane, err := d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", LaneID: "main", EventType: "stream.delta",
		Payload: toolPayload,
	})
	require.NoError(t, err)
	require.Equal(t, "open", lane.Status)
	// non-text delta replaces snapshot payload
	require.JSONEq(t, string(toolPayload), string(lane.SnapshotPayload))
}

func TestAppendMessageEventWithLaneState_EmptyLaneNormalization(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_empty_lane"

	// send event with empty LaneID
	_, lane, err := d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", LaneID: "", EventType: "stream.open",
	})
	require.NoError(t, err)
	require.Equal(t, "main", lane.LaneID)

	// send another with whitespace LaneID
	_, lane, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", LaneID: "  ", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"x"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "main", lane.LaneID)

	// should only have 1 lane (both normalized to "main")
	lanes, err := d.GetMessageLaneStates(testChannelId, testChannelType, clientMsgNo)
	require.NoError(t, err)
	require.Len(t, lanes, 1)
	require.Equal(t, "main", lanes[0].LaneID)
}

func TestAppendMessageEventWithLaneState_SnapshotOverride(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	require.NoError(t, err)
	defer func() {
		err := d.Close()
		require.NoError(t, err)
	}()

	clientMsgNo := "cmn_snapshot_override"

	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", LaneID: "main", EventType: "stream.open",
	})
	require.NoError(t, err)

	// text deltas accumulate
	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", LaneID: "main", EventType: "stream.delta",
		Payload: []byte(`{"kind":"text","delta":"hello"}`),
	})
	require.NoError(t, err)

	// stream.snapshot replaces everything
	_, lane, err := d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_3", LaneID: "main", EventType: "stream.snapshot",
		Payload: []byte(`{"kind":"text","text":"full replacement"}`),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"kind":"text","text":"full replacement"}`, string(lane.SnapshotPayload))
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

	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", LaneID: "main", EventType: "stream.open",
	})
	require.NoError(t, err)

	// found
	evt, err := d.GetMessageEventByEventID(testChannelId, testChannelType, clientMsgNo, "evt_1")
	require.NoError(t, err)
	require.NotNil(t, evt)
	require.Equal(t, "evt_1", evt.EventID)
	require.Equal(t, "main", evt.LaneID)

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

	// create 3 lanes, each with its own last_msg_event_seq
	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_1", LaneID: "lane_a", EventType: "stream.open",
	})
	require.NoError(t, err)
	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_2", LaneID: "lane_b", EventType: "stream.open",
	})
	require.NoError(t, err)
	_, _, err = d.AppendMessageEventWithLaneState(&wkdb.MessageEvent{
		ChannelId: testChannelId, ChannelType: testChannelType,
		ClientMsgNo: clientMsgNo, EventID: "evt_3", LaneID: "lane_c", EventType: "stream.open",
	})
	require.NoError(t, err)

	// fromSeq=0 → all 3
	list, err := d.ListMessageEvents(testChannelId, testChannelType, clientMsgNo, 0, "", 10)
	require.NoError(t, err)
	require.Len(t, list, 3)

	// fromSeq=1 → skip lane_a (seq=1), return lane_b (seq=2) and lane_c (seq=3)
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
