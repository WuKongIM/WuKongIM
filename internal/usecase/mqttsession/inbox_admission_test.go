package mqttsession_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

type inboxAdmissionStore struct {
	*groupSourceStore
	after  func(meta.MQTTInboxAdmission) error
	writes int
	reads  func(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
}

func (s *inboxAdmissionStore) ReadMQTT(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if s.reads != nil {
		return s.reads(ctx, q)
	}
	return s.groupSourceStore.ReadMQTT(ctx, q)
}
func (s *inboxAdmissionStore) CompareAndSwapMQTTInboxAdmission(ctx context.Context, expected uint64, row meta.MQTTInboxAdmission) (meta.MQTTInboxAdmissionResult, error) {
	s.writes++
	b := s.db.NewBatch()
	defer b.Close()
	result, err := b.CompareAndSwapMQTTInboxAdmission(7, expected, row)
	if err != nil {
		return meta.MQTTInboxAdmissionResult{}, err
	}
	if err = b.Commit(ctx); err != nil {
		return meta.MQTTInboxAdmissionResult{}, err
	}
	if s.after != nil {
		if err = s.after(row); err != nil {
			return meta.MQTTInboxAdmissionResult{}, err
		}
	}
	return *result, nil
}

type inboxAdmissionPrepare func(context.Context, meta.MQTTSourceBindingKey, app.SourceChannel) (app.PreparedInboxSource, error)

func (f inboxAdmissionPrepare) Prepare(ctx context.Context, k meta.MQTTSourceBindingKey, c app.SourceChannel) (app.PreparedInboxSource, error) {
	return f(ctx, k, c)
}

func setupInboxAdmission(t *testing.T) (*inboxSourceFixture, *inboxAdmissionStore, app.InboxAdmissionOptions) {
	t.Helper()
	f := setupInboxSource(t)
	s := &inboxAdmissionStore{groupSourceStore: f.store}
	_, err := s.db.HashSlot(7).UpsertChannelRuntimeMeta(context.Background(), meta.ChannelRuntimeMeta{ChannelID: f.channel.ID, ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1})
	require.NoError(t, err)
	require.NoError(t, s.db.HashSlot(7).UpsertChannel(context.Background(), meta.Channel{ChannelID: f.channel.ID, ChannelType: 1, DirectoryProjectionState: meta.DirectoryProjectionReady, DirectoryProjectionGeneration: 1}))
	return f, s, app.InboxAdmissionOptions{Store: s, Sources: f.sources, PageSize: 2, Now: func() time.Time { return f.now }}
}
func readInboxAdmission(t *testing.T, s *inboxAdmissionStore, id string) *meta.MQTTInboxAdmission {
	t.Helper()
	r, err := s.groupSourceStore.ReadMQTT(context.Background(), meta.MQTTRead{Kind: meta.MQTTReadInboxAdmission, AdmissionChannel: id})
	require.NoError(t, err)
	return r.Admission.Checkpoint
}

func TestMQTTInboxAdmissionTurnPreparesOfflineAfterDirectoryBarrier(t *testing.T) {
	f, s, o := setupInboxAdmission(t)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
	p, err := app.NewInboxAdmission(o)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		got, err := p.Advance(context.Background(), f.channel)
		require.NoError(t, err)
		require.Equal(t, i == 1, got.Ready)
	}
	require.Equal(t, uint8(2), readInboxAdmission(t, s, f.channel.ID).Participant)
	require.Equal(t, meta.MQTTSessionOffline, f.row(t).State)
	require.Equal(t, 2, f.protectCalls)
	before := s.writes
	again, err := p.Advance(context.Background(), f.channel)
	require.NoError(t, err)
	require.True(t, again.Ready)
	require.Equal(t, before, s.writes)
	require.Equal(t, 2, f.protectCalls)
}

func TestMQTTInboxAdmissionTurnRequiresCurrentCompletedDirectory(t *testing.T) {
	for _, mode := range []string{"runtime_missing", "channel_missing", "pending", "generation"} {
		t.Run(mode, func(t *testing.T) {
			f, s, o := setupInboxAdmission(t)
			s.reads = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				require.Equal(t, meta.MQTTReadInboxAdmission, q.Kind, "no qualification scan before the directory barrier")
				r, err := s.groupSourceStore.ReadMQTT(ctx, q)
				switch mode {
				case "runtime_missing":
					r.Admission.Runtime = nil
				case "channel_missing":
					r.Admission.Channel = nil
				case "pending":
					r.Admission.Channel.DirectoryProjectionState = meta.DirectoryProjectionPending
				case "generation":
					r.Admission.Channel.DirectoryProjectionGeneration = 2
				}
				return r, err
			}
			p, err := app.NewInboxAdmission(o)
			require.NoError(t, err)
			got, err := p.Advance(context.Background(), f.channel)
			require.NoError(t, err)
			require.False(t, got.Ready)
			require.Zero(t, s.writes)
			require.Zero(t, f.protectCalls)
		})
	}
}

func TestMQTTInboxAdmissionTurnResumesBoundedCandidatesAfterLostReceipt(t *testing.T) {
	f, s, o := setupInboxAdmission(t)
	left, _, err := channelid.DecodePersonChannel(f.channel.ID)
	require.NoError(t, err)
	// Use distinct lifetime keys to exercise persistent scan progress; the trusted
	// preparation fixture reports authoritative closed intent for each candidate.
	for i := range 5 {
		row := f.qualification
		row.Key.Owner.ID = left
		row.UID = left
		row.Topic = "wk/v1/users/" + base64.RawURLEncoding.EncodeToString([]byte(left)) + "/inbox"
		row.Key.ClientID = fmt.Sprintf("c%02d", i)
		_, err = s.CompareAndSwapMQTTSourceBinding(context.Background(), 0, row)
		require.NoError(t, err)
	}
	var visited []string
	o.Sources = inboxAdmissionPrepare(func(ctx context.Context, k meta.MQTTSourceBindingKey, ch app.SourceChannel) (app.PreparedInboxSource, error) {
		_, ok := ctx.Deadline()
		require.True(t, ok)
		require.Equal(t, f.channel, ch)
		visited = append(visited, k.ClientID)
		return app.PreparedInboxSource{}, nil
	})
	lost := errors.New("committed admission reply lost")
	s.after = func(r meta.MQTTInboxAdmission) error {
		if r.After.ClientID == "c01" {
			return lost
		}
		return nil
	}
	p, err := app.NewInboxAdmission(o)
	require.NoError(t, err)
	got, err := p.Advance(context.Background(), f.channel)
	require.ErrorIs(t, err, lost)
	require.Zero(t, got)
	require.Equal(t, []string{"c00", "c01"}, visited)
	require.Equal(t, "c01", readInboxAdmission(t, s, f.channel.ID).After.ClientID)
	s.after = nil
	got, err = p.Advance(context.Background(), f.channel)
	require.NoError(t, err)
	require.False(t, got.Ready)
	require.Equal(t, []string{"c00", "c01", "c02", "c03"}, visited)
	for i := 0; i < 3 && !got.Ready; i++ {
		got, err = p.Advance(context.Background(), f.channel)
		require.NoError(t, err)
	}
	require.True(t, got.Ready)
	require.Equal(t, []string{"c00", "c01", "c02", "c03", "c04", "client"}, visited)
}

func TestMQTTInboxAdmissionTurnCannotAdvanceFailedOrRecreatedSource(t *testing.T) {
	for _, mode := range []string{"source_error", "panic", "cancel", "delete", "stale_readback", "clock"} {
		t.Run(mode, func(t *testing.T) {
			f, s, o := setupInboxAdmission(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// Force the qualification to the first participant so the tested turn prepares it.
			first, _, err := channelid.DecodePersonChannel(f.channel.ID)
			require.NoError(t, err)
			if first != f.qualification.UID {
				q := f.qualification
				q.Key.Owner.ID, q.UID = first, first
				q.Key.ClientID = "first"
				_, err = s.CompareAndSwapMQTTSourceBinding(ctx, 0, q)
				require.NoError(t, err)
			}
			sourceErr := errors.New("source unavailable")
			o.Sources = inboxAdmissionPrepare(func(context.Context, meta.MQTTSourceBindingKey, app.SourceChannel) (app.PreparedInboxSource, error) {
				switch mode {
				case "source_error":
					return app.PreparedInboxSource{}, sourceErr
				case "panic":
					panic("secret callback detail")
				case "cancel":
					cancel()
				case "delete":
					require.NoError(t, s.db.HashSlot(7).DeleteChannel(ctx, f.channel.ID, 1))
				case "clock":
					f.now = f.now.Add(-time.Hour)
				}
				return app.PreparedInboxSource{}, nil
			})
			if mode == "stale_readback" {
				s.reads = func(c context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					r, e := s.groupSourceStore.ReadMQTT(c, q)
					if r.Admission != nil && r.Admission.Checkpoint != nil && r.Admission.Checkpoint.Revision > 1 {
						r.Admission.Checkpoint.Revision = 1
					}
					return r, e
				}
			}
			p, err := app.NewInboxAdmission(o)
			require.NoError(t, err)
			got, err := p.Advance(ctx, f.channel)
			require.Error(t, err)
			require.Zero(t, got)
			if mode == "source_error" {
				require.ErrorIs(t, err, sourceErr)
			}
			if mode == "panic" {
				require.ErrorIs(t, err, app.ErrSubscriptionCallback)
				require.NotContains(t, err.Error(), "secret")
			}
			if mode == "cancel" {
				require.ErrorIs(t, err, context.Canceled)
			}
			cp := readInboxAdmission(t, s, f.channel.ID)
			require.NotNil(t, cp)
			require.Less(t, cp.Participant, uint8(2))
		})
	}
}

func TestMQTTInboxAdmissionTurnRejectsMalformedQualificationPages(t *testing.T) {
	for _, mode := range []string{"duplicate", "foreign", "short_nonfinal", "cursor", "mixed", "removed"} {
		t.Run(mode, func(t *testing.T) {
			f, s, o := setupInboxAdmission(t)
			s.reads = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				if q.Kind != meta.MQTTReadSourceCandidates {
					return s.groupSourceStore.ReadMQTT(ctx, q)
				}
				row := f.qualification
				row.Key.Owner = q.Owner
				row.UID = q.Owner.ID
				r := meta.MQTTReadResult{Bindings: []meta.MQTTSourceBinding{row}, Done: true, After: q.After}
				switch mode {
				case "duplicate":
					r.Bindings = append(r.Bindings, row)
				case "foreign":
					r.Bindings[0].Key.Owner.ID = "foreign"
				case "short_nonfinal":
					r.Done = false
				case "cursor":
					r.After.Binding = row.Key
				case "mixed":
					r.Admission = &meta.MQTTInboxAdmissionView{}
				case "removed":
					r.Bindings[0].Stage = meta.MQTTBindingRemoved
				}
				return r, nil
			}
			p, err := app.NewInboxAdmission(o)
			require.NoError(t, err)
			got, err := p.Advance(context.Background(), f.channel)
			require.ErrorIs(t, err, app.ErrEvidence)
			require.Zero(t, got)
			require.Zero(t, f.protectCalls)
		})
	}
}

func TestMQTTInboxAdmissionTurnAcceptsPreparedSourceAfterIntentAdvances(t *testing.T) {
	f, s, o := setupInboxAdmission(t)
	prepared, err := f.prepare()
	require.NoError(t, err)
	f.project.establish = func(_ context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		return projectionReceipt(r), nil
	}
	active, err := f.subscriptions.Reconcile(context.Background(), f.connection.Owner, f.intent.Topic)
	require.NoError(t, err)
	q := f.qualification
	q.Revision++
	q.IntentRevision = active.Revision
	q.Stage = meta.MQTTBindingActive
	q.DiscoveryDone = true
	_, err = s.CompareAndSwapMQTTSourceBinding(context.Background(), 1, q)
	require.NoError(t, err)
	require.Greater(t, q.IntentRevision, prepared.Binding.IntentRevision)
	p, err := app.NewInboxAdmission(o)
	require.NoError(t, err)
	var got app.InboxAdmissionProgress
	for range 2 {
		got, err = p.Advance(context.Background(), f.channel)
		require.NoError(t, err)
	}
	require.True(t, got.Ready)
}
