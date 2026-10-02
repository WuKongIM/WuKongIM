package meta

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTMembershipReadPinsChannelMemberAndSequence(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	ctx := context.Background()
	s := st.db.HashSlot(7)
	q := MQTTRead{Kind: MQTTReadMembership, MembershipKey: SubscriberKey{ChannelID: "group", ChannelType: 2, UID: "alice"}}
	empty, err := st.db.ReadMQTTState(ctx, 7, q)
	require.NoError(t, err)
	require.Equal(t, &MQTTMembershipView{Key: q.MembershipKey, Sequence: 1}, empty.Membership)
	require.True(t, empty.Done)
	require.NoError(t, s.CreateChannel(ctx, Channel{ChannelID: "group", ChannelType: 2, Ban: 1, SendBan: 1}))
	require.NoError(t, s.AddSubscribers(ctx, "group", 2, []string{"alice"}, 1))
	before, err := st.db.ReadMQTTState(ctx, 7, q)
	require.NoError(t, err)
	require.NoError(t, ValidateMQTTMembershipView(q.MembershipKey, before.Membership))
	snapshot, err := st.engine.NewSnapshot()
	require.NoError(t, err)
	defer snapshot.Close()
	require.NoError(t, s.RemoveSubscribers(ctx, "group", 2, []string{"alice"}, 1))
	require.NoError(t, s.AddSubscribers(ctx, "group", 2, []string{"alice"}, 1))
	require.NoError(t, s.UpsertChannel(ctx, Channel{ChannelID: "group", ChannelType: 2, Disband: 1}))
	live, found, err := s.GetChannel(ctx, "group", 2) // Populate live LRU after snapshot.
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 1, live.Disband)
	frozen := &Shard{db: st.db, hashSlot: 7, readSnapshot: snapshot}
	old, err := frozen.readMQTTState(ctx, q)
	require.NoError(t, err)
	require.Equal(t, before, old, "channel cache must not override the pinned snapshot")
	current, err := st.db.ReadMQTTState(ctx, 7, q)
	require.NoError(t, err)
	require.EqualValues(t, 1, current.Membership.Channel.Disband)
	require.Greater(t, current.Membership.Member.Incarnation, before.Membership.Member.Incarnation)
	require.Greater(t, current.Membership.Sequence, before.Membership.Sequence)
	// A regressed allocator is corrupt evidence, even when the member row decodes.
	b := st.engine.NewBatch()
	defer b.Close()
	require.NoError(t, b.Delete(subscriberSequenceKey(7)))
	require.NoError(t, b.Commit(true))
	_, err = st.db.ReadMQTTState(ctx, 7, q)
	require.ErrorIs(t, err, ErrCorruptValue)
}

func TestMQTTMembershipReadClosedContract(t *testing.T) {
	q := MQTTRead{Kind: MQTTReadMembership, MembershipKey: SubscriberKey{ChannelID: "group", ChannelType: 2, UID: "alice"}}
	require.NoError(t, ValidateMQTTRead(q))
	require.False(t, q.Recovery())
	_, _, owned := q.SessionIdentity()
	require.False(t, owned)
	for _, mutate := range []func(*MQTTRead){
		func(q *MQTTRead) { q.Limit = 1 }, func(q *MQTTRead) { q.Namespace = "main" },
		func(q *MQTTRead) { q.After.Topic = "topic" }, func(q *MQTTRead) { q.MembershipKey.UID = "" },
		func(q *MQTTRead) { q.MembershipKey.ChannelType = 256 }, func(q *MQTTRead) { q.MembershipKey.ChannelType = 0 },
		func(q *MQTTRead) { q.Kind = MQTTReadSession; q.Namespace = "main"; q.ClientID = "client" },
	} {
		bad := q
		mutate(&bad)
		require.ErrorIs(t, ValidateMQTTRead(bad), ErrInvalidArgument)
	}
	raw, err := json.Marshal(MQTTRead{Kind: MQTTReadSession, Namespace: "main", ClientID: "client"})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "membership_key")
	raw, err = json.Marshal(MQTTReadResult{Done: true})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "membership")
}
