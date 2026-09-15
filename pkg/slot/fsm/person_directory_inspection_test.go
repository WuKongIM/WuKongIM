package fsm

import (
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

func TestDecodeCommandInspectionPersonDirectoryBatches(t *testing.T) {
	channelID := channelid.EncodePersonChannel("u1", "u2")
	t.Run("membership", func(t *testing.T) {
		data, err := EncodeEnsureUserChannelMembershipBatchCommandChecked([]UserChannelMembershipBatchItem{
			{HashSlot: 7, Membership: metadb.UserChannelMembership{UID: "u2", ChannelID: channelID, ChannelType: 1, JoinSeq: 10, ReadSeq: 9, DeletedToSeq: 8, SourceVersion: 3, UpdatedAt: 123}},
			{HashSlot: 5, Membership: metadb.UserChannelMembership{UID: "u1", ChannelID: channelID, ChannelType: 1, JoinSeq: 10, ReadSeq: 9, DeletedToSeq: 8, SourceVersion: 3, UpdatedAt: 123}},
		})
		require.NoError(t, err)
		got, err := DecodeCommandInspection(data)
		require.NoError(t, err)
		require.Equal(t, "ensure_user_channel_membership_batch", got.Type)
		require.Equal(t, got.Type, got.Payload["command"])
		items := got.Payload["items"].([]map[string]any)
		require.Len(t, items, 2)
		for i, uid := range []string{"u1", "u2"} {
			require.Equal(t, uid, items[i]["uid"])
			require.Equal(t, []uint16{5, 7}[i], items[i]["hash_slot"])
			require.Equal(t, channelID, items[i]["channel_id"])
			require.Equal(t, int64(1), items[i]["channel_type"])
			require.Equal(t, uint64(10), items[i]["join_seq"])
			require.Equal(t, uint64(9), items[i]["read_seq"])
			require.Equal(t, uint64(8), items[i]["deleted_to_seq"])
			require.Equal(t, uint64(3), items[i]["source_version"])
			require.Equal(t, int64(123), items[i]["updated_at"])
		}
	})
	t.Run("admission", func(t *testing.T) {
		meta := metadb.ChannelRuntimeMeta{ChannelID: channelID, ChannelType: 1, ChannelEpoch: 2, LeaderEpoch: 3, Replicas: []uint64{1}, ISR: []uint64{1}, Leader: 1, MinISR: 1}
		data, err := EncodeAdmitPersonDirectoryTaskBatchCommandChecked([]PersonDirectoryAdmissionBatchItem{{
			HashSlot: 7, Task: metadb.PersonDirectoryTask{ChannelID: channelID, ChannelType: 1, CommittedTail: 9, CreatedAt: 123}, RuntimeMeta: meta,
		}})
		require.NoError(t, err)
		got, err := DecodeCommandInspection(data)
		require.NoError(t, err)
		require.Equal(t, "admit_person_directory_task_batch", got.Type)
		require.Equal(t, got.Type, got.Payload["command"])
		items := got.Payload["items"].([]map[string]any)
		require.Len(t, items, 1)
		require.Equal(t, uint16(7), items[0]["hash_slot"])
		require.Equal(t, channelID, items[0]["channel_id"])
		require.Equal(t, int64(1), items[0]["channel_type"])
		require.Equal(t, uint64(9), items[0]["committed_tail"])
		require.Equal(t, int64(123), items[0]["created_at"])
		runtime := items[0]["runtime_meta"].(map[string]any)
		require.Equal(t, channelID, runtime["channel_id"])
		require.Equal(t, uint64(2), runtime["channel_epoch"])
		require.Equal(t, uint64(3), runtime["leader_epoch"])
		require.Equal(t, uint64(1), runtime["leader"])
		require.Equal(t, []uint64{1}, runtime["replicas"])
	})
	t.Run("completion", func(t *testing.T) {
		data, err := EncodeCompletePersonDirectoryTaskBatchCommandChecked([]PersonDirectoryCompletionBatchItem{{HashSlot: 7, ChannelID: channelID, ChannelType: 1, Generation: 3}})
		require.NoError(t, err)
		got, err := DecodeCommandInspection(data)
		require.NoError(t, err)
		require.Equal(t, CommandInspection{
			Type: "complete_person_directory_task_batch",
			Payload: map[string]any{
				"command": "complete_person_directory_task_batch",
				"items":   []map[string]any{{"hash_slot": uint16(7), "channel_id": channelID, "channel_type": int64(1), "generation": uint64(3)}},
			},
		}, got)
	})
}
