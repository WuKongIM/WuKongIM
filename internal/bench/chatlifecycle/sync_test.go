package chatlifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/stretchr/testify/require"
)

func TestConversationSyncRequestAlwaysStartsFromZero(t *testing.T) {
	first := NewConversationSyncRequest("derived-user")
	require.Equal(t, target.ConversationSyncRequest{
		UID: "derived-user", Version: 0, LastMsgSeqs: "", MsgCount: 20, OnlyUnread: 0, Limit: 500,
	}, first)

	first.Version = 29
	first.LastMsgSeqs = "peer:1:99"
	first.MsgCount = 1
	first.Limit = 2

	require.Equal(t, target.ConversationSyncRequest{
		UID: "derived-user", Version: 0, LastMsgSeqs: "", MsgCount: 20, OnlyUnread: 0, Limit: 500,
	}, NewConversationSyncRequest("derived-user"))
}

func TestConversationSyncValidationAllows499AndClassifiesLimitAsHarnessInvalid(t *testing.T) {
	conversations := make([]target.ConversationSyncConversation, 499)
	for i := range conversations {
		conversations[i] = target.ConversationSyncConversation{
			ChannelID: fmt.Sprintf("peer-%d", i), ChannelType: 1,
		}
	}
	require.NoError(t, ValidateConversationSync(conversations))

	for _, count := range []int{500, 501} {
		t.Run(fmt.Sprintf("count_%d", count), func(t *testing.T) {
			rows := append([]target.ConversationSyncConversation(nil), conversations...)
			for len(rows) < count {
				rows = append(rows, target.ConversationSyncConversation{
					ChannelID: fmt.Sprintf("peer-%d", len(rows)), ChannelType: 1,
				})
			}

			err := ValidateConversationSync(rows)

			var validationErr *ConversationSyncValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, SyncClassificationHarnessInvalid, validationErr.Classification())
			require.Equal(t, "conversation_limit_reached", validationErr.ReasonCode())
		})
	}
}

func TestConversationSyncValidationRequiresStrictlyDescendingRecentSequences(t *testing.T) {
	valid := target.ConversationSyncConversation{
		ChannelID: "peer", ChannelType: 1,
		Recents: []target.ConversationSyncMessage{
			{ChannelID: "peer", ChannelType: 1, MessageSeq: 11},
			{ChannelID: "peer", ChannelType: 1, MessageSeq: 10},
			{ChannelID: "peer", ChannelType: 1, MessageSeq: 7},
		},
	}
	require.NoError(t, ValidateConversationSync([]target.ConversationSyncConversation{valid}))

	for _, test := range []struct {
		name string
		seqs []uint64
	}{
		{name: "duplicate", seqs: []uint64{11, 11}},
		{name: "increase", seqs: []uint64{10, 11}},
		{name: "zero", seqs: []uint64{1, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := target.ConversationSyncConversation{ChannelID: "peer", ChannelType: 1}
			for _, seq := range test.seqs {
				row.Recents = append(row.Recents, target.ConversationSyncMessage{
					ChannelID: "peer", ChannelType: 1, MessageSeq: seq,
				})
			}

			err := ValidateConversationSync([]target.ConversationSyncConversation{row})

			var validationErr *ConversationSyncValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, SyncClassificationProductFailure, validationErr.Classification())
			require.Equal(t, "recent_sequence_invalid", validationErr.ReasonCode())
			require.NotContains(t, err.Error(), "peer")
		})
	}
}

func TestConversationSyncValidationRequiresConversationAndRecentIdentity(t *testing.T) {
	tests := []struct {
		name string
		rows []target.ConversationSyncConversation
		code string
	}{
		{
			name: "missing conversation identity",
			rows: []target.ConversationSyncConversation{{ChannelType: 1}},
			code: "conversation_identity_invalid",
		},
		{
			name: "duplicate conversation",
			rows: []target.ConversationSyncConversation{
				{ChannelID: "peer", ChannelType: 1},
				{ChannelID: "peer", ChannelType: 1},
			},
			code: "duplicate_conversation",
		},
		{
			name: "recent channel id mismatch",
			rows: []target.ConversationSyncConversation{{
				ChannelID: "peer", ChannelType: 1,
				Recents: []target.ConversationSyncMessage{{ChannelID: "other", ChannelType: 1, MessageSeq: 1}},
			}},
			code: "recent_identity_mismatch",
		},
		{
			name: "recent channel type mismatch",
			rows: []target.ConversationSyncConversation{{
				ChannelID: "group", ChannelType: 2,
				Recents: []target.ConversationSyncMessage{{ChannelID: "group", ChannelType: 1, MessageSeq: 1}},
			}},
			code: "recent_identity_mismatch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateConversationSync(test.rows)

			var validationErr *ConversationSyncValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, SyncClassificationProductFailure, validationErr.Classification())
			require.Equal(t, test.code, validationErr.ReasonCode())
			require.NotContains(t, err.Error(), "peer")
			require.NotContains(t, err.Error(), "group")
			require.NotContains(t, err.Error(), "other")
		})
	}
}

func TestLoginSyncConnectsBeforeSyncAndRecordsSeparateLatency(t *testing.T) {
	var calls []string
	connector := loginSyncConnectorFunc(func(ctx context.Context, uid string) error {
		require.NoError(t, ctx.Err())
		require.Equal(t, "derived-user", uid)
		calls = append(calls, "connect")
		return nil
	})
	syncer := conversationSyncerFunc(func(ctx context.Context, req target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
		require.NoError(t, ctx.Err())
		require.Equal(t, NewConversationSyncRequest("derived-user"), req)
		calls = append(calls, "sync")
		return []target.ConversationSyncConversation{{ChannelID: "peer", ChannelType: 1}}, nil
	})
	now := sequenceClock(t,
		time.Unix(0, 0),
		time.Unix(0, int64(20*time.Millisecond)),
		time.Unix(0, int64(25*time.Millisecond)),
		time.Unix(0, int64(75*time.Millisecond)),
	)

	got, err := RunLoginSync(context.Background(), "derived-user", connector, syncer, now)

	require.NoError(t, err)
	require.Equal(t, []string{"connect", "sync"}, calls)
	require.True(t, got.TrafficReady)
	require.Equal(t, 20*time.Millisecond, got.GatewayConnectLatency)
	require.Equal(t, 50*time.Millisecond, got.ConversationSyncLatency)
	require.Len(t, got.Conversations, 1)
}

func TestLoginSyncConnectFailureNeverCallsSync(t *testing.T) {
	wantErr := errors.New("connect unavailable")
	connector := loginSyncConnectorFunc(func(context.Context, string) error { return wantErr })
	syncer := conversationSyncerFunc(func(context.Context, target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
		t.Fatal("conversation sync must not run after CONNECT failure")
		return nil, nil
	})
	now := sequenceClock(t, time.Unix(0, 0), time.Unix(0, int64(15*time.Millisecond)))

	got, err := RunLoginSync(context.Background(), "derived-user", connector, syncer, now)

	require.NotErrorIs(t, err, wantErr)
	require.EqualError(t, err, "login sync gateway connect failed")
	require.Equal(t, LoginSyncFailure{
		Stage: LoginSyncStageConnect, Reason: LoginSyncReasonTransport, Classification: SyncClassificationHarnessInvalid,
	}, mustLoginSyncFailure(t, err))
	require.Nil(t, errors.Unwrap(err))
	require.False(t, got.TrafficReady)
	require.Equal(t, 15*time.Millisecond, got.GatewayConnectLatency)
	require.Zero(t, got.ConversationSyncLatency)
}

func TestLoginSyncFailureOrInvalidResponseIsNeverTrafficReady(t *testing.T) {
	tests := []struct {
		name    string
		rows    []target.ConversationSyncConversation
		syncErr error
	}{
		{name: "http failure", syncErr: context.DeadlineExceeded},
		{name: "invalid response", rows: make([]target.ConversationSyncConversation, 500)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connector := loginSyncConnectorFunc(func(context.Context, string) error { return nil })
			syncer := conversationSyncerFunc(func(context.Context, target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
				return test.rows, test.syncErr
			})
			now := sequenceClock(t,
				time.Unix(0, 0), time.Unix(0, int64(time.Millisecond)),
				time.Unix(0, int64(2*time.Millisecond)), time.Unix(0, int64(5*time.Millisecond)),
			)

			got, err := RunLoginSync(context.Background(), "derived-user", connector, syncer, now)

			require.Error(t, err)
			if test.syncErr != nil {
				require.NotErrorIs(t, err, test.syncErr)
				require.Equal(t, LoginSyncFailure{
					Stage: LoginSyncStageSync, Reason: LoginSyncReasonTransport, Classification: SyncClassificationHarnessInvalid,
				}, mustLoginSyncFailure(t, err))
				require.Nil(t, errors.Unwrap(err))
			}
			require.False(t, got.TrafficReady)
			require.Equal(t, time.Millisecond, got.GatewayConnectLatency)
			require.Equal(t, 3*time.Millisecond, got.ConversationSyncLatency)
		})
	}
}

func TestLoginSyncPropagatesCanceledContextWithoutStartingWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	connector := loginSyncConnectorFunc(func(context.Context, string) error {
		t.Fatal("CONNECT must not start for an already canceled login")
		return nil
	})
	syncer := conversationSyncerFunc(func(context.Context, target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
		t.Fatal("sync must not start for an already canceled login")
		return nil, nil
	})

	got, err := RunLoginSync(ctx, "derived-user", connector, syncer, time.Now)

	require.Equal(t, LoginSyncFailure{
		Stage: LoginSyncStageConnect, Reason: LoginSyncReasonCanceled, Classification: SyncClassificationHarnessInvalid,
	}, mustLoginSyncFailure(t, err))
	require.Nil(t, errors.Unwrap(err))
	require.EqualError(t, err, "login sync canceled")
	require.False(t, got.TrafficReady)
}

func TestLoginSyncCancellationAfterConnectPreventsSync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	connector := loginSyncConnectorFunc(func(context.Context, string) error {
		cancel()
		return nil
	})
	syncer := conversationSyncerFunc(func(context.Context, target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
		t.Fatal("sync must not start after login context cancellation")
		return nil, nil
	})
	now := sequenceClock(t, time.Unix(0, 0), time.Unix(0, int64(time.Millisecond)))

	got, err := RunLoginSync(ctx, "derived-user", connector, syncer, now)

	require.Equal(t, LoginSyncFailure{
		Stage: LoginSyncStageConnect, Reason: LoginSyncReasonCanceled, Classification: SyncClassificationHarnessInvalid,
	}, mustLoginSyncFailure(t, err))
	require.Nil(t, errors.Unwrap(err))
	require.EqualError(t, err, "login sync canceled")
	require.False(t, got.TrafficReady)
	require.Equal(t, time.Millisecond, got.GatewayConnectLatency)
	require.Zero(t, got.ConversationSyncLatency)
}

func TestLoginSyncCancellationAfterHTTPResponsePreventsTraffic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	connector := loginSyncConnectorFunc(func(context.Context, string) error { return nil })
	syncer := conversationSyncerFunc(func(context.Context, target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
		cancel()
		return []target.ConversationSyncConversation{{ChannelID: "peer", ChannelType: 1}}, nil
	})
	now := sequenceClock(t,
		time.Unix(0, 0), time.Unix(0, int64(time.Millisecond)),
		time.Unix(0, int64(2*time.Millisecond)), time.Unix(0, int64(5*time.Millisecond)),
	)

	got, err := RunLoginSync(ctx, "derived-user", connector, syncer, now)

	require.Equal(t, LoginSyncFailure{
		Stage: LoginSyncStageSync, Reason: LoginSyncReasonCanceled, Classification: SyncClassificationHarnessInvalid,
	}, mustLoginSyncFailure(t, err))
	require.Nil(t, errors.Unwrap(err))
	require.EqualError(t, err, "login sync canceled")
	require.False(t, got.TrafficReady)
	require.Equal(t, 3*time.Millisecond, got.ConversationSyncLatency)
}

type loginSyncConnectorFunc func(context.Context, string) error

func (f loginSyncConnectorFunc) Connect(ctx context.Context, uid string) error {
	return f(ctx, uid)
}

type conversationSyncerFunc func(context.Context, target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error)

func (f conversationSyncerFunc) ConversationSync(ctx context.Context, req target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
	return f(ctx, req)
}

func mustLoginSyncFailure(t *testing.T, err error) LoginSyncFailure {
	t.Helper()
	failure, ok := LoginSyncFailureOf(err)
	require.True(t, ok, "error does not expose closed login sync diagnostics: %v", err)
	return failure
}

func sequenceClock(t *testing.T, times ...time.Time) func() time.Time {
	t.Helper()
	index := 0
	return func() time.Time {
		t.Helper()
		require.Less(t, index, len(times), "clock exhausted")
		value := times[index]
		index++
		return value
	}
}
