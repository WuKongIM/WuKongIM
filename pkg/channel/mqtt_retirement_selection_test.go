package channel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTRetirementSelectionContractKeepsCaptureFloorAndClosedOutcomes(t *testing.T) {
	repair, p := repairPlanContractFixture()
	q := MQTTReplayRetirementScan{Generation: repair.Generation, CapturedAnchor: 7, Through: 5, Limit: 1}
	require.True(t, q.Valid())
	continuation := MQTTReplayRetirementSelection{Captured: p.Target, BeforeAnchor: 7}
	require.True(t, continuation.ValidFor(q))
	q.BeforeAnchor = 7
	require.False(t, continuation.ValidFor(q), "a continuation must move backward")
	selected := MQTTReplayRetirementSelection{Captured: p.Target, Candidate: p.Next, HasCandidate: true, Done: true}
	require.True(t, selected.ValidFor(q))
	exhausted := MQTTReplayRetirementSelection{Captured: p.Target, Done: true}
	require.True(t, exhausted.ValidFor(q))
	for _, mutate := range []func(*MQTTReplayRetirementSelection){
		func(p *MQTTReplayRetirementSelection) { p.Done = false },
		func(p *MQTTReplayRetirementSelection) { p.HasCandidate = false },
		func(p *MQTTReplayRetirementSelection) { p.BeforeAnchor = 5 },
		func(p *MQTTReplayRetirementSelection) { p.Candidate = p.Captured },
		func(p *MQTTReplayRetirementSelection) { p.Candidate.Anchor.SourceCommand[0]++ },
		func(p *MQTTReplayRetirementSelection) {
			p.Candidate.Anchor.TotalStoredBytes = p.Captured.Anchor.TotalStoredBytes + 1
		},
		func(p *MQTTReplayRetirementSelection) { p.Captured.Manifest.LastOffset++ },
		func(p *MQTTReplayRetirementSelection) { p.Captured.Manifest.Version = 6 },
		func(p *MQTTReplayRetirementSelection) { p.Captured.Anchor.Through = 4 },
	} {
		bad := selected
		mutate(&bad)
		require.False(t, bad.ValidFor(q))
	}
	q.BeforeAnchor, q.Through = 0, 6
	latest := MQTTReplayRetirementSelection{Captured: p.Target, Candidate: p.Target, HasCandidate: true, Done: true}
	require.True(t, latest.ValidFor(q))
	latest.Candidate.Manifest.CommandID[0]++
	require.False(t, latest.ValidFor(q), "one captured position cannot identify two proposals")
	for _, mutate := range []func(*MQTTReplayRetirementScan){
		func(q *MQTTReplayRetirementScan) { q.Generation = "foreign" },
		func(q *MQTTReplayRetirementScan) { q.CapturedAnchor = 0 },
		func(q *MQTTReplayRetirementScan) { q.BeforeAnchor = 8 },
		func(q *MQTTReplayRetirementScan) { q.Through = 7 },
		func(q *MQTTReplayRetirementScan) { q.Limit = 0 },
		func(q *MQTTReplayRetirementScan) { q.Limit = 65 },
	} {
		bad := q
		mutate(&bad)
		require.False(t, bad.Valid())
		require.False(t, selected.ValidFor(bad))
	}
}
