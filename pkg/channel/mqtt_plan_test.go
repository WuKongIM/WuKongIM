package channel

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTPlanDerivesOnlyAcceptedBoundedRanges(t *testing.T) {
	gen := quorumlog.MQTTSourceGeneration(CommandID{1})
	q := MQTTReplayPlanRequest{ChannelID: ChannelID{ID: "plan", Type: 2}, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, Generation: gen}
	p := MQTTReplayPlan{Source: MQTTSourceSnapshot{Generation: gen, StartAfter: 4, CommittedThrough: 8}}
	require.True(t, q.Valid())
	require.True(t, p.ValidFor(q))
	r, more, err := p.NextRange(256, 1<<20)
	require.NoError(t, err)
	require.True(t, more)
	require.Equal(t, MQTTReplayRange{Generation: gen, From: 5, Through: 8, Limit: 256, MaxBytes: 1 << 20}, r)
	p.HasAnchor = true
	p.Anchor = MQTTReplayAnchorProof{Anchor: quorumlog.MQTTReplayAnchor{SourceCommand: CommandID{1}, StartAfter: 4, Through: 7, TotalBytes: 10, TotalStoredBytes: 100, Digest: EntryDigest{2}},
		Manifest: ProposalManifest{Version: 5, ChannelEpoch: 2, LeaderTerm: 3, FenceVersion: 4, CommandID: CommandID{3}, BaseOffset: 7, LastOffset: 8, PreviousIndex: 7, PreviousTerm: 3, PreviousDigest: EntryDigest{4}, Digest: EntryDigest{5}}}
	require.True(t, p.ValidFor(q))
	r, more, err = p.NextRange(1, 1024)
	require.NoError(t, err)
	require.False(t, more)
	require.Zero(t, r)
	p.Source.CommittedThrough = 9
	r, more, err = p.NextRange(1, 1024)
	require.NoError(t, err)
	require.True(t, more)
	require.Equal(t, uint64(8), r.From, "include the old control before new business content")
	require.Equal(t, uint64(9), r.Through)
	for _, bad := range []func(*MQTTReplayPlan){
		func(p *MQTTReplayPlan) { p.Source.Generation = "foreign" }, func(p *MQTTReplayPlan) { p.Source.CommittedThrough = 7 },
		func(p *MQTTReplayPlan) { p.Anchor.Anchor.StartAfter++ }, func(p *MQTTReplayPlan) { p.Anchor.Anchor.SourceCommand[0]++ },
		func(p *MQTTReplayPlan) { p.HasAnchor = false }, func(p *MQTTReplayPlan) { p.Anchor.Manifest.Version = 3 },
		func(p *MQTTReplayPlan) { p.Anchor.Manifest.LastOffset = 9 },
	} {
		invalid := p
		bad(&invalid)
		require.False(t, invalid.ValidFor(q))
		_, _, err := invalid.NextRange(1, 1024)
		require.Error(t, err)
	}
	for _, bounds := range [][2]int{{0, 1}, {257, 1}, {1, 0}, {1, (16 << 20) + 1}} {
		_, _, err := p.NextRange(bounds[0], bounds[1])
		require.Error(t, err)
	}
	q.ExpectedRouteGeneration = 0
	require.False(t, q.Valid())
	require.False(t, p.ValidFor(q))
	// The final representable position is legal without incrementing past it.
	p = MQTTReplayPlan{Source: MQTTSourceSnapshot{Generation: gen, StartAfter: ^uint64(0) - 1, CommittedThrough: ^uint64(0)}}
	r, more, err = p.NextRange(1, 1)
	require.NoError(t, err)
	require.True(t, more)
	require.Equal(t, ^uint64(0), r.From)
}
