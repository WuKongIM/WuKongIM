package management

import (
	"context"
	"hash/crc32"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestListUsersAggregatesDeviceAndPresenceSummary(t *testing.T) {
	reader := newFakeManagementUserReader()
	reader.slotPages[1] = map[metadb.UserCursor]fakeManagementUserPage{
		{}: {
			items:  []metadb.User{{UID: "u1"}, {UID: "u2"}},
			cursor: metadb.UserCursor{UID: "u2"},
			done:   true,
		},
	}
	reader.devices[managementDeviceKey{"u1", int64(frame.APP)}] = metadb.Device{
		UID: "u1", DeviceFlag: int64(frame.APP), Token: "token-1", DeviceLevel: int64(frame.DeviceLevelMaster),
	}
	presenceDir := &fakeManagementPresence{routes: map[string][]presence.Route{
		"u1": {{UID: "u1", NodeID: 2, SessionID: 10, DeviceFlag: uint8(frame.APP), DeviceLevel: uint8(frame.DeviceLevelMaster)}},
	}}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotIDs:        []multiraft.SlotID{1},
			slotForKey:     map[string]multiraft.SlotID{"u1": 1, "u2": 1},
			hashSlotForKey: map[string]uint16{"u1": 7, "u2": 8},
		},
		Users:        reader,
		UserPresence: presenceDir,
	})

	got, err := app.ListUsers(context.Background(), ListUsersRequest{Limit: 50})
	require.NoError(t, err)
	require.Equal(t, []UserListItem{
		{UID: "u1", SlotID: 1, HashSlot: 7, Online: true, OnlineDeviceCount: 1, OnlineDeviceFlags: []string{"app"}, DeviceCount: 1, TokenSetCount: 1},
		{UID: "u2", SlotID: 1, HashSlot: 8},
	}, got.Items)
	require.False(t, got.HasMore)
}

func TestListUsersFiltersByKeywordAndBindsCursor(t *testing.T) {
	reader := newFakeManagementUserReader()
	reader.slotPages[1] = map[metadb.UserCursor]fakeManagementUserPage{
		{}: {
			items:  []metadb.User{{UID: "alice"}, {UID: "bob"}, {UID: "carol"}},
			cursor: metadb.UserCursor{UID: "carol"},
			done:   true,
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotIDs:        []multiraft.SlotID{1},
			slotForKey:     map[string]multiraft.SlotID{"alice": 1, "bob": 1, "carol": 1},
			hashSlotForKey: map[string]uint16{"alice": 1, "bob": 2, "carol": 3},
		},
		Users: reader,
	})

	got, err := app.ListUsers(context.Background(), ListUsersRequest{Limit: 1, Keyword: "bo"})
	require.NoError(t, err)
	require.Equal(t, []UserListItem{{UID: "bob", SlotID: 1, HashSlot: 2}}, got.Items)
	require.False(t, got.HasMore)

	_, err = app.ListUsers(context.Background(), ListUsersRequest{
		Limit:   1,
		Keyword: "alice",
		Cursor:  UserListCursor{SlotID: 1, UID: "bob", KeywordHash: crc32.ChecksumIEEE([]byte("bob"))},
	})
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestGetUserReturnsDetailWithDevicesAndConnections(t *testing.T) {
	reader := newFakeManagementUserReader()
	reader.users["u1"] = metadb.User{UID: "u1"}
	reader.devices[managementDeviceKey{"u1", int64(frame.APP)}] = metadb.Device{
		UID: "u1", DeviceFlag: int64(frame.APP), Token: "token-1", DeviceLevel: int64(frame.DeviceLevelMaster),
	}
	presenceDir := &fakeManagementPresence{routes: map[string][]presence.Route{
		"u1": {{
			UID: "u1", NodeID: 2, BootID: 4, SessionID: 10, DeviceID: "d1",
			DeviceFlag: uint8(frame.APP), DeviceLevel: uint8(frame.DeviceLevelMaster), Listener: "tcp",
		}},
	}}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey:     map[string]multiraft.SlotID{"u1": 1},
			hashSlotForKey: map[string]uint16{"u1": 7},
		},
		Users:        reader,
		UserPresence: presenceDir,
	})

	got, err := app.GetUser(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, "u1", got.UID)
	require.Equal(t, uint32(1), got.SlotID)
	require.Equal(t, uint16(7), got.HashSlot)
	require.True(t, got.Online)
	require.Equal(t, []UserDevice{{
		DeviceFlag: "app", DeviceLevel: "master", TokenSet: true, Online: true, OnlineSessionCount: 1,
	}}, got.Devices)
	require.Equal(t, []Connection{{
		NodeID: 2, SessionID: 10, UID: "u1", DeviceID: "d1", DeviceFlag: "app", DeviceLevel: "master", Listener: "tcp",
	}}, got.Connections)
}

func TestKickUserClearsTokenAndAppliesRouteActions(t *testing.T) {
	operator := &fakeManagementUserOperator{}
	actions := &fakeManagementRouteActions{}
	presenceDir := &fakeManagementPresence{routes: map[string][]presence.Route{
		"u1": {
			{UID: "u1", NodeID: 2, BootID: 9, SessionID: 10, DeviceFlag: uint8(frame.APP)},
			{UID: "u1", NodeID: 3, BootID: 8, SessionID: 11, DeviceFlag: uint8(frame.WEB)},
			{UID: "u1", NodeID: 4, BootID: 7, SessionID: 12, DeviceFlag: uint8(frame.SYSTEM)},
		},
	}}
	app := New(Options{UserOperator: operator, UserPresence: presenceDir, UserActions: actions})

	got, err := app.KickUser(context.Background(), KickUserRequest{UID: "u1", DeviceFlag: "all"})
	require.NoError(t, err)
	require.Equal(t, KickUserResponse{UID: "u1", DeviceFlag: "all", Changed: true}, got)
	require.Equal(t, []userusecase.DeviceQuitCommand{{UID: "u1", DeviceFlag: -1}}, operator.deviceQuitCalls)
	require.Equal(t, []presence.RouteAction{
		{UID: "u1", NodeID: 2, BootID: 9, SessionID: 10, Kind: "kick_then_close", Reason: "manager force offline"},
		{UID: "u1", NodeID: 3, BootID: 8, SessionID: 11, Kind: "kick_then_close", Reason: "manager force offline"},
	}, actions.calls)
}

func TestResetUserTokenGeneratesTokenAndUpdatesDevice(t *testing.T) {
	operator := &fakeManagementUserOperator{}
	app := New(Options{UserOperator: operator})

	got, err := app.ResetUserToken(context.Background(), ResetUserTokenRequest{
		UID: "u1", DeviceFlag: "app", DeviceLevel: "master",
	})
	require.NoError(t, err)
	require.NotEmpty(t, got.Token)
	require.Equal(t, "u1", got.UID)
	require.Equal(t, "app", got.DeviceFlag)
	require.Equal(t, "master", got.DeviceLevel)
	require.Len(t, operator.updateTokenCalls, 1)
	require.Equal(t, userusecase.UpdateTokenCommand{
		UID: "u1", Token: got.Token, DeviceFlag: frame.APP, DeviceLevel: frame.DeviceLevelMaster,
	}, operator.updateTokenCalls[0])
}

type fakeManagementUserReader struct {
	slotPages map[multiraft.SlotID]map[metadb.UserCursor]fakeManagementUserPage
	users     map[string]metadb.User
	devices   map[managementDeviceKey]metadb.Device
}

type fakeManagementUserPage struct {
	items  []metadb.User
	cursor metadb.UserCursor
	done   bool
}

type managementDeviceKey struct {
	uid        string
	deviceFlag int64
}

func newFakeManagementUserReader() *fakeManagementUserReader {
	return &fakeManagementUserReader{
		slotPages: make(map[multiraft.SlotID]map[metadb.UserCursor]fakeManagementUserPage),
		users:     make(map[string]metadb.User),
		devices:   make(map[managementDeviceKey]metadb.Device),
	}
}

func (f *fakeManagementUserReader) ScanUsersSlotPage(_ context.Context, slotID multiraft.SlotID, after metadb.UserCursor, _ int) ([]metadb.User, metadb.UserCursor, bool, error) {
	if slotPages := f.slotPages[slotID]; slotPages != nil {
		if page, ok := slotPages[after]; ok {
			return append([]metadb.User(nil), page.items...), page.cursor, page.done, nil
		}
	}
	return nil, after, true, nil
}

func (f *fakeManagementUserReader) GetUser(_ context.Context, uid string) (metadb.User, error) {
	user, ok := f.users[uid]
	if !ok {
		return metadb.User{}, metadb.ErrNotFound
	}
	return user, nil
}

func (f *fakeManagementUserReader) GetDevice(_ context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	device, ok := f.devices[managementDeviceKey{uid, deviceFlag}]
	if !ok {
		return metadb.Device{}, metadb.ErrNotFound
	}
	return device, nil
}

type fakeManagementPresence struct {
	routes map[string][]presence.Route
}

func (f *fakeManagementPresence) EndpointsByUIDs(_ context.Context, uids []string) (map[string][]presence.Route, error) {
	out := make(map[string][]presence.Route, len(uids))
	for _, uid := range uids {
		out[uid] = append([]presence.Route(nil), f.routes[uid]...)
	}
	return out, nil
}

type fakeManagementRouteActions struct {
	calls []presence.RouteAction
}

func (f *fakeManagementRouteActions) ApplyRouteAction(_ context.Context, action presence.RouteAction) error {
	f.calls = append(f.calls, action)
	return nil
}

type fakeManagementUserOperator struct {
	updateTokenCalls []userusecase.UpdateTokenCommand
	deviceQuitCalls  []userusecase.DeviceQuitCommand
}

func (f *fakeManagementUserOperator) UpdateToken(_ context.Context, cmd userusecase.UpdateTokenCommand) error {
	f.updateTokenCalls = append(f.updateTokenCalls, cmd)
	return nil
}

func (f *fakeManagementUserOperator) DeviceQuit(_ context.Context, cmd userusecase.DeviceQuitCommand) error {
	f.deviceQuitCalls = append(f.deviceQuitCalls, cmd)
	return nil
}
