//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTConnectionSupervisorSingleNodeCluster(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	owners, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: cfg.NodeID, BootID: "connection-supervisor", Capacity: 4, MaxOperations: 4, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, owners.Close(ctx))
	})
	require.NoError(t, node.UpsertDeviceMetadata(ctx, meta.Device{UID: "alice", DeviceFlag: 1, Token: "secret", DeviceLevel: 1}))
	service, err := sessioncase.New(sessioncase.Options{Store: node, Owners: owners, Isolation: owners, Tokens: user.New(user.Options{DeviceReader: mqttAcquisitionDeviceReader{node: node}}), Wills: mqttWillAuthorizer{messages: a.Messages()}, LeaseDuration: 2 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: 86400, QuotaMessages: 100, QuotaBytes: 1 << 20, WindowLimit: 16})
	require.NoError(t, err)
	s, err := runtime.NewConnections(runtime.ConnectionOptions{Owners: owners, Control: mqttConnectionControl{sessions: service}, Workers: 2, CallTimeout: time.Second, Retry: 50 * time.Millisecond})
	require.NoError(t, err)
	require.NoError(t, s.Start(ctx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, s.Stop(ctx))
	})
	key := contract.Key{Namespace: "main", ClientID: "supervised"}
	connection, err := service.Connect(ctx, sessioncase.ConnectCommand{Key: key, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }})
	require.NoError(t, err)
	require.NoError(t, s.Register(connection.Owner))
	require.Eventually(t, func() bool { return s.Snapshot().Renewed >= 2 }, 6*time.Second, 20*time.Millisecond)
	row, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: key.Namespace, ClientID: key.ClientID})
	require.NoError(t, err)
	require.NotNil(t, row.Session)
	require.Greater(t, row.Session.Revision, connection.Lease.Revision)
	held, err := owners.Begin(ctx, connection.Owner)
	require.NoError(t, err)
	observed := time.Now()
	expiry := uint32(12)
	require.NoError(t, s.Disconnect(runtime.DisconnectIntent{Owner: connection.Owner, Normal: true, ObservedAt: observed, SessionExpirySec: &expiry}))
	require.Error(t, held.Context().Err())
	held.Done()
	require.Eventually(t, func() bool { return s.Snapshot().Tracked == 0 }, 3*time.Second, 10*time.Millisecond)
	row, err = node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: key.Namespace, ClientID: key.ClientID})
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionOffline, row.Session.State)
	require.Equal(t, observed.UnixMilli()+12000, row.Session.OfflineExpiresAtMS)
	require.Zero(t, owners.Snapshot().Held)
	t.Log("mqtt_connection_supervisor: hash_slots=256 automatic_renewal=true callback_nonblocking=true original_disconnect_clock=true joined_cleanup=true")
}
