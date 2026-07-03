//go:build integration
// +build integration

package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestAppLegacyUserManagementEndpointsPersistAndQueryRuntime(t *testing.T) {
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
	postLegacyJSON(t, baseURL+"/user/token", `{"uid":"legacy-user","token":"token-1","device_flag":0,"device_level":1}`, `{"status":200}`)

	gotDevice, err := app.Store().GetDevice(context.Background(), "legacy-user", int64(frame.APP))
	require.NoError(t, err)
	require.Equal(t, "token-1", gotDevice.Token)

	postLegacyJSON(t, baseURL+"/user/device_quit", `{"uid":"legacy-user","device_flag":0}`, `{"status":200}`)
	gotDevice, err = app.Store().GetDevice(context.Background(), "legacy-user", int64(frame.APP))
	require.NoError(t, err)
	require.Empty(t, gotDevice.Token)

	session := &userIntegrationSession{id: 101, listener: "api"}
	require.NoError(t, app.presenceApp.Activate(context.Background(), presence.ActivateCommand{
		UID:         "online-user",
		DeviceID:    "device-1",
		DeviceFlag:  frame.WEB,
		DeviceLevel: frame.DeviceLevelMaster,
		Listener:    "api",
		Session:     session,
	}))

	resp, err := http.Post(baseURL+"/user/onlinestatus", "application/json", bytes.NewBufferString(`["online-user","offline-user"]`))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `[{"uid":"online-user","device_flag":1,"online":1}]`, string(body))

	postLegacyJSON(t, baseURL+"/user/systemuids_add", `{"uids":["sys1","sys2"]}`, `{"status":200}`)
	resp, err = http.Get(baseURL + "/user/systemuids")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `["sys1","sys2"]`, string(body))

	postLegacyJSON(t, baseURL+"/user/systemuids_remove", `{"uids":["sys1"]}`, `{"status":200}`)
	resp, err = http.Get(baseURL + "/user/systemuids")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `["sys2"]`, string(body))

	postLegacyJSON(t, baseURL+"/user/systemuids_add_to_cache", `{"uids":["sys-cache"]}`, `{"status":200}`)
	postLegacyJSON(t, baseURL+"/user/systemuids_remove_from_cache", `{"uids":["sys-cache"]}`, `{"status":200}`)
}

func TestAppThreeNodeLegacyUserManagementEndpointsRouteThroughCluster(t *testing.T) {
	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	apiNodeID := leaderID%3 + 1
	apiNode := harness.apps[apiNodeID]
	require.NotNil(t, apiNode)

	baseURL := "http://" + apiNode.API().Addr()
	uid := "three-node-user-it"

	postLegacyJSON(t, baseURL+"/user/token", `{"uid":"three-node-user-it","token":"token-cluster","device_flag":0,"device_level":1}`, `{"status":200}`)
	for _, node := range harness.orderedApps() {
		node := node
		require.Eventually(t, func() bool {
			device, err := node.DB().ForHashSlot(node.Cluster().HashSlotForKey(uid)).GetDevice(context.Background(), uid, int64(frame.APP))
			return err == nil && device.Token == "token-cluster"
		}, 10*time.Second, 50*time.Millisecond)
	}

	postLegacyJSON(t, baseURL+"/user/device_quit", `{"uid":"three-node-user-it","device_flag":0}`, `{"status":200}`)
	for _, node := range harness.orderedApps() {
		node := node
		require.Eventually(t, func() bool {
			device, err := node.DB().ForHashSlot(node.Cluster().HashSlotForKey(uid)).GetDevice(context.Background(), uid, int64(frame.APP))
			return err == nil && device.Token == ""
		}, 10*time.Second, 50*time.Millisecond)
	}

	onlineNode := harness.apps[leaderID]
	session := &userIntegrationSession{id: 202, listener: "api"}
	require.NoError(t, onlineNode.presenceApp.Activate(context.Background(), presence.ActivateCommand{
		UID:         "three-node-online-user",
		DeviceID:    "device-2",
		DeviceFlag:  frame.PC,
		DeviceLevel: frame.DeviceLevelMaster,
		Listener:    "api",
		Session:     session,
	}))

	resp, err := http.Post(baseURL+"/user/onlinestatus", "application/json", bytes.NewBufferString(`["three-node-online-user","missing-user"]`))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `[{"uid":"three-node-online-user","device_flag":2,"online":1}]`, string(body))

	postLegacyJSON(t, baseURL+"/user/systemuids_add", `{"uids":["sys-three-a","sys-three-b"]}`, `{"status":200}`)
	for _, node := range harness.orderedApps() {
		node := node
		require.Eventually(t, func() bool {
			uids, _, done, err := node.DB().ForHashSlot(node.Cluster().HashSlotForKey(userSystemUIDsChannelID)).ListSubscribersPage(context.Background(), userSystemUIDsChannelID, int64(frame.SYSTEM), "", 10)
			return err == nil && done && equalStrings(uids, []string{"sys-three-a", "sys-three-b"}) && node.userApp.IsSystemUID("sys-three-a")
		}, 10*time.Second, 50*time.Millisecond)
	}

	postLegacyJSON(t, baseURL+"/user/systemuids_remove", `{"uids":["sys-three-a"]}`, `{"status":200}`)
	for _, node := range harness.orderedApps() {
		node := node
		require.Eventually(t, func() bool {
			uids, _, done, err := node.DB().ForHashSlot(node.Cluster().HashSlotForKey(userSystemUIDsChannelID)).ListSubscribersPage(context.Background(), userSystemUIDsChannelID, int64(frame.SYSTEM), "", 10)
			return err == nil && done && equalStrings(uids, []string{"sys-three-b"}) && !node.userApp.IsSystemUID("sys-three-a")
		}, 10*time.Second, 50*time.Millisecond)
	}
}

const userSystemUIDsChannelID = "__wk_internal_system_uids__"

type userIntegrationSession struct {
	id       uint64
	listener string
	closed   bool
}

func (s *userIntegrationSession) ID() uint64                   { return s.id }
func (s *userIntegrationSession) Listener() string             { return s.listener }
func (s *userIntegrationSession) RemoteAddr() string           { return "" }
func (s *userIntegrationSession) LocalAddr() string            { return "" }
func (s *userIntegrationSession) WriteFrame(frame.Frame) error { return nil }
func (s *userIntegrationSession) Close() error {
	s.closed = true
	return nil
}
func (s *userIntegrationSession) SetValue(string, any) {}
func (s *userIntegrationSession) Value(string) any     { return nil }
