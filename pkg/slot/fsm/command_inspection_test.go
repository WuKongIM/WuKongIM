package fsm

import (
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestDecodeCommandInspectionRedactsUserToken(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeUpsertUserCommand(metadb.User{
		UID:         "u1",
		Token:       "secret-token",
		DeviceFlag:  3,
		DeviceLevel: 7,
	}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "upsert_user",
		Payload: map[string]any{
			"command":      "upsert_user",
			"uid":          "u1",
			"token":        "***",
			"device_flag":  int64(3),
			"device_level": int64(7),
		},
	}, got)
}

func TestDecodeCommandInspectionIncludesRuntimeMetaRetention(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeUpsertChannelRuntimeMetaCommand(metadb.ChannelRuntimeMeta{
		ChannelID:            "inspect-retention-meta",
		ChannelType:          2,
		ChannelEpoch:         3,
		LeaderEpoch:          4,
		Replicas:             []uint64{2, 1},
		ISR:                  []uint64{1, 2},
		Leader:               1,
		MinISR:               2,
		Status:               2,
		Features:             1,
		LeaseUntilMS:         1700000000000,
		RetentionThroughSeq:  99,
		RetentionUpdatedAtMS: 1700000000123,
	}))
	require.NoError(t, err)

	require.Equal(t, uint64(99), got.Payload["retention_through_seq"])
	require.Equal(t, int64(1700000000123), got.Payload["retention_updated_at_ms"])
}

func TestDecodeCommandInspectionIncludesAdvanceChannelRetentionThroughSeq(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeAdvanceChannelRetentionThroughSeqCommand(metadb.ChannelRetentionAdvance{
		ChannelID:            "inspect-retention-advance",
		ChannelType:          2,
		ExpectedChannelEpoch: 3,
		ExpectedLeaderEpoch:  4,
		ExpectedLeader:       1,
		ExpectedLeaseUntilMS: 1700000000000,
		RetentionThroughSeq:  99,
		RetentionUpdatedAtMS: 1700000000123,
	}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "advance_channel_retention_through_seq",
		Payload: map[string]any{
			"command":                 "advance_channel_retention_through_seq",
			"channel_id":              "inspect-retention-advance",
			"channel_type":            int64(2),
			"expected_channel_epoch":  uint64(3),
			"expected_leader_epoch":   uint64(4),
			"expected_leader":         uint64(1),
			"expected_lease_until_ms": int64(1700000000000),
			"retention_through_seq":   uint64(99),
			"retention_updated_at_ms": int64(1700000000123),
		},
	}, got)
}

func TestDecodeCommandInspectionIncludesUserConversationActiveMessageSeq(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeTouchUserConversationActiveAtCommand([]metadb.UserConversationActivePatch{{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    100,
		MessageSeq:  11,
	}}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "touch_user_conversation_active_at",
		Payload: map[string]any{
			"command": "touch_user_conversation_active_at",
			"patches": []map[string]any{{
				"uid":          "u1",
				"channel_id":   "g1",
				"channel_type": int64(2),
				"active_at":    int64(100),
				"message_seq":  uint64(11),
			}},
		},
	}, got)
}

func TestDecodeCommandInspectionExpandsApplyDeltaOriginalCommand(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeApplyDeltaCommand(
		11,
		99,
		7,
		EncodeUpsertChannelCommand(metadb.Channel{ChannelID: "room-1", ChannelType: 2, Ban: 1}),
	))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "apply_delta",
		Payload: map[string]any{
			"command":        "apply_delta",
			"source_slot_id": multiraft.SlotID(11),
			"source_index":   uint64(99),
			"hash_slot":      uint16(7),
			"original": map[string]any{
				"command":      "upsert_channel",
				"channel_id":   "room-1",
				"channel_type": int64(2),
				"ban":          int64(1),
			},
		},
	}, got)
}
