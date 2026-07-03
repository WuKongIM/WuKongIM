package raft

import (
	"testing"

	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/plane"
	"github.com/stretchr/testify/require"
)

func TestDecodeCommandInspectionSummarizesAddSlot(t *testing.T) {
	data, err := encodeCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindAddSlot,
		AddSlot: &slotcontroller.AddSlotRequest{
			NewSlotID:       9,
			Peers:           []uint64{1, 2, 3},
			PreferredLeader: 2,
		},
	})
	require.NoError(t, err)

	got, err := DecodeCommandInspection(data)
	require.NoError(t, err)
	require.Equal(t, CommandInspection{
		Type: "add_slot",
		Payload: map[string]any{
			"command":          "add_slot",
			"new_slot_id":      uint64(9),
			"peers":            []uint64{1, 2, 3},
			"preferred_leader": uint64(2),
		},
	}, got)
}
