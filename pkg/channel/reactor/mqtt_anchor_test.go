package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type anchorCapableFactory struct{ mqttSourceCapableFactory }

func (anchorCapableFactory) SupportsMQTTReplayAnchors() bool { return true }

type anchorCaptureLog struct{ reactorCaptureQuorumLog }

func (*anchorCaptureLog) CommitMQTTReplayAnchor(context.Context, ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
	return ch.MQTTReplayAnchorProof{}, ch.ErrNotReady
}

func anchorReactorFixture(t *testing.T) (*Reactor, *runtimeChannel, worker.Result, *Future, context.CancelFunc) {
	r, rc, _, cancel := mqttSourceCompletionFixture(t)
	ctx := rc.lookupWaiters[11].ctx
	delete(rc.lookupWaiters, 11)
	r.unregisterLookupCancelContext(rc)
	r.cfg.Store = anchorCapableFactory{r.cfg.Store.(mqttSourceCapableFactory)}
	r.cfg.QuorumLog = &anchorCaptureLog{}
	rc.recentRecords = newRecentRecordCache(16, 4096)
	m := ch.Meta{Key: rc.state.Key, ID: rc.state.ID, Epoch: 2, LeaderEpoch: 3, RouteGeneration: 4, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	var err error
	rc.quorumAuthority, err = quorumAuthorityFromMeta(m)
	require.NoError(t, err)
	gen := quorumlog.MQTTSourceGeneration(ch.CommandID{1})
	copy := ch.MQTTReplayCopyReceipt{Request: ch.MQTTReplayRequest{ChannelID: m.ID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, Range: ch.MQTTReplayRange{Generation: gen, From: 1, Through: 9, Limit: 9, MaxBytes: 900}}, Leader: 1, Authority: ch.MQTTReplayCopyAuthority(m), WriteQuorum: 1, Copies: []ch.NodeID{1}, Before: ch.MQTTReplayPrefix{Generation: gen}, After: ch.MQTTReplayPrefix{Generation: gen, Through: 9, TotalBytes: 90, TotalStoredBytes: 900, Digest: [32]byte{7}}}
	q := ch.MQTTReplayAnchorRequest{Meta: m, Copy: copy, MessageID: 99, ServerTimestampMS: 99}
	expected, e := q.Anchor()
	require.NoError(t, e)
	body, e := expected.MarshalBinary()
	require.NoError(t, e)
	f := NewFuture()
	event := Event{Kind: EventAppend, Key: m.Key, OpID: 12, Context: ctx, Future: f, MQTTAnchor: &q, Append: ch.AppendBatchRequest{ChannelID: m.ID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, CommitMode: ch.CommitModeQuorum, Messages: []ch.Message{{MessageID: 99, ServerTimestampMS: 99, SyncOnce: true, Payload: body}}}}
	req := newAppendRequest(event, time.Now())
	rc.appendQ = newAppendQueue(appendQueueConfig{MaxPending: 8})
	require.NoError(t, r.enqueueAppendRequest(rc, req))
	r.registerAppendCancelContext(rc, req.opID, ctx)
	batch := rc.appendQ.popProposal(100, rc.state, time.Now())
	d := rc.state.ProposeAppendBatch(machine.AppendBatchCommand{BatchOpID: 100, Waiters: appendBatchWaiters(batch.requests)})
	require.Len(t, d.Tasks, 1)
	batch.fence = d.Tasks[0].Fence
	batch.authority = rc.quorumAuthority.ID
	rc.appendInflight = &batch
	manifest := ch.ProposalManifest{Version: 5, ChannelEpoch: 2, LeaderTerm: 3, FenceVersion: 4, CommandID: ch.CommandID{4}, BaseOffset: 9, LastOffset: 10, PreviousIndex: 9, PreviousTerm: 3, PreviousDigest: [32]byte{8}, Digest: [32]byte{9}}
	res := worker.Result{Kind: worker.TaskQuorumMQTTAnchor, Fence: batch.fence, QuorumMQTTAnchor: &worker.QuorumMQTTAnchorResult{Proof: ch.MQTTReplayAnchorProof{Anchor: expected, Manifest: manifest}}}
	return r, rc, res, f, cancel
}

func TestMQTTAnchorCompletionPreservesDurabilityAndFences(t *testing.T) {
	for _, mode := range []string{"success", "cancel", "guard", "guard_cancel", "generation", "foreign_op", "route", "nil", "error", "wrong_source", "wrong_prefix", "invalid_manifest"} {
		t.Run(mode, func(t *testing.T) {
			r, rc, res, f, cancel := anchorReactorFixture(t)
			require.True(t, r.hasPendingRuntimeWork(rc))
			switch mode {
			case "cancel":
				cancel()
				require.True(t, r.cancelAppendWaiter(rc, 12, context.Canceled))
			case "guard":
				r.appendAdmissionGuard = ch.AppendAdmissionGuardFunc(func(context.Context, ch.AppendAdmissionRequest) error { return ch.ErrNotReady })
			case "guard_cancel":
				r.appendAdmissionGuard = ch.AppendAdmissionGuardFunc(func(context.Context, ch.AppendAdmissionRequest) error { cancel(); return nil })
			case "generation":
				res.Fence.Generation++
			case "foreign_op":
				res.Fence.OpID++
			case "route":
				rc.quorumAuthority.ID.FenceVersion++
			case "nil":
				res.QuorumMQTTAnchor = nil
			case "error":
				res.Err = ch.ErrNotReady
			case "wrong_source":
				res.QuorumMQTTAnchor.Proof.Anchor.SourceCommand[0]++
			case "wrong_prefix":
				res.QuorumMQTTAnchor.Proof.Anchor.Digest[0]++
			case "invalid_manifest":
				res.QuorumMQTTAnchor.Proof.Manifest.Digest = [32]byte{}
			}
			r.handleQuorumMQTTAnchorResult(res)
			if mode == "generation" || mode == "foreign_op" {
				require.NotNil(t, rc.appendInflight)
				require.Equal(t, uint64(9), rc.state.HW)
				select {
				case <-f.Done():
					t.Fatal("foreign result completed waiter")
				default:
				}
				return
			}
			require.Nil(t, rc.appendInflight)
			require.Empty(t, rc.waiters)
			require.Empty(t, rc.state.PendingAppends)
			select {
			case <-f.Done():
			default:
				t.Fatal("waiter not completed")
			}
			if mode == "success" {
				require.NoError(t, f.Result().Err)
				require.Equal(t, res.QuorumMQTTAnchor.Proof, f.Result().MQTTAnchor)
			} else {
				require.Error(t, f.Result().Err)
				require.Zero(t, f.Result().MQTTAnchor)
			}
			if mode == "success" || mode == "cancel" || mode == "guard" || mode == "guard_cancel" {
				require.Equal(t, uint64(10), rc.state.HW)
			} else {
				require.Equal(t, uint64(9), rc.state.HW)
			}
			require.Zero(t, rc.recentRecords.bytes, "request record must never enter recent cache")
		})
	}
}

func TestMQTTAnchorQueueOwnsAndAccountsMembership(t *testing.T) {
	_, rc, _, _, _ := anchorReactorFixture(t)
	original := *rc.appendInflight.requests[0].mqttAnchor
	event := Event{MQTTAnchor: &original, Append: rc.appendInflight.requests[0].req}
	req := newAppendRequest(event, time.Now())
	original.Meta.ISR[0] = 9
	original.Copy.Copies[0] = 9
	require.Equal(t, ch.NodeID(1), req.mqttAnchor.Meta.ISR[0])
	require.Equal(t, ch.NodeID(1), req.mqttAnchor.Copy.Copies[0])
	q := newAppendQueue(appendQueueConfig{MaxPending: 8, MaxPendingBytes: recordsBytes(req.records)})
	require.ErrorIs(t, q.push(req), ch.ErrBackpressured)
}
