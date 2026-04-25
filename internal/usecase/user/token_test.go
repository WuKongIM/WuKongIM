package user

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestUpdateTokenCreatesUserWhenMissingAndUpsertsDevice(t *testing.T) {
	users := &fakeUserStore{getErr: metadb.ErrNotFound}
	devices := &fakeDeviceStore{}
	app := New(Options{
		Users:   users,
		Devices: devices,
	})

	err := app.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:         "u1",
		Token:       "token-1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelSlave,
	})

	require.NoError(t, err)
	require.Equal(t, 1, users.getCalls)
	require.Len(t, users.created, 1)
	require.Equal(t, metadb.User{UID: "u1"}, users.created[0])
	require.Len(t, devices.upserted, 1)
	require.Equal(t, metadb.Device{
		UID:         "u1",
		DeviceFlag:  int64(frame.APP),
		Token:       "token-1",
		DeviceLevel: int64(frame.DeviceLevelSlave),
	}, devices.upserted[0])
}

func TestUpdateTokenDoesNotRewriteExistingUser(t *testing.T) {
	users := &fakeUserStore{}
	devices := &fakeDeviceStore{}
	app := New(Options{
		Users:   users,
		Devices: devices,
	})

	err := app.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:         "u1",
		Token:       "token-1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelSlave,
	})

	require.NoError(t, err)
	require.Equal(t, 1, users.getCalls)
	require.Empty(t, users.created)
	require.Len(t, devices.upserted, 1)
}

func TestUpdateTokenMissingUserCreateAlreadyExistsStillSucceeds(t *testing.T) {
	users := &fakeUserStore{
		getErr:    metadb.ErrNotFound,
		createErr: metadb.ErrAlreadyExists,
	}
	devices := &fakeDeviceStore{}
	app := New(Options{
		Users:   users,
		Devices: devices,
	})

	err := app.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:         "u1",
		Token:       "token-1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelSlave,
	})

	require.NoError(t, err)
	require.Equal(t, 1, users.getCalls)
	require.Len(t, users.created, 1)
	require.Equal(t, metadb.User{UID: "u1"}, users.created[0])
	require.Len(t, devices.upserted, 1)
}

func TestUpdateTokenRejectsEmptyUID(t *testing.T) {
	app := New(Options{})

	err := app.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:   "",
		Token: "token-1",
	})

	require.EqualError(t, err, "uid不能为空！")
}

func TestUpdateTokenRejectsEmptyToken(t *testing.T) {
	app := New(Options{})

	err := app.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:   "u1",
		Token: "",
	})

	require.EqualError(t, err, "token不能为空！")
}

func TestUpdateTokenRejectsLegacyForbiddenUIDChars(t *testing.T) {
	app := New(Options{})

	for _, uid := range []string{"u@1", "u#1", "u&1"} {
		t.Run(uid, func(t *testing.T) {
			err := app.UpdateToken(context.Background(), UpdateTokenCommand{
				UID:   uid,
				Token: "token-1",
			})
			require.EqualError(t, err, "uid不能包含特殊字符！")
		})
	}
}

func TestUpdateTokenReturnsDependencyErrorWhenStoresMissing(t *testing.T) {
	appWithoutUsers := New(Options{
		Devices: &fakeDeviceStore{},
	})
	err := appWithoutUsers.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:   "u1",
		Token: "token-1",
	})
	require.ErrorIs(t, err, ErrUserStoreRequired)

	appWithoutDevices := New(Options{
		Users: &fakeUserStore{},
	})
	err = appWithoutDevices.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:   "u1",
		Token: "token-1",
	})
	require.ErrorIs(t, err, ErrDeviceStoreRequired)
}

func TestUpdateTokenMasterKicksOnlySameDeviceFlagSessions(t *testing.T) {
	reg := online.NewRegistry()
	sameDevice := newRecordingSession(11, "tcp")
	otherDevice := newRecordingSession(12, "tcp")
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     sameDevice,
	}))
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   12,
		UID:         "u1",
		DeviceFlag:  frame.WEB,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     otherDevice,
	}))

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

	require.NoError(t, err)
	require.Len(t, sameDevice.WrittenFrames(), 1)
	require.True(t, sameDevice.closed)
	require.Empty(t, otherDevice.WrittenFrames())
	require.False(t, otherDevice.closed)
	require.Equal(t, []time.Duration{10 * time.Second}, scheduled)

	disconnect, ok := sameDevice.WrittenFrames()[0].(*frame.DisconnectPacket)
	require.True(t, ok)
	require.Equal(t, frame.ReasonConnectKick, disconnect.ReasonCode)
	require.Equal(t, "账号在其他设备上登录", disconnect.Reason)
}

func TestUpdateTokenSlaveDoesNotKickSessions(t *testing.T) {
	reg := online.NewRegistry()
	sess := newRecordingSession(11, "tcp")
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Session:     sess,
	}))

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
		DeviceLevel: frame.DeviceLevelSlave,
	})

	require.NoError(t, err)
	require.Empty(t, sess.WrittenFrames())
	require.False(t, sess.closed)
	require.Empty(t, scheduled)
}

func TestNewInstallsDefaultOnlineAndAfterFunc(t *testing.T) {
	app := New(Options{})
	require.NotNil(t, app.online)
	require.NotNil(t, app.afterFunc)

	done := make(chan struct{})
	app.afterFunc(0, func() {
		close(done)
	})

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("default AfterFunc did not invoke callback")
	}
}

type fakeUserStore struct {
	getErr    error
	createErr error

	getCalls int
	created  []metadb.User
}

func (f *fakeUserStore) GetUser(context.Context, string) (metadb.User, error) {
	f.getCalls++
	if f.getErr != nil {
		return metadb.User{}, f.getErr
	}
	return metadb.User{}, nil
}

func (f *fakeUserStore) CreateUser(_ context.Context, u metadb.User) error {
	f.created = append(f.created, u)
	if f.createErr != nil {
		return f.createErr
	}
	return nil
}

type fakeDeviceStore struct {
	upsertErr error
	upserted  []metadb.Device
}

func (f *fakeDeviceStore) UpsertDevice(_ context.Context, d metadb.Device) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserted = append(f.upserted, d)
	return nil
}

type recordingSession struct {
	id       uint64
	listener string

	mu            sync.Mutex
	writtenFrames []frame.Frame
	closed        bool
}

func newRecordingSession(id uint64, listener string) *recordingSession {
	return &recordingSession{id: id, listener: listener}
}

func (s *recordingSession) ID() uint64 {
	return s.id
}

func (s *recordingSession) Listener() string {
	return s.listener
}

func (s *recordingSession) RemoteAddr() string {
	return ""
}

func (s *recordingSession) LocalAddr() string {
	return ""
}

func (s *recordingSession) WriteFrame(f frame.Frame, _ ...session.WriteOption) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.writtenFrames = append(s.writtenFrames, f)
	return nil
}

func (s *recordingSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *recordingSession) SetValue(string, any) {}

func (s *recordingSession) Value(string) any {
	return nil
}

func (s *recordingSession) WrittenFrames() []frame.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]frame.Frame, len(s.writtenFrames))
	copy(out, s.writtenFrames)
	return out
}

var _ UserStore = (*fakeUserStore)(nil)
var _ DeviceStore = (*fakeDeviceStore)(nil)
var _ session.Session = (*recordingSession)(nil)
