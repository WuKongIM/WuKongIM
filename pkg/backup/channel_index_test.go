package backup_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestChannelIndexStrictRoundTrip(t *testing.T) {
	body, err := backup.MarshalChannelIndex(7, []backup.ChannelBoundary{
		{ChannelID: "beta", ChannelType: 2, Epoch: 3, LogStartOffset: 1, HW: 9},
		{ChannelID: "alpha", ChannelType: 1, Epoch: 2, HW: 4},
	})
	require.NoError(t, err)
	hashSlot, boundaries, err := backup.LoadChannelIndex(body)
	require.NoError(t, err)
	require.Equal(t, uint16(7), hashSlot)
	require.Equal(t, []backup.ChannelBoundary{
		{ChannelID: "alpha", ChannelType: 1, Epoch: 2, HW: 4},
		{ChannelID: "beta", ChannelType: 2, Epoch: 3, LogStartOffset: 1, HW: 9},
	}, boundaries)

	body[len(body)-1] ^= 1
	_, _, err = backup.LoadChannelIndex(body)
	require.ErrorIs(t, err, backup.ErrObjectCorrupt)
}
