package replica

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestQuorumProgressCandidate(t *testing.T) {
	tests := []struct {
		name          string
		isr           []channel.NodeID
		progress      map[channel.NodeID]uint64
		minISR        int
		hw            uint64
		leo           uint64
		wantCandidate uint64
		wantOK        bool
		wantErr       error
	}{
		{
			name: "MinISR 1 uses highest ISR progress",
			isr:  []channel.NodeID{1, 2, 3},
			progress: map[channel.NodeID]uint64{
				1: 4,
				2: 9,
				3: 7,
			},
			minISR:        1,
			hw:            3,
			leo:           9,
			wantCandidate: 9,
			wantOK:        true,
		},
		{
			name: "MinISR 2 uses second highest ISR progress",
			isr:  []channel.NodeID{1, 2, 3},
			progress: map[channel.NodeID]uint64{
				1: 4,
				2: 9,
				3: 7,
			},
			minISR:        2,
			hw:            3,
			leo:           9,
			wantCandidate: 7,
			wantOK:        true,
		},
		{
			name: "MinISR 3 uses lowest ISR progress",
			isr:  []channel.NodeID{1, 2, 3},
			progress: map[channel.NodeID]uint64{
				1: 4,
				2: 9,
				3: 7,
			},
			minISR:        3,
			hw:            3,
			leo:           9,
			wantCandidate: 4,
			wantOK:        true,
		},
		{
			name: "missing progress entries default to HW",
			isr:  []channel.NodeID{1, 2, 3},
			progress: map[channel.NodeID]uint64{
				1: 8,
				2: 6,
			},
			minISR:  3,
			hw:      5,
			leo:     8,
			wantOK:  false,
			wantErr: nil,
		},
		{
			name: "candidate not beyond HW returns no advancement",
			isr:  []channel.NodeID{1, 2, 3},
			progress: map[channel.NodeID]uint64{
				1: 5,
				2: 5,
				3: 7,
			},
			minISR:  2,
			hw:      5,
			leo:     7,
			wantOK:  false,
			wantErr: nil,
		},
		{
			name: "invalid MinISR zero returns no advancement",
			isr:  []channel.NodeID{1, 2, 3},
			progress: map[channel.NodeID]uint64{
				1: 5,
				2: 5,
				3: 5,
			},
			minISR:  0,
			hw:      1,
			leo:     5,
			wantOK:  false,
			wantErr: nil,
		},
		{
			name: "invalid MinISR above ISR returns no advancement",
			isr:  []channel.NodeID{1, 2, 3},
			progress: map[channel.NodeID]uint64{
				1: 5,
				2: 5,
				3: 5,
			},
			minISR:  4,
			hw:      1,
			leo:     5,
			wantOK:  false,
			wantErr: nil,
		},
		{
			name: "invalid MinISR negative returns no advancement",
			isr:  []channel.NodeID{1, 2, 3},
			progress: map[channel.NodeID]uint64{
				1: 5,
				2: 5,
				3: 5,
			},
			minISR:  -1,
			hw:      1,
			leo:     5,
			wantOK:  false,
			wantErr: nil,
		},
		{
			name: "candidate beyond LEO returns corrupt state",
			isr:  []channel.NodeID{1, 2, 3},
			progress: map[channel.NodeID]uint64{
				1: 9,
				2: 8,
				3: 7,
			},
			minISR:  2,
			hw:      6,
			leo:     7,
			wantOK:  false,
			wantErr: channel.ErrCorruptState,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCandidate, gotOK, err := quorumProgressCandidate(tt.isr, tt.progress, tt.minISR, tt.hw, tt.leo)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.wantOK, gotOK)
			require.Equal(t, tt.wantCandidate, gotCandidate)
		})
	}
}

func TestReconcileQuorumCandidateDefaultsMissingISRToHW(t *testing.T) {
	gotCandidate, gotOK, err := reconcileQuorumCandidate(
		[]channel.NodeID{1, 2, 3},
		map[channel.NodeID]uint64{1: 8, 2: 6},
		3,
		5,
		8,
	)

	require.NoError(t, err)
	require.True(t, gotOK)
	require.Equal(t, uint64(5), gotCandidate)
}
