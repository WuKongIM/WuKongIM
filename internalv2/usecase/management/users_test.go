package management

import (
	"context"
	"hash/crc32"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	userusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestListUsersAggregatesDeviceAndPresenceSummary(t *testing.T) {
	snapshot := singleUserSlotSnapshot()
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
		"u1": {{UID: "u1", OwnerNodeID: 2, SessionID: 10, DeviceFlag: uint8(frame.APP), DeviceLevel: uint8(frame.DeviceLevelMaster)}},
	}}
	app := New(Options{
		Cluster:      fakeNodeSnapshotReader{snapshot: snapshot},
		Users:        reader,
		UserPresence: presenceDir,
	})

	got, err := app.ListUsers(context.Background(), ListUsersRequest{Limit: 50})
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	want := []UserListItem{
		{UID: "u1", SlotID: 1, HashSlot: routing.HashSlotForKey("u1", snapshot.HashSlots.Count), Online: true, OnlineDeviceCount: 1, OnlineDeviceFlags: []string{"app"}, DeviceCount: 1, TokenSetCount: 1},
		{UID: "u2", SlotID: 1, HashSlot: routing.HashSlotForKey("u2", snapshot.HashSlots.Count)},
	}
	if !sameUserListItems(got.Items, want) {
		t.Fatalf("items = %#v, want %#v", got.Items, want)
	}
	if got.HasMore {
		t.Fatalf("HasMore = true, want false")
	}
}

func TestListUsersFiltersByKeywordAndBindsCursor(t *testing.T) {
	snapshot := singleUserSlotSnapshot()
	reader := newFakeManagementUserReader()
	reader.slotPages[1] = map[metadb.UserCursor]fakeManagementUserPage{
		{}: {
			items:  []metadb.User{{UID: "alice"}, {UID: "bob"}, {UID: "carol"}},
			cursor: metadb.UserCursor{UID: "carol"},
			done:   true,
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		Users:   reader,
	})

	got, err := app.ListUsers(context.Background(), ListUsersRequest{Limit: 1, Keyword: "bo"})
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	want := []UserListItem{{UID: "bob", SlotID: 1, HashSlot: routing.HashSlotForKey("bob", snapshot.HashSlots.Count)}}
	if !sameUserListItems(got.Items, want) {
		t.Fatalf("items = %#v, want %#v", got.Items, want)
	}
	if got.HasMore {
		t.Fatalf("HasMore = true, want false")
	}

	_, err = app.ListUsers(context.Background(), ListUsersRequest{
		Limit:   1,
		Keyword: "alice",
		Cursor:  UserListCursor{SlotID: 1, UID: "bob", KeywordHash: crc32.ChecksumIEEE([]byte("bob"))},
	})
	if err != metadb.ErrInvalidArgument {
		t.Fatalf("ListUsers() filter mismatch error = %v, want %v", err, metadb.ErrInvalidArgument)
	}
}

func TestListUsersUsesLastItemCursorWhenNextSlotHasData(t *testing.T) {
	snapshot := control.Snapshot{
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1}},
			{SlotID: 2, DesiredPeers: []uint64{1}},
		},
		HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{
			{From: 0, To: 1, SlotID: 1},
			{From: 2, To: 3, SlotID: 2},
		}},
	}
	reader := newFakeManagementUserReader()
	reader.slotPages[1] = map[metadb.UserCursor]fakeManagementUserPage{
		{}:          {items: []metadb.User{{UID: "u1"}}, cursor: metadb.UserCursor{UID: "u1"}, done: true},
		{UID: "u1"}: {done: true},
	}
	reader.slotPages[2] = map[metadb.UserCursor]fakeManagementUserPage{
		{}: {items: []metadb.User{{UID: "u2"}}, cursor: metadb.UserCursor{UID: "u2"}, done: true},
	}
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: snapshot}, Users: reader})

	first, err := app.ListUsers(context.Background(), ListUsersRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListUsers(page1) error = %v", err)
	}
	if !first.HasMore || first.NextCursor.UID != "u1" {
		t.Fatalf("page1 cursor = %#v has_more=%t, want last emitted UID cursor", first.NextCursor, first.HasMore)
	}
	second, err := app.ListUsers(context.Background(), ListUsersRequest{Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("ListUsers(page2) error = %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].UID != "u2" {
		t.Fatalf("page2 items = %#v, want u2", second.Items)
	}
}

func TestGetUserReturnsDetailWithDevicesAndConnections(t *testing.T) {
	snapshot := singleUserSlotSnapshot()
	reader := newFakeManagementUserReader()
	reader.users["u1"] = metadb.User{UID: "u1"}
	reader.devices[managementDeviceKey{"u1", int64(frame.APP)}] = metadb.Device{
		UID: "u1", DeviceFlag: int64(frame.APP), Token: "token-1", DeviceLevel: int64(frame.DeviceLevelMaster),
	}
	presenceDir := &fakeManagementPresence{routes: map[string][]presence.Route{
		"u1": {{
			UID: "u1", OwnerNodeID: 2, OwnerBootID: 4, SessionID: 10, DeviceID: "d1",
			DeviceFlag: uint8(frame.APP), DeviceLevel: uint8(frame.DeviceLevelMaster), Listener: "tcp",
		}},
	}}
	app := New(Options{
		Cluster:      fakeNodeSnapshotReader{snapshot: snapshot},
		Users:        reader,
		UserPresence: presenceDir,
	})

	got, err := app.GetUser(context.Background(), "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.UID != "u1" || got.SlotID != 1 || got.HashSlot != routing.HashSlotForKey("u1", snapshot.HashSlots.Count) || !got.Online {
		t.Fatalf("detail identity = %#v, want u1 online in slot 1", got)
	}
	wantDevices := []UserDevice{{DeviceFlag: "app", DeviceLevel: "master", TokenSet: true, Online: true, OnlineSessionCount: 1}}
	if !sameUserDevices(got.Devices, wantDevices) {
		t.Fatalf("devices = %#v, want %#v", got.Devices, wantDevices)
	}
	wantConnections := []Connection{{NodeID: 2, SessionID: 10, UID: "u1", DeviceID: "d1", DeviceFlag: "app", DeviceLevel: "master", Listener: "tcp"}}
	if !sameConnections(got.Connections, wantConnections) {
		t.Fatalf("connections = %#v, want %#v", got.Connections, wantConnections)
	}
}

func TestKickUserClearsTokenAndAppliesRouteActions(t *testing.T) {
	operator := &fakeManagementUserOperator{}
	actions := &fakeManagementRouteActions{}
	presenceDir := &fakeManagementPresence{routes: map[string][]presence.Route{
		"u1": {
			{UID: "u1", OwnerNodeID: 2, OwnerBootID: 9, SessionID: 10, DeviceFlag: uint8(frame.APP)},
			{UID: "u1", OwnerNodeID: 3, OwnerBootID: 8, SessionID: 11, DeviceFlag: uint8(frame.WEB)},
			{UID: "u1", OwnerNodeID: 4, OwnerBootID: 7, SessionID: 12, DeviceFlag: uint8(frame.SYSTEM)},
		},
	}}
	app := New(Options{UserOperator: operator, UserPresence: presenceDir, UserActions: actions})

	got, err := app.KickUser(context.Background(), KickUserRequest{UID: "u1", DeviceFlag: "all"})
	if err != nil {
		t.Fatalf("KickUser() error = %v", err)
	}
	if got != (KickUserResponse{UID: "u1", DeviceFlag: "all", Changed: true}) {
		t.Fatalf("KickUser() = %#v, want all changed", got)
	}
	if len(operator.deviceQuitCalls) != 1 || operator.deviceQuitCalls[0] != (userusecase.DeviceQuitCommand{UID: "u1", DeviceFlag: -1}) {
		t.Fatalf("deviceQuitCalls = %#v, want all-device quit", operator.deviceQuitCalls)
	}
	wantActions := []presence.RouteAction{
		{UID: "u1", OwnerNodeID: 2, OwnerBootID: 9, SessionID: 10, Kind: "kick_then_close", Reason: "manager force offline"},
		{UID: "u1", OwnerNodeID: 3, OwnerBootID: 8, SessionID: 11, Kind: "kick_then_close", Reason: "manager force offline"},
	}
	if !sameRouteActions(actions.calls, wantActions) {
		t.Fatalf("route actions = %#v, want %#v", actions.calls, wantActions)
	}
}

func TestResetUserTokenGeneratesTokenAndUpdatesDevice(t *testing.T) {
	operator := &fakeManagementUserOperator{}
	app := New(Options{UserOperator: operator})

	got, err := app.ResetUserToken(context.Background(), ResetUserTokenRequest{
		UID: "u1", DeviceFlag: "app", DeviceLevel: "master",
	})
	if err != nil {
		t.Fatalf("ResetUserToken() error = %v", err)
	}
	if got.UID != "u1" || got.DeviceFlag != "app" || got.DeviceLevel != "master" || got.Token == "" {
		t.Fatalf("ResetUserToken() = %#v, want generated app master token", got)
	}
	if len(operator.updateTokenCalls) != 1 {
		t.Fatalf("updateTokenCalls = %#v, want one call", operator.updateTokenCalls)
	}
	want := userusecase.UpdateTokenCommand{UID: "u1", Token: got.Token, DeviceFlag: frame.APP, DeviceLevel: frame.DeviceLevelMaster}
	if operator.updateTokenCalls[0] != want {
		t.Fatalf("updateTokenCalls[0] = %#v, want %#v", operator.updateTokenCalls[0], want)
	}
}

func singleUserSlotSnapshot() control.Snapshot {
	return control.Snapshot{
		Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}}},
		HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
}

type fakeManagementUserReader struct {
	slotPages map[uint32]map[metadb.UserCursor]fakeManagementUserPage
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
		slotPages: map[uint32]map[metadb.UserCursor]fakeManagementUserPage{},
		users:     map[string]metadb.User{},
		devices:   map[managementDeviceKey]metadb.Device{},
	}
}

func (f *fakeManagementUserReader) ScanUsersSlotPage(_ context.Context, slotID uint32, after metadb.UserCursor, _ int) ([]metadb.User, metadb.UserCursor, bool, error) {
	if pages := f.slotPages[slotID]; pages != nil {
		if page, ok := pages[after]; ok {
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

func sameUserListItems(left, right []UserListItem) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].UID != right[i].UID ||
			left[i].SlotID != right[i].SlotID ||
			left[i].HashSlot != right[i].HashSlot ||
			left[i].Online != right[i].Online ||
			left[i].OnlineDeviceCount != right[i].OnlineDeviceCount ||
			left[i].DeviceCount != right[i].DeviceCount ||
			left[i].TokenSetCount != right[i].TokenSetCount ||
			!sameStrings(left[i].OnlineDeviceFlags, right[i].OnlineDeviceFlags) {
			return false
		}
	}
	return true
}

func sameUserDevices(left, right []UserDevice) bool {
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

func sameConnections(left, right []Connection) bool {
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

func sameRouteActions(left, right []presence.RouteAction) bool {
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

func sameStrings(left, right []string) bool {
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
