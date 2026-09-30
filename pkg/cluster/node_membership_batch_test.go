package cluster

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/stretchr/testify/require"
)

// Failure cases: same-owner logical shards must not create one proposal each;
// every batch remains owned, bounded, identity-unique and independent of input order.
func TestUpsertMembershipsCoalescesSamePhysicalSlot(t *testing.T) {
	proposer := &collectingMembershipProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	u0, u1, u3 := keyForNodeHashSlot(t, 4, 0), keyForNodeHashSlot(t, 4, 1), keyForNodeHashSlot(t, 4, 3)
	require.NoError(t, node.UpsertUserChannelMemberships(context.Background(), "group", 2, []string{u3, u1, u0, u0}, 9, 7, 123))
	requests := proposer.take()
	require.Len(t, requests, 2, "two physical owners, not one proposal per logical shard")
	total := 0
	for _, r := range requests {
		require.True(t, r.Target.HasSlotID)
		require.True(t, r.Target.HasHashSlot)
		hashes, err := metafsm.DecodeCommandHashSlots(r.Command, r.Target.HashSlot)
		require.NoError(t, err)
		for _, hs := range hashes {
			want := uint32(1)
			if hs >= 2 {
				want = 2
			}
			require.Equal(t, want, r.Target.SlotID)
		}
		info, err := metafsm.DecodeCommandInspection(r.Command)
		require.NoError(t, err)
		require.Equal(t, "upsert_user_channel_membership_batch", info.Type)
		rows, ok := info.Payload["items"].([]map[string]any)
		require.True(t, ok)
		total += len(rows)
	}
	require.Equal(t, 3, total, "repeated UID must remain idempotent")
}

func TestUpsertMembershipsChunksOneOwnerWithoutDroppingRows(t *testing.T) {
	proposer := &collectingMembershipProposer{}
	node := newStartedSlotProxyPortNode(t, proposer)
	var uids []string
	for i := 0; len(uids) < 260; i++ {
		uid := fmt.Sprintf("batch-uid-%d", i)
		if routing.HashSlotForKey(uid, 4) < 2 {
			uids = append(uids, uid)
		}
	}
	require.NoError(t, node.UpsertUserChannelMemberships(context.Background(), "group", 2, uids, 0, 1, 123))
	requests := proposer.take()
	require.Len(t, requests, 3)
	seen := map[string]bool{}
	for _, r := range requests {
		require.EqualValues(t, 1, r.Target.SlotID)
		require.LessOrEqual(t, len(r.Command), 256<<10)
		info, err := metafsm.DecodeCommandInspection(r.Command)
		require.NoError(t, err)
		rows, ok := info.Payload["items"].([]map[string]any)
		require.True(t, ok)
		require.LessOrEqual(t, len(rows), 128)
		for _, row := range rows {
			uid, ok := row["uid"].(string)
			require.True(t, ok)
			require.False(t, seen[uid])
			seen[uid] = true
		}
	}
	require.Len(t, seen, len(uids))
}

func TestUpsertMembershipsSplitsByteLimitsBeforeSubmission(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		uidBytes, channelBytes int
	}{
		{"uid budget", 32760, 1}, {"encoded command budget", 1, 140000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proposer := &collectingMembershipProposer{}
			node := newStartedSlotProxyPortNode(t, proposer)
			var uids []string
			for i := 0; len(uids) < 3; i++ {
				uid := strings.Repeat("u", tc.uidBytes) + fmt.Sprint(i)
				if routing.HashSlotForKey(uid, 4) < 2 {
					uids = append(uids, uid)
				}
			}
			require.NoError(t, node.UpsertUserChannelMemberships(context.Background(), strings.Repeat("g", tc.channelBytes), 2, uids, ^uint64(0), 1, 123))
			seen := 0
			requests := proposer.take()
			require.Greater(t, len(requests), 1)
			for _, r := range requests {
				require.True(t, r.Target.HasSlotID)
				require.LessOrEqual(t, len(r.Command), 256<<10)
				info, err := metafsm.DecodeCommandInspection(r.Command)
				require.NoError(t, err)
				rows := info.Payload["items"].([]map[string]any)
				uidBytes := 0
				for _, row := range rows {
					uidBytes += len(row["uid"].(string))
					require.Equal(t, ^uint64(0), row["join_seq"])
				}
				require.LessOrEqual(t, uidBytes, 64<<10)
				seen += len(rows)
			}
			require.Equal(t, 3, seen)
		})
	}
}
