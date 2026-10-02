//go:build integration

package replication

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	cs "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTAnchorAdmissionOrderedIdempotentAndRecovered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	router := &runtimeTestRouter{servers: make(map[ch.NodeID]*ExchangeServer)}
	paths := map[ch.NodeID]string{}
	runtimes := map[ch.NodeID]*Runtime{}
	factories := map[ch.NodeID]*cs.MessageDBFactory{}
	open := func() {
		for _, n := range []ch.NodeID{1, 2, 3} {
			if paths[n] == "" {
				paths[n] = t.TempDir()
			}
			f := cs.NewMessageDBFactory(paths[n])
			st, e := NewStoreAdapter(StoreAdapterConfig{Factory: f, MaxBatchItems: 64, MaxBatchBytes: 4 << 20})
			require.NoError(t, e)
			r, e := NewRuntime(RuntimeConfig{LocalNode: n, Store: st, Link: mqttActivationWireLink{runtimeTestLink{from: n, router: router}}, Goroutines: gr.New()})
			require.NoError(t, e)
			factories[n], runtimes[n] = f, r
			router.register(n, r.ExchangeServer())
		}
	}
	closeAll := func() {
		closeCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		for n, r := range runtimes {
			require.NoError(t, r.Close(closeCtx))
			delete(runtimes, n)
		}
		for n, f := range factories {
			require.NoError(t, f.Close())
			delete(factories, n)
		}
	}
	t.Cleanup(closeAll)
	open()
	meta := ch.Meta{ID: ch.ChannelID{ID: "anchor-admit", Type: 1}, Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	meta.Key = ch.ChannelKeyForID(meta.ID)
	authority := func() Authority {
		return Authority{Key: meta.Key, ChannelID: meta.ID, ID: AuthorityID{ChannelEpoch: meta.Epoch, LeaderTerm: meta.LeaderEpoch, FenceVersion: meta.RouteGeneration}, Leader: meta.Leader, Voters: meta.ISR, WriteQuorum: meta.MinISR}
	}
	_, err := runtimes[1].Log().Install(ctx, authority())
	require.NoError(t, err)
	sourceCommand := ch.CommandID{1}
	_, err = runtimes[1].Log().Commit(ctx, Proposal{Key: meta.Key, Expected: authority().ID, CommandID: sourceCommand, MQTTSourceActivation: true, Records: []ch.Record{{ID: 100, Epoch: 1, ServerTimestampMS: 1000, SyncOnce: true, Payload: []byte(quorumlog.MQTTSourceActivationPayload), SizeBytes: len(quorumlog.MQTTSourceActivationPayload)}}})
	require.NoError(t, err)
	appendBusiness := func(id uint64) uint64 {
		receipt, e := runtimes[meta.Leader].Log().Commit(ctx, Proposal{Key: meta.Key, Expected: authority().ID, CommandID: ch.CommandID{byte(id)}, Records: []ch.Record{{ID: id, Epoch: 1, ServerTimestampMS: int64(id), FromUID: "sender", Payload: []byte("business"), SizeBytes: len("business")}}})
		require.NoError(t, e)
		return receipt.Last
	}
	appendBusiness(2)
	copyPage := func(from, through uint64) ch.MQTTReplayCopyReceipt {
		q := ch.MQTTReplayRequest{ChannelID: meta.ID, ExpectedChannelEpoch: meta.Epoch, ExpectedLeaderEpoch: meta.LeaderEpoch, ExpectedRouteGeneration: meta.RouteGeneration, Range: ch.MQTTReplayRange{Generation: quorumlog.MQTTSourceGeneration(sourceCommand), From: from, Through: through, Limit: 256, MaxBytes: 1 << 20}}
		// Fixture-controlled committed checkpoint, then the real native refresh.
		leader, e := factories[meta.Leader].ChannelStore(meta.Key, meta.ID)
		require.NoError(t, e)
		require.NoError(t, leader.StoreCheckpoint(ctx, ch.Checkpoint{HW: through}))
		require.NoError(t, leader.Close())
		require.NoError(t, runtimes[meta.Leader].Log().(CommittedReplicaRefresher).RequestCommittedReplicaRefresh(ctx, authority()))
		var page ch.MQTTReplayPage
		for _, n := range meta.ISR {
			require.Eventually(t, func() bool {
				st, e := factories[n].ChannelStore(meta.Key, meta.ID)
				if e != nil {
					return false
				}
				defer st.Close()
				s, ok, e := st.(cs.MQTTSourceReader).LoadCommittedMQTTSource(ctx, through)
				if e != nil || !ok || s.CommittedThrough < through {
					return false
				}
				preparer := st.(cs.MQTTReplayPreparer)
				// Already-covered preparation may stop at an older local prefix.
				// Copy each remaining bounded segment before requesting the full page.
				rangeToCopy := q.Range
				for {
					piece, e := preparer.PrepareMQTTReplay(ctx, rangeToCopy)
					if e != nil {
						return false
					}
					if piece.After.Through == through {
						break
					}
					if piece.After.Through < rangeToCopy.From {
						return false
					}
					rangeToCopy.From = piece.After.Through + 1
				}
				p, e := preparer.PrepareMQTTReplay(ctx, q.Range)
				if e != nil || p.After.Through != through {
					return false
				}
				if page.After.Through != 0 && page.After != p.After {
					return false
				}
				page = p
				return true
			}, 3*time.Second, time.Millisecond)
		}
		q.Range.Through = page.After.Through
		q.Range.Limit = len(page.Records)
		q.Range.MaxBytes = int(page.After.TotalStoredBytes - page.Before.TotalStoredBytes)
		return ch.MQTTReplayCopyReceipt{Request: q, Leader: meta.Leader, Authority: ch.MQTTReplayCopyAuthority(meta), WriteQuorum: meta.MinISR, Before: page.Before, After: page.After, Copies: []ch.NodeID{1, 2, 3}}
	}
	copied := copyPage(1, 2)
	admit := func(q ch.MQTTReplayCopyReceipt, id uint64) (ch.MQTTReplayAnchorProof, error) {
		return runtimes[meta.Leader].Log().(MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(ctx, MQTTReplayAnchorAdmission{Meta: meta, Copy: q, MessageID: id, ServerTimestampMS: int64(id)})
	}
	for _, mode := range []string{"digest", "before", "uncommitted", "membership", "quorum", "leader"} {
		bad := copied
		bad.Copies = append([]ch.NodeID(nil), copied.Copies...)
		switch mode {
		case "digest":
			bad.After.Digest = [32]byte{}
		case "before":
			bad.Before.StartAfter = 1
		case "uncommitted":
			bad.After.Through++
			bad.Request.Range.Through++
			bad.Request.Range.Limit++
		case "membership":
			bad.Authority[0] ^= 1
		case "quorum":
			bad.Copies = bad.Copies[:1]
		case "leader":
			bad.Leader = 2
		}
		_, e := admit(bad, 500)
		require.Error(t, e, mode)
	}
	// A coherently reissued receipt cannot replace installed membership.
	changedMeta := meta
	changedMeta.ISR = []ch.NodeID{1, 2}
	changed := copied
	changed.Copies = []ch.NodeID{1, 2}
	changed.Authority = ch.MQTTReplayCopyAuthority(changedMeta)
	_, err = runtimes[1].Log().(MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(ctx, MQTTReplayAnchorAdmission{Meta: changedMeta, Copy: changed, MessageID: 490, ServerTimestampMS: 490})
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	owner := runtimes[1].Log().(*quorumLog)
	oldBudget := owner.cfg.MaxProposalBytes
	owner.cfg.MaxProposalBytes = 200 // fixed payload fits, full proposal does not
	_, err = admit(copied, 491)
	owner.cfg.MaxProposalBytes = oldBudget
	require.ErrorIs(t, err, ch.ErrBackpressured)
	first, err := admit(copied, 501)
	require.NoError(t, err)
	require.Equal(t, uint64(3), first.Manifest.LastOffset)
	const count = 8
	proofs := make([]ch.MQTTReplayAnchorProof, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(i int) { defer wg.Done(); proofs[i], errs[i] = admit(copied, uint64(600+i)) }(i)
	}
	wg.Wait()
	for i := range count {
		require.NoError(t, errs[i])
		require.Equal(t, first, proofs[i])
	}
	idle := copyPage(3, 3)
	idleProof, err := admit(idle, 700)
	require.NoError(t, err)
	require.Equal(t, first, idleProof, "anchor-only tail must not reproduce itself")
	appendBusiness(4)
	next := copyPage(3, 4)
	// A valid copied subpage that skips the accepted prefix cannot be admitted.
	skipped := copyPage(4, 4)
	_, err = admit(skipped, 701)
	require.ErrorIs(t, err, ch.ErrLogConflict)
	second, err := admit(next, 702)
	require.NoError(t, err)
	require.Equal(t, uint64(5), second.Manifest.LastOffset)
	old, err := admit(copied, 703)
	require.NoError(t, err)
	require.Equal(t, first, old, "older accepted prefix still reuses exact command proof")
	altered := copied
	altered.After.Digest[0] ^= 1
	_, err = admit(altered, 704)
	require.ErrorIs(t, err, ch.ErrLogConflict)
	// Persist native commit visibility everywhere before a full runtime restart.
	_ = copyPage(5, 5)
	closeAll()
	open()
	meta.Leader = 2
	meta.LeaderEpoch++
	meta.RouteGeneration++
	_, err = runtimes[2].Log().Install(ctx, authority())
	require.NoError(t, err)
	_, err = admit(copied, 800)
	require.Error(t, err, "old receipt cannot cross authority")
	recovered := copyPage(1, 2)
	old, err = admit(recovered, 801)
	require.NoError(t, err)
	require.Equal(t, first, old)
	newTail := appendBusiness(6)
	current := copyPage(5, newTail)
	router.register(1, nil)
	router.register(3, nil)
	short, done := context.WithTimeout(ctx, 100*time.Millisecond)
	proof, err := runtimes[2].Log().(MQTTReplayAnchorCommitter).CommitMQTTReplayAnchor(short, MQTTReplayAnchorAdmission{Meta: meta, Copy: current, MessageID: 900, ServerTimestampMS: 900})
	done()
	require.Error(t, err)
	require.Zero(t, proof)
	// Resume the exact uncertain proposal while allowing fresh control IDs.
	router.register(1, runtimes[1].ExchangeServer())
	router.register(3, runtimes[3].ExchangeServer())
	resumed, err := admit(current, 901)
	require.NoError(t, err)
	require.Equal(t, newTail+1, resumed.Manifest.LastOffset)
	require.Equal(t, newTail, resumed.Anchor.Through)
	stored, err := factories[2].ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	defer stored.Close()
	original, found, err := stored.(cs.ExactProposalLookup).LoadExactProposal(ctx, cs.ExactProposalRequest{CommandID: resumed.Manifest.CommandID, MaxRecords: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, original.Records, 1)
	require.Equal(t, uint64(900), original.Records[0].ID, "uncertain retry keeps the first row identity")
	t.Log("mqtt_anchor_admission_evidence: voters=3 disk=true exchange_codec=true copy_membership=true serialized=true exact_retry=true concurrent_retry=true anchor_only_idle=true chained_prefix=true restart=true leader_change=true pending_retry=true source_release=false fresh_slot_entry=false product_listener=false")
}
