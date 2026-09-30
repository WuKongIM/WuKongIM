package fsm

import (
	"context"
	"encoding/json"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure cases: preceding reads race with apply, adjacent mutations see stale
// old values, no-ops advance audit versions, and rejected CAS reports intent
// as the committed state. Audit facts must come from the actual atomic apply.
func TestSendBanAuditUsesApplyTimeState(t *testing.T) {
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	zero := uint64(0)
	var commands []multiraft.Command
	for i, q := range []metadb.SendBanMutation{
		{UID: "audit-user", SendBan: 1},
		{UID: "audit-user", SendBan: 0, ExpectedVersion: &zero},
		{UID: "audit-user", SendBan: 1},
		{UID: "audit-user", SendBan: 0},
	} {
		raw, err := EncodeSendBanCommand(q)
		require.NoError(t, err)
		commands = append(commands, multiraft.Command{SlotID: 11, HashSlot: 11, Index: uint64(i + 1), Term: 1, Data: raw})
	}
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(context.Background(), commands)
	require.NoError(t, err)
	for i, want := range []struct{ previous, version, current, currentVersion float64 }{
		{0, 0, 1, 1}, {1, 1, 1, 1}, {1, 1, 1, 1}, {1, 1, 0, 2},
	} {
		var out map[string]any
		require.NoError(t, json.Unmarshal(results[i], &out))
		previous, ok := out["previous_policy"].(map[string]any)
		require.True(t, ok, "mutation result must include atomic previous policy")
		require.Equal(t, want.previous, previous["send_ban"])
		require.Equal(t, want.version, previous["send_ban_version"])
		require.Equal(t, want.current, out["send_ban"])
		require.Equal(t, want.currentVersion, out["send_ban_version"])
	}
	one := int64(1)
	raw, err := EncodeChannelInfoCommand(metadb.ChannelInfoMutation{ChannelID: "audit-group", ChannelType: 2, SendBan: &one})
	require.NoError(t, err)
	result, err := sm.Apply(context.Background(), multiraft.Command{SlotID: 11, HashSlot: 11, Index: 5, Term: 1, Data: raw})
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(result, &out))
	previous, ok := out["previous_policy"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(0), previous["send_ban"])
	require.Equal(t, float64(0), previous["send_ban_version"])
}
