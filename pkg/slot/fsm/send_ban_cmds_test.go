package fsm

import (
	"context"
	"encoding/json"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure cases: two proposals in one apply batch must observe each other;
// stale CAS must not mutate, retries must not advance versions, policy-only
// users must survive credential creation, and target channels must retain
// subscriber/directory state. Invalid values and ambiguous ownership must fail
// before proposal; logical rejection must not prevent neighboring commits.
func TestSendBanAtomicApplyAndPreservedMetadata(t *testing.T) {
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	ctx := context.Background()
	var index uint64
	command := func(q metadb.SendBanMutation) multiraft.Command {
		raw, err := EncodeSendBanCommand(q)
		require.NoError(t, err)
		index++
		return multiraft.Command{SlotID: 11, HashSlot: 11, Index: index, Term: 1, Data: raw}
	}
	apply := func(q metadb.SendBanMutation) metadb.SendBanResult {
		raw, err := sm.Apply(ctx, command(q))
		require.NoError(t, err)
		var out metadb.SendBanResult
		require.NoError(t, json.Unmarshal(raw, &out))
		return out
	}
	q := metadb.SendBanMutation{UID: "u1", SendBan: 1}
	one, zero := uint64(1), uint64(0)
	stale := q
	stale.SendBan = 0
	stale.ExpectedVersion = &zero
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, []multiraft.Command{command(q), command(stale), command(q)})
	require.NoError(t, err)
	for i, want := range []string{"ok", "version_conflict", "ok"} {
		var out metadb.SendBanResult
		require.NoError(t, json.Unmarshal(results[i], &out))
		require.Equal(t, want, out.Status)
		require.EqualValues(t, 1, out.SendBan)
		require.EqualValues(t, 1, out.Version)
	}
	wb := db.NewWriteBatch()
	require.NoError(t, wb.CreateUser(11, metadb.User{UID: "u1", Token: "must-not-replace"}))
	require.NoError(t, wb.UpsertUser(11, metadb.User{UID: "u1", Token: "new-token"}))
	require.NoError(t, wb.UpsertChannel(11, metadb.Channel{ChannelID: "g", ChannelType: 2, AllowStranger: 1, Large: 1}))
	require.NoError(t, wb.Commit())
	require.NoError(t, wb.Close())
	user, err := db.ForHashSlot(11).GetUser(ctx, "u1")
	require.NoError(t, err)
	require.EqualValues(t, 1, user.SendBan)
	require.EqualValues(t, 1, user.SendBanVersion)
	require.Equal(t, "new-token", user.Token)
	q.ExpectedVersion = &one
	q.SendBan = 0
	unban := apply(q)
	require.Equal(t, "ok", unban.Status)
	require.EqualValues(t, 2, unban.Version)
	require.EqualValues(t, 0, unban.SendBan)
	require.EqualValues(t, 0, apply(metadb.SendBanMutation{UID: "missing", SendBan: 0}).Version)
	_, err = db.ForHashSlot(11).GetUser(ctx, "missing")
	require.ErrorIs(t, err, metadb.ErrNotFound)
	room := metadb.SendBanMutation{ChannelID: "g", ChannelType: 2, SendBan: 1}
	require.Equal(t, "ok", apply(room).Status)
	wb = db.NewWriteBatch()
	require.NoError(t, wb.UpsertChannel(11, metadb.Channel{ChannelID: "g", ChannelType: 2, Large: 1}))
	require.NoError(t, wb.Commit())
	require.NoError(t, wb.Close())
	ch, err := db.ForHashSlot(11).GetChannel(ctx, "g", 2)
	require.NoError(t, err)
	require.EqualValues(t, 1, ch.SendBan)
	require.EqualValues(t, 1, ch.SendBanVersion)
	require.Equal(t, "not_found", apply(metadb.SendBanMutation{ChannelID: "missing-group", ChannelType: 2, SendBan: 1}).Status)
	require.Equal(t, "ok", apply(metadb.SendBanMutation{ChannelID: "a@b", ChannelType: 1, SendBan: 1}).Status)
	wb = db.NewWriteBatch()
	_, err = wb.PatchChannelBusinessFlags(11, "g", 2, metadb.ChannelBusinessFlags{Disband: 1})
	require.NoError(t, err)
	require.NoError(t, wb.Commit())
	require.NoError(t, wb.Close())
	require.Equal(t, "channel_disbanded", apply(room).Status)
	wb = db.NewWriteBatch()
	require.NoError(t, wb.UpsertChannel(11, metadb.Channel{ChannelID: "g", ChannelType: 2}))
	require.NoError(t, wb.Commit())
	require.NoError(t, wb.Close())
	ch, err = db.ForHashSlot(11).GetChannel(ctx, "g", 2)
	require.NoError(t, err)
	require.EqualValues(t, 1, ch.Disband, "ordinary stale metadata must not revive a terminal channel")
}

func TestSendBanCommandRejectsMalformedOwnershipAndValues(t *testing.T) {
	for _, q := range []metadb.SendBanMutation{
		{}, {UID: "u", ChannelID: "g", ChannelType: 2}, {UID: "u", ChannelType: 1},
		{UID: "u", SendBan: 2}, {UID: "u", SendBan: -1}, {ChannelID: "g"},
	} {
		_, err := EncodeSendBanCommand(q)
		require.Error(t, err)
	}
	_, err := decodeSendBanCommand([]byte(`{"uid":"u","send_ban":1,"unknown":true}`))
	require.Error(t, err)
	_, err = decodeSendBanCommand([]byte(`{"uid":"u","send_ban":1} {}`))
	require.Error(t, err)
}

// Channel info and Manager flag writes must preserve an omitted policy, apply
// explicit unban atomically with other flags, and never revive a terminal row.
func TestChannelInfoPolicyIntentAtApply(t *testing.T) {
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	ctx := context.Background()
	var index uint64
	apply := func(q metadb.ChannelInfoMutation) metadb.SendBanResult {
		raw, err := EncodeChannelInfoCommand(q)
		require.NoError(t, err)
		index++
		data, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 11, Index: index, Term: 1, Data: raw})
		require.NoError(t, err)
		var out metadb.SendBanResult
		require.NoError(t, json.Unmarshal(data, &out))
		return out
	}
	one, zero := int64(1), int64(0)
	q := metadb.ChannelInfoMutation{ChannelID: "g", ChannelType: 2, SendBan: &one, Large: 1}
	require.EqualValues(t, 1, apply(q).Version)
	q.SendBan = nil
	q.Ban = 1
	q.Large = 0
	require.EqualValues(t, 1, apply(q).SendBan)
	q.SendBan = &zero
	q.FlagsOnly = true
	q.Large = 1
	require.EqualValues(t, 2, apply(q).Version)
	ch, err := db.ForHashSlot(11).GetChannel(ctx, "g", 2)
	require.NoError(t, err)
	require.EqualValues(t, 0, ch.Large)
	require.EqualValues(t, 0, ch.SendBan)
	require.EqualValues(t, 1, ch.Ban)
	q.SendBan = nil
	q.Disband = 1
	require.Equal(t, "ok", apply(q).Status)
	q.SendBan = &one
	q.Disband = 0
	require.Equal(t, "channel_disbanded", apply(q).Status)
	q.ChannelID = "missing"
	q.ExistingOnly = true
	require.Equal(t, "not_found", apply(q).Status)
}

func TestLegacyFlagPatchPreservesOrVersionsPolicy(t *testing.T) {
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	ctx := context.Background()
	wb := db.NewWriteBatch()
	require.NoError(t, wb.UpsertChannel(11, metadb.Channel{ChannelID: "g", ChannelType: 2}))
	require.NoError(t, wb.Commit())
	require.NoError(t, wb.Close())
	for i, flags := range []metadb.ChannelBusinessFlags{{SendBan: 1}, {Ban: 1, PreserveSendBan: true}, {SendBan: 0}} {
		_, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 11, Index: uint64(i + 1), Term: 1, Data: EncodePatchChannelBusinessFlagsCommand("g", 2, flags)})
		require.NoError(t, err)
		ch, err := db.ForHashSlot(11).GetChannel(ctx, "g", 2)
		require.NoError(t, err)
		if i < 2 {
			require.EqualValues(t, 1, ch.SendBan)
			require.EqualValues(t, 1, ch.SendBanVersion)
		} else {
			require.EqualValues(t, 0, ch.SendBan)
			require.EqualValues(t, 2, ch.SendBanVersion)
		}
	}
}

func TestLegacyFlagPolicyConflictRemainsItemLocal(t *testing.T) {
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	ctx := context.Background()
	require.NoError(t, db.ForHashSlot(11).UpsertChannel(ctx, metadb.Channel{ChannelID: "g", ChannelType: 2, SendBanVersion: ^uint64(0)}))
	commands := []multiraft.Command{
		{SlotID: 11, HashSlot: 11, Index: 1, Term: 1, Data: EncodePatchChannelBusinessFlagsCommand("g", 2, metadb.ChannelBusinessFlags{SendBan: 1})},
		{SlotID: 11, HashSlot: 11, Index: 2, Term: 1, Data: EncodePatchChannelBusinessFlagsCommand("g", 2, metadb.ChannelBusinessFlags{Disband: 1, PreserveSendBan: true})},
	}
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, commands)
	require.NoError(t, err)
	_, err = DecodeChannelConditionalMutationResult(results[0])
	require.ErrorIs(t, err, metadb.ErrStaleMeta)
	applied, err := DecodeChannelConditionalMutationResult(results[1])
	require.NoError(t, err)
	require.True(t, applied)
	ch, err := db.ForHashSlot(11).GetChannel(ctx, "g", 2)
	require.NoError(t, err)
	require.EqualValues(t, 1, ch.Disband)
	require.Zero(t, ch.SendBan)
}
