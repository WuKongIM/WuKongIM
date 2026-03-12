package wkcache

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessageEventCache_OpenDeltaTerminal(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	meta := MessageEventSessionMeta{
		ClientMsgNo: "cmn_1",
		ChannelId:   "ch_1",
		ChannelType: 1,
		FromUid:     "u1",
	}

	lane, err := cache.UpsertSession(meta, "main", 10)
	require.NoError(t, err)
	require.Equal(t, uint64(10), lane.PersistedSeq)
	require.Equal(t, "open", lane.Status)

	lane, err = cache.AppendDelta("cmn_1", "ch_1", 1, "main", "evt_delta_1", "stream.delta", "public", 1, []byte(`{"kind":"text","delta":"hello "}`))
	require.NoError(t, err)
	require.Equal(t, "hello ", lane.TextSnapshot)

	lane, err = cache.AppendDelta("cmn_1", "ch_1", 1, "main", "evt_delta_2", "stream.delta", "public", 2, []byte(`{"kind":"text","delta":"world"}`))
	require.NoError(t, err)
	require.Equal(t, "hello world", lane.TextSnapshot)

	payload, lane, err := cache.BuildTerminalPayload("cmn_1", "ch_1", 1, "main", []byte(`{"end_reason":0}`), "evt_close", "stream.close", "public", 3)
	require.NoError(t, err)
	require.NotNil(t, lane)

	var m map[string]interface{}
	err = json.Unmarshal(payload, &m)
	require.NoError(t, err)
	snapshot := m["snapshot"].(map[string]interface{})
	require.Equal(t, "text", snapshot["kind"])
	require.Equal(t, "hello world", snapshot["text"])

	err = cache.MarkEventKeyPersisted("cmn_1", "ch_1", 1, "main", "closed", 11)
	require.NoError(t, err)
}

func TestMessageEventCache_ChannelMismatch(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_2",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "main", 1)
	require.NoError(t, err)

	_, err = cache.AppendDelta("cmn_2", "ch_2", 1, "main", "evt", "stream.delta", "public", 1, []byte(`{"kind":"text","delta":"x"}`))
	require.ErrorIs(t, err, ErrMessageEventChannelMismatch)
}

// ---- AppendDelta tests ----

func TestMessageEventCache_AppendDelta_TextAccumulation(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_ad_text",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "main", 0)
	require.NoError(t, err)

	lane, err := cache.AppendDelta("cmn_ad_text", "ch_1", 1, "main",
		"evt_1", "stream.delta", "public", 1,
		[]byte(`{"kind":"text","delta":"hello "}`))
	require.NoError(t, err)
	require.Equal(t, "hello ", lane.TextSnapshot)
	require.Empty(t, lane.SnapshotPayload)

	lane, err = cache.AppendDelta("cmn_ad_text", "ch_1", 1, "main",
		"evt_2", "stream.delta", "public", 2,
		[]byte(`{"kind":"text","delta":"world"}`))
	require.NoError(t, err)
	require.Equal(t, "hello world", lane.TextSnapshot)
}

func TestMessageEventCache_AppendDelta_NonTextPayload(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_ad_nontext",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "main", 0)
	require.NoError(t, err)

	rawPayload := []byte(`{"kind":"tool_call","tool":"weather","args":{"city":"北京"}}`)
	lane, err := cache.AppendDelta("cmn_ad_nontext", "ch_1", 1, "main",
		"evt_1", "stream.delta", "public", 1, rawPayload)
	require.NoError(t, err)
	require.Empty(t, lane.TextSnapshot)
	require.JSONEq(t, string(rawPayload), string(lane.SnapshotPayload))

	// second non-text delta replaces the first
	rawPayload2 := []byte(`{"kind":"tool_result","result":"晴天 25°C"}`)
	lane, err = cache.AppendDelta("cmn_ad_nontext", "ch_1", 1, "main",
		"evt_2", "stream.delta", "public", 2, rawPayload2)
	require.NoError(t, err)
	require.JSONEq(t, string(rawPayload2), string(lane.SnapshotPayload))
}

func TestMessageEventCache_AppendDelta_SessionNotFound(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.AppendDelta("no_such_session", "ch_1", 1, "main",
		"evt_1", "stream.delta", "public", 1,
		[]byte(`{"kind":"text","delta":"x"}`))
	require.ErrorIs(t, err, ErrMessageEventSessionNotFound)
}

func TestMessageEventCache_AppendDelta_ClosedKey(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_ad_closed",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "main", 0)
	require.NoError(t, err)

	err = cache.MarkEventKeyPersisted("cmn_ad_closed", "ch_1", 1, "main", "closed", 5)
	require.NoError(t, err)

	// re-create session since MarkEventKeyPersisted may have cleaned it up
	_, err = cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_ad_closed2",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "main", 0)
	require.NoError(t, err)
	err = cache.MarkEventKeyPersisted("cmn_ad_closed2", "ch_1", 1, "main", "closed", 5)
	require.NoError(t, err)

	_, err = cache.AppendDelta("cmn_ad_closed2", "ch_1", 1, "main",
		"evt_1", "stream.delta", "public", 1,
		[]byte(`{"kind":"text","delta":"x"}`))
	// session should be cleaned up after all lanes terminal, so we get SessionNotFound
	require.Error(t, err)
}

func TestMessageEventCache_AppendDelta_ChannelMismatch(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_ad_mismatch",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "main", 0)
	require.NoError(t, err)

	_, err = cache.AppendDelta("cmn_ad_mismatch", "ch_wrong", 1, "main",
		"evt_1", "stream.delta", "public", 1,
		[]byte(`{"kind":"text","delta":"x"}`))
	require.ErrorIs(t, err, ErrMessageEventChannelMismatch)
}

func TestMessageEventCache_AppendDelta_NonMainKey(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_ad_lane",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "tool_weather", 0)
	require.NoError(t, err)

	lane, err := cache.AppendDelta("cmn_ad_lane", "ch_1", 1, "tool_weather",
		"evt_1", "stream.delta", "public", 1,
		[]byte(`{"kind":"text","delta":"天气"}`))
	require.NoError(t, err)
	require.Equal(t, "天气", lane.TextSnapshot)
	require.Equal(t, "tool_weather", lane.EventKey)
}

// ---- BuildTerminalPayload with non-text snapshot ----

func TestMessageEventCache_BuildTerminalPayload_NonTextSnapshot(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_term_nontext",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "main", 0)
	require.NoError(t, err)

	// append non-text delta
	_, err = cache.AppendDelta("cmn_term_nontext", "ch_1", 1, "main",
		"evt_1", "stream.delta", "public", 1,
		[]byte(`{"kind":"tool_result","data":"ok"}`))
	require.NoError(t, err)

	// close
	payload, lane, err := cache.BuildTerminalPayload(
		"cmn_term_nontext", "ch_1", 1, "main",
		[]byte(`{"end_reason":0}`),
		"evt_close", "stream.close", "public", 2)
	require.NoError(t, err)
	require.NotNil(t, lane)

	var m map[string]interface{}
	err = json.Unmarshal(payload, &m)
	require.NoError(t, err)
	snapshot := m["snapshot"].(map[string]interface{})
	require.Equal(t, "tool_result", snapshot["kind"])
	require.Equal(t, "ok", snapshot["data"])
}

func TestMessageEventCache_BuildTerminalPayload_NoSnapshot(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_term_empty",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "main", 0)
	require.NoError(t, err)

	// close without any deltas
	originalPayload := []byte(`{"end_reason":0}`)
	payload, _, err := cache.BuildTerminalPayload(
		"cmn_term_empty", "ch_1", 1, "main",
		originalPayload,
		"evt_close", "stream.close", "public", 1)
	require.NoError(t, err)
	// payload should be unchanged (no snapshot to merge)
	require.JSONEq(t, string(originalPayload), string(payload))
}

// ---- Session auto-cleanup ----

func TestMessageEventCache_SessionAutoCleanup(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_cleanup",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "main", 0)
	require.NoError(t, err)

	// session exists
	meta, ok := cache.GetSessionMeta("cmn_cleanup", "ch_1", 1)
	require.True(t, ok)
	require.NotNil(t, meta)

	// mark lane as closed → session should be auto-removed
	err = cache.MarkEventKeyPersisted("cmn_cleanup", "ch_1", 1, "main", "closed", 10)
	require.NoError(t, err)

	meta, ok = cache.GetSessionMeta("cmn_cleanup", "ch_1", 1)
	require.False(t, ok)
	require.Nil(t, meta)
}

func TestMessageEventCache_SessionAutoCleanup_MultiKey(t *testing.T) {
	cache := NewMessageEventCache(nil)
	defer cache.Close()

	_, err := cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_cleanup_ml",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "main", 0)
	require.NoError(t, err)
	_, err = cache.UpsertSession(MessageEventSessionMeta{
		ClientMsgNo: "cmn_cleanup_ml",
		ChannelId:   "ch_1",
		ChannelType: 1,
	}, "tool_key", 0)
	require.NoError(t, err)

	// close only one lane → session should remain
	err = cache.MarkEventKeyPersisted("cmn_cleanup_ml", "ch_1", 1, "main", "closed", 5)
	require.NoError(t, err)
	meta, ok := cache.GetSessionMeta("cmn_cleanup_ml", "ch_1", 1)
	require.True(t, ok)
	require.NotNil(t, meta)

	// close second lane → session should be removed
	err = cache.MarkEventKeyPersisted("cmn_cleanup_ml", "ch_1", 1, "tool_key", "closed", 6)
	require.NoError(t, err)
	meta, ok = cache.GetSessionMeta("cmn_cleanup_ml", "ch_1", 1)
	require.False(t, ok)
	require.Nil(t, meta)
}
