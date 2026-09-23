//go:build integration

package replication

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

// Consumer admission is controlled here; the real sequencer, wire codec and
// replica stores must preserve the explicit decision and reject business retries.
func verifyMQTTRetirementQuorum(t *testing.T, ctx context.Context, a Authority, runtimes map[ch.NodeID]*Runtime, factories map[ch.NodeID]*channelstore.MessageDBFactory, router *runtimeTestRouter) func(ch.NodeID) bool {
	t.Helper()
	st, err := factories[a.Leader].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	anchor, found, err := st.(channelstore.MQTTReplayAnchorReader).LoadMQTTReplayAnchor(ctx, 3)
	require.NoError(t, err)
	require.True(t, found)
	scan := ch.MQTTReplayRetirementScan{Generation: anchor.Prefix().Generation, CapturedAnchor: anchor.Manifest.LastOffset, Through: anchor.Anchor.Through, Limit: 1}
	selection, err := st.(channelstore.MQTTReplayRetirementSelector).SelectMQTTReplayRetirementAnchor(ctx, scan)
	require.NoError(t, err)
	require.True(t, selection.ValidFor(scan))
	require.True(t, selection.HasCandidate)
	require.Equal(t, anchor, selection.Candidate)
	require.NoError(t, st.Close())
	r := quorumlog.MQTTReplayRetirement{Anchor: selection.Candidate.Anchor, AnchorPosition: selection.Candidate.Manifest.LastOffset, AnchorDigest: selection.Candidate.Manifest.Digest}
	body, err := r.MarshalBinary()
	require.NoError(t, err)
	q := ch.MQTTReplayRetirementRequest{Meta: ch.Meta{Key: a.Key, ID: a.ChannelID, Epoch: a.ID.ChannelEpoch, LeaderEpoch: a.ID.LeaderTerm, RouteGeneration: a.ID.FenceVersion, Leader: a.Leader, Replicas: append(append([]ch.NodeID(nil), a.Voters...), a.Learners...), ISR: append([]ch.NodeID(nil), a.Voters...), MinISR: a.WriteQuorum, Status: ch.StatusActive}, Captured: selection.Captured, Candidate: selection.Candidate, ConsumerThrough: scan.Through, MessageID: 2900, ServerTimestampMS: 29000}
	committer, ok := runtimes[a.Leader].Log().(ch.MQTTReplayRetirementCommitter)
	require.True(t, ok)
	for _, mode := range []string{"floor", "proof", "authority", "members", "budget", "cancel"} {
		bad := q.Clone()
		call := ctx
		owner := runtimes[a.Leader].Log().(*quorumLog)
		budget := owner.cfg.MaxProposalBytes
		switch mode {
		case "floor":
			bad.ConsumerThrough--
		case "proof":
			bad.Captured.Anchor.Digest[0]++
			bad.Candidate = bad.Captured
		case "authority":
			bad.Meta.RouteGeneration++
		case "members":
			bad.Meta.ISR = []ch.NodeID{1, 2}
			bad.Meta.MinISR = 2
		case "budget":
			owner.cfg.MaxProposalBytes = 100
		case "cancel":
			var cancel context.CancelFunc
			call, cancel = context.WithCancel(ctx)
			cancel()
		}
		p, e := committer.CommitMQTTReplayRetirement(call, bad)
		owner.cfg.MaxProposalBytes = budget
		require.Error(t, e, mode)
		require.Zero(t, p, mode)
	}
	// The first uncertain attempt retains its exact record. Restored voters must
	// finish it even when the observer supplies different server identities.
	for _, node := range a.Voters {
		if node != a.Leader {
			router.register(node, nil)
		}
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	failed, err := committer.CommitMQTTReplayRetirement(short, q)
	cancel()
	require.Error(t, err)
	require.Zero(t, failed)
	_, err = runtimes[a.Leader].Log().Commit(ctx, Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{80}, Records: []ch.Record{{ID: 8000, Epoch: 1, ServerTimestampMS: 8000, Payload: []byte("pending"), SizeBytes: 7}}})
	require.ErrorIs(t, err, ch.ErrBackpressured)
	for _, node := range a.Voters {
		if node != a.Leader {
			router.register(node, runtimes[node].ExchangeServer())
		}
	}
	retry := q.Clone()
	retry.MessageID++
	retry.ServerTimestampMS++
	proof, err := committer.CommitMQTTReplayRetirement(ctx, retry)
	require.NoError(t, err)
	require.True(t, q.AcceptsProof(proof))
	var wg sync.WaitGroup
	var proofs [8]ch.MQTTReplayRetirementProof
	var errs [8]error
	for i := range proofs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			again := q.Clone()
			again.MessageID += uint64(i + 10)
			proofs[i], errs[i] = committer.CommitMQTTReplayRetirement(ctx, again)
		}(i)
	}
	wg.Wait()
	for i := range proofs {
		require.NoError(t, errs[i])
		require.Equal(t, proof, proofs[i])
	}
	proposal := Proposal{Key: a.Key, Expected: a.ID, CommandID: proof.Manifest.CommandID, MQTTReplayRetirement: true, Records: []ch.Record{{ID: 2900, Epoch: 1, ServerTimestampMS: 29000, SyncOnce: true, Payload: body, SizeBytes: len(body)}}}
	receipt, err := runtimes[a.Leader].Log().Commit(ctx, proposal)
	require.NoError(t, err, "typed pending retry must retain the original row identity")
	require.Equal(t, proof.Manifest.LastOffset, receipt.Last)
	wrong := proposal
	wrong.MQTTReplayRetirement = false
	_, err = runtimes[a.Leader].Log().Commit(ctx, wrong)
	require.ErrorIs(t, err, ch.ErrLogConflict)
	for _, activation := range []bool{false, true} {
		wrong = proposal
		wrong.MQTTSourceActivation, wrong.MQTTReplayAnchor = activation, !activation
		_, err = runtimes[a.Leader].Log().Commit(ctx, wrong)
		require.ErrorIs(t, err, ch.ErrInvalidConfig)
	}
	st, err = factories[a.Leader].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	require.NoError(t, st.StoreCheckpoint(ctx, ch.Checkpoint{HW: receipt.HW}))
	require.NoError(t, st.Close())
	require.NoError(t, runtimes[a.Leader].Log().(CommittedReplicaRefresher).RequestCommittedReplicaRefresh(ctx, a))
	check := func(node ch.NodeID) bool {
		st, err := factories[node].ChannelStore(a.Key, a.ChannelID)
		if err != nil {
			return false
		}
		defer st.Close()
		selected, err := st.(channelstore.MQTTReplayRetirementSelector).SelectMQTTReplayRetirementAnchor(ctx, scan)
		if err != nil || !selected.ValidFor(scan) || selected.Candidate != anchor {
			return false
		}
		proof, found, err := st.(channelstore.MQTTReplayRetirementReader).LoadMQTTReplayRetirement(ctx, receipt.Last)
		if err != nil || !found || proof.Retirement != r || proof.Manifest.CommandID != proposal.CommandID || proof.Manifest.Version != quorumlog.MQTTReplayRetirementProposalManifestVersion {
			return false
		}
		retired, err := st.(channelstore.MQTTReplayRetirer).RetireMQTTReplay(ctx, scan.Generation, receipt.Last, 1)
		if err != nil || !retired.Done || retired.Deleted > 1 || retired.Retired != anchor.Prefix() {
			return false
		}
		ready, err := st.(channelstore.MQTTReplayReadinessReader).ReadMQTTReplayReadiness(ctx, receipt.HW)
		return err == nil && ready.Covered
	}
	for _, node := range []ch.NodeID{1, 2, 3, 4} {
		require.Eventually(t, func() bool { return check(node) }, 3*time.Second, time.Millisecond)
	}
	verifyMQTTRetirementAdvancement(t, ctx, a, q, runtimes, factories)
	t.Log("mqtt_retirement_evidence: voters=3 learner=1 real_disk=true wire_codec=true bounded_anchor_selection=true typed_admission=true uncertain_retry=true original_identity=true concurrent_dedup=true advancing_decision=true delayed_old_retry=true business_retry_rejected=true committed_journal=true retired_baseline=true bounded_cleanup=true readiness=true consumer_admission=controlled product_listener=false")
	return check
}

// The existing baseline remains at the first decision throughout this scenario:
// committing another retirement must not itself delete shared replay content.
func verifyMQTTRetirementAdvancement(t *testing.T, ctx context.Context, a Authority, old ch.MQTTReplayRetirementRequest, runtimes map[ch.NodeID]*Runtime, factories map[ch.NodeID]*channelstore.MessageDBFactory) {
	t.Helper()
	log := runtimes[a.Leader].Log()
	business, err := log.Commit(ctx, Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{81}, Records: []ch.Record{{ID: 8100, Epoch: a.ID.ChannelEpoch, ServerTimestampMS: 81000, Payload: []byte("later"), SizeBytes: 5}}})
	require.NoError(t, err)
	st, err := factories[a.Leader].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	defer st.Close()
	require.NoError(t, st.StoreCheckpoint(ctx, ch.Checkpoint{HW: business.HW}))
	page, err := st.(channelstore.MQTTReplayPreparer).PrepareMQTTReplay(ctx, ch.MQTTReplayRange{Generation: old.Captured.Prefix().Generation, From: old.Candidate.Anchor.Through + 1, Through: business.Last, Limit: 256, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Equal(t, business.Last, page.After.Through)
	anchor := quorumlog.MQTTReplayAnchor{SourceCommand: old.Captured.Anchor.SourceCommand, StartAfter: page.After.StartAfter, Through: page.After.Through, TotalBytes: page.After.TotalBytes, TotalStoredBytes: page.After.TotalStoredBytes, Digest: page.After.Digest}
	body, err := anchor.MarshalBinary()
	require.NoError(t, err)
	// Upstream copy permission, like the consumer floor, is controlled here.
	accepted, err := log.Commit(ctx, Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{82}, MQTTReplayAnchor: true, Records: []ch.Record{{ID: 8200, Epoch: a.ID.ChannelEpoch, ServerTimestampMS: 82000, SyncOnce: true, Payload: body, SizeBytes: len(body)}}})
	require.NoError(t, err)
	require.NoError(t, st.StoreCheckpoint(ctx, ch.Checkpoint{HW: accepted.HW}))
	proof, found, err := st.(channelstore.MQTTReplayAnchorReader).LoadMQTTReplayAnchor(ctx, accepted.Last)
	require.NoError(t, err)
	require.True(t, found)
	q := old.Clone()
	q.Captured, q.Candidate, q.ConsumerThrough = proof, proof, anchor.Through
	q.MessageID, q.ServerTimestampMS = 8300, 83000
	committer := log.(ch.MQTTReplayRetirementCommitter)
	newer, err := committer.CommitMQTTReplayRetirement(ctx, q)
	require.NoError(t, err)
	require.True(t, q.AcceptsProof(newer))
	require.Greater(t, newer.Retirement.Anchor.Through, old.Candidate.Anchor.Through)
	before, err := st.Load(ctx)
	require.NoError(t, err)
	delayed, err := committer.CommitMQTTReplayRetirement(ctx, old)
	require.NoError(t, err)
	require.Equal(t, newer, delayed)
	require.True(t, old.AcceptsProof(delayed))
	after, err := st.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "a delayed older selection cannot append another control")
	// Only explicit baseline application can remove the copied suffix.
	retained, err := st.(channelstore.MQTTReplayPreparer).PrepareMQTTReplay(ctx, ch.MQTTReplayRange{Generation: page.After.Generation, From: page.Before.Through + 1, Through: page.After.Through, Limit: 256, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Equal(t, page, retained)
	require.NoError(t, log.(CommittedReplicaRefresher).RequestCommittedReplicaRefresh(ctx, a))
	for _, node := range []ch.NodeID{1, 2, 3, 4} {
		require.Eventually(t, func() bool {
			local, e := factories[node].ChannelStore(a.Key, a.ChannelID)
			if e != nil {
				return false
			}
			defer local.Close()
			p, found, e := local.(channelstore.MQTTReplayLatestRetirementReader).LoadLatestMQTTReplayRetirement(ctx, page.After.Generation)
			return e == nil && found && p == newer
		}, 3*time.Second, time.Millisecond)
	}
}

func verifyMQTTRetirementTypedRestartRetry(t *testing.T, ctx context.Context, a Authority, runtimes map[ch.NodeID]*Runtime, factories map[ch.NodeID]*channelstore.MessageDBFactory) Authority {
	t.Helper()
	st, err := factories[a.Leader].ChannelStore(a.Key, a.ChannelID)
	require.NoError(t, err)
	defer st.Close()
	anchor, found, err := st.(channelstore.MQTTReplayAnchorReader).LoadMQTTReplayAnchor(ctx, 3)
	require.NoError(t, err)
	require.True(t, found)
	before, found, err := st.(channelstore.MQTTReplayLatestRetirementReader).LoadLatestMQTTReplayRetirement(ctx, anchor.Prefix().Generation)
	require.NoError(t, err)
	require.True(t, found)
	q := ch.MQTTReplayRetirementRequest{Meta: ch.Meta{Key: a.Key, ID: a.ChannelID, Epoch: a.ID.ChannelEpoch, LeaderEpoch: a.ID.LeaderTerm, RouteGeneration: a.ID.FenceVersion, Leader: a.Leader, Replicas: append(append([]ch.NodeID(nil), a.Voters...), a.Learners...), ISR: a.Voters, MinISR: a.WriteQuorum, Status: ch.StatusActive}, Captured: anchor, Candidate: anchor, ConsumerThrough: anchor.Anchor.Through, MessageID: 9900, ServerTimestampMS: 99000}
	after, err := runtimes[a.Leader].Log().(ch.MQTTReplayRetirementCommitter).CommitMQTTReplayRetirement(ctx, q)
	require.NoError(t, err)
	require.Equal(t, before, after)
	// A current write fence also blocks otherwise idempotent control admission.
	a.ID.FenceVersion++
	a.WriteFence = ch.WriteFence{Token: "retirement-transfer", Version: a.ID.FenceVersion, Reason: ch.WriteFenceReasonLeaderTransfer}
	_, err = runtimes[a.Leader].Log().Install(ctx, a)
	require.NoError(t, err)
	q.Meta.RouteGeneration, q.Meta.WriteFence = a.ID.FenceVersion, a.WriteFence
	after, err = runtimes[a.Leader].Log().(ch.MQTTReplayRetirementCommitter).CommitMQTTReplayRetirement(ctx, q)
	require.ErrorIs(t, err, ch.ErrWriteFenced)
	require.Zero(t, after)
	a.ID.FenceVersion++
	a.WriteFence = ch.WriteFence{}
	_, err = runtimes[a.Leader].Log().Install(ctx, a)
	require.NoError(t, err)
	return a
}
