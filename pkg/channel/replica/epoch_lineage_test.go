package replica

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestEpochLineageDecisionTruthTable(t *testing.T) {
	epochHistory := []channel.EpochPoint{
		{Epoch: 3, StartOffset: 0},
		{Epoch: 4, StartOffset: 10},
		{Epoch: 5, StartOffset: 20},
	}

	tests := []struct {
		name           string
		history        []channel.EpochPoint
		logStartOffset uint64
		currentHW      uint64
		leaderLEO      uint64
		remoteOffset   uint64
		offsetEpoch    uint64
		wantMatch      uint64
		wantTruncate   *uint64
		wantErr        error
	}{
		{
			name:         "known epoch keeps remote offset inside epoch range",
			history:      epochHistory,
			currentHW:    18,
			leaderLEO:    25,
			remoteOffset: 7,
			offsetEpoch:  3,
			wantMatch:    7,
		},
		{
			name:         "known epoch caps at next epoch start",
			history:      epochHistory,
			currentHW:    18,
			leaderLEO:    25,
			remoteOffset: 15,
			offsetEpoch:  3,
			wantMatch:    10,
			wantTruncate: uint64Ptr(10),
		},
		{
			name:         "future epoch returns stale meta before offset capping",
			history:      epochHistory,
			currentHW:    18,
			leaderLEO:    25,
			remoteOffset: 30,
			offsetEpoch:  9,
			wantErr:      channel.ErrStaleMeta,
		},
		{
			name: "unknown non-future epoch caps to current high watermark",
			history: []channel.EpochPoint{
				{Epoch: 3, StartOffset: 0},
				{Epoch: 5, StartOffset: 20},
			},
			currentHW:    18,
			leaderLEO:    25,
			remoteOffset: 30,
			offsetEpoch:  4,
			wantMatch:    18,
			wantTruncate: uint64Ptr(18),
		},
		{
			name:         "zero epoch with empty history caps to leader LEO",
			currentHW:    18,
			leaderLEO:    25,
			remoteOffset: 30,
			offsetEpoch:  0,
			wantMatch:    25,
			wantTruncate: uint64Ptr(25),
		},
		{
			name:         "zero epoch with non-empty history caps to current high watermark",
			history:      epochHistory,
			currentHW:    18,
			leaderLEO:    25,
			remoteOffset: 30,
			offsetEpoch:  0,
			wantMatch:    18,
			wantTruncate: uint64Ptr(18),
		},
		{
			name:           "offset below log start requires snapshot",
			history:        epochHistory,
			logStartOffset: 10,
			currentHW:      18,
			leaderLEO:      25,
			remoteOffset:   9,
			offsetEpoch:    3,
			wantErr:        channel.ErrSnapshotRequired,
		},
		{
			name: "known epoch cap below log start requires snapshot",
			history: []channel.EpochPoint{
				{Epoch: 3, StartOffset: 0},
				{Epoch: 4, StartOffset: 6},
			},
			logStartOffset: 10,
			currentHW:      18,
			leaderLEO:      25,
			remoteOffset:   12,
			offsetEpoch:    3,
			wantErr:        channel.ErrSnapshotRequired,
		},
		{
			name: "safe match caps at leader LEO",
			history: []channel.EpochPoint{
				{Epoch: 3, StartOffset: 0},
			},
			currentHW:    30,
			leaderLEO:    25,
			remoteOffset: 40,
			offsetEpoch:  3,
			wantMatch:    25,
			wantTruncate: uint64Ptr(25),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideLineage(tt.history, tt.logStartOffset, tt.currentHW, tt.leaderLEO, tt.remoteOffset, tt.offsetEpoch)
			if tt.wantErr != nil {
				require.ErrorIs(t, got.err, tt.wantErr)
				return
			}
			require.NoError(t, got.err)
			require.Equal(t, tt.wantMatch, got.matchOffset)
			require.Equal(t, tt.wantTruncate, got.truncateTo)
		})
	}
}

func TestEpochLineageProofMatch(t *testing.T) {
	epochHistory := []channel.EpochPoint{
		{Epoch: 3, StartOffset: 0},
		{Epoch: 4, StartOffset: 10},
		{Epoch: 5, StartOffset: 20},
	}

	tests := []struct {
		name        string
		history     []channel.EpochPoint
		currentHW   uint64
		leaderLEO   uint64
		logEnd      uint64
		offsetEpoch uint64
		wantMatch   uint64
		wantErr     error
	}{
		{
			name:        "proof for known epoch caps to next epoch start",
			history:     epochHistory,
			currentHW:   18,
			leaderLEO:   25,
			logEnd:      15,
			offsetEpoch: 3,
			wantMatch:   10,
		},
		{
			name:        "proof with zero epoch and non-empty history caps to current high watermark",
			history:     epochHistory,
			currentHW:   18,
			leaderLEO:   25,
			logEnd:      30,
			offsetEpoch: 0,
			wantMatch:   18,
		},
		{
			name:        "proof with future epoch returns stale meta",
			history:     epochHistory,
			currentHW:   18,
			leaderLEO:   25,
			logEnd:      30,
			offsetEpoch: 9,
			wantErr:     channel.ErrStaleMeta,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := matchOffsetForProof(tt.history, tt.currentHW, tt.leaderLEO, tt.logEnd, tt.offsetEpoch)
			if tt.wantErr != nil {
				require.True(t, errors.Is(err, tt.wantErr), "got err %v", err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMatch, got)
		})
	}
}

func uint64Ptr(v uint64) *uint64 {
	return &v
}
