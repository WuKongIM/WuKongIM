package raft

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestServiceLogEntriesReadsLatestEntriesDescending(t *testing.T) {
	ctx := context.Background()
	store, err := raftstore.Open(ctx, raftstore.Config{Dir: filepath.Join(t.TempDir(), "controller-raft"), NodeID: 1})
	require.NoError(t, err)
	issuedAt := time.Date(2026, 6, 18, 9, 10, 11, 123000000, time.FixedZone("plus-eight", 8*60*60))
	cmd, err := command.Encode(command.Command{
		Kind:     command.KindUpsertNode,
		IssuedAt: issuedAt,
		Node:     &state.Node{NodeID: 2, Name: "node-2"},
	})
	require.NoError(t, err)
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryNormal},
		{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: cmd},
		{Index: 3, Term: 1, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, 2)},
	}
	require.NoError(t, store.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: 3}, entries, raftpb.Snapshot{}))
	require.NoError(t, store.MarkAppliedBatch(ctx, 2))

	service := &Service{cfg: Config{NodeID: 1}, store: store, started: true}
	t.Cleanup(func() { _ = store.Close() })

	got, err := service.LogEntries(ctx, LogEntriesOptions{Limit: 2})
	require.NoError(t, err)

	require.Equal(t, uint64(1), got.FirstIndex)
	require.Equal(t, uint64(3), got.LastIndex)
	require.Equal(t, uint64(3), got.CommitIndex)
	require.Equal(t, uint64(2), got.AppliedIndex)
	require.Equal(t, uint64(2), got.NextCursor)
	require.Len(t, got.Items, 2)
	require.Equal(t, uint64(3), got.Items[0].Index)
	require.Equal(t, "conf_change", got.Items[0].Type)
	require.Equal(t, uint64(2), got.Items[1].Index)
	require.Equal(t, "ok", got.Items[1].DecodeStatus)
	require.Equal(t, "upsert_node", got.Items[1].DecodedType)
	require.Equal(t, issuedAt.UTC().UnixMilli(), got.Items[1].CreatedAtMS)
	require.Equal(t, "upsert_node", got.Items[1].Decoded["command"])
}

func mustConfChangeData(t *testing.T, nodeID uint64) []byte {
	t.Helper()
	data, err := (&raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: nodeID}).Marshal()
	require.NoError(t, err)
	return data
}
