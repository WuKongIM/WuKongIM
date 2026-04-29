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
