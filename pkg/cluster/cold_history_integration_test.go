//go:build integration

package cluster_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/stretchr/testify/require"
)

// TestColdHistoryReplicaRestart uses only acknowledged native quorum writes.
// Reads must recover an intact or empty restarted Leader without a new append.
func TestColdHistoryReplicaRestart(t *testing.T) {
	t.Run("intact", func(t *testing.T) { runColdHistoryReplicaRestart(t, false, false) })
	t.Run("empty", func(t *testing.T) { runColdHistoryReplicaRestart(t, true, false) })
}

// Delivering a delayed replica activation after startup must not permanently
// strand reads when Slot metadata already names this replica as the new leader.
func TestCommittedReadRefreshesLoadedFollowerAfterAuthorityChange(t *testing.T) {
	runColdHistoryReplicaRestart(t, false, true)
}

func runColdHistoryReplicaRestart(t *testing.T, empty, staleRuntime bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	var voters []cluster.ControlVoter
	for i := 0; i < 3; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		voters = append(voters, cluster.ControlVoter{NodeID: uint64(201 + i), Addr: listener.Addr().String()})
		require.NoError(t, listener.Close())
	}
	nodes := make([]*cluster.Node, 3)
	cfgs := make([]cluster.Config, 3)
	for i, voter := range voters {
		cfgs[i] = cluster.Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: filepath.Join(t.TempDir(), "node"), Control: cluster.ControlConfig{ClusterID: "cold-history-recovery", Voters: voters, AllowBootstrap: true}, Slots: cluster.SlotConfig{InitialSlotCount: 4, HashSlotCount: 256, ReplicaCount: 3}, Channel: cluster.ChannelConfig{ReplicaCount: 3}}
	}
	if staleRuntime {
		// The foreground read must refresh authority without a background
		// migration task repairing the fixture first.
		for i := range cfgs {
			cfgs[i].ChannelMigration = cluster.ChannelMigrationConfig{EnabledSet: true, Enabled: false}
		}
	}
	start := func(i int) error {
		node, err := cluster.New(cfgs[i])
		if err != nil {
			return err
		}
		nodes[i] = node
		return node.Start(ctx)
	}
	t.Cleanup(func() {
		var wg sync.WaitGroup
		for _, node := range nodes {
			if node != nil {
				wg.Add(1)
				go func(node *cluster.Node) { defer wg.Done(); _ = node.Stop(context.Background()) }(node)
			}
		}
		wg.Wait()
	})
	errs := make(chan error, 3)
	for i := range nodes {
		go func(i int) { errs <- start(i) }(i)
	}
	for range nodes {
		require.NoError(t, <-errs)
	}
	require.Eventually(t, func() bool {
		for _, node := range nodes {
			s := node.Snapshot()
			if !s.RoutesReady || !s.SlotsReady || !s.ChannelsReady {
				return false
			}
		}
		return true
	}, 15*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		probeCtx, done := context.WithTimeout(ctx, 500*time.Millisecond)
		defer done()
		return nodes[0].ProbeWriteReady(probeCtx) == nil
	}, 10*time.Second, 50*time.Millisecond)
	id := ch.ChannelID{ID: "cold-history-group", Type: 2}
	for i := uint64(1); i <= 3; i++ {
		callCtx, done := context.WithTimeout(ctx, 5*time.Second)
		result, err := nodes[0].AppendChannel(callCtx, ch.AppendRequest{ChannelID: id, CommitMode: ch.CommitModeQuorum, Message: ch.Message{MessageID: 1000 + i, ServerTimestampMS: 1788670602000, FromUID: "alice", ClientMsgNo: fmt.Sprintf("control-%d", i), Payload: []byte(fmt.Sprintf("control-%d", i))}})
		done()
		require.NoError(t, err)
		require.Equal(t, i, result.MessageSeq)
	}
	localComplete := func(node *cluster.Node) bool {
		for seq := uint64(1); seq <= 3; seq++ {
			hit, found, err := node.LookupChannelIdempotency(ctx, id, "alice", fmt.Sprintf("control-%d", seq))
			if err != nil || !found {
				return false
			}
			row := hit.Message
			if row.MessageID != 1000+seq || row.MessageSeq != seq || string(row.Payload) != fmt.Sprintf("control-%d", seq) || row.ServerTimestampMS != 1788670602000 {
				return false
			}
		}
		return true
	}

	for _, node := range nodes {

		require.Eventually(t, func() bool { return localComplete(node) }, 15*time.Second, 50*time.Millisecond, "all original replicas must be locally complete before injecting loss")
	}
	metadata, err := nodes[0].GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	failed := -1
	for i, node := range nodes {
		if node.NodeID() == metadata.Leader {
			failed = i
		}
	}
	require.NotEqual(t, -1, failed)
	oldMetadata := metadata
	if staleRuntime {
		failed = (failed + 1) % len(nodes)
	}
	t.Logf("pre-loss: all 3 local replicas complete through local idempotency primary reads, channel metadata=%+v", metadata)
	readBefore, err := nodes[0].ReadChannelCommitted(ctx, id, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Len(t, readBefore.Messages, 3)
	require.NoError(t, nodes[failed].Stop(ctx))
	nodes[failed] = nil
	if staleRuntime {
		metadata.Leader = cfgs[failed].NodeID
		metadata.LeaderEpoch++
		metadata.RouteGeneration++
		require.NoError(t, nodes[(failed+1)%len(nodes)].Propose(ctx, cluster.ProposeRequest{Key: id.ID, Command: metafsm.EncodeUpsertChannelRuntimeMetaCommand(metadata)}))
	}
	messageDir := filepath.Join(cfgs[failed].DataDir, "messages")
	if empty {
		require.NoError(t, os.Rename(messageDir, messageDir+"-before-control"))
	}
	require.NoError(t, start(failed))
	if staleRuntime {
		// Start returns before the restarted node has caught up its Slot route
		// and authority. Establish that prerequisite before injecting delayed
		// replica activation, so the regression exercises the loaded runtime.
		require.Eventually(t, func() bool {
			authoritative, err := nodes[failed].GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
			return err == nil && authoritative.Leader == nodes[failed].NodeID() && authoritative.LeaderEpoch == metadata.LeaderEpoch
		}, 15*time.Second, 50*time.Millisecond)
		require.NoError(t, nodes[failed].ApplyChannelMeta(ctx, nodes[failed].NodeID(), oldMetadata))
		probe, err := nodes[failed].ChannelRuntimeProbe(ctx, ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{id}})
		require.NoError(t, err)
		require.Len(t, probe.Channels, 1)
		require.Equal(t, ch.RoleFollower, probe.Channels[0].Role)
		require.Equal(t, oldMetadata.LeaderEpoch, probe.Channels[0].LeaderEpoch)
		authoritative, err := nodes[failed].GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		require.NoError(t, err)
		require.Equal(t, nodes[failed].NodeID(), authoritative.Leader)
		require.Equal(t, metadata.LeaderEpoch, authoritative.LeaderEpoch)
	}
	var read store.ReadCommittedResult
	var readErr error
	observe := func() {
		meta, metaErr := nodes[failed].GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		rows, _, _, localErr := nodes[failed].ReadLocalLatestMessages(ctx, 0, 10)
		t.Logf("post-loss node=%d snapshot=%+v metadata=%+v meta_error=%v routed_count=%d routed_error=%v local_count=%d local_error=%v physical_complete=%t", nodes[failed].NodeID(), nodes[failed].Snapshot(), meta, metaErr, len(read.Messages), readErr, len(rows), localErr, localComplete(nodes[failed]))
	}
	defer observe()
	require.Eventually(t, func() bool {
		callCtx, done := context.WithTimeout(ctx, time.Second)
		defer done()
		rows, err := nodes[failed].ReadChannelCommittedBatch(callCtx, []channels.CommittedRead{{ChannelID: id, Request: store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20}}})
		readErr = err
		if err != nil || len(rows) != 1 {
			return false
		}
		read, readErr = rows[0].Read, rows[0].Err
		ordinary := read.Messages[:0]
		for _, message := range read.Messages {
			if !message.SyncOnce {
				ordinary = append(ordinary, message)
			}
		}
		read.Messages = ordinary
		return readErr == nil && len(read.Messages) == 3 && localComplete(nodes[failed])
	}, 15*time.Second, 50*time.Millisecond, "restarted replica must expose every previously committed message")
	for index, message := range read.Messages {
		require.Equal(t, uint64(1001+index), message.MessageID)
		require.Equal(t, uint64(1+index), message.MessageSeq)
		require.Equal(t, "alice", message.FromUID)
		require.Equal(t, fmt.Sprintf("control-%d", index+1), message.ClientMsgNo)
		require.Equal(t, int64(1788670602000), message.ServerTimestampMS)
		require.Equal(t, fmt.Sprintf("control-%d", index+1), string(message.Payload))
	}
}
