package presence

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestAuthorityRegisterMasterDifferentDeviceReturnsKickThenCloseActions(t *testing.T) {
	app := New(Options{})

	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 10, 100, "old-device", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)

	result, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 2, 20, 200, "new-device", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)
	require.Len(t, result.Actions, 1)
	require.Equal(t, "kick_then_close", result.Actions[0].Kind)
	require.Equal(t, uint64(100), result.Actions[0].SessionID)

	endpoints := requireAuthorityEndpoints(t, app, "u1")
	require.Len(t, endpoints, 1)
	require.Equal(t, uint64(200), endpoints[0].SessionID)
}

func TestAuthorityRegisterDuplicateSameRouteIsIdempotent(t *testing.T) {
	app := New(Options{})

	route := testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster))
	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  route,
	})
	require.NoError(t, err)

	result, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  route,
	})
	require.NoError(t, err)
	require.Empty(t, result.Actions)

	endpoints := requireAuthorityEndpoints(t, app, "u1")
	require.Len(t, endpoints, 1)
	require.Equal(t, uint64(100), endpoints[0].SessionID)
}

func TestAuthorityRegisterMasterSameDeviceReturnsCloseActions(t *testing.T) {
	app := New(Options{})

	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 10, 100, "same-device", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)

	result, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 11, 101, "same-device", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)
	require.Len(t, result.Actions, 1)
	require.Equal(t, "close", result.Actions[0].Kind)
	require.Equal(t, uint64(100), result.Actions[0].SessionID)

	endpoints := requireAuthorityEndpoints(t, app, "u1")
	require.Len(t, endpoints, 1)
	require.Equal(t, uint64(101), endpoints[0].SessionID)
}

func TestAuthorityRegisterSlaveOnlyReplacesSameDeviceID(t *testing.T) {
	app := New(Options{})

	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelSlave)),
	})
	require.NoError(t, err)
	_, err = app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 10, 101, "device-b", uint8(frame.DeviceLevelSlave)),
	})
	require.NoError(t, err)

	result, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 2, 20, 200, "device-a", uint8(frame.DeviceLevelSlave)),
	})
	require.NoError(t, err)
	require.Len(t, result.Actions, 1)
	require.Equal(t, "close", result.Actions[0].Kind)
	require.Equal(t, uint64(100), result.Actions[0].SessionID)

	endpoints := requireAuthorityEndpoints(t, app, "u1")
	require.Len(t, endpoints, 2)
	require.ElementsMatch(t, []uint64{101, 200}, []uint64{endpoints[0].SessionID, endpoints[1].SessionID})
}

func TestAuthorityHeartbeatDetectsDigestMismatchWhenCountMatches(t *testing.T) {
	app := New(Options{})

	route := testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster))
	route.Listener = "tcp"
	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  route,
	})
	require.NoError(t, err)

	result, err := app.HeartbeatAuthoritative(context.Background(), HeartbeatAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:        1,
			GatewayNodeID: 1,
			GatewayBootID: 10,
			RouteCount:    1,
			RouteDigest:   999,
		},
	})
	require.NoError(t, err)
	require.True(t, result.Mismatch)
	require.Equal(t, 1, result.RouteCount)
	require.NotEqual(t, uint64(999), result.RouteDigest)
}

func TestAuthorityHeartbeatMatchesGatewayGroupDigest(t *testing.T) {
	app := New(Options{})
	reg := online.NewRegistry()

	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   100,
		UID:         "u1",
		DeviceID:    "device-a",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		SlotID:      1,
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		Session:     session.New(session.Config{ID: 100, Listener: "tcp"}),
	}))

	groups := reg.ActiveSlots()
	require.Len(t, groups, 1)

	route := testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster))
	route.Listener = "tcp"
	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  route,
	})
	require.NoError(t, err)

	result, err := app.HeartbeatAuthoritative(context.Background(), HeartbeatAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:         1,
			GatewayNodeID:  1,
			GatewayBootID:  10,
			RouteCount:     groups[0].Count,
			RouteDigest:    groups[0].Digest,
			LeaseUntilUnix: time.Unix(200, 0).Add(30 * time.Second).Unix(),
		},
	})
	require.NoError(t, err)
	require.False(t, result.Mismatch)
	require.Equal(t, groups[0].Digest, result.RouteDigest)
}

func TestAuthorityReplayReplacesOwnerSetWithActiveRoutesOnly(t *testing.T) {
	now := time.Unix(200, 0)
	app := New(Options{
		Now: func() time.Time { return now },
	})

	err := app.ReplayAuthoritative(context.Background(), ReplayAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:         1,
			GatewayNodeID:  9,
			GatewayBootID:  99,
			LeaseUntilUnix: now.Add(30 * time.Second).Unix(),
		},
		Routes: []Route{
			testRoute("u1", 9, 99, 100, "device-a", uint8(frame.DeviceLevelMaster)),
			testRoute("u2", 9, 99, 200, "device-b", uint8(frame.DeviceLevelMaster)),
		},
	})
	require.NoError(t, err)

	err = app.ReplayAuthoritative(context.Background(), ReplayAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:         1,
			GatewayNodeID:  9,
			GatewayBootID:  99,
			LeaseUntilUnix: now.Add(30 * time.Second).Unix(),
		},
		Routes: []Route{
			testRoute("u1", 9, 99, 100, "device-a", uint8(frame.DeviceLevelMaster)),
		},
	})
	require.NoError(t, err)

	endpointsU1 := requireAuthorityEndpoints(t, app, "u1")
	require.Len(t, endpointsU1, 1)
	require.Equal(t, uint64(100), endpointsU1[0].SessionID)

	endpointsU2 := requireAuthorityEndpoints(t, app, "u2")
	require.Empty(t, endpointsU2)
}

func TestAuthorityReplayDoesNotResurrectSupersededMasterRoute(t *testing.T) {
	now := time.Unix(200, 0)
	app := New(Options{
		Now: func() time.Time { return now },
	})

	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 10, 100, "old-device", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)

	_, err = app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 2, 20, 200, "new-device", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)

	err = app.ReplayAuthoritative(context.Background(), ReplayAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:         1,
			GatewayNodeID:  1,
			GatewayBootID:  10,
			LeaseUntilUnix: now.Add(30 * time.Second).Unix(),
		},
		Routes: []Route{
			testRoute("u1", 1, 10, 100, "old-device", uint8(frame.DeviceLevelMaster)),
		},
	})
	require.NoError(t, err)

	endpoints := requireAuthorityEndpoints(t, app, "u1")
	require.Len(t, endpoints, 1)
	require.Equal(t, uint64(200), endpoints[0].SessionID)
}

func TestAuthorityEndpointsByUIDReturnsCurrentRoutes(t *testing.T) {
	app := New(Options{})

	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)
	_, err = app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route: Route{
			UID:         "u1",
			NodeID:      2,
			BootID:      20,
			SessionID:   200,
			DeviceID:    "device-b",
			DeviceFlag:  uint8(frame.WEB),
			DeviceLevel: uint8(frame.DeviceLevelMaster),
		},
	})
	require.NoError(t, err)

	endpoints := requireAuthorityEndpoints(t, app, "u1")
	require.Len(t, endpoints, 2)
	require.ElementsMatch(t, []uint64{100, 200}, []uint64{endpoints[0].SessionID, endpoints[1].SessionID})
}

func TestPresenceAuthorityEndpointsByUIDsReturnsBatchRoutes(t *testing.T) {
	app := New(Options{})

	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)
	_, err = app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u2", 2, 20, 200, "device-b", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)

	endpoints, err := app.EndpointsByUIDs(context.Background(), []string{"u2", "u1", "missing"})
	require.NoError(t, err)
	require.Len(t, endpoints["u1"], 1)
	require.Equal(t, uint64(100), endpoints["u1"][0].SessionID)
	require.Len(t, endpoints["u2"], 1)
	require.Equal(t, uint64(200), endpoints["u2"][0].SessionID)
	_, ok := endpoints["missing"]
	require.False(t, ok)
}

func TestAuthorityRegisterSeedsLeaseDeadlineAndExpiresWithoutHeartbeat(t *testing.T) {
	now := time.Unix(200, 0)
	app := New(Options{
		Now: func() time.Time { return now },
	})

	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)
	require.Len(t, requireAuthorityEndpoints(t, app, "u1"), 1)

	now = now.Add(31 * time.Second)
	require.Empty(t, requireAuthorityEndpoints(t, app, "u1"))
}

func TestAuthorityHeartbeatExplicitLeaseKeepsRouteAlive(t *testing.T) {
	now := time.Unix(200, 0)
	app := New(Options{
		Now: func() time.Time { return now },
	})

	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)

	heartbeat, err := app.HeartbeatAuthoritative(context.Background(), HeartbeatAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:         1,
			GatewayNodeID:  1,
			GatewayBootID:  10,
			RouteCount:     1,
			RouteDigest:    999,
			LeaseUntilUnix: now.Add(90 * time.Second).Unix(),
		},
	})
	require.NoError(t, err)
	require.True(t, heartbeat.Mismatch)

	now = now.Add(31 * time.Second)
	endpoints := requireAuthorityEndpoints(t, app, "u1")
	require.Len(t, endpoints, 1)
	require.Equal(t, uint64(100), endpoints[0].SessionID)
}

func TestAuthorityHeartbeatStaleLeaseDoesNotShortenDeadline(t *testing.T) {
	now := time.Unix(200, 0)
	app := New(Options{
		Now: func() time.Time { return now },
	})

	_, err := app.RegisterAuthoritative(context.Background(), RegisterAuthoritativeCommand{
		SlotID: 1,
		Route:  testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster)),
	})
	require.NoError(t, err)

	_, err = app.HeartbeatAuthoritative(context.Background(), HeartbeatAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:         1,
			GatewayNodeID:  1,
			GatewayBootID:  10,
			RouteCount:     1,
			RouteDigest:    999,
			LeaseUntilUnix: now.Add(90 * time.Second).Unix(),
		},
	})
	require.NoError(t, err)

	_, err = app.HeartbeatAuthoritative(context.Background(), HeartbeatAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:         1,
			GatewayNodeID:  1,
			GatewayBootID:  10,
			RouteCount:     1,
			RouteDigest:    999,
			LeaseUntilUnix: now.Add(5 * time.Second).Unix(),
		},
	})
	require.NoError(t, err)

	now = now.Add(31 * time.Second)
	require.Len(t, requireAuthorityEndpoints(t, app, "u1"), 1)

	now = time.Unix(291, 0)
	require.Empty(t, requireAuthorityEndpoints(t, app, "u1"))
}

func TestAuthorityReplayStaleLeaseDoesNotShortenDeadline(t *testing.T) {
	now := time.Unix(200, 0)
	app := New(Options{
		Now: func() time.Time { return now },
	})

	err := app.ReplayAuthoritative(context.Background(), ReplayAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:         1,
			GatewayNodeID:  1,
			GatewayBootID:  10,
			LeaseUntilUnix: now.Add(90 * time.Second).Unix(),
		},
		Routes: []Route{
			testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster)),
		},
	})
	require.NoError(t, err)

	err = app.ReplayAuthoritative(context.Background(), ReplayAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:         1,
			GatewayNodeID:  1,
			GatewayBootID:  10,
			LeaseUntilUnix: now.Add(5 * time.Second).Unix(),
		},
		Routes: []Route{
			testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster)),
		},
	})
	require.NoError(t, err)

	now = now.Add(31 * time.Second)
	require.Len(t, requireAuthorityEndpoints(t, app, "u1"), 1)

	now = time.Unix(291, 0)
	require.Empty(t, requireAuthorityEndpoints(t, app, "u1"))
}

func TestAuthorityEndpointsByUIDEvictsExpiredLeaseRoutes(t *testing.T) {
	now := time.Unix(200, 0)
	app := New(Options{
		Now: func() time.Time { return now },
	})

	err := app.ReplayAuthoritative(context.Background(), ReplayAuthoritativeCommand{
		Lease: GatewayLease{
			SlotID:         1,
			GatewayNodeID:  9,
			GatewayBootID:  99,
			LeaseUntilUnix: now.Add(-time.Second).Unix(),
		},
		Routes: []Route{
			testRoute("u1", 9, 99, 100, "device-a", uint8(frame.DeviceLevelMaster)),
		},
	})
	require.NoError(t, err)

	endpoints := requireAuthorityEndpoints(t, app, "u1")
	require.Empty(t, endpoints)
}

func TestDirectoryUnregisterRemovesOwnerSetWhenAssumedGroupIsWrong(t *testing.T) {
	dir := newDirectory()
	nowUnix := time.Unix(200, 0).Unix()
	route := testRoute("u1", 1, 10, 100, "device-a", uint8(frame.DeviceLevelMaster))

	dir.register(1, route, nowUnix)
	require.Len(t, dir.endpointsByUID("u1", nowUnix), 1)
	require.Len(t, dir.ownerSet, 1)
	require.Len(t, dir.leases, 1)

	dir.unregister(99, route, nowUnix)

	require.Empty(t, dir.endpointsByUID("u1", nowUnix))
	require.Empty(t, dir.ownerSet)
	require.Empty(t, dir.leases)
}

func testRoute(uid string, nodeID, bootID, sessionID uint64, deviceID string, level uint8) Route {
	return Route{
		UID:         uid,
		NodeID:      nodeID,
		BootID:      bootID,
		SessionID:   sessionID,
		DeviceID:    deviceID,
		DeviceFlag:  uint8(frame.APP),
		DeviceLevel: level,
	}
}

func requireAuthorityEndpoints(t *testing.T, app *App, uid string) []Route {
	t.Helper()

	endpoints, err := app.EndpointsByUID(context.Background(), uid)
	require.NoError(t, err)
	return endpoints
}
