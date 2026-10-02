package proxy

import (
	"context"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

type freshRuntimeBarrierCluster struct {
	Cluster
	run func(context.Context, multiraft.SlotID) error
}

func (c freshRuntimeBarrierCluster) ReadSlotBarrier(ctx context.Context, id multiraft.SlotID) error {
	return c.run(ctx, id)
}

func TestMQTTSourceRuntimeFreshMetadataRoutesAndBarriers(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 256)
	ctx := context.Background()
	s := nodes[0].store
	id := ""
	for i := range 1000 {
		candidate := fmt.Sprintf("fresh-source-%d", i)
		if s.cluster.SlotForKey(candidate) == 2 {
			id = candidate
			break
		}
	}
	require.NotEmpty(t, id)
	meta := metadb.ChannelRuntimeMeta{ChannelID: id, ChannelType: 2, ChannelEpoch: 3, LeaderEpoch: 4, RouteGeneration: 7, Leader: 2, Replicas: []uint64{1, 2}, ISR: []uint64{1, 2}, MinISR: 2, Status: 1}
	require.NoError(t, s.UpsertChannelRuntimeMeta(ctx, meta))
	for range 2 {
		before := nodes[1].cluster.nextIndex[2]
		got, err := s.GetChannelRuntimeMetaFresh(ctx, id, 2)
		require.NoError(t, err)
		require.Equal(t, metadb.NormalizeChannelRuntimeMeta(meta), got)
		require.Equal(t, before+1, nodes[1].cluster.nextIndex[2])
	}
	_, err := s.GetChannelRuntimeMetaFresh(ctx, id, 3)
	require.ErrorIs(t, err, metadb.ErrNotFound)
	local := nodes[1].store
	local.cluster = freshRuntimeBarrierCluster{Cluster: nodes[1].cluster, run: func(context.Context, multiraft.SlotID) error { return context.DeadlineExceeded }}
	_, err = s.GetChannelRuntimeMetaFresh(ctx, id, 2)
	require.Error(t, err, "read must not use the previously successful barrier")
	local.cluster = freshRuntimeBarrierCluster{Cluster: nodes[1].cluster, run: func(context.Context, multiraft.SlotID) error { nodes[1].cluster.layout.version++; return nil }}
	_, err = local.GetChannelRuntimeMetaFresh(ctx, id, 2)
	require.ErrorIs(t, err, ErrReadStaleRoute)
}

func TestMQTTSourceRuntimeFreshCodecRejectsDowngradeAndForgedSlot(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 256)
	s := nodes[0].store
	req := runtimeMetaRPCRequest{Op: runtimeMetaRPCGetFresh, SlotID: 1, ChannelID: "source", ChannelType: 2, CodecVersion: 3}
	body, err := encodeRuntimeMetaRPCRequestBinary(req)
	require.NoError(t, err)
	decoded, err := decodeRuntimeMetaRPCRequest(body)
	require.NoError(t, err)
	require.Empty(t, decoded.Keys)
	decoded.Keys = nil // The shared codec normalizes an empty collection.
	require.Equal(t, req, decoded)
	for _, version := range []byte{1, 2} {
		bad := req
		bad.CodecVersion = version
		_, err = encodeRuntimeMetaRPCRequestBinary(bad)
		require.Error(t, err)
		b := append([]byte(nil), body...)
		b[4] = version
		_, err = decodeRuntimeMetaRPCRequest(b)
		require.Error(t, err)
	}
	req.SlotID = uint64(s.cluster.SlotForKey(req.ChannelID)) + 20
	body, err = encodeRuntimeMetaRPCRequestBinary(req)
	require.NoError(t, err)
	_, err = s.handleRuntimeMetaRPC(context.Background(), body)
	require.Error(t, err)
}

func TestMQTTSourceRuntimeFreshRejectsUnrelatedReplyFields(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 256)
	s := nodes[0].store
	id := ""
	for i := range 1000 {
		candidate := fmt.Sprintf("reply-source-%d", i)
		if s.cluster.SlotForKey(candidate) == 2 {
			id = candidate
			break
		}
	}
	require.NotEmpty(t, id)
	meta := metadb.ChannelRuntimeMeta{ChannelID: id, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2}, ISR: []uint64{1, 2}, MinISR: 2, Status: 1}
	for _, mode := range []string{"legacy", "rows", "cursor", "done", "identity"} {
		t.Run(mode, func(t *testing.T) {
			reply := runtimeMetaRPCResponse{Status: rpcStatusOK, Meta: &meta}
			version := byte(3)
			switch mode {
			case "legacy":
				version = 2
			case "rows":
				reply.Metas = []metadb.ChannelRuntimeMeta{meta}
			case "cursor":
				reply.Cursor = metadb.ChannelRuntimeMetaCursor{ChannelID: id, ChannelType: 2}
			case "done":
				reply.Done = true
			case "identity":
				copy := meta
				copy.ChannelID = "different"
				reply.Meta = &copy
			}
			nodes[1].cluster.handlers[runtimeMetaRPCServiceID] = func(context.Context, []byte) ([]byte, error) {
				return encodeRuntimeMetaRPCResponseForVersion(reply, version)
			}
			_, err := s.GetChannelRuntimeMetaFresh(context.Background(), id, 2)
			require.Error(t, err)
		})
	}
}

func TestMQTTSourceRuntimeFreshBoundsBeforeSharedDecoding(t *testing.T) {
	req := runtimeMetaRPCRequest{Op: runtimeMetaRPCGetFresh, SlotID: 1, ChannelID: "source", ChannelType: 2, CodecVersion: 3}
	b, err := encodeRuntimeMetaRPCRequestBinary(req)
	require.NoError(t, err)
	_, err = decodeRuntimeMetaRPCRequest(append(b, make([]byte, 4096)...))
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}
