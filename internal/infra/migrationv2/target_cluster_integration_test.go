//go:build integration

package migrationv2_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompatibleMigrationStartsAndRestartsAsNativeSingleNodeCluster(t *testing.T) {
	for _, expire := range []uint32{0, 3600} {
		t.Run(fmt.Sprintf("expire_%d", expire), func(t *testing.T) { testCompatibleMigrationStartsAndRestartsAsNativeSingleNodeCluster(t, expire) })
	}
}

func testCompatibleMigrationStartsAndRestartsAsNativeSingleNodeCluster(t *testing.T, expire uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "native-cluster", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	capture, err := migration.CaptureSources(ctx, []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: compatibleExpiryMessageFixture(t, expire), ShardCount: 2}}}, r, w, nil)
	require.NoError(t, err)
	catalog, err := migration.BuildSourceCatalog(ctx, capture, w, r)
	require.NoError(t, err)
	selected, err := migration.SelectSources(ctx, capture, catalog, w, r, nil)
	require.NoError(t, err)
	report, err := migration.BuildTargetRecords(ctx, selected, w, r)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	dir := filepath.Join(t.TempDir(), "node101")
	plan := migration.TargetPlan{ClusterID: "migration-fixture", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: addr, DataDir: dir}}}
	require.NoError(t, migrationv3.Install(ctx, plan, report, w))
	_, err = migration.VerifyTargets(ctx, plan, selected, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
	for run := 0; run < 2; run++ {
		cfg := cluster.Config{NodeID: 101, ListenAddr: addr, DataDir: dir, Control: cluster.ControlConfig{ClusterID: "migration-fixture", Voters: []cluster.ControlVoter{{NodeID: 101, Addr: addr}}}, Slots: cluster.SlotConfig{InitialSlotCount: 4, HashSlotCount: 256, ReplicaCount: 1}, Channel: cluster.ChannelConfig{ReplicaCount: 1}}
		node, err := cluster.New(cfg)
		require.NoError(t, err)
		require.NoError(t, node.Start(ctx))
		func() {
			defer func() { require.NoError(t, node.Stop(context.Background())) }()
			id := ch.ChannelID{ID: "migrationgroup", Type: 2}
			var read store.ReadCommittedResult
			require.Eventually(t, func() bool {
				read, err = node.ReadChannelCommitted(ctx, id, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20})
				expected := 3
				if expire != 0 && run == 1 {
					expected = 4
				}
				return err == nil && len(read.Messages) == expected
			}, 15*time.Second, 50*time.Millisecond)
			require.Equal(t, uint64(2096462572973723648), read.Messages[0].MessageID)
			require.Equal(t, uint64(3), read.Messages[2].MessageSeq)
			require.Equal(t, int64(1788670602000), read.Messages[0].ServerTimestampMS)
			require.Equal(t, []byte("消息0"), read.Messages[0].Payload)
			require.Equal(t, expire, read.Messages[0].Expire)
			if expire != 0 {
				if run == 0 {
					var appended ch.AppendResult
					require.Eventually(t, func() bool {
						appended, err = node.AppendChannel(ctx, ch.AppendRequest{ChannelID: id, CommitMode: ch.CommitModeQuorum, Message: ch.Message{MessageID: 2100000000000000001, FromUID: "expiry-sender", ClientMsgNo: "expiry-new", ServerTimestampMS: 1788670702000, Expire: 1800, Payload: []byte("new expiring message")}})
						return !errors.Is(err, ch.ErrNotReady)
					}, 15*time.Second, 50*time.Millisecond)
					require.NoError(t, err)
					require.Greater(t, appended.MessageSeq, uint64(3))
				} else {
					require.Equal(t, uint32(1800), read.Messages[3].Expire)
				}
			}
		}()
	}
}

func TestCompatibleMigrationRepairsAnEmptyChannelReplicaInNativeThreeNodeCluster(t *testing.T) {
	for _, expire := range []uint32{0, 3600} {
		t.Run(fmt.Sprintf("expire_%d", expire), func(t *testing.T) {
			testCompatibleMigrationRepairsAnEmptyChannelReplicaInNativeThreeNodeCluster(t, expire)
		})
	}
}

func testCompatibleMigrationRepairsAnEmptyChannelReplicaInNativeThreeNodeCluster(t *testing.T, expire uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "three-node-target", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	capture, err := migration.CaptureSources(ctx, []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: compatibleExpiryMessageFixture(t, expire), ShardCount: 2}}}, r, w, nil)
	require.NoError(t, err)
	catalog, err := migration.BuildSourceCatalog(ctx, capture, w, r)
	require.NoError(t, err)
	selected, err := migration.SelectSources(ctx, capture, catalog, w, r, nil)
	require.NoError(t, err)
	report, err := migration.BuildTargetRecords(ctx, selected, w, r)
	require.NoError(t, err)
	plan := migration.TargetPlan{ClusterID: "migration-three", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 3, ChannelReplicas: 3}
	for i := 0; i < 3; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := listener.Addr().String()
		require.NoError(t, listener.Close())
		plan.Nodes = append(plan.Nodes, migration.TargetNode{NodeID: uint64(201 + i), Addr: addr, DataDir: filepath.Join(t.TempDir(), fmt.Sprintf("node%d", 201+i))})
	}
	require.NoError(t, migrationv3.Install(ctx, plan, report, w))
	_, err = migration.VerifyTargets(ctx, plan, selected, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
	voters := []cluster.ControlVoter{}
	for _, n := range plan.Nodes {
		voters = append(voters, cluster.ControlVoter{NodeID: n.NodeID, Addr: n.Addr})
	}
	nodes := make([]*cluster.Node, 3)
	start := func(i int) error {
		n := plan.Nodes[i]
		node, err := cluster.New(cluster.Config{NodeID: n.NodeID, ListenAddr: n.Addr, DataDir: n.DataDir, Control: cluster.ControlConfig{ClusterID: plan.ClusterID, Voters: voters}, Slots: cluster.SlotConfig{InitialSlotCount: 4, HashSlotCount: 256, ReplicaCount: 3}, Channel: cluster.ChannelConfig{ReplicaCount: 3}})
		if err != nil {
			return err
		}
		nodes[i] = node
		return node.Start(ctx)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := range nodes {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs <- start(i) }(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	defer func() {
		var wg sync.WaitGroup
		for _, node := range nodes {
			if node != nil {
				wg.Add(1)
				go func(node *cluster.Node) { defer wg.Done(); _ = node.Stop(context.Background()) }(node)
			}
		}
		wg.Wait()
	}()
	id := ch.ChannelID{ID: "migrationgroup", Type: 2}
	for _, node := range nodes {
		require.Eventually(t, func() bool {
			read, err := node.ReadChannelCommitted(ctx, id, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20})
			return err == nil && len(read.Messages) == 3 && read.Messages[0].MessageID == 2096462572973723648 && read.Messages[0].Expire == expire
		}, 20*time.Second, 50*time.Millisecond)
	}
	metadata, err := nodes[0].GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	failed := -1
	for i, n := range plan.Nodes {
		if n.NodeID == metadata.Leader {
			failed = i
		}
	}
	require.NotEqual(t, -1, failed)
	require.NoError(t, nodes[failed].Stop(ctx))
	nodes[failed] = nil
	require.NoError(t, os.RemoveAll(filepath.Join(plan.Nodes[failed].DataDir, "messages")))
	require.NoError(t, start(failed))
	var read store.ReadCommittedResult
	defer func() {
		if t.Failed() {
			t.Logf("last repaired read: %+v error=%v; node=%+v", read, err, nodes[failed].Snapshot())
		}
	}()
	recovered := assert.Eventually(t, func() bool {
		rows, callErr := nodes[failed].ReadChannelCommittedBatch(ctx, []channels.CommittedRead{{ChannelID: id, Request: store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20}}})
		err = callErr
		if callErr != nil || len(rows) != 1 {
			return false
		}
		read = rows[0].Read
		err = rows[0].Err
		return err == nil && len(read.Messages) == 3
	}, 25*time.Second, 50*time.Millisecond)
	if !recovered {
		// Preserve the failed read-only acceptance result. An isolated normal
		// append probes whether unchanged v3 recovery accepts the imported log.
		callCtx, done := context.WithTimeout(ctx, 5*time.Second)
		appended, appendErr := nodes[failed].AppendChannel(callCtx, ch.AppendRequest{ChannelID: id, CommitMode: ch.CommitModeQuorum, Message: ch.Message{MessageID: 2100000000000000000, FromUID: "migration-control", ClientMsgNo: "migration-control-next", ServerTimestampMS: 1788670605000, Payload: []byte("migration-control-next")}})
		done()
		read, err = nodes[failed].ReadChannelCommitted(ctx, id, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20})
		t.Logf("after failed imported recovery assertion: append_seq=%d append_error=%v read_count=%d read_error=%v", appended.MessageSeq, appendErr, len(read.Messages), err)
		require.NoError(t, appendErr)
		require.Equal(t, uint64(4), appended.MessageSeq)
		require.NoError(t, err)
		require.Len(t, read.Messages, 4)
		for i := 0; i < 3; i++ {
			require.Equal(t, uint64(i+1), read.Messages[i].MessageSeq)
			require.Equal(t, []byte(fmt.Sprintf("消息%d", i)), read.Messages[i].Payload)
		}
	}
	require.Equal(t, int64(1788670602000), read.Messages[0].ServerTimestampMS)
	require.Equal(t, []byte("消息0"), read.Messages[0].Payload)
	require.Equal(t, expire, read.Messages[0].Expire)
	require.Eventually(t, func() bool {
		rows, _, _, err := nodes[failed].ReadLocalLatestMessages(ctx, 0, 10)
		if err != nil {
			return false
		}
		for _, row := range rows {
			if row.MessageID == 2096462572973723648 {
				return string(row.Payload) == "消息0" && row.Expire == expire
			}
		}
		return false
	}, 25*time.Second, 50*time.Millisecond, "the empty node must actually repair its own message replica")
}
