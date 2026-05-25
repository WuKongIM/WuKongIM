package machine

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestCheckInvariantsAllowsValidOrdering(t *testing.T) {
	state := &ChannelState{CheckpointHW: 2, HW: 3, LEO: 4}
	require.NoError(t, state.CheckInvariants())
}

func TestCheckInvariantsRejectsCheckpointAboveHW(t *testing.T) {
	state := &ChannelState{CheckpointHW: 4, HW: 3, LEO: 4}
	require.ErrorIs(t, state.CheckInvariants(), ch.ErrInvalidConfig)
}

func TestCheckInvariantsRejectsHWAboveLEO(t *testing.T) {
	state := &ChannelState{CheckpointHW: 2, HW: 5, LEO: 4}
	require.ErrorIs(t, state.CheckInvariants(), ch.ErrInvalidConfig)
}
