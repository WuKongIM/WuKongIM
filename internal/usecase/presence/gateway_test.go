package presence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestActivateRegistersLocalRouteAndRollsBackWhenAuthorityRegisterFails(t *testing.T) {
	onlineReg := online.NewRegistry()
	authority := &fakeAuthorityClient{registerErr: errors.New("register failed")}
	app := New(Options{
		LocalNodeID:     1,
		GatewayBootID:   101,
		Online:          onlineReg,
		Router:          fakeRouter{slotID: 1},
		AuthorityClient: authority,
	})

	err := app.Activate(context.Background(), ActivateCommand{
		UID:         "u1",
		DeviceID:    "d1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Listener:    "tcp",
		ConnectedAt: time.Unix(200, 0),
		Session:     session.New(session.Config{ID: 11, Listener: "tcp"}),
	})

	require.Error(t, err)
	_, ok := onlineReg.Connection(11)
	require.False(t, ok)
	require.Len(t, authority.registerCalls, 1)
	require.Empty(t, authority.unregisterCalls)
}

func TestActivateRollsBackNewRouteWhenRouteActionAckFails(t *testing.T) {
	onlineReg := online.NewRegistry()
	authority := &fakeAuthorityClient{
		registerResult: RegisterAuthoritativeResult{
			Actions: []RouteAction{{
				UID:       "u1",
				NodeID:    9,
				BootID:    88,
				SessionID: 77,
				Kind:      "close",
			}},
		},
	}
	dispatcher := &fakeActionDispatcher{applyErr: errors.New("ack timeout")}
	app := New(Options{
		LocalNodeID:      1,
		GatewayBootID:    101,
		Online:           onlineReg,
		Router:           fakeRouter{slotID: 1},
		AuthorityClient:  authority,
		ActionDispatcher: dispatcher,
	})

	err := app.Activate(context.Background(), ActivateCommand{
		UID:         "u1",
		DeviceID:    "d1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Listener:    "tcp",
		ConnectedAt: time.Unix(200, 0),
		Session:     session.New(session.Config{ID: 11, Listener: "tcp"}),
	})

	require.Error(t, err)
	_, ok := onlineReg.Connection(11)
	require.False(t, ok)
	require.Len(t, authority.unregisterCalls, 1)
	require.Len(t, dispatcher.actions, 1)
}

func TestDeactivateRemovesLocalRouteAndBestEffortUnregistersAuthority(t *testing.T) {
	onlineReg := online.NewRegistry()
	sess := session.New(session.Config{ID: 11, Listener: "tcp"})
	require.NoError(t, onlineReg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u1",
		DeviceID:    "d1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		SlotID:      1,
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		ConnectedAt: time.Unix(200, 0),
		Session:     sess,
	}))

	authority := &fakeAuthorityClient{unregisterErr: errors.New("best effort")}
	app := New(Options{
		LocalNodeID:     1,
		GatewayBootID:   101,
		Online:          onlineReg,
		Router:          fakeRouter{slotID: 1},
		AuthorityClient: authority,
	})

	require.NoError(t, app.Deactivate(context.Background(), DeactivateCommand{
		UID:       "u1",
		SessionID: 11,
	}))

	_, ok := onlineReg.Connection(11)
	require.False(t, ok)
	require.Len(t, authority.unregisterCalls, 1)
	require.Equal(t, uint64(11), authority.unregisterCalls[0].Route.SessionID)
}

func TestApplyRouteActionMarksRouteClosingBeforeAck(t *testing.T) {
	onlineReg := online.NewRegistry()
	sess := session.New(session.Config{ID: 11, Listener: "tcp"})
	require.NoError(t, onlineReg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u1",
		DeviceID:    "d1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		SlotID:      1,
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		ConnectedAt: time.Unix(200, 0),
		Session:     sess,
	}))

	app := New(Options{
		LocalNodeID:   1,
		GatewayBootID: 101,
		Online:        onlineReg,
	})

	err := app.ApplyRouteAction(context.Background(), RouteAction{
		UID:       "u1",
		NodeID:    1,
		BootID:    101,
		SessionID: 11,
		Kind:      "close",
	})

	require.NoError(t, err)
	conn, ok := onlineReg.Connection(11)
	require.True(t, ok)
	require.Equal(t, online.LocalRouteStateClosing, conn.State)
	require.Empty(t, onlineReg.ConnectionsByUID("u1"))
}

func TestApplyRouteActionRejectsMismatchedActiveRoute(t *testing.T) {
	onlineReg := online.NewRegistry()
	sess := session.New(session.Config{ID: 11, Listener: "tcp"})
	require.NoError(t, onlineReg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u1",
		DeviceID:    "d1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		SlotID:      1,
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		ConnectedAt: time.Unix(200, 0),
		Session:     sess,
	}))

	app := New(Options{
		LocalNodeID:   1,
		GatewayBootID: 101,
		Online:        onlineReg,
	})

	err := app.ApplyRouteAction(context.Background(), RouteAction{
		UID:       "u2",
		NodeID:    1,
		BootID:    101,
		SessionID: 11,
		Kind:      "close",
	})

	require.Error(t, err)
	conn, ok := onlineReg.Connection(11)
	require.True(t, ok)
	require.Equal(t, online.LocalRouteStateActive, conn.State)
}

type fakeRouter struct {
	slotID uint64
	byKey  map[string]uint64
}

func (f fakeRouter) SlotForKey(key string) uint64 {
	if slotID, ok := f.byKey[key]; ok {
		return slotID
	}
	return f.slotID
}

type fakeAuthorityClient struct {
	registerResult     RegisterAuthoritativeResult
	registerErr        error
	unregisterErr      error
	heartbeatErr       error
	replayErr          error
	heartbeatBySlot    map[uint64]HeartbeatAuthoritativeResult
	heartbeatErrBySlot map[uint64]error

	registerCalls   []RegisterAuthoritativeCommand
	unregisterCalls []UnregisterAuthoritativeCommand
	heartbeatCalls  []HeartbeatAuthoritativeCommand
	replayCalls     []ReplayAuthoritativeCommand
}

func (f *fakeAuthorityClient) RegisterAuthoritative(_ context.Context, cmd RegisterAuthoritativeCommand) (RegisterAuthoritativeResult, error) {
	f.registerCalls = append(f.registerCalls, cmd)
	if f.registerErr != nil {
		return RegisterAuthoritativeResult{}, f.registerErr
	}
	return f.registerResult, nil
}

func (f *fakeAuthorityClient) UnregisterAuthoritative(_ context.Context, cmd UnregisterAuthoritativeCommand) error {
	f.unregisterCalls = append(f.unregisterCalls, cmd)
	return f.unregisterErr
}

func (f *fakeAuthorityClient) HeartbeatAuthoritative(_ context.Context, cmd HeartbeatAuthoritativeCommand) (HeartbeatAuthoritativeResult, error) {
	f.heartbeatCalls = append(f.heartbeatCalls, cmd)
	if f.heartbeatErr != nil {
		return HeartbeatAuthoritativeResult{}, f.heartbeatErr
	}
	if err, ok := f.heartbeatErrBySlot[cmd.Lease.SlotID]; ok {
		return HeartbeatAuthoritativeResult{}, err
	}
	if result, ok := f.heartbeatBySlot[cmd.Lease.SlotID]; ok {
		return result, nil
	}
	return HeartbeatAuthoritativeResult{}, nil
}

func (f *fakeAuthorityClient) ReplayAuthoritative(_ context.Context, cmd ReplayAuthoritativeCommand) error {
	f.replayCalls = append(f.replayCalls, cmd)
	return f.replayErr
}

func (f *fakeAuthorityClient) EndpointsByUID(context.Context, string) ([]Route, error) {
	return nil, nil
}

func (f *fakeAuthorityClient) EndpointsByUIDs(_ context.Context, uids []string) (map[string][]Route, error) {
	out := make(map[string][]Route, len(uids))
	for _, uid := range uids {
		out[uid] = nil
	}
	return out, nil
}

type fakeActionDispatcher struct {
	applyErr error
	actions  []RouteAction
}

func (f *fakeActionDispatcher) ApplyRouteAction(_ context.Context, action RouteAction) error {
	f.actions = append(f.actions, action)
	return f.applyErr
}
