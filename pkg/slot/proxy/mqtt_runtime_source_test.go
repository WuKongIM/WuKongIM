package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTRuntimeSourceUsesCurrentChannelSlotAndBarrier(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 256)
	store := nodes[0].store
	var id string
	for i := range 10000 {
		id = fmt.Sprintf("runtime-source-%d", i)
		if store.cluster.SlotForKey(id) == 2 && store.cluster.HashSlotForKey(id) != 2 {
			break
		}
	}
	require.EqualValues(t, 2, store.cluster.SlotForKey(id))
	hs := store.cluster.HashSlotForKey(id)
	m := meta.ChannelRuntimeMeta{ChannelID: id, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{2}, ISR: []uint64{2}, MinISR: 1}
	b := nodes[1].db.NewWriteBatch()
	defer b.Close()
	require.NoError(t, b.UpsertChannelRuntimeMeta(hs, m))
	require.NoError(t, b.DeleteChannelRuntimeMeta(hs, id, 2))
	_, err := b.CreateChannelRuntimeMeta(hs, m)
	require.NoError(t, err)
	require.NoError(t, b.Commit())
	q := meta.MQTTRead{Kind: meta.MQTTReadChannelRuntime, RuntimeChannel: meta.ChannelKey{ChannelID: id, ChannelType: 2}}
	local, err := nodes[0].db.ReadMQTTState(ctx, hs, q)
	require.NoError(t, err)
	require.Nil(t, local.Runtime.Meta)
	for _, reader := range []*Store{store, nodes[1].store} {
		before := nodes[1].cluster.nextIndex[2]
		got, err := reader.ReadMQTT(ctx, q)
		require.NoError(t, err)
		require.EqualValues(t, 1, got.Runtime.RetiredThrough)
		require.EqualValues(t, 2, got.Runtime.Meta.ChannelEpoch)
		require.Equal(t, before+1, nodes[1].cluster.nextIndex[2])
	}
	nodes[1].store.cluster = &changingReadAuthority{proxyTestCluster: nodes[1].cluster}
	_, err = nodes[1].store.ReadMQTT(ctx, q)
	require.ErrorIs(t, err, ErrReadStaleRoute)
}

func TestMQTTRuntimeSourceReplyIsClosedAndOlderWireUnchanged(t *testing.T) {
	key := meta.ChannelKey{ChannelID: "source", ChannelType: 2}
	q := meta.MQTTRead{Kind: meta.MQTTReadChannelRuntime, RuntimeChannel: key}
	req := mqttReadRPC{Format: 1, SlotID: 2, HashSlot: 17, Query: q}
	for _, mode := range []string{"absent", "retired", "missing", "foreign", "orphan", "cursor", "unfinished", "mixed"} {
		r := meta.MQTTReadResult{Done: true, Runtime: &meta.MQTTRuntimeView{Channel: key}}
		switch mode {
		case "retired":
			r.Runtime.RetiredThrough = 10
		case "missing":
			r.Runtime = nil
		case "foreign":
			r.Runtime.Channel.ChannelID = "other"
		case "orphan":
			r.Runtime.RetiredThrough = 10
			r.Runtime.Meta = &meta.ChannelRuntimeMeta{ChannelID: "source", ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{2}, ISR: []uint64{2}, MinISR: 1}
		case "cursor":
			r.After.Topic = "x"
		case "unfinished":
			r.Done = false
		case "mixed":
			r.Admission = &meta.MQTTInboxAdmissionView{ChannelID: "alice@bob"}
		}
		raw, err := json.Marshal(mqttReadReply{Format: 1, SlotID: 2, HashSlot: 17, Query: q, Status: rpcStatusOK, Result: &r})
		require.NoError(t, err)
		_, err = decodeMQTTReadReply(raw, req)
		if mode == "absent" || mode == "retired" {
			require.NoError(t, err)
		} else {
			require.Error(t, err, mode)
		}
	}
	old := meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: "n", ClientID: "c"}
	raw, err := json.Marshal(old)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "runtime_channel")
	r := meta.MQTTReadResult{Done: true}
	raw, err = json.Marshal(r)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "runtime")
	r.Runtime = &meta.MQTTRuntimeView{Channel: key}
	require.Error(t, validateMQTTReadShape(old, r))
}
