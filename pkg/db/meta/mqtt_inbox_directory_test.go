package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTInboxDirectoryStablePrimaryPages(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	ctx := context.Background()
	s := st.db.HashSlot(7)
	want := []ChannelKey{{"z", 1}, {"z", 2}, {"aa", 1}, {"bbb", 1}}
	for i, k := range want {
		require.NoError(t, s.UpsertUserChannelMembership(ctx, UserChannelMembership{
			UID: "alice", ChannelID: k.ChannelID, ChannelType: k.ChannelType,
			ActivatedAt: int64(i + 1), Tombstone: i == 2, TombstoneAt: int64(i + 1),
			DeletedToSeq: 20, ConversationHiddenThroughSeq: 30,
		}))
	}
	require.NoError(t, s.UpsertUserChannelMembership(ctx, UserChannelMembership{UID: "bob", ChannelID: "a", ChannelType: 1}))
	q := MQTTRead{Kind: MQTTReadInboxDirectory, Owner: MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}, Limit: 1}
	for i, key := range want {
		r, err := st.db.ReadMQTTState(ctx, 7, q)
		require.NoError(t, err)
		require.Equal(t, []ChannelKey{key}, r.Directory)
		require.Equal(t, MQTTReadCursor{Directory: key}, r.After)
		require.Equal(t, i == len(want)-1, r.Done)
		q.After = r.After
		// Reordering the conversation activation index cannot change discovery.
		require.NoError(t, s.SetUserChannelMembershipActivatedAt(ctx, "alice", want[3], int64(100+i), int64(100+i)))
		require.NoError(t, s.HideUserChannelMembership(ctx, "alice", key, 40, int64(100+i)))
	}
	r, err := st.db.ReadMQTTState(ctx, 7, q)
	require.NoError(t, err)
	require.Empty(t, r.Directory)
	require.True(t, r.Done)
	require.Equal(t, q.After, r.After)
	q.Owner.ID = "absent"
	r, err = st.db.ReadMQTTState(ctx, 7, q)
	require.NoError(t, err)
	require.Empty(t, r.Directory)
	require.True(t, r.Done)
	require.Equal(t, q.After, r.After)
}

func TestMQTTInboxDirectorySnapshotAndBoundedMixedTypes(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	ctx := context.Background()
	b := st.db.NewBatch()
	defer b.Close()
	for i := range 256 {
		require.NoError(t, b.UpsertUserChannelMembership(7, UserChannelMembership{UID: "alice", ChannelID: fmt.Sprintf("g%03d", i), ChannelType: 2}))
	}
	require.NoError(t, b.Commit(ctx))
	snap, err := st.engine.NewSnapshot()
	require.NoError(t, err)
	defer snap.Close()
	frozen := &Shard{db: st.db, hashSlot: 7, readSnapshot: snap}
	q := MQTTRead{Kind: MQTTReadInboxDirectory, Owner: MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}, Limit: 1}
	before, err := frozen.readMQTTState(&mqttSourceOwnerBudgetContext{Context: ctx}, q)
	require.NoError(t, err, "groups must consume the page budget instead of being skipped")
	require.Equal(t, []ChannelKey{{"g000", 2}}, before.Directory)
	require.False(t, before.Done)
	require.NoError(t, st.db.HashSlot(7).UpsertUserChannelMembership(ctx, UserChannelMembership{UID: "alice", ChannelID: "p", ChannelType: 1}))
	after, err := frozen.readMQTTState(ctx, q)
	require.NoError(t, err)
	require.Equal(t, before, after)
	live, err := st.db.ReadMQTTState(ctx, 7, q)
	require.NoError(t, err)
	require.Equal(t, []ChannelKey{{"p", 1}}, live.Directory)
}

func TestMQTTInboxDirectoryCorruptionAndCancellation(t *testing.T) {
	for _, mode := range []string{"value", "key", "lookahead", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			st := openTestMetaStore(t)
			defer st.close(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := st.db.HashSlot(7)
			for _, id := range []string{"a", "b"} {
				require.NoError(t, s.UpsertUserChannelMembership(ctx, UserChannelMembership{UID: "alice", ChannelID: id, ChannelType: 1}))
			}
			q := MQTTRead{Kind: MQTTReadInboxDirectory, Owner: MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}, Limit: 1}
			if mode == "cancel" {
				cancel()
			} else {
				pk := userChannelMembershipPrimaryKey("alice", "a", 1)
				if mode == "lookahead" {
					pk = userChannelMembershipPrimaryKey("alice", "b", 1)
				}
				key, err := userChannelMembershipTable.primaryRowKey(7, pk)
				require.NoError(t, err)
				if mode == "key" {
					key, err = encodeKeyParts(encodeRowPrefix(7, TableIDUserChannelMembership), KeyParts{String("alice")})
					require.NoError(t, err)
				}
				raw := st.engine.NewBatch()
				defer raw.Close()
				require.NoError(t, raw.Set(key, []byte("corrupt")))
				require.NoError(t, raw.Commit(true))
			}
			r, err := st.db.ReadMQTTState(ctx, 7, q)
			require.Error(t, err)
			require.Zero(t, r, "no partial page or false absence on failure")
		})
	}
}

func TestMQTTInboxDirectoryClosedRequestAndLegacyJSON(t *testing.T) {
	q := MQTTRead{Kind: MQTTReadInboxDirectory, Owner: MQTTBindingOwner{Kind: MQTTBindingUID, ID: "alice"}, Limit: 64}
	require.Equal(t, MQTTReadKind(20), q.Kind)
	require.NoError(t, ValidateMQTTRead(q))
	require.False(t, q.Recovery())
	_, _, sessionOwned := q.SessionIdentity()
	require.False(t, sessionOwned)
	for _, change := range []func(*MQTTRead){
		func(q *MQTTRead) { q.Limit = 0 }, func(q *MQTTRead) { q.Limit = 65 },
		func(q *MQTTRead) { q.Owner.Kind = MQTTBindingChannel },
		func(q *MQTTRead) { q.Owner.Generation = "g" }, func(q *MQTTRead) { q.Owner.ID = "" },
		func(q *MQTTRead) { q.ClientID = "client" }, func(q *MQTTRead) { q.After.Topic = "x" },
		func(q *MQTTRead) { q.After.Directory = ChannelKey{"", 1} },
		func(q *MQTTRead) { q.After.Directory = ChannelKey{"x", 0} },
		func(q *MQTTRead) { q.After.Directory = ChannelKey{"x", 256} },
		func(q *MQTTRead) { q.Kind = MQTTReadSourceCandidates; q.After.Directory = ChannelKey{"x", 1} },
	} {
		bad := q
		change(&bad)
		require.ErrorIs(t, ValidateMQTTRead(bad), ErrInvalidArgument)
	}
	q.After.Directory = ChannelKey{"x", 255}
	require.NoError(t, ValidateMQTTRead(q))
	for _, value := range []any{MQTTRead{Kind: MQTTReadSession, Namespace: "main", ClientID: "c"}, MQTTReadResult{Done: true}} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "directory")
	}
}
