package mqttsession

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type retirementFixture struct {
	*retentionFixture
	selectPage func(ch.MQTTReplayRetirementSelectionRequest) ch.MQTTReplayRetirementSelection
	committed  ch.MQTTReplayRetirementProof
	requests   []ch.MQTTReplayRetirementSelectionRequest
	writes     []ch.MQTTReplayRetirementRequest
}

func (f *retirementFixture) SelectChannelMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementSelectionRequest) (ch.MQTTReplayRetirementSelection, error) {
	f.requests = append(f.requests, q)
	if err := f.visit(ctx, "select"); err != nil {
		return ch.MQTTReplayRetirementSelection{}, err
	}
	return f.selectPage(q), nil
}

func (f *retirementFixture) CommitChannelMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
	f.writes = append(f.writes, q.Clone())
	if err := f.visit(ctx, "commit"); err != nil {
		return ch.MQTTReplayRetirementProof{}, err
	}
	if f.committed != (ch.MQTTReplayRetirementProof{}) {
		return f.committed, nil
	}
	r, err := q.Retirement()
	require.NoError(f.t, err)
	m := q.Captured.Manifest
	m.Version, m.BaseOffset, m.PreviousIndex, m.LastOffset = 6, 9, 9, 10
	return ch.MQTTReplayRetirementProof{Retirement: r, Manifest: m}, nil
}

func newRetirementFixture(t *testing.T) (*ReplayRetirement, *retirementFixture, meta.MQTTBindingOwner) {
	t.Helper()
	p, retention := newRetentionFixture(t)
	retention.view.Bindings[0].CompletedThrough = 2
	f := &retirementFixture{retentionFixture: retention}
	f.selectPage = func(q ch.MQTTReplayRetirementSelectionRequest) ch.MQTTReplayRetirementSelection {
		return ch.MQTTReplayRetirementSelection{Captured: q.Captured, Candidate: q.Captured, HasCandidate: true, Done: true}
	}
	c, err := NewReplayRetirement(ReplayRetirementOptions{Retention: p, Channels: f, MessageIDs: f, Now: func() time.Time { return time.UnixMilli(1000) }, PageSize: 1})
	require.NoError(t, err)
	return c, f, retentionSource(retention)
}

func TestReplayRetirementOrdersFreshPermissionAndCommit(t *testing.T) {
	c, f, source := newRetirementFixture(t)
	r, err := c.Step(context.Background(), source, ReplayRetirementCursor{})
	require.NoError(t, err)
	require.True(t, r.Committed)
	require.False(t, r.ContinueScan)
	require.Zero(t, r.Next)
	require.Equal(t, f.proof.Anchor, r.Proof.Retirement.Anchor)
	require.Equal(t, []string{"placement", "anchor", "consumer", "recheck", "select", "commit"}, f.trace)
	require.Len(t, f.writes, 1)
	require.EqualValues(t, 2, f.writes[0].ConsumerThrough)
	require.EqualValues(t, 55, f.writes[0].MessageID)
	require.EqualValues(t, 1000, f.writes[0].ServerTimestampMS)
	// An uncertain/idempotent response may return an already committed decision.
	f.committed = r.Proof
	f.id++
	again, err := c.Step(context.Background(), source, ReplayRetirementCursor{})
	require.NoError(t, err)
	require.Equal(t, r.Proof, again.Proof)
}

func retirementLaterAnchor(old ch.MQTTReplayAnchorProof, through, position uint64) ch.MQTTReplayAnchorProof {
	old.Anchor.Through, old.Anchor.TotalBytes, old.Anchor.TotalStoredBytes = through, through*2, through*3
	old.Manifest.LastOffset, old.Manifest.BaseOffset, old.Manifest.PreviousIndex = position, position-1, position-1
	return old
}

func TestReplayRetirementKeepsFiniteCaptureWhileConsumersAdvance(t *testing.T) {
	c, f, source := newRetirementFixture(t)
	first := f.proof
	captured := retirementLaterAnchor(first, 5, 6)
	f.plan.Anchor, f.plan.Source.CommittedThrough = captured, 6
	f.view.Bindings[0].CompletedThrough = 3
	f.selectPage = func(q ch.MQTTReplayRetirementSelectionRequest) ch.MQTTReplayRetirementSelection {
		require.Equal(t, 1, q.Limit)
		require.Equal(t, captured, q.Captured)
		require.EqualValues(t, 3, q.Through)
		if q.BeforeAnchor == 0 {
			return ch.MQTTReplayRetirementSelection{Captured: captured, BeforeAnchor: 6}
		}
		require.EqualValues(t, 6, q.BeforeAnchor)
		return ch.MQTTReplayRetirementSelection{Captured: captured, Candidate: first, HasCandidate: true, Done: true}
	}
	r, err := c.Step(context.Background(), source, ReplayRetirementCursor{})
	require.NoError(t, err)
	require.True(t, r.ContinueScan)
	require.Empty(t, f.writes)
	f.plan.Anchor, f.plan.Source.CommittedThrough = retirementLaterAnchor(first, 8, 9), 9
	f.view.Bindings[0].CompletedThrough = 4
	f.trace = nil
	continued, err := c.Step(context.Background(), source, r.Next)
	require.NoError(t, err)
	require.True(t, continued.Committed)
	require.Equal(t, first.Anchor, continued.Proof.Retirement.Anchor)
	require.Equal(t, []string{"recheck", "anchor", "consumer", "recheck", "select", "commit"}, f.trace)
	require.EqualValues(t, 3, f.writes[0].ConsumerThrough, "increased progress preserves the conservative original floor")
}

func TestReplayRetirementYieldsWithoutPermission(t *testing.T) {
	for _, mode := range []string{"unknown", "no_anchor", "fenced", "exhausted", "floor_decreased", "authority_changed", "source_changed"} {
		t.Run(mode, func(t *testing.T) {
			c, f, source := newRetirementFixture(t)
			var cursor ReplayRetirementCursor
			if mode == "floor_decreased" || mode == "authority_changed" || mode == "source_changed" {
				cursor = ReplayRetirementCursor{Source: source, Authority: ch.MQTTReplayCopyAuthority(f.m), Captured: f.proof, Through: 2, BeforeAnchor: 3}
			}
			switch mode {
			case "unknown":
				b := &f.view.Bindings[0]
				b.Stage, b.BoundaryKnown, b.CompletedThrough, b.ProgressRevision, b.ProtectionRevision = meta.MQTTBindingPreparing, false, 0, 0, 0
			case "no_anchor":
				f.plan.HasAnchor, f.plan.Anchor = false, ch.MQTTReplayAnchorProof{}
			case "fenced":
				f.m.WriteFence = ch.WriteFence{Token: "migration", Version: 1}
			case "exhausted":
				f.selectPage = func(q ch.MQTTReplayRetirementSelectionRequest) ch.MQTTReplayRetirementSelection {
					return ch.MQTTReplayRetirementSelection{Captured: q.Captured, Done: true}
				}
			case "floor_decreased":
				f.view.Bindings[0].CompletedThrough = 1
			case "authority_changed":
				f.m.RouteGeneration++
			case "source_changed":
				cursor.Source.ID = "2:old"
			}
			r, err := c.Step(context.Background(), source, cursor)
			require.NoError(t, err)
			require.Zero(t, r)
			require.Empty(t, f.writes)
			if mode != "exhausted" {
				require.Empty(t, f.requests)
			}
		})
	}
}

func TestReplayRetirementRejectsUnprovenEffects(t *testing.T) {
	for _, mode := range []string{"upward", "changed_capture", "mixed", "nondecreasing", "future_capture", "bad_proof", "zero_id", "bad_clock", "select_error", "commit_error", "cancel_select", "cancel_commit", "cancel_consumer"} {
		t.Run(mode, func(t *testing.T) {
			c, f, source := newRetirementFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var cursor ReplayRetirementCursor
			switch mode {
			case "upward":
				f.view.Bindings[0].CompletedThrough = 1
			case "changed_capture", "mixed", "nondecreasing":
				f.selectPage = func(q ch.MQTTReplayRetirementSelectionRequest) ch.MQTTReplayRetirementSelection {
					r := ch.MQTTReplayRetirementSelection{Captured: q.Captured, Candidate: q.Captured, HasCandidate: true, Done: true}
					if mode == "changed_capture" {
						r.Captured.Manifest.Digest[0]++
					}
					if mode == "mixed" {
						r.BeforeAnchor = 2
					}
					if mode == "nondecreasing" {
						r = ch.MQTTReplayRetirementSelection{Captured: q.Captured, BeforeAnchor: q.BeforeAnchor}
					}
					return r
				}
				if mode == "nondecreasing" {
					cursor = ReplayRetirementCursor{Source: source, Authority: ch.MQTTReplayCopyAuthority(f.m), Captured: f.proof, Through: 1, BeforeAnchor: 3}
				}
			case "future_capture":
				cursor = ReplayRetirementCursor{Source: source, Authority: ch.MQTTReplayCopyAuthority(f.m), Captured: retirementLaterAnchor(f.proof, 5, 6), Through: 1, BeforeAnchor: 6}
			case "bad_proof":
				f.committed.Manifest.Version = 5
			case "zero_id":
				f.id = 0
			case "bad_clock":
				c.options.Now = func() time.Time { return time.Time{} }
			case "select_error":
				f.fail = "select"
			case "commit_error":
				f.fail = "commit"
			case "cancel_select", "cancel_commit", "cancel_consumer":
				f.hook = func(step string) {
					if mode == "cancel_"+step {
						cancel()
					}
				}
			}
			r, err := c.Step(ctx, source, cursor)
			require.Error(t, err)
			require.Zero(t, r)
			if mode != "bad_proof" && mode != "commit_error" && mode != "cancel_commit" {
				require.Empty(t, f.writes)
			}
		})
	}
}
