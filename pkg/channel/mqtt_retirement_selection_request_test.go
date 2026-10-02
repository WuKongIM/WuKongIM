package channel

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMQTTRetirementSelectionRequestBindsCaptureAndAuthority(t *testing.T) {
	r := retirementRequestFixture()
	q := MQTTReplayRetirementSelectionRequest{Source: MQTTReplayPlanRequest{ChannelID: r.Meta.ID, ExpectedChannelEpoch: r.Meta.Epoch, ExpectedLeaderEpoch: r.Meta.LeaderEpoch, ExpectedRouteGeneration: r.Meta.RouteGeneration, Generation: r.Captured.Prefix().Generation}, Captured: r.Captured, Through: 2, Limit: 1}
	p := MQTTReplayRetirementSelection{Captured: q.Captured, Candidate: q.Captured, HasCandidate: true, Done: true}
	require.True(t, q.Valid())
	require.True(t, q.Accepts(p))
	for _, mutate := range []func(*MQTTReplayRetirementSelectionRequest){
		func(q *MQTTReplayRetirementSelectionRequest) { q.Source.Generation = "invalid" },
		func(q *MQTTReplayRetirementSelectionRequest) { q.Source.ExpectedLeaderEpoch = 0 },
		func(q *MQTTReplayRetirementSelectionRequest) { q.Through++ },
		func(q *MQTTReplayRetirementSelectionRequest) { q.Limit = 65 },
		func(q *MQTTReplayRetirementSelectionRequest) { q.Captured.Manifest.LeaderTerm = 3 },
	} {
		bad := q
		mutate(&bad)
		require.False(t, bad.Valid())
		require.False(t, bad.Accepts(p))
	}
	p.Captured.Manifest.Digest[0]++
	require.False(t, q.Accepts(p))
}
