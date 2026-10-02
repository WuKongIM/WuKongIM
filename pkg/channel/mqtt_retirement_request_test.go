package channel

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func retirementRequestFixture() MQTTReplayRetirementRequest {
	a := MQTTReplayAnchorProof{Anchor: quorumlog.MQTTReplayAnchor{SourceCommand: CommandID{1}, Through: 2, TotalBytes: 8, TotalStoredBytes: 100, Digest: EntryDigest{2}}, Manifest: ProposalManifest{Version: 5, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: CommandID{3}, BaseOffset: 2, LastOffset: 3, PreviousIndex: 2, PreviousTerm: 1, PreviousDigest: EntryDigest{2}, Digest: EntryDigest{3}}}
	return MQTTReplayRetirementRequest{Meta: Meta{ID: ChannelID{ID: "retirement", Type: 1}, Epoch: 1, LeaderEpoch: 2, RouteGeneration: 1, Leader: 1, Replicas: []NodeID{1, 2, 3, 4}, ISR: []NodeID{1, 2, 3}, MinISR: 2, Status: StatusActive}, Captured: a, Candidate: a, ConsumerThrough: 2, MessageID: 4, ServerTimestampMS: 4000}
}

func TestMQTTRetirementRequestRequiresWholeHistoricalSelectionAndPlacement(t *testing.T) {
	q := retirementRequestFixture()
	require.True(t, q.Valid())
	r, err := q.Retirement()
	require.NoError(t, err)
	require.Equal(t, q.Candidate.Anchor, r.Anchor)
	require.Equal(t, q.Candidate.Manifest.LastOffset, r.AnchorPosition)
	owned := q.Clone()
	owned.Meta.ISR[0] = 99
	owned.Meta.Replicas[0] = 99
	require.Equal(t, NodeID(1), q.Meta.ISR[0])
	require.Equal(t, NodeID(1), q.Meta.Replicas[0])
	for _, mutate := range []func(*MQTTReplayRetirementRequest){
		func(q *MQTTReplayRetirementRequest) { q.ConsumerThrough-- },
		func(q *MQTTReplayRetirementRequest) { q.ConsumerThrough++ },
		func(q *MQTTReplayRetirementRequest) { q.MessageID = 0 },
		func(q *MQTTReplayRetirementRequest) { q.ServerTimestampMS = 0 },
		func(q *MQTTReplayRetirementRequest) { q.Candidate.Anchor.Digest[0]++ },
		func(q *MQTTReplayRetirementRequest) { q.Captured.Manifest.LeaderTerm = 3 },
		func(q *MQTTReplayRetirementRequest) { q.Meta.Replicas = []NodeID{1, 1, 3} },
		func(q *MQTTReplayRetirementRequest) { q.Meta.ISR = []NodeID{1, 1, 3} },
		func(q *MQTTReplayRetirementRequest) { q.Meta.ISR = []NodeID{1, 2, 5} },
		func(q *MQTTReplayRetirementRequest) { q.Meta.Leader = 4 },
		func(q *MQTTReplayRetirementRequest) { q.Meta.MinISR = 1 },
		func(q *MQTTReplayRetirementRequest) { q.Meta.Key = "foreign" },
		func(q *MQTTReplayRetirementRequest) { q.Meta.Status = StatusDeleted },
	} {
		bad := q.Clone()
		mutate(&bad)
		require.False(t, bad.Valid())
		_, err = q.Retirement()
		require.NoError(t, err)
		_, err = bad.Retirement()
		require.Error(t, err)
	}
}

func TestMQTTRetirementReplyCannotChangeEqualReferenceOrRegress(t *testing.T) {
	q := retirementRequestFixture()
	r, err := q.Retirement()
	require.NoError(t, err)
	m := q.Candidate.Manifest
	m.Version = 6
	m.BaseOffset, m.LastOffset, m.PreviousIndex = 3, 4, 3
	p := MQTTReplayRetirementProof{Retirement: r, Manifest: m}
	require.True(t, q.AcceptsProof(p))
	for _, mutate := range []func(*MQTTReplayRetirementProof){
		func(p *MQTTReplayRetirementProof) { p.Retirement.AnchorDigest[0]++ },
		func(p *MQTTReplayRetirementProof) { p.Retirement.Anchor.SourceCommand[0]++ },
		func(p *MQTTReplayRetirementProof) { p.Manifest.LeaderTerm = 3 },
		func(p *MQTTReplayRetirementProof) { p.Retirement.Anchor.Through-- },
		func(p *MQTTReplayRetirementProof) { p.Manifest.Version = 5 },
	} {
		bad := p
		mutate(&bad)
		require.False(t, q.AcceptsProof(bad))
	}
	p.Retirement.Anchor.Through = 4
	p.Retirement.Anchor.TotalStoredBytes = 200
	p.Retirement.AnchorPosition = 5
	p.Manifest.BaseOffset, p.Manifest.LastOffset, p.Manifest.PreviousIndex = 5, 6, 5
	require.True(t, q.AcceptsProof(p), "a newer independently committed decision covers delayed old intent")
}
