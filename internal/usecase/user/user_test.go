package user

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestUpdateTokenCreatesMissingUserAndUpsertsDevice(t *testing.T) {
	users := &fakeUserStore{getErr: metadb.ErrNotFound}
	devices := &fakeDeviceStore{}
	app := New(Options{Users: users, Devices: devices})

	err := app.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:         "u1",
		Token:       "token-1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelSlave,
	})

	if err != nil {
		t.Fatalf("UpdateToken() error = %v", err)
	}
	if users.getCalls != 1 || len(users.created) != 1 || users.created[0].UID != "u1" {
		t.Fatalf("user store getCalls=%d created=%#v, want create u1", users.getCalls, users.created)
	}
	wantDevice := metadb.Device{UID: "u1", DeviceFlag: int64(frame.APP), Token: "token-1", DeviceLevel: int64(frame.DeviceLevelSlave)}
	if len(devices.upserted) != 1 || devices.upserted[0] != wantDevice {
		t.Fatalf("upserted devices = %#v, want %#v", devices.upserted, []metadb.Device{wantDevice})
	}
}

func TestUpdateTokenValidatesLegacyInputs(t *testing.T) {
	for _, tt := range []struct {
		name string
		cmd  UpdateTokenCommand
		want string
	}{
		{name: "missing uid", cmd: UpdateTokenCommand{Token: "token-1"}, want: "uid不能为空！"},
		{name: "missing token", cmd: UpdateTokenCommand{UID: "u1"}, want: "token不能为空！"},
		{name: "special char", cmd: UpdateTokenCommand{UID: "u@1", Token: "token-1"}, want: "uid不能包含特殊字符！"},
		{name: "system uid", cmd: UpdateTokenCommand{UID: DefaultSystemUID, Token: "token-1"}, want: "系统账号不允许更新token！"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := New(Options{}).UpdateToken(context.Background(), tt.cmd)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("UpdateToken() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestUpdateTokenMasterSchedulesLocalSameDeviceClose(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{})
	sameDevice := &recordingSession{}
	otherDevice := &recordingSession{}
	if err := reg.RegisterPending(online.LocalSession{Route: online.OwnerRoute{UID: "u1", SessionID: 11, DeviceFlag: uint8(frame.APP)}, Session: sameDevice}); err != nil {
		t.Fatalf("RegisterPending(same) error = %v", err)
	}
	if err := reg.RegisterPending(online.LocalSession{Route: online.OwnerRoute{UID: "u1", SessionID: 12, DeviceFlag: uint8(frame.WEB)}, Session: otherDevice}); err != nil {
		t.Fatalf("RegisterPending(other) error = %v", err)
	}
	var scheduled []time.Duration
	app := New(Options{
		Users:   &fakeUserStore{getErr: metadb.ErrNotFound},
		Devices: &fakeDeviceStore{},
		Online:  reg,
		AfterFunc: func(d time.Duration, fn func()) {
			scheduled = append(scheduled, d)
			fn()
		},
	})

	err := app.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:         "u1",
		Token:       "token-1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
	})

	if err != nil {
		t.Fatalf("UpdateToken() error = %v", err)
	}
	if len(scheduled) != 1 || scheduled[0] != 10*time.Second {
		t.Fatalf("scheduled = %#v, want one 10s close", scheduled)
	}
	if sameDevice.reason != "账号在其他设备上登录" || sameDevice.closed != 1 {
		t.Fatalf("same device close reason=%q closed=%d, want kick", sameDevice.reason, sameDevice.closed)
	}
	if otherDevice.closed != 0 {
		t.Fatalf("other device closed=%d, want 0", otherDevice.closed)
	}
}

func TestDeviceQuitClearsSelectedTokenAndSchedulesClose(t *testing.T) {
	reg := online.NewRegistry(online.RegistryOptions{})
	session := &recordingSession{}
	if err := reg.RegisterPending(online.LocalSession{Route: online.OwnerRoute{UID: "u1", SessionID: 11, DeviceFlag: uint8(frame.APP)}, Session: session}); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	devices := &fakeDeviceStore{
		devices: map[deviceKey]metadb.Device{
			{uid: "u1", flag: int64(frame.APP)}: {UID: "u1", DeviceFlag: int64(frame.APP), Token: "token-1", DeviceLevel: int64(frame.DeviceLevelSlave)},
		},
	}
	var scheduled []time.Duration
	app := New(Options{
		Devices: devices,
		Online:  reg,
		AfterFunc: func(d time.Duration, fn func()) {
			scheduled = append(scheduled, d)
			fn()
		},
	})

	err := app.DeviceQuit(context.Background(), DeviceQuitCommand{UID: "u1", DeviceFlag: int(frame.APP)})

	if err != nil {
		t.Fatalf("DeviceQuit() error = %v", err)
	}
	want := metadb.Device{UID: "u1", DeviceFlag: int64(frame.APP), Token: "", DeviceLevel: int64(frame.DeviceLevelMaster)}
	if len(devices.upserted) != 1 || devices.upserted[0] != want {
		t.Fatalf("upserted devices = %#v, want %#v", devices.upserted, []metadb.Device{want})
	}
	if len(scheduled) != 1 || scheduled[0] != 2*time.Second {
		t.Fatalf("scheduled = %#v, want one 2s close", scheduled)
	}
	if session.closed != 1 {
		t.Fatalf("session closed = %d, want 1", session.closed)
	}
}

func TestOnlineStatusUsesPresenceRoutesInRequestedUIDOrder(t *testing.T) {
	directory := &fakePresenceDirectory{
		routes: map[string][]presence.Route{
			"u1": {
				{UID: "u1", DeviceFlag: uint8(frame.APP)},
				{UID: "u1", DeviceFlag: uint8(frame.WEB)},
			},
			"u2": {{UID: "u2", DeviceFlag: uint8(frame.PC)}},
		},
	}
	app := New(Options{Presence: directory})

	statuses, err := app.OnlineStatus(context.Background(), []string{"u2", "u1", "missing"})

	if err != nil {
		t.Fatalf("OnlineStatus() error = %v", err)
	}
	want := []OnlineStatus{
		{UID: "u2", DeviceFlag: uint8(frame.PC), Online: 1},
		{UID: "u1", DeviceFlag: uint8(frame.APP), Online: 1},
		{UID: "u1", DeviceFlag: uint8(frame.WEB), Online: 1},
	}
	if !equalOnlineStatuses(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
}

func TestSystemUIDsPersistListAndCacheThroughStore(t *testing.T) {
	store := &fakeSystemUIDStore{}
	app := New(Options{SystemUIDs: store})

	if err := app.AddSystemUIDs(context.Background(), []string{"sys1", "sys2"}); err != nil {
		t.Fatalf("AddSystemUIDs() error = %v", err)
	}
	if !app.IsSystemUID("sys1") || !app.IsSystemUID("sys2") {
		t.Fatalf("system UID cache missing added values")
	}
	if err := app.RemoveSystemUIDs(context.Background(), []string{"sys1"}); err != nil {
		t.Fatalf("RemoveSystemUIDs() error = %v", err)
	}
	got, err := app.ListSystemUIDs(context.Background())
	if err != nil {
		t.Fatalf("ListSystemUIDs() error = %v", err)
	}
	if !equalStrings(got, []string{"sys2"}) {
		t.Fatalf("ListSystemUIDs() = %#v, want sys2", got)
	}
	if app.IsSystemUID("sys1") || !app.IsSystemUID("sys2") {
		t.Fatalf("system UID cache did not remove only sys1")
	}
}

type fakeUserStore struct {
	getErr    error
	createErr error
	getCalls  int
	created   []metadb.User
}

func (f *fakeUserStore) GetUser(context.Context, string) (metadb.User, error) {
	f.getCalls++
	if f.getErr != nil {
		return metadb.User{}, f.getErr
	}
	return metadb.User{}, nil
}

func (f *fakeUserStore) CreateUser(_ context.Context, user metadb.User) error {
	f.created = append(f.created, user)
	if f.createErr != nil {
		return f.createErr
	}
	return nil
}

type fakeDeviceStore struct {
	upsertErr error
	upserted  []metadb.Device
	devices   map[deviceKey]metadb.Device
}

func (f *fakeDeviceStore) UpsertDevice(_ context.Context, device metadb.Device) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserted = append(f.upserted, device)
	return nil
}

func (f *fakeDeviceStore) GetDevice(_ context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	if f.devices == nil {
		return metadb.Device{}, metadb.ErrNotFound
	}
	device, ok := f.devices[deviceKey{uid: uid, flag: deviceFlag}]
	if !ok {
		return metadb.Device{}, metadb.ErrNotFound
	}
	return device, nil
}

type deviceKey struct {
	uid  string
	flag int64
}

type fakePresenceDirectory struct {
	routes map[string][]presence.Route
}

func (f *fakePresenceDirectory) EndpointsByUIDs(_ context.Context, uids []string) (map[string][]presence.Route, error) {
	out := make(map[string][]presence.Route, len(uids))
	for _, uid := range uids {
		out[uid] = append([]presence.Route(nil), f.routes[uid]...)
	}
	return out, nil
}

type fakeSystemUIDStore struct {
	subscribers []string
}

func (f *fakeSystemUIDStore) AddChannelSubscribers(_ context.Context, _ string, _ int64, uids []string, _ ...uint64) error {
	f.subscribers = append(f.subscribers, uids...)
	return nil
}

func (f *fakeSystemUIDStore) RemoveChannelSubscribers(_ context.Context, _ string, _ int64, uids []string, _ ...uint64) error {
	remove := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		remove[uid] = struct{}{}
	}
	next := f.subscribers[:0]
	for _, uid := range f.subscribers {
		if _, ok := remove[uid]; !ok {
			next = append(next, uid)
		}
	}
	f.subscribers = next
	return nil
}

func (f *fakeSystemUIDStore) ListChannelSubscribers(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	return append([]string(nil), f.subscribers...), "", true, nil
}

type recordingSession struct {
	reason string
	closed int
}

func (r *recordingSession) WriteDelivery(any) error { return nil }

func (r *recordingSession) CloseSession(reason string) error {
	r.reason = reason
	r.closed++
	return nil
}

func equalOnlineStatuses(a, b []OnlineStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
