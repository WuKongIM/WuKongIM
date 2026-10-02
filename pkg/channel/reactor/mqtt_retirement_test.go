package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type retirementCapableFactory struct{ anchorCapableFactory }

func (retirementCapableFactory) SupportsMQTTReplayRetirements() bool { return true }

type retirementCaptureLog struct{ anchorCaptureLog }

func (*retirementCaptureLog) CommitMQTTReplayRetirement(context.Context, ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
	return ch.MQTTReplayRetirementProof{}, ch.ErrNotReady
}

func retirementReactorFixture(t *testing.T) (*Reactor, *runtimeChannel, worker.Result, *Future, context.CancelFunc) {
	r, rc, old, f, cancel := anchorReactorFixture(t)
	r.cfg.Store = retirementCapableFactory{r.cfg.Store.(anchorCapableFactory)}
	r.cfg.QuorumLog = &retirementCaptureLog{}
	anchor := old.QuorumMQTTAnchor.Proof
	anchor.Anchor.Through = 8
	anchor.Manifest.BaseOffset, anchor.Manifest.LastOffset, anchor.Manifest.PreviousIndex = 8, 9, 8
	q := ch.MQTTReplayRetirementRequest{Meta: rc.appendInflight.requests[0].mqttAnchor.Meta, Captured: anchor, Candidate: anchor, ConsumerThrough: 8, MessageID: 99, ServerTimestampMS: 99}
	retirement, err := q.Retirement()
	require.NoError(t, err)
	body, err := retirement.MarshalBinary()
	require.NoError(t, err)
	rc.appendInflight.requests[0].mqttAnchor = nil
	rc.appendInflight.requests[0].mqttRetirement = &q
	rc.appendInflight.requests[0].req.Messages[0].Payload = body
	manifest := old.QuorumMQTTAnchor.Proof.Manifest
	manifest.Version = quorumlog.MQTTReplayRetirementProposalManifestVersion
	res := worker.Result{Kind: worker.TaskQuorumMQTTRetirement, Fence: old.Fence, QuorumMQTTRetirement: &worker.QuorumMQTTRetirementResult{Proof: ch.MQTTReplayRetirementProof{Retirement: retirement, Manifest: manifest}}}
	return r, rc, res, f, cancel
}

func TestMQTTRetirementCompletionPreservesDurabilityAndFences(t *testing.T) {
	for _, mode := range []string{"success", "cancel", "guard", "guard_cancel", "generation", "foreign_op", "route", "nil", "error", "wrong_source", "wrong_reference", "invalid_manifest", "future_authority"} {
		t.Run(mode, func(t *testing.T) {
			r, rc, res, f, cancel := retirementReactorFixture(t)
			t.Cleanup(cancel)
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
				res.QuorumMQTTRetirement = nil
			case "error":
				res.Err = ch.ErrNotReady
			case "wrong_source":
				res.QuorumMQTTRetirement.Proof.Retirement.Anchor.SourceCommand[0]++
			case "wrong_reference":
				res.QuorumMQTTRetirement.Proof.Retirement.AnchorDigest[0]++
			case "invalid_manifest":
				res.QuorumMQTTRetirement.Proof.Manifest.Digest = [32]byte{}
			case "future_authority":
				res.QuorumMQTTRetirement.Proof.Manifest.LeaderTerm++
			}
			r.handleQuorumMQTTRetirementResult(res)
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
				require.Equal(t, res.QuorumMQTTRetirement.Proof, f.Result().MQTTRetirement)
			} else {
				require.Error(t, f.Result().Err)
				require.Zero(t, f.Result().MQTTRetirement)
			}
			if mode == "success" || mode == "cancel" || mode == "guard" || mode == "guard_cancel" {
				require.Equal(t, uint64(10), rc.state.HW)
			} else {
				require.Equal(t, uint64(9), rc.state.HW)
			}
			require.Zero(t, rc.recentRecords.bytes, "request identity is not the durable row on retry")
		})
	}
}

func TestMQTTRetirementQueueOwnsMembershipAndRejectsMixedControl(t *testing.T) {
	r, rc, _, _, cancel := retirementReactorFixture(t)
	defer cancel()
	original := *rc.appendInflight.requests[0].mqttRetirement
	event := Event{MQTTRetirement: &original, Append: rc.appendInflight.requests[0].req}
	require.NoError(t, r.validateAppendEvent(context.Background(), rc, event))
	for _, mode := range []string{"activation", "anchor", "body", "uncommitted", "unsupported"} {
		bad := event
		q := original.Clone()
		bad.MQTTRetirement = &q
		saved := r.cfg.Store
		switch mode {
		case "activation":
			bad.MQTTSourceActivation = true
		case "anchor":
			bad.MQTTAnchor = &ch.MQTTReplayAnchorRequest{}
		case "body":
			bad.Append.Messages = append([]ch.Message(nil), bad.Append.Messages...)
			bad.Append.Messages[0].Payload = []byte("business")
		case "uncommitted":
			q.Captured.Manifest.BaseOffset++
			q.Captured.Manifest.LastOffset++
			q.Captured.Manifest.PreviousIndex++
			q.Candidate = q.Captured
		case "unsupported":
			r.cfg.Store = saved.(retirementCapableFactory).anchorCapableFactory
		}
		require.Error(t, r.validateAppendEvent(context.Background(), rc, bad), mode)
		r.cfg.Store = saved
	}
	req := newAppendRequest(event, time.Now())
	original.Meta.ISR[0], original.Meta.Replicas[0] = 99, 99
	require.Equal(t, ch.NodeID(1), req.mqttRetirement.Meta.ISR[0])
	queue := newAppendQueue(appendQueueConfig{MaxPending: 8, MaxPendingBytes: recordsBytes(req.records)})
	require.ErrorIs(t, queue.push(req), ch.ErrBackpressured)
}
