//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

// Edits change the history projection, not the original publication's retry
// identity. This exercises the real app wiring, Slot authority and Channel log.
func TestPublicationSingleNodeClusterRetryAfterEdit(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	id := ch.ChannelID{ID: "mqtt-publication-retry", Type: 2}
	waitSingleNodeClusterRouteLeader(t, node, id.ID, cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	seedGroupSendPermission(t, node, id, "u1")
	metadata, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	require.NoError(t, err)
	cmd := message.SendCommand{FromUID: "u1", DeviceID: "d1", ChannelID: id.ID, ChannelType: id.Type, ClientMsgNo: "publication-1", Payload: []byte("original"), PublicationMetadata: metadata}
	first, err := a.Messages().Send(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, first.Reason)
	require.Equal(t, uint64(1), first.MessageSeq)
	init, err := node.ApplyMessageUpdate(ctx, metadb.MessageUpdateMutation{Op: "init", ChannelID: id.ID, ChannelType: 2, Generation: "generation"})
	require.NoError(t, err)
	require.Equal(t, "ok", init.Status)
	runtime, err := node.GetChannelRuntimeMeta(ctx, id.ID, 2)
	require.NoError(t, err)
	edit, err := node.ApplyMessageUpdate(ctx, metadb.MessageUpdateMutation{
		Op: "update", ChannelID: id.ID, ChannelType: 2,
		Generation: init.Head.Generation, ReplicaSet: init.Head.ReplicaSet,
		ExpectedChannelEpoch: runtime.ChannelEpoch, ExpectedRouteGeneration: runtime.RouteGeneration,
		MessageID: first.MessageID, MessageSeq: first.MessageSeq,
		RequestID: "edit-1", Digest: strings.Repeat("a", 64), Payload: []byte("edited"),
	})
	require.NoError(t, err)
	require.Equal(t, "ok", edit.Status)
	query := []channels.CommittedRead{{ChannelID: id, Request: store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1024}}}
	checkHistory := func() {
		t.Helper()
		rows, err := node.ReadChannelCommittedBatch(ctx, query)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.NoError(t, rows[0].Err)
		require.Len(t, rows[0].Read.Messages, 1)
		require.Equal(t, []byte("edited"), rows[0].Read.Messages[0].Payload)
		require.Equal(t, metadata, rows[0].Read.Messages[0].PublicationMetadata)
	}
	checkHistory()
	retry := cmd.Clone()
	retryMetadata, err := publication.Decode(metadata)
	require.NoError(t, err)
	retryMetadata.AcceptedAtMS++
	retry.PublicationMetadata, err = publication.Encode(retryMetadata)
	require.NoError(t, err)
	result, err := a.Messages().Send(ctx, retry)
	require.NoError(t, err)
	require.Equal(t, first, result, "history edits must not hide committed retry proof")
	conflict := cmd.Clone()
	conflict.PublicationMetadata = bytes.Clone(metadata)
	conflict.PublicationMetadata[2] = 0
	result, err = a.Messages().Send(ctx, conflict)
	require.True(t, err != nil || result.Reason != message.ReasonSuccess, "changed QoS reused original success")
	checkHistory()
}
