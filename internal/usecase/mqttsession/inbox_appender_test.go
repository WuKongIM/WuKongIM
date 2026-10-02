package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	appendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type inboxAppendNext func(context.Context, appendcontract.AppendBatchRequest) (appendcontract.AppendBatchResult, error)

func (f inboxAppendNext) AppendBatch(ctx context.Context, q appendcontract.AppendBatchRequest) (appendcontract.AppendBatchResult, error) {
	return f(ctx, q)
}

type inboxAppendDirectory func(context.Context, app.SourceChannel) error

func (f inboxAppendDirectory) Admit(ctx context.Context, ch app.SourceChannel) error {
	return f(ctx, ch)
}

func inboxAppendRequest(f *inboxSourceFixture) appendcontract.AppendBatchRequest {
	return appendcontract.AppendBatchRequest{ChannelID: appendcontract.ChannelID{ID: f.channel.ID, Type: 1}, ExpectedEpoch: 1, ExpectedLeaderEpoch: 1, CommitMode: appendcontract.CommitModeQuorum,
		Messages: []appendcontract.Message{{MessageID: 99, Payload: []byte("native body")}, {MessageID: 100, Payload: []byte("second body")}}}
}

func TestMQTTInboxAppenderPreparesOfflineBeforeBusinessAppend(t *testing.T) {
	f, s, options := setupInboxAdmission(t)
	require.NoError(t, f.service.Disconnect(context.Background(), app.DisconnectCommand{Owner: f.connection.Owner, Normal: true}))
	admission, err := app.NewInboxAdmission(options)
	require.NoError(t, err)
	q := inboxAppendRequest(f)
	before := q.Clone()
	calls := 0
	next := inboxAppendNext(func(ctx context.Context, actual appendcontract.AppendBatchRequest) (appendcontract.AppendBatchResult, error) {
		calls++
		checkpoint := readInboxAdmission(t, s, f.channel.ID)
		require.Equal(t, uint8(2), checkpoint.Participant)
		cursors, err := s.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: "main", ClientID: "client", SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation, Limit: 64})
		require.NoError(t, err)
		require.Len(t, cursors.DeliveryCursors, 1)
		require.Equal(t, uint64(10), cursors.DeliveryCursors[0].StartAfter)
		require.Equal(t, meta.MQTTSessionOffline, f.row(t).State)
		want := before
		want.ExpectedRouteGeneration = 1
		require.Equal(t, want, actual)
		return appendcontract.AppendBatchResult{Items: []appendcontract.AppendBatchItemResult{{MessageID: 99, MessageSeq: 11}, {MessageID: 100, MessageSeq: 12}}}, nil
	})
	p, err := app.NewInboxAppender(app.InboxAppenderOptions{Admission: admission, Directory: inboxAppendDirectory(func(context.Context, app.SourceChannel) error {
		t.Fatal("ready directory must not be readmitted")
		return nil
	}), Next: next})
	require.NoError(t, err)
	got, err := p.AppendBatch(context.Background(), q)
	require.NoError(t, err)
	require.Len(t, got.Items, 2)
	require.Equal(t, before, q)
	require.Equal(t, 1, calls)
	require.Equal(t, 2, f.protectCalls)
	writes := s.writes
	_, err = p.AppendBatch(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, writes, s.writes)
	require.Equal(t, 2, f.protectCalls)
	require.Equal(t, 2, calls)
}

func TestMQTTInboxAppenderRejectsUnfinishedOrChangedEvidence(t *testing.T) {
	for _, mode := range []string{"turn-budget", "epoch", "leader-epoch", "route", "directory-error", "directory-panic", "source-error", "source-panic", "cancel-source", "final-removed", "final-checkpoint", "final-directory", "final-mixed"} {
		t.Run(mode, func(t *testing.T) {
			f, s, options := setupInboxAdmission(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			admission, err := app.NewInboxAdmission(options)
			require.NoError(t, err)
			q := inboxAppendRequest(f)
			directoryCalls, appendCalls := 0, 0
			want := error(nil)
			o := app.InboxAppenderOptions{Admission: admission, Attempts: 2, Directory: inboxAppendDirectory(func(context.Context, app.SourceChannel) error {
				directoryCalls++
				if mode == "directory-panic" {
					panic("sensitive")
				}
				return errors.New("directory unavailable")
			}), Next: inboxAppendNext(func(context.Context, appendcontract.AppendBatchRequest) (appendcontract.AppendBatchResult, error) {
				appendCalls++
				return appendcontract.AppendBatchResult{}, nil
			})}
			switch mode {
			case "turn-budget":
				o.Attempts = 1
				want = app.ErrInboxAdmissionPending
			case "epoch":
				q.ExpectedEpoch = 2
				want = appendcontract.ErrStaleRoute
			case "leader-epoch":
				q.ExpectedLeaderEpoch = 2
				want = appendcontract.ErrStaleRoute
			case "route":
				q.ExpectedRouteGeneration = 2
				want = appendcontract.ErrStaleRoute
			case "directory-error", "directory-panic":
				require.NoError(t, s.db.HashSlot(7).DeleteChannel(ctx, f.channel.ID, 1))
			case "source-error":
				f.protect = func(context.Context, int, app.SourceChannel) (app.ProtectedSource, error) {
					return app.ProtectedSource{}, errors.New("source unavailable")
				}
			case "source-panic":
				f.protect = func(context.Context, int, app.SourceChannel) (app.ProtectedSource, error) { panic("sensitive") }
			case "cancel-source":
				f.protect = func(context.Context, int, app.SourceChannel) (app.ProtectedSource, error) {
					cancel()
					return app.ProtectedSource{}, nil
				}
				want = context.Canceled
			default:
				readyReads := 0
				s.reads = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
					r, err := s.groupSourceStore.ReadMQTT(ctx, q)
					if r.Admission != nil && r.Admission.Checkpoint != nil && r.Admission.Checkpoint.Participant == 2 {
						readyReads++
						if readyReads == 2 {
							switch mode {
							case "final-removed":
								r.Admission.Channel = nil
							case "final-checkpoint":
								r.Admission.Checkpoint.Revision++
							case "final-directory":
								r.Admission.Runtime.DirectoryGeneration++
							case "final-mixed":
								r.Bindings = []meta.MQTTSourceBinding{f.qualification}
							}
						}
					}
					return r, err
				}
			}
			p, err := app.NewInboxAppender(o)
			require.NoError(t, err)
			got, err := p.AppendBatch(ctx, q)
			require.Error(t, err)
			require.Zero(t, got)
			require.Zero(t, appendCalls)
			if want != nil {
				require.ErrorIs(t, err, want)
			}
			if mode == "directory-error" || mode == "directory-panic" {
				require.Equal(t, 1, directoryCalls)
			} else {
				require.Zero(t, directoryCalls)
			}
			if mode == "source-panic" || mode == "directory-panic" {
				require.NotContains(t, err.Error(), "sensitive")
			}
			if mode == "turn-budget" {
				require.Equal(t, uint8(1), readInboxAdmission(t, s, f.channel.ID).Participant)
			}
		})
	}
}

func TestMQTTInboxAppenderPreservesOtherBatchesAndCommittedReceipt(t *testing.T) {
	for _, mode := range []string{"group", "commands", "mixed", "empty", "malformed-person", "missing-epoch", "local-commit", "committed-cancel", "append-error", "append-panic"} {
		t.Run(mode, func(t *testing.T) {
			f, _, options := setupInboxAdmission(t)
			admission, err := app.NewInboxAdmission(options)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			q := inboxAppendRequest(f)
			switch mode {
			case "group":
				q.ChannelID = appendcontract.ChannelID{ID: "group", Type: 2}
			case "commands":
				q.ChannelID.ID += "cmd"
				for i := range q.Messages {
					q.Messages[i].SyncOnce = true
				}
			case "mixed":
				q.Messages[0].SyncOnce = true
			case "empty":
				q.Messages = nil
			case "malformed-person":
				q.ChannelID.ID = "not-a-person"
			case "missing-epoch":
				q.ExpectedEpoch = 0
			case "local-commit":
				q.CommitMode = appendcontract.CommitModeLocal
			}
			calls := 0
			appendErr := errors.New("append outcome unknown")
			p, err := app.NewInboxAppender(app.InboxAppenderOptions{Admission: admission, Directory: inboxAppendDirectory(func(context.Context, app.SourceChannel) error { t.Fatal("unexpected directory creation"); return nil }), Next: inboxAppendNext(func(_ context.Context, actual appendcontract.AppendBatchRequest) (appendcontract.AppendBatchResult, error) {
				calls++
				if mode == "group" || mode == "commands" {
					require.Equal(t, q, actual)
				}
				if mode == "committed-cancel" {
					cancel()
				}
				if mode == "append-error" {
					return appendcontract.AppendBatchResult{}, appendErr
				}
				if mode == "append-panic" {
					panic("sensitive")
				}
				return appendcontract.AppendBatchResult{Items: []appendcontract.AppendBatchItemResult{{MessageID: 99, MessageSeq: 11}}}, nil
			})})
			require.NoError(t, err)
			got, err := p.AppendBatch(ctx, q)
			switch mode {
			case "group", "commands", "committed-cancel":
				require.NoError(t, err)
				require.Len(t, got.Items, 1)
				require.Equal(t, 1, calls)
			case "append-error":
				require.ErrorIs(t, err, appendErr)
				require.Equal(t, 1, calls)
			case "append-panic":
				require.ErrorIs(t, err, appendcontract.ErrAppendFailed)
				require.NotContains(t, err.Error(), "sensitive")
				require.Equal(t, 1, calls)
			default:
				require.ErrorIs(t, err, appendcontract.ErrInvalidCommand)
				require.Zero(t, calls)
				require.Zero(t, got)
			}
			if mode == "group" || mode == "commands" {
				require.Zero(t, f.protectCalls)
			}
		})
	}
}

func TestMQTTInboxAppenderValidatesBounds(t *testing.T) {
	f, _, options := setupInboxAdmission(t)
	admission, err := app.NewInboxAdmission(options)
	require.NoError(t, err)
	base := app.InboxAppenderOptions{Admission: admission, Directory: inboxAppendDirectory(func(context.Context, app.SourceChannel) error { return nil }), Next: inboxAppendNext(func(context.Context, appendcontract.AppendBatchRequest) (appendcontract.AppendBatchResult, error) {
		return appendcontract.AppendBatchResult{}, nil
	})}
	for _, change := range []func(*app.InboxAppenderOptions){func(o *app.InboxAppenderOptions) { o.Admission = nil }, func(o *app.InboxAppenderOptions) { o.Directory = nil }, func(o *app.InboxAppenderOptions) { o.Next = nil }, func(o *app.InboxAppenderOptions) { o.Attempts = -1 }, func(o *app.InboxAppenderOptions) { o.Attempts = 257 }, func(o *app.InboxAppenderOptions) { o.Retry = -1 }, func(o *app.InboxAppenderOptions) { o.Retry = 2 * time.Second }, func(o *app.InboxAppenderOptions) { o.Timeout = -1 }, func(o *app.InboxAppenderOptions) { o.Timeout = 2 * time.Minute }} {
		o := base
		change(&o)
		_, err := app.NewInboxAppender(o)
		require.Error(t, err)
	}
	p, err := app.NewInboxAppender(base)
	require.NoError(t, err)
	_, err = p.AppendBatch(nil, inboxAppendRequest(f))
	require.Error(t, err)
}
