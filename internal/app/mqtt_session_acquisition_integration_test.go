//go:build integration

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

type mqttAcquisitionDeviceReader struct{ node *cluster.Node }

func (r mqttAcquisitionDeviceReader) GetDevice(ctx context.Context, uid string, flag int64) (meta.Device, error) {
	return r.node.GetDeviceMetadata(ctx, uid, flag)
}

type mqttAcquisitionRejectWill struct{}

func (mqttAcquisitionRejectWill) AuthorizeWill(context.Context, string, sessioncase.WillTarget) error {
	return errors.New("Will permission adapter intentionally absent in acquisition-only integration")
}

// This proves usecase composition with real Slot authority, device credentials
// and remote owner RPC. Transport callbacks are controlled local stubs; actual
// TCP/WebSocket physical close is covered by the gateway integration separately.
// It is not the process-level MQTT product acceptance test.
func TestMQTTSessionAcquisitionThreeNodeRPC(t *testing.T) {
	rootDir := t.TempDir()
	voters := []cluster.ControlVoter{{NodeID: 1, Addr: freeSendackSmokeTCPAddr(t)}, {NodeID: 2, Addr: freeSendackSmokeTCPAddr(t)}, {NodeID: 3, Addr: freeSendackSmokeTCPAddr(t)}}
	nodes := make([]*cluster.Node, 0, 3)
	owners := make([]*runtime.Owners, 0, 3)
	services := make([]*sessioncase.App, 0, 3)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, r := range owners {
			require.NoError(t, r.Close(ctx))
		}
		var wg sync.WaitGroup
		for _, n := range nodes {
			wg.Add(1)
			go func(n *cluster.Node) {
				defer wg.Done()
				if err := n.Stop(ctx); err != nil {
					t.Errorf("node stop: %v", err)
				}
			}(n)
		}
		wg.Wait()
	})
	for _, v := range voters {
		cfg := cluster.Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: filepath.Join(rootDir, fmt.Sprintf("node-%d", v.NodeID)), Control: cluster.ControlConfig{ClusterID: "mqtt-acquisition", Voters: voters, AllowBootstrap: true}, Slots: cluster.SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
		cfg.HealthReport.Interval = 100 * time.Millisecond
		n, e := cluster.New(cfg)
		require.NoError(t, e)
		nodes = append(nodes, n)
		r, e := runtime.NewOwners(runtime.OwnerOptions{NodeID: v.NodeID, BootID: fmt.Sprintf("acquisition-%d", v.NodeID), Capacity: 32, MaxOperations: 8, PendingTimeout: time.Minute, MaxLease: time.Minute, CloseRetry: time.Second})
		require.NoError(t, e)
		owners = append(owners, r)
		n.RegisterRPC(accessnode.MQTTOwnerRPCServiceID, accessnode.MQTTOwnerRPC{Owners: r})
		tokens := user.New(user.Options{DeviceReader: mqttAcquisitionDeviceReader{node: n}})
		service, e := sessioncase.New(sessioncase.Options{Store: n, Owners: r, Isolation: accessnode.NewMQTTOwnerClient(n), Tokens: tokens, Wills: mqttAcquisitionRejectWill{}, LeaseDuration: 30 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: 86400, QuotaMessages: 10000, QuotaBytes: 64 << 20, WindowLimit: 64})
		require.NoError(t, e)
		services = append(services, service)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	started := make(chan error, len(nodes))
	for _, n := range nodes {
		go func(n *cluster.Node) { started <- n.Start(ctx) }(n)
	}
	for range nodes {
		require.NoError(t, <-started)
	}
	key, e := slotproxy.MQTTSessionRoutingKey("main", "client")
	require.NoError(t, e)
	require.Eventually(t, func() bool {
		for _, n := range nodes {
			probe, done := context.WithTimeout(ctx, 200*time.Millisecond)
			err := n.ProbeWriteReady(probe)
			done()
			if err != nil {
				return false
			}
			route, err := n.RouteKey(key)
			if err != nil || route.Leader == 0 {
				return false
			}
		}
		return true
	}, 20*time.Second, 50*time.Millisecond)
	require.NoError(t, nodes[0].UpsertDeviceMetadata(ctx, meta.Device{UID: "alice", DeviceFlag: 1, DeviceLevel: 1, Token: "secret"}))
	oldClosed := make(chan struct{})
	var closeOnce sync.Once
	command := sessioncase.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: "client"}, UID: "alice", Token: "secret", DeviceFlag: protocolmeta.DeviceFlagWeb, SessionExpirySec: 86400, ReceiveMaximum: 64, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { closeOnce.Do(func() { close(oldClosed) }); return nil }}
	first, e := services[1].Connect(ctx, command)
	require.NoError(t, e)
	require.False(t, first.SessionPresent)
	wrong := command
	wrong.Token = "wrong"
	_, e = services[2].Connect(ctx, wrong)
	require.ErrorIs(t, e, user.ErrInvalidToken)
	select {
	case <-oldClosed:
		t.Fatal("bad credential evicted old owner")
	default:
	}
	held, e := owners[1].Begin(ctx, first.Owner)
	require.NoError(t, e)
	defer held.Done()
	nextCommand := command
	nextCommand.CloseTransport = func(context.Context) error { return nil }
	nextCommand.ReceiveMaximum = 1
	type result struct {
		connection sessioncase.Connection
		err        error
	}
	taken := make(chan result, 1)
	go func() { v, e := services[2].Connect(ctx, nextCommand); taken <- result{v, e} }()
	select {
	case <-oldClosed:
	case <-ctx.Done():
		t.Fatal("remote owner RPC did not close old transport")
	}
	select {
	case r := <-taken:
		t.Fatalf("takeover returned before remote scope drain: %+v", r)
	default:
	}
	held.Done()
	var next sessioncase.Connection
	select {
	case r := <-taken:
		require.NoError(t, r.err)
		next = r.connection
	case <-ctx.Done():
		t.Fatal("takeover did not finish")
	}
	require.True(t, next.SessionPresent)
	require.Equal(t, uint64(3), next.Owner.NodeID)
	require.Equal(t, uint64(2), next.Owner.OwnerGeneration)
	require.Equal(t, uint64(1), next.Owner.SessionGeneration)
	require.Zero(t, owners[1].Snapshot().Held)
	lease, e := services[2].Renew(ctx, next.Owner)
	require.NoError(t, e)
	require.Equal(t, uint64(3), lease.Revision)
	require.ErrorIs(t, services[1].Disconnect(ctx, sessioncase.DisconnectCommand{Owner: first.Owner, Normal: true}), sessioncase.ErrFenced)
	for _, n := range nodes {
		r, e := n.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: "main", ClientID: "client"})
		require.NoError(t, e)
		require.NotNil(t, r.Session)
		require.Equal(t, lease.Revision, r.Session.Revision)
		require.Equal(t, uint16(1), r.Session.ReceiveMaximum)
		require.Equal(t, next.Owner.ConnectionID, r.Session.ConnectionID)
		require.Equal(t, next.Owner.NodeID, r.Session.OwnerNodeID)
	}
	require.NoError(t, services[2].Disconnect(ctx, sessioncase.DisconnectCommand{Owner: next.Owner, Normal: true}))
	final, e := services[0].Connect(ctx, nextCommand)
	require.NoError(t, e)
	require.True(t, final.SessionPresent)
	require.Equal(t, uint64(3), final.Owner.OwnerGeneration)
	route, e := nodes[0].RouteKey(key)
	require.NoError(t, e)
	t.Logf("MQTT acquisition: hash_slots=256 physical_slots=2 replicas=3 hash_slot=%d slot=%d metadata_leader=%d owner_path=2->3->1 generation=1 owner_generation=3 bad_token_rejected=true remote_scope_drained=true stale_disconnect_rejected=true receive_maximum=1", route.HashSlot, route.SlotID, route.Leader)
}
