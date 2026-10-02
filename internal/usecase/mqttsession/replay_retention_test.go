package mqttsession

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type retentionFixture struct {
	*replayCoordinatorFixture
	view  meta.MQTTReadResult
	trace []string
	hook  func(string)
	fail  string
	t     *testing.T
}

func (f *retentionFixture) visit(ctx context.Context, step string) error {
	f.trace = append(f.trace, step)
	d, ok := ctx.Deadline()
	require.True(f.t, ok)
	require.LessOrEqual(f.t, time.Until(d), 5*time.Second)
	if f.hook != nil {
		f.hook(step)
	}
	if f.fail == step {
		return context.DeadlineExceeded
	}
	return nil
}
func (f *retentionFixture) ResolveChannelMetaFresh(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	step := "placement"
	if f.metaCalls > 0 {
		step = "recheck"
	}
	if err := f.visit(ctx, step); err != nil {
		return ch.Meta{}, err
	}
	return f.replayCoordinatorFixture.ResolveChannelMetaFresh(ctx, id)
}
func (f *retentionFixture) PlanChannelMQTTReplay(ctx context.Context, q ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
	if err := f.visit(ctx, "anchor"); err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	return f.replayCoordinatorFixture.PlanChannelMQTTReplay(ctx, q)
}
func (f *retentionFixture) ReadMQTT(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	require.Equal(f.t, meta.MQTTRead{Kind: meta.MQTTReadSourceRetention, Owner: retentionSource(f), Limit: 1}, q)
	if err := f.visit(ctx, "consumer"); err != nil {
		return meta.MQTTReadResult{}, err
	}
	return f.view, nil
}
func retentionSource(f *retentionFixture) meta.MQTTBindingOwner {
	return meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "2:group", Generation: f.receipt.Before.Generation}
}
func newRetentionFixture(t *testing.T) (*ReplayRetention, *retentionFixture) {
	t.Helper()
	_, replay, source := newReplayFixture(t)
	replay.plan.HasAnchor, replay.plan.Anchor, replay.plan.Source.CommittedThrough = true, replay.proof, 3
	f := &retentionFixture{replayCoordinatorFixture: replay, t: t}
	f.view = meta.MQTTReadResult{Done: true, Bindings: []meta.MQTTSourceBinding{{Key: meta.MQTTSourceBindingKey{Owner: source, Namespace: "main", ClientID: "client", SessionGeneration: 1, SubscriptionGeneration: 1}, UID: "alice", Topic: "wk/v1/groups/Z3JvdXA/messages", Revision: 4, IntentRevision: 2, ProgressRevision: 3, AuthorizationVersion: 7, OperationID: "subscribe", Stage: meta.MQTTBindingActive, BoundaryKnown: true, CompletedThrough: 1, ProtectionRevision: 2, RecoveryAtMS: 1000, UpdatedAtMS: 1000}}}
	c, err := NewReplayRetention(ReplayRetentionOptions{Metadata: f, Channels: f, Store: f})
	require.NoError(t, err)
	return c, f
}

func TestReplayRetentionCapsCoherentMinimumAndPreservesRemoval(t *testing.T) {
	for _, mode := range []string{"active", "unknown", "removing", "ended_removing", "above_anchor", "no_consumers", "more_consumers", "no_anchor"} {
		t.Run(mode, func(t *testing.T) {
			c, f := newRetentionFixture(t)
			want := uint64(1)
			b := &f.view.Bindings[0]
			switch mode {
			case "unknown":
				b.Stage, b.BoundaryKnown, b.CompletedThrough, b.ProgressRevision, b.ProtectionRevision = meta.MQTTBindingPreparing, false, 0, 0, 0
				want = 0
			case "removing", "ended_removing":
				b.Stage = meta.MQTTBindingRemoving
				if mode == "ended_removing" {
					b.ReleaseReason = meta.MQTTBindingSessionEnded
				}
			case "above_anchor":
				b.CompletedThrough, want = 20, 2
			case "no_consumers":
				f.view.Bindings, want = nil, 2
			case "more_consumers":
				f.view.Done = false
				f.view.After.Retention = meta.MQTTSourceBindingRetentionCursor{Key: b.Key, CompletedThrough: b.CompletedThrough}
			case "no_anchor":
				f.plan.HasAnchor, f.plan.Anchor, want = false, ch.MQTTReplayAnchorProof{}, 0
			}
			got, err := c.Plan(context.Background(), retentionSource(f))
			require.NoError(t, err)
			require.Equal(t, want, got.Through)
			require.Equal(t, f.plan, got.Replay)
			require.Equal(t, retentionSource(f), got.Source)
			if mode == "no_anchor" {
				require.Equal(t, []string{"placement", "anchor"}, f.trace)
				require.False(t, got.HasConsumer)
			} else {
				require.Equal(t, []string{"placement", "anchor", "consumer", "recheck"}, f.trace)
				require.Equal(t, len(f.view.Bindings) > 0, got.HasConsumer)
				if got.HasConsumer {
					require.Equal(t, f.view.Bindings[0], got.Consumer)
				}
			}
			got.Placement.Replicas[0] = 99
			require.Equal(t, ch.NodeID(1), f.m.Replicas[0], "results must detach mutable placement buffers")
		})
	}
}

func TestReplayRetentionRejectsUnprovenMinimum(t *testing.T) {
	for _, mode := range []string{"partial_empty", "extra_rows", "foreign_source", "removed", "bad_row", "missing_progress", "wrong_cursor", "missing_cursor", "extra_cursor", "session", "source_owners", "extra_children", "bad_anchor", "changed_placement", "changed_fence"} {
		t.Run(mode, func(t *testing.T) {
			c, f := newRetentionFixture(t)
			b := &f.view.Bindings[0]
			switch mode {
			case "partial_empty":
				f.view.Bindings, f.view.Done = nil, false
			case "extra_rows":
				f.view.Bindings = append(f.view.Bindings, *b)
			case "foreign_source":
				b.Key.Owner.Generation = "foreign"
			case "removed":
				b.Stage, b.ReleaseReason, b.RecoveryAtMS = meta.MQTTBindingRemoved, meta.MQTTBindingSessionEnded, 0
			case "bad_row":
				b.Revision = 0
			case "missing_progress":
				b.Stage, b.ProgressRevision = meta.MQTTBindingPreparing, 0
			case "wrong_cursor":
				f.view.Done = false
				f.view.After.Retention = meta.MQTTSourceBindingRetentionCursor{Key: b.Key, CompletedThrough: 9}
			case "missing_cursor":
				f.view.Done = false
			case "extra_cursor":
				f.view.After.Topic = "foreign"
			case "session":
				f.view.Session = &meta.MQTTSession{}
			case "source_owners":
				f.view.SourceOwners = []meta.MQTTBindingOwner{b.Key.Owner}
			case "extra_children":
				f.view.DeliveryCursors = []meta.MQTTDeliveryCursor{{}}
			case "bad_anchor":
				f.plan.Source.Generation = "foreign"
			case "changed_placement":
				f.hook = func(step string) {
					if step == "consumer" {
						f.m.Replicas[2] = 4
					}
				}
			case "changed_fence":
				f.hook = func(step string) {
					if step == "consumer" {
						f.m.WriteFence.Version++
					}
				}
			}
			got, err := c.Plan(context.Background(), retentionSource(f))
			require.Error(t, err)
			require.Zero(t, got)
		})
	}
}

func TestReplayRetentionOrdersAnchorBeforeNewConsumerAdmission(t *testing.T) {
	for _, admittedBeforeView := range []bool{false, true} {
		c, f := newRetentionFixture(t)
		f.view.Bindings = nil
		f.hook = func(step string) {
			if step != "consumer" {
				return
			}
			// New accepted content after anchor capture cannot enlarge this plan.
			f.plan.Anchor.Anchor.Through, f.plan.Anchor.Manifest.LastOffset, f.plan.Source.CommittedThrough = 8, 9, 9
			if admittedBeforeView {
				f.view.Bindings = []meta.MQTTSourceBinding{{Key: meta.MQTTSourceBindingKey{Owner: retentionSource(f), Namespace: "main", ClientID: "new", SessionGeneration: 1, SubscriptionGeneration: 1}, UID: "alice", Topic: "wk/v1/groups/Z3JvdXA/messages", Revision: 1, IntentRevision: 1, OperationID: "new", Stage: meta.MQTTBindingPreparing, RecoveryAtMS: 1000, UpdatedAtMS: 1000}}
			}
		}
		got, err := c.Plan(context.Background(), retentionSource(f))
		require.NoError(t, err)
		if admittedBeforeView {
			require.Zero(t, got.Through)
		} else {
			require.EqualValues(t, 2, got.Through)
		}
		require.EqualValues(t, 2, got.Replay.Anchor.Anchor.Through)
		require.Equal(t, []string{"placement", "anchor", "consumer", "recheck"}, f.trace)
	}
}

func TestReplayRetentionBoundsCallsAndFailures(t *testing.T) {
	for _, step := range []string{"placement", "anchor", "consumer", "recheck"} {
		for _, cancelCall := range []bool{false, true} {
			c, f := newRetentionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			if cancelCall {
				f.hook = func(s string) {
					if s == step {
						cancel()
					}
				}
			} else {
				f.fail = step
			}
			got, err := c.Plan(ctx, retentionSource(f))
			cancel()
			require.True(t, errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
			require.Zero(t, got)
			require.Equal(t, step, f.trace[len(f.trace)-1])
		}
	}
	c, f := newRetentionFixture(t)
	for _, o := range []ReplayRetentionOptions{{}, {Metadata: f, Channels: f, Store: f, Timeout: -time.Second}, {Metadata: f, Channels: f, Store: f, Timeout: 2 * time.Minute}} {
		_, err := NewReplayRetention(o)
		require.ErrorIs(t, err, ErrInvalid)
	}
	_, err := c.Plan(nil, retentionSource(f))
	require.ErrorIs(t, err, ErrInvalid)
	_, err = c.Plan(context.Background(), meta.MQTTBindingOwner{})
	require.ErrorIs(t, err, ErrInvalid)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Plan(ctx, retentionSource(f))
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, f.trace)
}
