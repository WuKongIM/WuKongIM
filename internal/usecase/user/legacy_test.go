package user

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestDeviceQuitClearsTokenAndKicksSelectedDevice(t *testing.T) {
	devices := &fakeDeviceStore{
		devices: map[deviceKey]metadb.Device{
			{uid: "u1", flag: int64(frame.APP)}: {UID: "u1", DeviceFlag: int64(frame.APP), Token: "token-1", DeviceLevel: int64(frame.DeviceLevelSlave)},
		},
	}
	reg := online.NewRegistry()
	sess := newRecordingSession(11, "tcp")
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:  11,
		UID:        "u1",
		DeviceFlag: frame.APP,
		Session:    sess,
	}))

	var scheduled []time.Duration
	app := New(Options{
		Devices: devices,
		Online:  reg,
		AfterFunc: func(d time.Duration, fn func()) {
			scheduled = append(scheduled, d)
			fn()
		},
	})

	require.NoError(t, app.DeviceQuit(context.Background(), DeviceQuitCommand{
		UID:        "u1",
		DeviceFlag: int(frame.APP),
	}))

	require.Equal(t, []metadb.Device{{
		UID:         "u1",
		DeviceFlag:  int64(frame.APP),
		Token:       "",
		DeviceLevel: int64(frame.DeviceLevelMaster),
	}}, devices.upserted)
	require.Len(t, sess.WrittenFrames(), 1)
	require.True(t, sess.closed)
	require.Equal(t, []time.Duration{2 * time.Second}, scheduled)
}

func TestDeviceQuitAllDevicesIgnoresMissingDevices(t *testing.T) {
	devices := &fakeDeviceStore{
		devices: map[deviceKey]metadb.Device{
			{uid: "u1", flag: int64(frame.WEB)}: {UID: "u1", DeviceFlag: int64(frame.WEB), Token: "web-token"},
		},
	}
	app := New(Options{Devices: devices})

	require.NoError(t, app.DeviceQuit(context.Background(), DeviceQuitCommand{
		UID:        "u1",
		DeviceFlag: -1,
	}))

	require.Equal(t, []metadb.Device{{
		UID:         "u1",
		DeviceFlag:  int64(frame.WEB),
		Token:       "",
		DeviceLevel: int64(frame.DeviceLevelMaster),
	}}, devices.upserted)
}

func TestOnlineStatusUsesPresenceDirectory(t *testing.T) {
	directory := &fakePresenceDirectory{
		routes: map[string][]presence.Route{
			"u1": {
				{UID: "u1", DeviceFlag: uint8(frame.APP)},
				{UID: "u1", DeviceFlag: uint8(frame.WEB)},
			},
			"u2": {
				{UID: "u2", DeviceFlag: uint8(frame.PC)},
			},
		},
	}
	app := New(Options{Presence: directory})

	statuses, err := app.OnlineStatus(context.Background(), []string{"u2", "u1", "missing"})

	require.NoError(t, err)
	require.Equal(t, []OnlineStatus{
		{UID: "u2", DeviceFlag: uint8(frame.PC), Online: 1},
		{UID: "u1", DeviceFlag: uint8(frame.APP), Online: 1},
		{UID: "u1", DeviceFlag: uint8(frame.WEB), Online: 1},
	}, statuses)
	require.Equal(t, [][]string{{"u2", "u1", "missing"}}, directory.calls)
}

func TestSystemUIDsPersistAndListThroughStore(t *testing.T) {
	systemUIDs := &fakeSystemUIDStore{}
	app := New(Options{SystemUIDs: systemUIDs})

	require.NoError(t, app.AddSystemUIDs(context.Background(), []string{"sys1", "sys2"}))
	require.NoError(t, app.RemoveSystemUIDs(context.Background(), []string{"sys1"}))
	got, err := app.ListSystemUIDs(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"sys2"}, got)
	require.Equal(t, []subscriberCall{{channelID: systemUIDChannelID, channelType: systemUIDChannelType, uids: []string{"sys1", "sys2"}}}, systemUIDs.added)
	require.Equal(t, []subscriberCall{{channelID: systemUIDChannelID, channelType: systemUIDChannelType, uids: []string{"sys1"}}}, systemUIDs.removed)
}

func TestSystemUIDCacheEndpointsDoNotRequireStore(t *testing.T) {
	app := New(Options{})

	require.NoError(t, app.AddSystemUIDsToCache([]string{"sys1", "sys2"}))
	require.True(t, app.IsSystemUID("sys1"))
	require.NoError(t, app.RemoveSystemUIDsFromCache([]string{"sys1"}))
	require.False(t, app.IsSystemUID("sys1"))
	require.True(t, app.IsSystemUID("sys2"))
}

func TestUpdateTokenRejectsDefaultSystemUID(t *testing.T) {
	app := New(Options{})

	err := app.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:   DefaultSystemUID,
		Token: "token-1",
	})

	require.EqualError(t, err, "系统账号不允许更新token！")
}

type fakePresenceDirectory struct {
	routes map[string][]presence.Route
	calls  [][]string
}

func (f *fakePresenceDirectory) EndpointsByUIDs(_ context.Context, uids []string) (map[string][]presence.Route, error) {
	f.calls = append(f.calls, append([]string(nil), uids...))
	out := make(map[string][]presence.Route, len(f.routes))
	for uid, routes := range f.routes {
		out[uid] = append([]presence.Route(nil), routes...)
	}
	return out, nil
}

type fakeSystemUIDStore struct {
	added       []subscriberCall
	removed     []subscriberCall
	subscribers []string
}

type subscriberCall struct {
	channelID   string
	channelType int64
	uids        []string
}

func (f *fakeSystemUIDStore) AddChannelSubscribers(_ context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	f.added = append(f.added, subscriberCall{channelID: channelID, channelType: channelType, uids: append([]string(nil), uids...)})
	f.subscribers = append(f.subscribers, uids...)
	return nil
}

func (f *fakeSystemUIDStore) RemoveChannelSubscribers(_ context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	f.removed = append(f.removed, subscriberCall{channelID: channelID, channelType: channelType, uids: append([]string(nil), uids...)})
	removeSet := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		removeSet[uid] = struct{}{}
	}
	next := f.subscribers[:0]
	for _, uid := range f.subscribers {
		if _, ok := removeSet[uid]; !ok {
			next = append(next, uid)
		}
	}
	f.subscribers = next
	return nil
}

func (f *fakeSystemUIDStore) ListChannelSubscribers(_ context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	return append([]string(nil), f.subscribers...), "", true, nil
}
