package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func qualificationMaintenanceFixture(t *testing.T) (*inboxSourceFixture, *progressStore, *app.SourceProgress, *app.SourceRemoval, *app.ConsumerMaintenance) {
	t.Helper()
	f := setupInboxSource(t)
	s := &progressStore{groupSourceStore: f.store}
	progress, e := app.NewSourceProgress(app.SourceProgressOptions{Store: s, Now: func() time.Time { return f.now }})
	require.NoError(t, e)
	removal, e := app.NewSourceRemoval(app.SourceRemovalOptions{Store: s, Now: func() time.Time { return f.now }})
	require.NoError(t, e)
	// UID turns must never use this unrelated Channel accounting fixture.
	_, accounting := setupAccounting(t)
	consumer, e := app.NewConsumerMaintenance(app.ConsumerMaintenanceOptions{Store: s, Accounting: accounting, Progress: progress, Removal: removal, Ender: f.service})
	require.NoError(t, e)
	return f, s, progress, removal, consumer
}

func TestQualificationRetirementPreservesIndependentSourceDebt(t *testing.T) {
	for _, mode := range []string{"preparing", "active", "partial-drain", "new-lifetime", "lost-progress", "lost-removal"} {
		t.Run(mode, func(t *testing.T) {
			f, _, _, _, c := qualificationMaintenanceFixture(t)
			ctx := context.Background()
			source, e := f.prepare()
			require.NoError(t, e)
			q := f.qualification
			if mode != "preparing" {
				q.Revision++
				q.Stage = meta.MQTTBindingActive
				q.DiscoveryDone = true
				applied, e := f.store.CompareAndSwapMQTTSourceBinding(ctx, q.Revision-1, q)
				require.NoError(t, e)
				require.Equal(t, meta.MQTTSessionCASApplied, applied.Status)
			}
			if mode == "partial-drain" {
				_, e := f.subscriptions.Unsubscribe(ctx, f.connection.Owner, f.intent.Topic)
				require.NoError(t, e)
				q.Revision++
				q.Stage = meta.MQTTBindingRemoving
				q.DrainVersion = 1
				q.IntentRevision = f.subscription(t, f.intent.Topic).Revision
				applied, e := f.store.CompareAndSwapMQTTSourceBinding(ctx, q.Revision-1, q)
				require.NoError(t, e)
				require.Equal(t, meta.MQTTSessionCASApplied, applied.Status)
			}
			if mode == "new-lifetime" {
				cmd := command()
				cmd.CleanStart = true
				_, e = f.service.Connect(ctx, cmd)
				require.NoError(t, e)
			} else {
				zero := uint32(0)
				require.NoError(t, f.service.Disconnect(ctx, app.DisconnectCommand{Owner: f.connection.Owner, Normal: true, SessionExpirySec: &zero}))
			}
			parent := f.row(t)
			lost := errors.New("committed reply lost")
			if mode == "lost-progress" || mode == "lost-removal" {
				f.store.afterBinding = func(b meta.MQTTSourceBinding) error {
					if mode == "lost-progress" && b.Stage == meta.MQTTBindingRemoving || mode == "lost-removal" && b.Stage == meta.MQTTBindingRemoved {
						return lost
					}
					return nil
				}
				out, e := c.Maintain(ctx, q.Key)
				require.ErrorIs(t, e, lost)
				require.False(t, out.QualificationRemoved)
				f.store.afterBinding = nil
			}
			_, e = c.Maintain(ctx, q.Key)
			require.NoError(t, e)
			again, e := c.Maintain(ctx, q.Key)
			require.NoError(t, e)
			require.False(t, again.QualificationRemoved, "removed tombstone must not be counted again")
			stored, found, e := f.store.db.HashSlot(7).GetMQTTSourceBinding(ctx, q.Key)
			require.NoError(t, e)
			require.True(t, found)
			require.Equal(t, meta.MQTTBindingRemoved, stored.Stage)
			require.Equal(t, meta.MQTTBindingSessionEnded, stored.ReleaseReason)
			require.Equal(t, parent.Revision, stored.ProgressRevision)
			require.Zero(t, stored.ProtectionRevision)
			require.Zero(t, stored.RecoveryAtMS)
			require.Equal(t, q.DiscoveryDone, stored.DiscoveryDone)
			require.Equal(t, q.DrainVersion, stored.DrainVersion)
			require.Equal(t, q.DrainDone, stored.DrainDone)
			retained, found, e := f.store.db.HashSlot(7).GetMQTTSourceBinding(ctx, source.Binding.Key)
			require.NoError(t, e)
			require.True(t, found)
			require.Equal(t, source.Binding, retained)
			require.Equal(t, parent, f.row(t), "old qualification cleanup mutated the current parent")
			candidates, e := f.store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceCandidates, Owner: q.Key.Owner, Limit: 1})
			require.NoError(t, e)
			require.Empty(t, candidates.Bindings)
		})
	}
}

func TestQualificationRetirementDoesNotInferTermination(t *testing.T) {
	for _, mode := range []string{"active", "offline", "past-lease", "normal-removing"} {
		t.Run(mode, func(t *testing.T) {
			f, s, _, _, c := qualificationMaintenanceFixture(t)
			q := f.qualification
			if mode == "offline" {
				require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
			}
			if mode == "past-lease" {
				f.now = f.now.Add(time.Hour)
			}
			if mode == "normal-removing" {
				q.Revision++
				q.Stage = meta.MQTTBindingRemoving
				_, e := f.subscriptions.Unsubscribe(context.Background(), f.connection.Owner, f.intent.Topic)
				require.NoError(t, e)
				q.IntentRevision = f.subscription(t, f.intent.Topic).Revision
				q.DrainVersion = 1
				_, e = f.store.CompareAndSwapMQTTSourceBinding(context.Background(), q.Revision-1, q)
				require.NoError(t, e)
			}
			out, e := c.Maintain(context.Background(), q.Key)
			require.NoError(t, e)
			require.Zero(t, out)
			require.Zero(t, s.writes)
		})
	}
}

func TestQualificationRetirementRejectsUnprovenAuthority(t *testing.T) {
	for _, phase := range []string{"progress", "removal"} {
		for _, fault := range []string{"missing", "uid", "generation", "revision", "partial", "extra", "cursor", "error", "cancel", "conflict", "write-error", "write-receipt", "clock"} {
			t.Run(phase+"/"+fault, func(t *testing.T) {
				f, s, progress, removal, _ := qualificationMaintenanceFixture(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				zero := uint32(0)
				require.NoError(t, f.service.Disconnect(ctx, app.DisconnectCommand{Owner: f.connection.Owner, Normal: true, SessionExpirySec: &zero}))
				if phase == "removal" {
					_, e := progress.Reconcile(ctx, f.qualification.Key)
					require.NoError(t, e)
				}
				before := s.writes
				s.read = func(q meta.MQTTRead, r *meta.MQTTReadResult) error {
					if q.Kind != meta.MQTTReadSession {
						return nil
					}
					switch fault {
					case "missing":
						r.Session = nil
					case "uid":
						r.Session.UID = "other"
					case "generation":
						r.Session.Generation = 0
					case "revision":
						r.Session.Revision = 0
					case "partial":
						r.Done = false
					case "extra":
						r.Runtime = &meta.MQTTRuntimeView{}
					case "cursor":
						r.After.Topic = "unexpected"
					case "error":
						return errors.New("unavailable")
					case "cancel":
						cancel()
					}
					return nil
				}
				s.write = func(_ context.Context, _ uint64, b meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error) {
					switch fault {
					case "write-error":
						return meta.MQTTSourceBindingResult{}, errors.New("uncertain")
					case "write-receipt":
						return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASApplied, CurrentRevision: b.Revision + 1}, nil
					default:
						return meta.MQTTSourceBindingResult{Status: meta.MQTTSessionCASConflict}, nil
					}
				}
				if fault == "clock" {
					f.now = f.now.Add(-time.Hour)
				}
				if phase == "progress" {
					out, e := progress.Reconcile(ctx, f.qualification.Key)
					require.Error(t, e)
					require.Zero(t, out)
				} else {
					out, e := removal.Reconcile(ctx, f.qualification.Key)
					require.Error(t, e)
					require.Zero(t, out)
				}
				if fault != "conflict" && fault != "write-error" && fault != "write-receipt" {
					require.Equal(t, before, s.writes)
				}
			})
		}
	}
}
