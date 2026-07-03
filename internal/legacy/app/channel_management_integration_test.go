//go:build integration
// +build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestAppChannelManagementLegacyEndpointsPersistThroughStore(t *testing.T) {
	cfg := testConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"

	app, err := New(cfg)
	require.NoError(t, err)

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})
	require.Eventually(t, func() bool {
		_, err := app.Cluster().LeaderOf(1)
		return err == nil
	}, 3*time.Second, 50*time.Millisecond)

	baseURL := "http://" + app.API().Addr()
	channelID := "channel-it"

	postLegacyJSON(t, baseURL+"/channel", `{"channel_id":"channel-it","channel_type":2,"ban":1,"subscribers":["u1","u2"]}`, `{"status":200}`)

	gotChannel, err := app.Store().GetChannel(context.Background(), channelID, int64(frame.ChannelTypeGroup))
	require.NoError(t, err)
	require.Equal(t, channelID, gotChannel.ChannelID)
	require.Equal(t, int64(frame.ChannelTypeGroup), gotChannel.ChannelType)
	require.Equal(t, int64(1), gotChannel.Ban)

	subscribers, cursor, done, err := app.Store().ListChannelSubscribers(context.Background(), channelID, int64(frame.ChannelTypeGroup), "", 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, "u2", cursor)
	require.Equal(t, []string{"u1", "u2"}, subscribers)

	postLegacyJSON(t, baseURL+"/channel/subscriber_remove", `{"channel_id":"channel-it","channel_type":2,"subscribers":["u1"]}`, `{"status":200}`)
	subscribers, _, _, err = app.Store().ListChannelSubscribers(context.Background(), channelID, int64(frame.ChannelTypeGroup), "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"u2"}, subscribers)

	postLegacyJSON(t, baseURL+"/channel/whitelist_set", `{"channel_id":"channel-it","channel_type":2,"uids":["u3","u4"]}`, `{"status":200}`)
	resp, err := http.Get(baseURL + "/channel/whitelist?channel_id=channel-it&channel_type=2")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `[{"id":0,"uid":"u3"},{"id":0,"uid":"u4"}]`, string(body))

	postLegacyJSON(t, baseURL+"/channel/blacklist_set", `{"channel_id":"channel-it","channel_type":2,"uids":["u5"]}`, `{"status":200}`)
	postLegacyJSON(t, baseURL+"/tmpchannel/subscriber_set", `{"channel_id":"tmp-it","uids":["u6","u7"]}`, `{"status":200}`)

	postLegacyJSON(t, baseURL+"/channel/subscriber_remove_all", `{"channel_id":"channel-it","channel_type":2}`, `{"status":200}`)
	subscribers, _, done, err = app.Store().ListChannelSubscribers(context.Background(), channelID, int64(frame.ChannelTypeGroup), "", 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Empty(t, subscribers)
}

func TestAppThreeNodeChannelManagementLegacyEndpointsReplicate(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	apiNodeID := leaderID%3 + 1
	apiNode := harness.apps[apiNodeID]
	require.NotNil(t, apiNode)

	baseURL := "http://" + apiNode.API().Addr()
	channelID := "three-node-channel-it"

	postLegacyJSON(t, baseURL+"/channel", `{"channel_id":"three-node-channel-it","channel_type":2,"ban":1,"subscribers":["u1","u2"]}`, `{"status":200}`)
	postLegacyJSON(t, baseURL+"/channel/whitelist_set", `{"channel_id":"three-node-channel-it","channel_type":2,"uids":["u3","u4"]}`, `{"status":200}`)
	postLegacyJSON(t, baseURL+"/channel/blacklist_set", `{"channel_id":"three-node-channel-it","channel_type":2,"uids":["u5"]}`, `{"status":200}`)

	for _, node := range harness.orderedApps() {
		node := node
		require.Eventually(t, func() bool {
			got, err := node.Store().GetChannel(context.Background(), channelID, int64(frame.ChannelTypeGroup))
			if err != nil || got.ChannelID != channelID || got.Ban != 1 {
				return false
			}
			subscribers, _, done, err := node.Store().ListChannelSubscribers(context.Background(), channelID, int64(frame.ChannelTypeGroup), "", 10)
			return err == nil && done && equalStrings(subscribers, []string{"u1", "u2"})
		}, 10*time.Second, 50*time.Millisecond)
	}

	resp, err := http.Get(baseURL + "/channel/whitelist?channel_id=three-node-channel-it&channel_type=2")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `[{"id":0,"uid":"u3"},{"id":0,"uid":"u4"}]`, string(body))

	postLegacyJSON(t, baseURL+"/channel/subscriber_remove_all", `{"channel_id":"three-node-channel-it","channel_type":2}`, `{"status":200}`)
	for _, node := range harness.orderedApps() {
		node := node
		require.Eventually(t, func() bool {
			subscribers, _, done, err := node.Store().ListChannelSubscribers(context.Background(), channelID, int64(frame.ChannelTypeGroup), "", 10)
			return err == nil && done && len(subscribers) == 0
		}, 10*time.Second, 50*time.Millisecond)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func postLegacyJSON(t *testing.T, url, payload, wantBody string) {
	t.Helper()

	resp, err := http.Post(url, "application/json", bytes.NewBufferString(payload))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, wantBody, string(body))

	var envelope struct {
		Status int `json:"status"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	require.Equal(t, http.StatusOK, envelope.Status)
}
