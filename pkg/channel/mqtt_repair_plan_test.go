package channel

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func repairPlanContractFixture() (MQTTReplayRepairScan, MQTTReplayRepairPlan) {
	gen := quorumlog.MQTTSourceGeneration(CommandID{1})
	proof := func(position, through, bytes, stored uint64) MQTTReplayAnchorProof {
		return MQTTReplayAnchorProof{Anchor: quorumlog.MQTTReplayAnchor{SourceCommand: CommandID{1}, Through: through, TotalBytes: bytes, TotalStoredBytes: stored, Digest: [32]byte{byte(through)}}, Manifest: ProposalManifest{Version: 5, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: CommandID{byte(position)}, BaseOffset: position - 1, LastOffset: position, PreviousIndex: position - 1, PreviousTerm: 1, PreviousDigest: EntryDigest{1}, Digest: EntryDigest{2}}}
	}
	return MQTTReplayRepairScan{Generation: gen, TargetAnchor: 7, Limit: 1}, MQTTReplayRepairPlan{Current: MQTTReplayPrefix{Generation: gen}, Target: proof(7, 6, 600, 1000), Next: proof(5, 4, 300, 600), HasNext: true}
}

func TestMQTTRepairPlanContractBoundsRangeAndContinuation(t *testing.T) {
	q, p := repairPlanContractFixture()
	require.True(t, q.Valid())
	require.True(t, p.ValidFor(q))
	r, found, err := p.NextRange()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, MQTTReplayRange{Generation: q.Generation, From: 1, Through: 4, Limit: 4, MaxBytes: 600}, r)
	p.Current = p.Next.Prefix()
	p.Next = MQTTReplayAnchorProof{}
	p.HasNext = false
	p.ScanAfter = 5
	require.True(t, p.ValidFor(q))
	r, found, err = p.NextRange()
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, r)
	q.AfterAnchor = 5
	require.False(t, p.ValidFor(q), "exhausted scan must advance its cursor")
	p.Next = p.Target
	p.HasNext = true
	require.True(t, p.ValidFor(q))
	r, found, err = p.NextRange()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, MQTTReplayRange{Generation: q.Generation, From: 5, Through: 6, Limit: 2, MaxBytes: 400}, r)
	p.Current = p.Target.Prefix()
	p.Next = MQTTReplayAnchorProof{}
	p.HasNext = false
	p.Complete = true
	p.ScanAfter = 0
	require.True(t, p.ValidFor(q))
	_, found, err = p.NextRange()
	require.NoError(t, err)
	require.False(t, found)
}

func TestMQTTRepairPlanContractRejectsMalformedState(t *testing.T) {
	for _, change := range []string{"zero_target", "after_target", "zero_limit", "big_limit", "foreign_generation", "current_generation", "current_empty_digest", "target_position", "target_format", "next_position", "next_generation", "next_counter", "big_bytes", "big_rows", "complete_with_next", "false_complete", "stalled_scan", "scan_past_target"} {
		t.Run(change, func(t *testing.T) {
			q, p := repairPlanContractFixture()
			switch change {
			case "zero_target":
				q.TargetAnchor = 0
			case "after_target":
				q.AfterAnchor = 8
			case "zero_limit":
				q.Limit = 0
			case "big_limit":
				q.Limit = 65
			case "foreign_generation":
				q.Generation = "not-canonical"
			case "current_generation":
				p.Current.Generation = "foreign"
			case "current_empty_digest":
				p.Current.Digest[0] = 1
			case "target_position":
				p.Target.Manifest.LastOffset++
			case "target_format":
				p.Target.Manifest.Version = 1
			case "next_position":
				p.Next.Manifest.LastOffset = 8
			case "next_generation":
				p.Next.Anchor.SourceCommand = CommandID{8}
			case "next_counter":
				p.Next.Anchor.TotalBytes = 2000
			case "big_bytes":
				p.Next.Anchor.TotalStoredBytes = 16<<20 + 1
				p.Target.Anchor.TotalStoredBytes = 16<<20 + 2
			case "big_rows":
				p.Next.Anchor.Through = 257
				p.Next.Manifest.BaseOffset = 257
				p.Next.Manifest.LastOffset = 258
				p.Next.Manifest.PreviousIndex = 257
				p.Target = p.Next
				q.TargetAnchor = 258
			case "complete_with_next":
				p.Complete = true
			case "false_complete":
				p.Complete = true
				p.HasNext = false
				p.Next = MQTTReplayAnchorProof{}
			case "stalled_scan":
				p.HasNext = false
				p.Next = MQTTReplayAnchorProof{}
			case "scan_past_target":
				p.HasNext = false
				p.Next = MQTTReplayAnchorProof{}
				p.ScanAfter = 7
			}
			require.False(t, p.ValidFor(q))
		})
	}
}
