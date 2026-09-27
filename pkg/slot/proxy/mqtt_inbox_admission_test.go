package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

func TestMQTTInboxAdmissionCurrentChannelAuthority(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 256)
	store := nodes[0].store
	var id string
	for i := range 10000 {
		candidate := channelid.EncodePersonChannel("alice", fmt.Sprintf("bob%d", i))
		if store.cluster.SlotForKey(candidate) == 2 && store.cluster.HashSlotForKey(candidate) != 2 {
			id = candidate
			break
		}
	}
	require.NotEmpty(t, id)
	hs := store.cluster.HashSlotForKey(id)
	b := nodes[1].db.NewWriteBatch()
	defer b.Close()
	require.NoError(t, b.UpsertChannelRuntimeMeta(hs, meta.ChannelRuntimeMeta{ChannelID: id, ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []uint64{2}, ISR: []uint64{2}, MinISR: 1}))
	require.NoError(t, b.Commit())
	row := meta.MQTTInboxAdmission{ChannelID: id, DirectoryGeneration: 1, Revision: 1, UpdatedAtMS: 1000}
	written, err := store.CompareAndSwapMQTTInboxAdmission(ctx, 0, row)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, written.Status)
	require.EqualValues(t, 1, written.CurrentRevision)
	q := meta.MQTTRead{Kind: meta.MQTTReadInboxAdmission, AdmissionChannel: id}
	absent, err := nodes[0].db.ReadMQTTState(ctx, hs, q)
	require.NoError(t, err)
	require.Nil(t, absent.Admission.Runtime)
	require.Nil(t, absent.Admission.Checkpoint)
	for _, reader := range []*Store{store, nodes[1].store} {
		before := nodes[1].cluster.nextIndex[2]
		got, err := reader.ReadMQTT(ctx, q)
		require.NoError(t, err)
		require.Equal(t, row, *got.Admission.Checkpoint)
		require.EqualValues(t, 1, got.Admission.Runtime.DirectoryGeneration)
		require.Equal(t, before+1, nodes[1].cluster.nextIndex[2], "every read needs a fresh apply barrier")
	}
	written, err = store.CompareAndSwapMQTTInboxAdmission(ctx, 0, row)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASUnchanged, written.Status)
	nodes[1].store.cluster = &changingReadAuthority{proxyTestCluster: nodes[1].cluster}
	_, err = nodes[1].store.ReadMQTT(ctx, q)
	require.ErrorIs(t, err, ErrReadStaleRoute)
}

func TestMQTTInboxAdmissionReplyIsClosedAndOlderWireUnchanged(t *testing.T) {
	id := channelid.EncodePersonChannel("alice", "bob")
	q := meta.MQTTRead{Kind: meta.MQTTReadInboxAdmission, AdmissionChannel: id}
	req := mqttReadRPC{Format: 1, SlotID: 2, HashSlot: 17, Query: q}
	for _, mode := range []string{"absent", "invalidated", "stale", "missing", "foreign", "runtime_type", "orphan", "cursor", "unfinished", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			v := &meta.MQTTInboxAdmissionView{ChannelID: id}
			r := meta.MQTTReadResult{Done: true, Admission: v}
			switch mode {
			case "invalidated":
				v.Checkpoint = &meta.MQTTInboxAdmission{ChannelID: id, Revision: 2, UpdatedAtMS: 1000}
			case "stale":
				v.Runtime = &meta.ChannelRuntimeMeta{ChannelID: id, ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 2, Replicas: []uint64{2}, ISR: []uint64{2}, MinISR: 1, DirectoryGeneration: 2}
				v.Checkpoint = &meta.MQTTInboxAdmission{ChannelID: id, DirectoryGeneration: 1, Revision: 3, Participant: 2, UpdatedAtMS: 1000}
			case "missing":
				r.Admission = nil
			case "foreign":
				v.ChannelID = channelid.EncodePersonChannel("alice", "eve")
			case "runtime_type":
				v.Runtime = &meta.ChannelRuntimeMeta{ChannelID: id, ChannelType: 2, DirectoryGeneration: 1}
			case "orphan":
				v.Checkpoint = &meta.MQTTInboxAdmission{ChannelID: id, DirectoryGeneration: 1, Revision: 1, UpdatedAtMS: 1000}
			case "cursor":
				r.After.Topic = "x"
			case "unfinished":
				r.Done = false
			case "mixed":
				r.Directory = []meta.ChannelKey{{ChannelID: id, ChannelType: 1}}
			}
			raw, err := json.Marshal(mqttReadReply{Format: 1, SlotID: 2, HashSlot: 17, Query: q, Status: rpcStatusOK, Result: &r})
			require.NoError(t, err)
			_, err = decodeMQTTReadReply(raw, req)
			if mode == "absent" || mode == "invalidated" || mode == "stale" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, meta.ErrCorruptValue)
			}
		})
	}
	old := meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: "main", ClientID: "c"}
	raw, err := json.Marshal(old)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "admission_channel")
	r := meta.MQTTReadResult{Done: true}
	raw, err = json.Marshal(r)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "admission")
	r.Admission = &meta.MQTTInboxAdmissionView{ChannelID: id}
	require.ErrorIs(t, validateMQTTReadShape(old, r), meta.ErrCorruptValue)
}
