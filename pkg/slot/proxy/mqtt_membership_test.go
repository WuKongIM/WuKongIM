package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTMembershipReadCurrentSlotAuthority(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 256)
	store := nodes[0].store
	var id string
	for i := 0; i < 10000; i++ {
		candidate := fmt.Sprintf("membership-%d", i)
		if store.cluster.SlotForKey(candidate) == 2 && store.cluster.HashSlotForKey(candidate) != 2 {
			id = candidate
			break
		}
	}
	require.NotEmpty(t, id)
	require.NoError(t, store.UpsertChannel(ctx, meta.Channel{ChannelID: id, ChannelType: 2}))
	require.NoError(t, store.AddChannelSubscribers(ctx, id, 2, []string{"alice"}, 1))
	_, err := nodes[0].db.ForHashSlot(store.cluster.HashSlotForKey(id)).GetChannel(ctx, id, 2)
	require.ErrorIs(t, err, meta.ErrNotFound, "origin intentionally has no channel")
	q := meta.MQTTRead{Kind: meta.MQTTReadMembership, MembershipKey: meta.SubscriberKey{ChannelID: id, ChannelType: 2, UID: "alice"}}
	var incarnation uint64
	for _, reader := range []*Store{store, nodes[1].store} {
		before := nodes[1].cluster.nextIndex[2]
		got, err := reader.ReadMQTT(ctx, q)
		require.NoError(t, err)
		require.NoError(t, meta.ValidateMQTTMembershipView(q.MembershipKey, got.Membership))
		require.NotNil(t, got.Membership.Channel)
		require.NotNil(t, got.Membership.Member)
		require.Equal(t, before+1, nodes[1].cluster.nextIndex[2], "each read has a new applied barrier")
		incarnation = got.Membership.Member.Incarnation
	}
	require.NoError(t, store.RemoveChannelSubscribers(ctx, id, 2, []string{"alice"}, 1))
	absent, err := store.ReadMQTT(ctx, q)
	require.NoError(t, err)
	require.Nil(t, absent.Membership.Member)
	require.NoError(t, store.AddChannelSubscribers(ctx, id, 2, []string{"alice"}, 1))
	again, err := store.ReadMQTT(ctx, q)
	require.NoError(t, err)
	require.Greater(t, again.Membership.Member.Incarnation, incarnation)
	nodes[1].store.cluster = &changingReadAuthority{proxyTestCluster: nodes[1].cluster}
	_, err = nodes[1].store.ReadMQTT(ctx, q)
	require.ErrorIs(t, err, ErrReadStaleRoute)
}

func TestMQTTMembershipReplyRejectsIncompleteOrForeignEvidence(t *testing.T) {
	key := meta.SubscriberKey{ChannelID: "g", ChannelType: 2, UID: "u"}
	q := meta.MQTTRead{Kind: meta.MQTTReadMembership, MembershipKey: key}
	req := mqttReadRPC{Format: 1, SlotID: 2, HashSlot: 17, Query: q}
	for _, mutate := range []func(*meta.MQTTReadResult){
		nil,
		func(r *meta.MQTTReadResult) { r.Membership = nil },
		func(r *meta.MQTTReadResult) { r.Done = false },
		func(r *meta.MQTTReadResult) { r.Membership.Key.UID = "other" },
		func(r *meta.MQTTReadResult) { r.Membership.Channel.ChannelID = "other" },
		func(r *meta.MQTTReadResult) { r.Membership.Member.UID = "other" },
		func(r *meta.MQTTReadResult) { r.Membership.Sequence = 1 },
		func(r *meta.MQTTReadResult) { r.Membership.Member.Incarnation = 0 },
		func(r *meta.MQTTReadResult) { r.After.Topic = "other" },
		func(r *meta.MQTTReadResult) { r.Session = &meta.MQTTSession{} },
		func(r *meta.MQTTReadResult) { r.Bindings = []meta.MQTTSourceBinding{{}} },
		func(r *meta.MQTTReadResult) { r.Accounting = &meta.MQTTAccountingRange{} },
	} {
		r := meta.MQTTReadResult{Done: true, Membership: &meta.MQTTMembershipView{Key: key, Sequence: 2, Channel: &meta.Channel{ChannelID: "g", ChannelType: 2}, Member: &meta.Subscriber{ChannelID: "g", ChannelType: 2, UID: "u", Incarnation: 2}}}
		if mutate != nil {
			mutate(&r)
		}
		raw, err := json.Marshal(mqttReadReply{Format: 1, SlotID: 2, HashSlot: 17, Query: q, Status: rpcStatusOK, Result: &r})
		require.NoError(t, err)
		_, err = decodeMQTTReadReply(raw, req)
		if mutate == nil {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, meta.ErrCorruptValue)
		}
	}
	require.Error(t, validateMQTTReadShape(meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: "main", ClientID: "c"}, meta.MQTTReadResult{Done: true, Membership: &meta.MQTTMembershipView{Key: key, Sequence: 1}}))
}
