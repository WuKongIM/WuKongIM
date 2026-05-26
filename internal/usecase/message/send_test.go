package message

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/runtime/userlimit"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

var fixedSendNow = time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

func TestSendHookInvokedAfterPermissionAndMutatesDurablePayload(t *testing.T) {
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(frame.ChannelTypeGroup)}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	cluster := &fakeChannelAppender{sendReplies: []fakeChannelAppenderSendReply{{result: channel.AppendResult{MessageID: 501, MessageSeq: 51}}}}
	hook := &recordingSendHook{mutate: func(cmd SendCommand) SendCommand {
		cmd.Payload = []byte("mutated")
		cmd.Topic = "hook-topic"
		return cmd
	}}
	app := New(Options{Now: fixedNowFn, ChannelAppender: cluster, PermissionStore: permissions, SendHook: hook})

	result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("original")})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Len(t, hook.calls, 1)
	require.Equal(t, "g1", hook.calls[0].ChannelID)
	require.Equal(t, []byte("original"), hook.calls[0].Payload)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, []byte("mutated"), cluster.sendRequests[0].Message.Payload)
	require.Equal(t, "hook-topic", cluster.sendRequests[0].Message.Topic)
}

func TestSendHookNotInvokedWhenPermissionRejects(t *testing.T) {
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u1", int64(frame.ChannelTypePerson))] = metadb.Channel{ChannelID: "u1", ChannelType: int64(frame.ChannelTypePerson), SendBan: 1}
	hook := &recordingSendHook{}
	app := New(Options{Now: fixedNowFn, PermissionStore: permissions, SendHook: hook})

	result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("hi")})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSendBan, result.Reason)
	require.Empty(t, hook.calls)
}

func TestSendHookRequestScopedSyncOnceInvokedExactlyOnce(t *testing.T) {
	cluster := &fakeChannelAppender{sendReplies: []fakeChannelAppenderSendReply{{result: channel.AppendResult{MessageID: 502, MessageSeq: 52}}}}
	hook := &recordingSendHook{mutate: func(cmd SendCommand) SendCommand {
		cmd.Payload = []byte("scoped-mutated")
		return cmd
	}}
	app := New(Options{Now: fixedNowFn, ChannelAppender: cluster, CommittedDispatcher: &recordingCommittedDispatcher{}, SendHook: hook})

	result, err := app.Send(context.Background(), SendCommand{Framer: frame.Framer{SyncOnce: true}, FromUID: "system", RequestSubscribers: []string{"u1", "u2"}, Payload: []byte("scoped")})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Len(t, hook.calls, 1)
	require.NotEmpty(t, hook.calls[0].ChannelID)
	require.Equal(t, frame.ChannelTypeTemp, hook.calls[0].ChannelType)
	require.Equal(t, []byte("scoped"), hook.calls[0].Payload)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, []byte("scoped-mutated"), cluster.sendRequests[0].Message.Payload)
}

func TestSendHookRequestScopedNoPersistRealtimePreservesScopedEnvelope(t *testing.T) {
	realtime := &recordingRealtimeDispatcher{}
	ids := &sequenceMessageIDGenerator{next: 951}
	hook := &recordingSendHook{mutate: func(cmd SendCommand) SendCommand {
		cmd.Payload = []byte("scoped-realtime-mutated")
		cmd.ChannelID = "malicious"
		cmd.ChannelType = frame.ChannelTypeGroup
		cmd.RequestSubscribers = []string{"mallory"}
		return cmd
	}}
	app := New(Options{Now: fixedNowFn, MessageIDs: ids, RealtimeDispatcher: realtime, SendHook: hook})

	result, err := app.Send(context.Background(), SendCommand{Framer: frame.Framer{NoPersist: true, SyncOnce: true}, FromUID: "system", RequestSubscribers: []string{"u1", "u2"}, Payload: []byte("scoped")})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Len(t, hook.calls, 1)
	require.Len(t, realtime.calls, 1)
	scoped, err := runtimechannelid.RequestSubscriberChannelFor([]string{"u1", "u2"})
	require.NoError(t, err)
	require.Equal(t, scoped.CommandChannelID, realtime.calls[0].Message.ChannelID)
	require.Equal(t, frame.ChannelTypeTemp, realtime.calls[0].Message.ChannelType)
	require.Equal(t, []string{"u1", "u2"}, realtime.calls[0].MessageScopedUIDs)
	require.Equal(t, []byte("scoped-realtime-mutated"), realtime.calls[0].Message.Payload)
}

func TestSendHookUnsupportedChannelTypeDoesNotInvokeHook(t *testing.T) {
	hook := &recordingSendHook{reason: frame.ReasonPayloadDecodeError}
	app := New(Options{Now: fixedNowFn, SendHook: hook})

	result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "bad", ChannelType: 99, Payload: []byte("bad")})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonNotSupportChannelType, result.Reason)
	require.Empty(t, hook.calls)
}

func TestSendHookReasonRejectsBeforeAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	hook := &recordingSendHook{reason: frame.ReasonPayloadDecodeError}
	app := New(Options{Now: fixedNowFn, ChannelAppender: cluster, SendHook: hook})

	result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("bad")})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonPayloadDecodeError, result.Reason)
	require.Empty(t, cluster.sendRequests)
	require.Len(t, hook.calls, 1)
}

func TestSendHookPluginOriginDepthAndLimit(t *testing.T) {
	hook := &recordingSendHook{}
	app := New(Options{Now: fixedNowFn, ChannelAppender: &fakeChannelAppender{}, SendHook: hook})

	_, err := app.Send(context.Background(), SendCommand{Origin: SendOriginPlugin, HookDepth: DefaultPluginSendMaxHookDepth, FromUID: "plugin-a", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("loop")})
	require.ErrorIs(t, err, ErrSendHookDepthExceeded)
	require.Empty(t, hook.calls)

	_, err = app.Send(context.Background(), SendCommand{Origin: SendOriginPlugin, FromUID: "plugin-a", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("hook")})
	require.NoError(t, err)
	require.Len(t, hook.calls, 1)
	require.Equal(t, SendOriginPlugin, hook.calls[0].Origin)
	require.Equal(t, 1, hook.calls[0].HookDepth)
}

func TestSendHookSkipPluginHooksBypassesHook(t *testing.T) {
	cluster := &fakeChannelAppender{sendReplies: []fakeChannelAppenderSendReply{{result: channel.AppendResult{MessageID: 503, MessageSeq: 53}}}}
	hook := &recordingSendHook{reason: frame.ReasonPayloadDecodeError}
	app := New(Options{Now: fixedNowFn, ChannelAppender: cluster, SendHook: hook})

	result, err := app.Send(context.Background(), SendCommand{SkipPluginHooks: true, FromUID: "u1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("skip")})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Empty(t, hook.calls)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendUserRateLimitRejectsBeforeExpensiveWork(t *testing.T) {
	permissions := newFakePermissionStore()
	cluster := &fakeChannelAppender{}
	hook := &recordingSendHook{}
	limiter := &recordingUserSendLimiter{decision: userlimit.Decision{Allowed: false, Reason: userlimit.ReasonRateExceeded, RetryAfter: time.Second}}
	app := New(Options{Now: fixedNowFn, ChannelAppender: cluster, PermissionStore: permissions, SendHook: hook, UserSendLimiter: limiter})

	result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("hot")})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonRateLimit, result.Reason)
	require.Len(t, limiter.calls, 1)
	require.Equal(t, "u1", limiter.calls[0].UID)
	require.Equal(t, "g1", limiter.calls[0].ChannelID)
	require.Equal(t, uint8(frame.ChannelTypeGroup), limiter.calls[0].ChannelType)
	require.Zero(t, permissions.getChannelCalls)
	require.Empty(t, hook.calls)
	require.Empty(t, cluster.sendRequests)
}

func TestSendUserRateLimitPassesSystemUIDAndPluginOrigin(t *testing.T) {
	limiter := &recordingUserSendLimiter{decision: userlimit.Decision{Allowed: true}}
	app := New(Options{Now: fixedNowFn, ChannelAppender: &fakeChannelAppender{}, UserSendLimiter: limiter, SystemUIDs: fakeSystemUIDChecker{"sys": true}})

	_, err := app.Send(context.Background(), SendCommand{Origin: SendOriginPlugin, FromUID: "sys", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("plugin")})

	require.NoError(t, err)
	require.Len(t, limiter.calls, 1)
	require.True(t, limiter.calls[0].IsSystemUID)
	require.Equal(t, string(SendOriginPlugin), limiter.calls[0].Origin)
}

type recordingSendHook struct {
	calls  []SendCommand
	mutate func(SendCommand) SendCommand
	reason frame.ReasonCode
	err    error
}

func (h *recordingSendHook) BeforeSend(_ context.Context, cmd SendCommand) (SendCommand, frame.ReasonCode, error) {
	copied := cmd
	copied.Payload = append([]byte(nil), cmd.Payload...)
	h.calls = append(h.calls, copied)
	if h.mutate != nil {
		cmd = h.mutate(cmd)
	}
	return cmd, h.reason, h.err
}

func TestSendRejectsUnauthenticatedSender(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.ErrorIs(t, err, ErrUnauthenticatedSender)
	require.Equal(t, SendResult{}, result)
}

func TestSendBatchSingleItemMatchesSend(t *testing.T) {
	newApp := func() (*App, *fakeChannelAppender) {
		cluster := &fakeChannelAppender{sendBatchReplies: []fakeChannelAppenderSendBatchReply{{
			result: channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{
				MessageID:  42,
				MessageSeq: 7,
				Message:    channel.Message{MessageID: 42, MessageSeq: 7},
			}}},
		}}}
		return New(Options{
			Now:             fixedNowFn,
			ChannelAppender: cluster,
		}), cluster
	}
	cmd := SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientMsgNo: "single-batch",
	}

	sendApp, sendCluster := newApp()
	sendResult, sendErr := sendApp.Send(context.Background(), cmd)
	batchApp, batchCluster := newApp()
	batchResults := batchApp.SendBatch([]SendBatchItem{{Context: context.Background(), Command: cmd}})

	require.Len(t, batchResults, 1)
	require.Equal(t, sendResult, batchResults[0].Result)
	require.Equal(t, sendErr, batchResults[0].Err)
	require.Zero(t, sendCluster.sendAppendCalls)
	require.Zero(t, batchCluster.sendAppendCalls)
	require.Len(t, sendCluster.sendBatchRequests, 1)
	require.Len(t, batchCluster.sendBatchRequests, 1)
}

func TestSendBatchUsesChannelAppendBatchForSameChannelSegment(t *testing.T) {
	cluster := &fakeChannelAppender{sendBatchReplies: []fakeChannelAppenderSendBatchReply{{
		result: channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{
			{MessageID: 101, MessageSeq: 1, Message: channel.Message{MessageID: 101, MessageSeq: 1}},
			{MessageID: 102, MessageSeq: 2, Message: channel.Message{MessageID: 102, MessageSeq: 2}},
		}},
	}}}
	app := New(Options{Now: fixedNowFn, ChannelAppender: cluster})

	results := app.SendBatch([]SendBatchItem{
		{Context: context.Background(), Command: SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("one"), ClientMsgNo: "b1"}},
		{Context: context.Background(), Command: SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("two"), ClientMsgNo: "b2"}},
	})

	require.Len(t, results, 2)
	require.NoError(t, results[0].Err)
	require.NoError(t, results[1].Err)
	require.Equal(t, uint64(1), results[0].Result.MessageSeq)
	require.Equal(t, uint64(2), results[1].Result.MessageSeq)
	require.Empty(t, cluster.sendRequests)
	require.Len(t, cluster.sendBatchRequests, 1)
	require.Len(t, cluster.sendBatchRequests[0].Messages, 2)
	require.Equal(t, "b1", cluster.sendBatchRequests[0].Messages[0].ClientMsgNo)
	require.Equal(t, "b2", cluster.sendBatchRequests[0].Messages[1].ClientMsgNo)
}

func TestSendDurableUsesChannelAppenderWithoutMetaRefreshDependency(t *testing.T) {
	appender := &fakeChannelAppender{sendBatchReplies: []fakeChannelAppenderSendBatchReply{{
		result: channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{
			MessageID:  501,
			MessageSeq: 9,
			Message:    channel.Message{MessageID: 501, MessageSeq: 9},
		}}},
	}}}
	app := New(Options{Now: fixedNowFn, ChannelAppender: appender})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g-plane",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
		ClientMsgNo: "plane-direct",
	})

	require.NoError(t, err)
	require.Equal(t, uint64(9), result.MessageSeq)
	require.Len(t, appender.sendBatchRequests, 1)
	require.Equal(t, "plane-direct", appender.sendBatchRequests[0].Messages[0].ClientMsgNo)
}

func TestSendReturnsUnsupportedChannelType(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "slot-1",
		ChannelType: 99,
		ClientSeq:   12,
		ClientMsgNo: "m3",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonNotSupportChannelType, result.Reason)
	require.Zero(t, result.MessageID)
	require.Zero(t, result.MessageSeq)
}

func TestSendReturnsChannelAppenderRequiredWhenAppenderNotConfigured(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.ErrorIs(t, err, ErrChannelAppenderRequired)
	require.Equal(t, SendResult{}, result)
}

func TestSendNoPersistReturnsSuccessWithoutChannelAppender(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Zero(t, result.MessageID)
	require.Zero(t, result.MessageSeq)
}

func TestSendNoPersistSkipsDurableAppendAndCommittedDispatch(t *testing.T) {
	cluster := &fakeChannelAppender{}
	dispatcher := &recordingCommittedDispatcher{}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Zero(t, result.MessageID)
	require.Zero(t, result.MessageSeq)
	require.Empty(t, cluster.sendRequests)
	require.Empty(t, dispatcher.calls)
}

func TestSendNoPersistStillChecksPermissions(t *testing.T) {
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u1", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:   "u1",
		ChannelType: int64(frame.ChannelTypePerson),
		SendBan:     1,
	}
	app := New(Options{
		Now:             fixedNowFn,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSendBan, result.Reason)
}

func TestSendRequestScopedRejectsWithoutSyncOnce(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:            "system",
		RequestSubscribers: []string{"u1"},
		Payload:            []byte("cmd"),
	})

	require.ErrorIs(t, err, ErrRequestSubscribersRequireSyncOnce)
	require.Equal(t, SendResult{}, result)
}

func TestSendRequestScopedRejectsChannelIDConflict(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{SyncOnce: true},
		FromUID:            "system",
		ChannelID:          "g1",
		ChannelType:        frame.ChannelTypeGroup,
		RequestSubscribers: []string{"u1"},
		Payload:            []byte("cmd"),
	})

	require.ErrorIs(t, err, ErrRequestSubscribersConflictChannel)
	require.Equal(t, SendResult{}, result)
}

func TestSendRequestScopedRejectsEmptySubscribers(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{SyncOnce: true},
		FromUID:            "system",
		RequestSubscribers: []string{" ", ""},
		Payload:            []byte("cmd"),
	})

	require.ErrorIs(t, err, ErrRequestSubscribersRequired)
	require.Equal(t, SendResult{}, result)
}

func TestSendRequestScopedDurableAppendsTempCommandAndDispatchesSubscribers(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelAppender{
		sendFn: func(_ context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
			msg := req.Message
			msg.MessageID = 808
			msg.MessageSeq = 18
			return channel.AppendResult{MessageID: 808, MessageSeq: 18, Message: msg}, nil
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{SyncOnce: true},
		FromUID:            "system",
		ChannelType:        frame.ChannelTypeGroup,
		RequestSubscribers: []string{"u1", "u2", "u1"},
		Payload:            []byte("cmd"),
		ClientMsgNo:        "request-scoped-1",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(808), result.MessageID)
	require.Equal(t, uint64(18), result.MessageSeq)

	scoped, err := runtimechannelid.RequestSubscriberChannelFor([]string{"u1", "u2"})
	require.NoError(t, err)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: scoped.CommandChannelID, Type: frame.ChannelTypeTemp}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, scoped.CommandChannelID, cluster.sendRequests[0].Message.ChannelID)
	require.Equal(t, frame.ChannelTypeTemp, cluster.sendRequests[0].Message.ChannelType)
	require.Equal(t, frame.Framer{SyncOnce: true}, cluster.sendRequests[0].Message.Framer)
	require.Equal(t, "system", cluster.sendRequests[0].Message.FromUID)
	require.Equal(t, []byte("cmd"), cluster.sendRequests[0].Message.Payload)

	require.Len(t, dispatcher.calls, 1)
	require.Equal(t, []string{"u1", "u2"}, dispatcher.calls[0].MessageScopedUIDs)
	require.Equal(t, scoped.CommandChannelID, dispatcher.calls[0].Message.ChannelID)
	require.Equal(t, frame.ChannelTypeTemp, dispatcher.calls[0].Message.ChannelType)
}

func TestSendRequestScopedDurableSubmitsCMDConversationIntent(t *testing.T) {
	intentSink := &recordingCMDConversationIntentSink{accepted: true}
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelAppender{
		sendFn: func(_ context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
			msg := req.Message
			msg.MessageID = 808
			msg.MessageSeq = 18
			return channel.AppendResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}, nil
		},
	}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		CommittedDispatcher:    dispatcher,
		CMDConversationIntents: intentSink,
	})

	_, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{SyncOnce: true},
		FromUID:            "system",
		RequestSubscribers: []string{"system", "u2", "u2"},
		Payload:            []byte("cmd"),
	})

	require.NoError(t, err)
	require.Len(t, intentSink.intents, 1)
	require.Len(t, dispatcher.calls, 1)
	require.True(t, dispatcher.calls[0].CMDConversationIntentSubmitted)
	require.Equal(t, uint64(18), intentSink.intents[0].MessageSeq)
	require.Equal(t, map[string]uint64{"system": 18, "u2": 0}, intentSink.intents[0].UserReadSeqs)
}

func TestSendRequestScopedDurableLeavesIntentSubmittedFalseOnIntentFailure(t *testing.T) {
	intentSink := &recordingCMDConversationIntentSink{err: errors.New("remote owner down")}
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelAppender{
		sendFn: func(_ context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
			msg := req.Message
			msg.MessageID = 809
			msg.MessageSeq = 19
			return channel.AppendResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}, nil
		},
	}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		CommittedDispatcher:    dispatcher,
		CMDConversationIntents: intentSink,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{SyncOnce: true},
		FromUID:            "system",
		RequestSubscribers: []string{"system", "u2"},
		Payload:            []byte("cmd"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Len(t, intentSink.intents, 1)
	require.Len(t, dispatcher.calls, 1)
	require.False(t, dispatcher.calls[0].CMDConversationIntentSubmitted)
}

func TestSendRequestScopedDurableRequiresCommittedDispatcherBeforeAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{SyncOnce: true},
		FromUID:            "system",
		RequestSubscribers: []string{"u1"},
		Payload:            []byte("cmd"),
	})

	require.ErrorIs(t, err, ErrCommittedDispatcherRequired)
	require.Equal(t, SendResult{}, result)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRequestScopedDurableReturnsDispatchError(t *testing.T) {
	dispatchErr := errors.New("dispatcher stopped")
	dispatcher := &recordingCommittedDispatcher{err: dispatchErr}
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 810, MessageSeq: 20}},
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{SyncOnce: true},
		FromUID:            "system",
		RequestSubscribers: []string{"u1"},
		Payload:            []byte("cmd"),
	})

	require.ErrorIs(t, err, dispatchErr)
	require.Equal(t, SendResult{}, result)
	require.Len(t, cluster.sendRequests, 1)
	require.Len(t, dispatcher.calls, 1)
}

func TestSendRequestScopedNoPersistDispatchesRealtimeScopedEnvelope(t *testing.T) {
	cluster := &fakeChannelAppender{}
	realtime := &recordingRealtimeDispatcher{}
	ids := &sequenceMessageIDGenerator{next: 909}
	app := New(Options{
		Now:                fixedNowFn,
		ChannelAppender:    cluster,
		MessageIDs:         ids,
		RealtimeDispatcher: realtime,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{NoPersist: true, SyncOnce: true},
		Setting:            frame.SettingReceiptEnabled,
		MsgKey:             "k-realtime",
		Expire:             30,
		FromUID:            "system",
		SenderSessionID:    77,
		RequestSubscribers: []string{"u1", "u2", "u1"},
		Payload:            []byte("cmd realtime"),
		ClientSeq:          11,
		ClientMsgNo:        "request-scoped-realtime-1",
		Topic:              "realtime-topic",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(909), result.MessageID)
	require.Zero(t, result.MessageSeq)
	require.Empty(t, cluster.sendRequests)

	scoped, err := runtimechannelid.RequestSubscriberChannelFor([]string{"u1", "u2"})
	require.NoError(t, err)
	require.Len(t, realtime.calls, 1)
	call := realtime.calls[0]
	require.Equal(t, uint64(77), call.SenderSessionID)
	require.Equal(t, []string{"u1", "u2"}, call.MessageScopedUIDs)
	require.Equal(t, uint64(909), call.Message.MessageID)
	require.Zero(t, call.Message.MessageSeq)
	require.Equal(t, scoped.CommandChannelID, call.Message.ChannelID)
	require.Equal(t, frame.ChannelTypeTemp, call.Message.ChannelType)
	require.Equal(t, frame.Framer{NoPersist: true, SyncOnce: true}, call.Message.Framer)
	require.Equal(t, frame.SettingReceiptEnabled, call.Message.Setting)
	require.Equal(t, "k-realtime", call.Message.MsgKey)
	require.Equal(t, uint32(30), call.Message.Expire)
	require.Equal(t, uint64(11), call.Message.ClientSeq)
	require.Equal(t, "request-scoped-realtime-1", call.Message.ClientMsgNo)
	require.Equal(t, "realtime-topic", call.Message.Topic)
	require.Equal(t, "system", call.Message.FromUID)
	require.Equal(t, []byte("cmd realtime"), call.Message.Payload)
	require.Equal(t, int32(fixedSendNow.Unix()), call.Message.Timestamp)
}

func TestSendRequestScopedNoPersistDoesNotSubmitCMDConversationIntent(t *testing.T) {
	cluster := &fakeChannelAppender{}
	realtime := &recordingRealtimeDispatcher{}
	intentSink := &recordingCMDConversationIntentSink{accepted: true}
	ids := &sequenceMessageIDGenerator{next: 911}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		MessageIDs:             ids,
		RealtimeDispatcher:     realtime,
		CMDConversationIntents: intentSink,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:             frame.Framer{NoPersist: true, SyncOnce: true},
		FromUID:            "system",
		RequestSubscribers: []string{"u1", "u2"},
		Payload:            []byte("cmd realtime"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Zero(t, result.MessageSeq)
	require.Empty(t, cluster.sendRequests)
	require.Len(t, realtime.calls, 1)
	require.Empty(t, intentSink.intents)
}

func TestSendNoPersistWithSyncOnceDispatchesRealtimeCommandMessage(t *testing.T) {
	cluster := &fakeChannelAppender{}
	dispatcher := &recordingCommittedDispatcher{}
	realtime := &recordingRealtimeDispatcher{}
	ids := &sequenceMessageIDGenerator{next: 910}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		CommittedDispatcher: dispatcher,
		RealtimeDispatcher:  realtime,
		MessageIDs:          ids,
		PermissionStore:     permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:          frame.Framer{NoPersist: true, SyncOnce: true},
		FromUID:         "u1",
		SenderSessionID: 77,
		ChannelID:       "g1",
		ChannelType:     frame.ChannelTypeGroup,
		Payload:         []byte("online cmd"),
		ClientSeq:       12,
		ClientMsgNo:     "cmd-np-1",
		Topic:           "cmd-topic",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(910), result.MessageID)
	require.Zero(t, result.MessageSeq)
	require.Empty(t, cluster.sendRequests)
	require.Empty(t, dispatcher.calls)
	require.Len(t, realtime.calls, 1)
	call := realtime.calls[0]
	require.Equal(t, uint64(77), call.SenderSessionID)
	require.Empty(t, call.MessageScopedUIDs)
	require.Equal(t, uint64(910), call.Message.MessageID)
	require.Zero(t, call.Message.MessageSeq)
	require.Equal(t, runtimechannelid.ToCommandChannel("g1"), call.Message.ChannelID)
	require.Equal(t, frame.ChannelTypeGroup, call.Message.ChannelType)
	require.Equal(t, frame.Framer{NoPersist: true, SyncOnce: true}, call.Message.Framer)
	require.Equal(t, uint64(12), call.Message.ClientSeq)
	require.Equal(t, "cmd-np-1", call.Message.ClientMsgNo)
	require.Equal(t, "cmd-topic", call.Message.Topic)
	require.Equal(t, "u1", call.Message.FromUID)
	require.Equal(t, []byte("online cmd"), call.Message.Payload)
	require.Equal(t, int32(fixedSendNow.Unix()), call.Message.Timestamp)
}

func TestSendNoPersistAlreadyDerivedCommandDispatchesRealtimeWithoutSyncOnce(t *testing.T) {
	realtime := &recordingRealtimeDispatcher{}
	ids := &sequenceMessageIDGenerator{next: 920}
	app := New(Options{
		Now:                fixedNowFn,
		RealtimeDispatcher: realtime,
		MessageIDs:         ids,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   runtimechannelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("already cmd"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(920), result.MessageID)
	require.Zero(t, result.MessageSeq)
	require.Len(t, realtime.calls, 1)
	require.Equal(t, runtimechannelid.ToCommandChannel("g1"), realtime.calls[0].Message.ChannelID)
	require.Equal(t, frame.Framer{NoPersist: true}, realtime.calls[0].Message.Framer)
}

func TestSendNoPersistCommandRequiresMessageIDGenerator(t *testing.T) {
	realtime := &recordingRealtimeDispatcher{}
	app := New(Options{
		Now:                fixedNowFn,
		RealtimeDispatcher: realtime,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("cmd"),
	})

	require.ErrorIs(t, err, ErrMessageIDGeneratorRequired)
	require.Equal(t, SendResult{}, result)
	require.Empty(t, realtime.calls)
}

func TestSendNoPersistCommandReturnsRealtimeDispatcherError(t *testing.T) {
	wantErr := errors.New("realtime stopped")
	realtime := &recordingRealtimeDispatcher{err: wantErr}
	ids := &sequenceMessageIDGenerator{next: 930}
	app := New(Options{
		Now:                fixedNowFn,
		RealtimeDispatcher: realtime,
		MessageIDs:         ids,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("cmd"),
	})

	require.ErrorIs(t, err, wantErr)
	require.Equal(t, SendResult{}, result)
	require.Len(t, realtime.calls, 1)
}

func TestSendRejectsBannedGroupBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
		Ban:         1,
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonBan, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsSenderSendBanBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u1", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:   "u1",
		ChannelType: int64(frame.ChannelTypePerson),
		SendBan:     1,
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSendBan, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsGroupDisbandBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
		Disband:     1,
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonDisband, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsGroupSenderInDenylistBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup})
	permissions.members[permissionKey(denyID, int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonInBlacklist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsGroupSenderThatIsNotSubscriberBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSubscriberNotExist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsGroupSenderNotInNonEmptyAllowlistBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	allowID := channelmembers.AllowlistChannelID(channelmembers.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup})
	permissions.hasAny[permissionKey(allowID, int64(frame.ChannelTypeGroup))] = true
	permissions.members[permissionKey(allowID, int64(frame.ChannelTypeGroup))] = map[string]bool{"u2": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonNotInWhitelist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendAllowsGroupSenderWhenSubscriberAndAllowlistEmpty(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 701, MessageSeq: 31}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(701), result.MessageID)
	require.Equal(t, uint64(31), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendRejectsPersonSenderInReceiverDenylistBeforeDurableAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	permissions := newFakePermissionStore()
	denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "u2", ChannelType: frame.ChannelTypePerson})
	permissions.members[permissionKey(denyID, int64(frame.ChannelTypePerson))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonInBlacklist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendAllowsPersonSenderWhenReceiverDenylistMisses(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 702, MessageSeq: 32}},
		},
	}
	permissions := newFakePermissionStore()
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(702), result.MessageID)
	require.Equal(t, uint64(32), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendAllowsPersonSenderWhenPersonWhitelistDisabledByDefault(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 704, MessageSeq: 34}},
		},
	}
	permissions := newFakePermissionStore()
	allowID := channelmembers.AllowlistChannelID(channelmembers.ChannelKey{ChannelID: "u2", ChannelType: frame.ChannelTypePerson})
	permissions.hasAny[permissionKey(allowID, int64(frame.ChannelTypePerson))] = true
	permissions.members[permissionKey(allowID, int64(frame.ChannelTypePerson))] = map[string]bool{"u3": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(704), result.MessageID)
	require.Equal(t, uint64(34), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendRejectsPersonSenderNotInReceiverAllowlistWhenPersonWhitelistEnabled(t *testing.T) {
	cluster := &fakeChannelAppender{}
	permissions := newFakePermissionStore()
	allowID := channelmembers.AllowlistChannelID(channelmembers.ChannelKey{ChannelID: "u2", ChannelType: frame.ChannelTypePerson})
	permissions.hasAny[permissionKey(allowID, int64(frame.ChannelTypePerson))] = true
	permissions.members[permissionKey(allowID, int64(frame.ChannelTypePerson))] = map[string]bool{"u3": true}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		PermissionStore:        permissions,
		PersonWhitelistEnabled: true,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonNotInWhitelist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendAllowsPersonSenderInReceiverAllowlistWhenPersonWhitelistEnabled(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 705, MessageSeq: 35}},
		},
	}
	permissions := newFakePermissionStore()
	allowID := channelmembers.AllowlistChannelID(channelmembers.ChannelKey{ChannelID: "u2", ChannelType: frame.ChannelTypePerson})
	permissions.hasAny[permissionKey(allowID, int64(frame.ChannelTypePerson))] = true
	permissions.members[permissionKey(allowID, int64(frame.ChannelTypePerson))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		PermissionStore:        permissions,
		PersonWhitelistEnabled: true,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(705), result.MessageID)
	require.Equal(t, uint64(35), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendAllowsPersonStrangerWhenReceiverAllowsStrangerAndWhitelistEnabled(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 710, MessageSeq: 40}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u2", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:     "u2",
		ChannelType:   int64(frame.ChannelTypePerson),
		AllowStranger: 1,
	}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		PermissionStore:        permissions,
		PersonWhitelistEnabled: true,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(710), result.MessageID)
	require.Equal(t, uint64(40), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendRejectsPersonDenylistBeforeAllowStranger(t *testing.T) {
	cluster := &fakeChannelAppender{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u2", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:     "u2",
		ChannelType:   int64(frame.ChannelTypePerson),
		AllowStranger: 1,
	}
	denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "u2", ChannelType: frame.ChannelTypePerson})
	permissions.members[permissionKey(denyID, int64(frame.ChannelTypePerson))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		PermissionStore:        permissions,
		PersonWhitelistEnabled: true,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonInBlacklist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsSenderSendBanBeforeReceiverAllowStranger(t *testing.T) {
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u1", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:   "u1",
		ChannelType: int64(frame.ChannelTypePerson),
		SendBan:     1,
	}
	permissions.channels[permissionKey("u2", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:     "u2",
		ChannelType:   int64(frame.ChannelTypePerson),
		AllowStranger: 1,
	}
	cluster := &fakeChannelAppender{}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		PermissionStore:        permissions,
		PersonWhitelistEnabled: true,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSendBan, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsPersonStrangerWhenWhitelistEnabledAndReceiverMetadataMissing(t *testing.T) {
	permissions := newFakePermissionStore()
	cluster := &fakeChannelAppender{}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		PermissionStore:        permissions,
		PersonWhitelistEnabled: true,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonNotInWhitelist, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendAllowsPersonWhenWhitelistDisabledEvenReceiverDoesNotAllowStranger(t *testing.T) {
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u2", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:     "u2",
		ChannelType:   int64(frame.ChannelTypePerson),
		AllowStranger: 0,
	}
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 711, MessageSeq: 41}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendReturnsSystemErrorWhenReceiverAllowStrangerLookupFails(t *testing.T) {
	permissions := newFakePermissionStore()
	permissions.channelErrs[permissionKey("u2", int64(frame.ChannelTypePerson))] = errors.New("receiver metadata failed")
	cluster := &fakeChannelAppender{}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		PermissionStore:        permissions,
		PersonWhitelistEnabled: true,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.Error(t, err)
	require.Equal(t, frame.ReasonSystemError, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendAllowsPersonWhenReceiverIsSystemUID(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 706, MessageSeq: 36}},
		},
	}
	permissions := newFakePermissionStore()
	denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "sys", ChannelType: frame.ChannelTypePerson})
	permissions.members[permissionKey(denyID, int64(frame.ChannelTypePerson))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:                    fixedNowFn,
		ChannelAppender:        cluster,
		PermissionStore:        permissions,
		SystemUIDs:             fakeSystemUIDChecker{"sys": true},
		PersonWhitelistEnabled: true,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "sys",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(706), result.MessageID)
	require.Equal(t, uint64(36), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendSyncOncePlainDurablePersonStillCanonicalizesAndAppendsOnce(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 704, MessageSeq: 34}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(704), result.MessageID)
	require.Equal(t, uint64(34), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, "u2@u1", cluster.sendRequests[0].Message.ChannelID)
}

func TestSendSyncOnceNormalGroupAppendsToCommandChannelAfterPermissionChecks(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 705, MessageSeq: 35}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{SyncOnce: true},
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(705), result.MessageID)
	require.Equal(t, uint64(35), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: runtimechannelid.ToCommandChannel("g1"), Type: frame.ChannelTypeGroup}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, runtimechannelid.ToCommandChannel("g1"), cluster.sendRequests[0].Message.ChannelID)
}

func TestSendAlreadyDerivedGroupChecksSourceAndAppendsWithoutDoubleCommandSuffix(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 706, MessageSeq: 36}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   runtimechannelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(706), result.MessageID)
	require.Equal(t, uint64(36), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: runtimechannelid.ToCommandChannel("g1"), Type: frame.ChannelTypeGroup}, cluster.sendRequests[0].ChannelID)
	require.NotContains(t, cluster.sendRequests[0].ChannelID.ID, runtimechannelid.CommandChannelSuffix+runtimechannelid.CommandChannelSuffix)
}

func TestSendAlreadyDerivedPersonStripsBeforeNormalizeAndRestoresCommandSuffixOnce(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 707, MessageSeq: 37}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   runtimechannelid.ToCommandChannel("u1@u2"),
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(707), result.MessageID)
	require.Equal(t, uint64(37), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: runtimechannelid.ToCommandChannel("u2@u1"), Type: frame.ChannelTypePerson}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, runtimechannelid.ToCommandChannel("u2@u1"), cluster.sendRequests[0].Message.ChannelID)
	require.NotContains(t, cluster.sendRequests[0].ChannelID.ID, runtimechannelid.CommandChannelSuffix+runtimechannelid.CommandChannelSuffix)
}

func TestSendNoPersistWithoutSyncOnceReturnsSuccessWithoutDurableAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	dispatcher := &recordingCommittedDispatcher{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		CommittedDispatcher: dispatcher,
		PermissionStore:     permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Zero(t, result.MessageID)
	require.Zero(t, result.MessageSeq)
	require.Empty(t, cluster.sendRequests)
	require.Empty(t, dispatcher.calls)
}

func TestSendSystemUIDBypassesPermissionChecks(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 703, MessageSeq: 33}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("sys", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:   "sys",
		ChannelType: int64(frame.ChannelTypePerson),
		SendBan:     1,
	}
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
		Disband:     1,
	}
	denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup})
	permissions.members[permissionKey(denyID, int64(frame.ChannelTypeGroup))] = map[string]bool{"sys": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
		SystemUIDs:      fakeSystemUIDChecker{"sys": true},
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "sys",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(703), result.MessageID)
	require.Equal(t, uint64(33), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendSystemDeviceBypassesChannelPermissionChecks(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 707, MessageSeq: 37}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
		Disband:     1,
	}
	denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup})
	permissions.members[permissionKey(denyID, int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
		SystemDeviceID:  "____device",
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		DeviceID:    "____device",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(707), result.MessageID)
	require.Equal(t, uint64(37), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendSystemDeviceDoesNotBypassSenderSendBan(t *testing.T) {
	cluster := &fakeChannelAppender{}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u1", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:   "u1",
		ChannelType: int64(frame.ChannelTypePerson),
		SendBan:     1,
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
		SystemDeviceID:  "____device",
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		DeviceID:    "____device",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSendBan, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendAllowsInfoChannel(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 708, MessageSeq: 38}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: newFakePermissionStore(),
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "info-1",
		ChannelType: frame.ChannelTypeInfo,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(708), result.MessageID)
	require.Equal(t, uint64(38), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: "info-1", Type: frame.ChannelTypeInfo}, cluster.sendRequests[0].ChannelID)
}

func TestSendAllowsCustomerServiceChannel(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 709, MessageSeq: 39}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: newFakePermissionStore(),
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "visitor-1",
		ChannelType: frame.ChannelTypeCustomerService,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(709), result.MessageID)
	require.Equal(t, uint64(39), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: "visitor-1", Type: frame.ChannelTypeCustomerService}, cluster.sendRequests[0].ChannelID)
}

func TestSendAllowsAgentChannelParticipantAndNormalizesBareAgentID(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 710, MessageSeq: 40}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: newFakePermissionStore(),
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "agent-a",
		ChannelType: frame.ChannelTypeAgent,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(710), result.MessageID)
	require.Equal(t, uint64(40), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: "u1@agent-a", Type: frame.ChannelTypeAgent}, cluster.sendRequests[0].ChannelID)
}

func TestSendRejectsAgentChannelNonParticipant(t *testing.T) {
	cluster := &fakeChannelAppender{}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: newFakePermissionStore(),
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u3",
		ChannelID:   "u1@agent-a",
		ChannelType: frame.ChannelTypeAgent,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonNotAllowSend, result.Reason)
	require.Empty(t, cluster.sendRequests)
}

func TestSendAllowsVisitorsSelfSender(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 711, MessageSeq: 41}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: newFakePermissionStore(),
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "visitor-1",
		ChannelID:   "visitor-1",
		ChannelType: frame.ChannelTypeVisitors,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(711), result.MessageID)
	require.Equal(t, uint64(41), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendVisitorsNonSelfUsesCustomerServicePermissionLists(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 712, MessageSeq: 42}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.members[permissionKey("visitor-1", int64(frame.ChannelTypeCustomerService))] = map[string]bool{"cs1": true}
	allowID := channelmembers.AllowlistChannelID(channelmembers.ChannelKey{
		ChannelID:   "visitor-1",
		ChannelType: frame.ChannelTypeCustomerService,
	})
	permissions.hasAny[permissionKey(allowID, int64(frame.ChannelTypeCustomerService))] = true
	permissions.members[permissionKey(allowID, int64(frame.ChannelTypeCustomerService))] = map[string]bool{"cs1": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "cs1",
		ChannelID:   "visitor-1",
		ChannelType: frame.ChannelTypeVisitors,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(712), result.MessageID)
	require.Equal(t, uint64(42), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
}

func TestSendAlreadyDerivedVisitorsChecksCustomerServicePermissionSource(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 940, MessageSeq: 41}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.members[permissionKey("visitor1", int64(frame.ChannelTypeCustomerService))] = map[string]bool{"agent1": true}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "agent1",
		ChannelID:   runtimechannelid.ToCommandChannel("visitor1"),
		ChannelType: frame.ChannelTypeVisitors,
		Payload:     []byte("visitor cmd"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(940), result.MessageID)
	require.Equal(t, uint64(41), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: runtimechannelid.ToCommandChannel("visitor1"), Type: frame.ChannelTypeVisitors}, cluster.sendRequests[0].ChannelID)
}

func TestSendPassesTraceIDToChannelAppend(t *testing.T) {
	cluster := &fakeChannelAppender{}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientMsgNo: "m-trace",
		TraceID:     "trace-1",
	})

	require.NoError(t, err)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, "trace-1", cluster.sendRequests[0].TraceID)
	require.Equal(t, 0, cluster.sendRequests[0].Attempt)
}

func TestSendRecordsDurableTraceWithChannelKey(t *testing.T) {
	sink := &recordingMessageSendTraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)
	cluster := &fakeChannelAppender{sendReplies: []fakeChannelAppenderSendReply{{result: channel.AppendResult{MessageID: 99, MessageSeq: 9}}}}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("hi"),
		ClientMsgNo: "m-channel",
		TraceID:     "trace-1",
	})

	require.NoError(t, err)
	var durable sendtrace.Event
	for _, event := range sink.events {
		if event.Stage == sendtrace.StageMessageSendDurable {
			durable = event
			break
		}
	}
	require.Equal(t, "trace-1", durable.TraceID)
	require.Equal(t, "m-channel", durable.ClientMsgNo)
	require.Equal(t, "u1", durable.FromUID)
	require.Equal(t, uint64(9), durable.MessageSeq)
	require.Equal(t, string(channelhandler.KeyFromChannelID(channel.ChannelID{ID: "g1", Type: frame.ChannelTypeGroup})), durable.ChannelKey)
}

func TestSendReturnsSuccessAfterDurableWriteAndSubmitsCommittedMessage(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{
				MessageID:  99,
				MessageSeq: 7,
				Message: channel.Message{
					MessageID:   99,
					MessageSeq:  7,
					Framer:      frame.Framer{RedDot: true, SyncOnce: true},
					Setting:     frame.SettingReceiptEnabled,
					MsgKey:      "k1",
					Expire:      60,
					ClientSeq:   9,
					ClientMsgNo: "m1",
					StreamNo:    "stream-1",
					Timestamp:   int32(fixedSendNow.Unix()),
					ChannelID:   runtimechannelid.ToCommandChannel("u2@u1"),
					ChannelType: frame.ChannelTypePerson,
					Topic:       "chat",
					FromUID:     "u1",
					Payload:     []byte("hi"),
				},
			}},
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:          frame.Framer{RedDot: true, SyncOnce: true},
		Setting:         frame.SettingReceiptEnabled,
		MsgKey:          "k1",
		Expire:          60,
		FromUID:         "u1",
		SenderSessionID: 42,
		ChannelID:       "u2",
		ChannelType:     frame.ChannelTypePerson,
		Topic:           "chat",
		Payload:         []byte("hi"),
		ClientSeq:       9,
		ClientMsgNo:     "m1",
		StreamNo:        "stream-1",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(99), result.MessageID)
	require.Equal(t, uint64(7), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: runtimechannelid.ToCommandChannel("u2@u1"), Type: frame.ChannelTypePerson}, cluster.sendRequests[0].ChannelID)
	require.Equal(t, frame.Framer{RedDot: true, SyncOnce: true}, cluster.sendRequests[0].Message.Framer)
	require.Equal(t, frame.SettingReceiptEnabled, cluster.sendRequests[0].Message.Setting)
	require.Equal(t, "k1", cluster.sendRequests[0].Message.MsgKey)
	require.Equal(t, uint32(60), cluster.sendRequests[0].Message.Expire)
	require.Equal(t, uint64(9), cluster.sendRequests[0].Message.ClientSeq)
	require.Equal(t, "m1", cluster.sendRequests[0].Message.ClientMsgNo)
	require.Equal(t, "stream-1", cluster.sendRequests[0].Message.StreamNo)
	require.Equal(t, int32(fixedSendNow.Unix()), cluster.sendRequests[0].Message.Timestamp)
	require.Equal(t, runtimechannelid.ToCommandChannel("u2@u1"), cluster.sendRequests[0].Message.ChannelID)
	require.Equal(t, "chat", cluster.sendRequests[0].Message.Topic)
	require.Equal(t, "u1", cluster.sendRequests[0].Message.FromUID)
	require.Equal(t, []byte("hi"), cluster.sendRequests[0].Message.Payload)
	require.Equal(t, channel.CommitModeQuorum, cluster.sendRequests[0].CommitMode)
	require.Len(t, dispatcher.calls, 1)
	require.Equal(t, uint64(42), dispatcher.calls[0].SenderSessionID)
	require.Equal(t, channel.Message{
		MessageID:   99,
		MessageSeq:  7,
		Framer:      frame.Framer{RedDot: true, SyncOnce: true},
		Setting:     frame.SettingReceiptEnabled,
		MsgKey:      "k1",
		Expire:      60,
		ClientSeq:   9,
		ClientMsgNo: "m1",
		StreamNo:    "stream-1",
		Timestamp:   int32(fixedSendNow.Unix()),
		ChannelID:   runtimechannelid.ToCommandChannel("u2@u1"),
		ChannelType: frame.ChannelTypePerson,
		Topic:       "chat",
		FromUID:     "u1",
		Payload:     []byte("hi"),
	}, dispatcher.calls[0].Message)
}

func TestSendDefaultsToQuorumCommitMode(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 88, MessageSeq: 12}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})
	require.NoError(t, err)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.CommitModeQuorum, cluster.sendRequests[0].CommitMode)
}

func TestSendPropagatesLocalCommitMode(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 89, MessageSeq: 13}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		CommitMode:  channel.CommitModeLocal,
		Payload:     []byte("hi"),
	})
	require.NoError(t, err)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.CommitModeLocal, cluster.sendRequests[0].CommitMode)
}

func TestSendHookCanOverrideCommitMode(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 90, MessageSeq: 14}},
		},
	}
	hook := &recordingSendHook{mutate: func(cmd SendCommand) SendCommand {
		cmd.CommitMode = channel.CommitModeLocal
		return cmd
	}}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		SendHook:        hook,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Len(t, hook.calls, 1)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.CommitModeLocal, cluster.sendRequests[0].CommitMode)
}

func TestSendRecanonicalizesPrecomposedPersonChannelBeforeDurableWrite(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 88, MessageSeq: 12}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson}, cluster.sendRequests[0].ChannelID)
}

func TestSendRejectsThirdPartyPrecomposedPersonChannel(t *testing.T) {
	cluster := &fakeChannelAppender{}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u3@u4",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.Error(t, err)
	require.Equal(t, SendResult{}, result)
	require.Empty(t, cluster.sendRequests)
}

func TestSendReturnsSuccessWhenCommittedSubmitFails(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{err: errors.New("queue full")}
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 101, MessageSeq: 5}},
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientSeq:   14,
		ClientMsgNo: "m5",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(101), result.MessageID)
	require.Equal(t, uint64(5), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Len(t, dispatcher.calls, 1)
}

func TestSendSubmitsCommittedMessageFromClusterResult(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{
				MessageID:  88,
				MessageSeq: 7,
				Message: channel.Message{
					MessageID:   88,
					MessageSeq:  7,
					ChannelID:   "u2@u1",
					ChannelType: frame.ChannelTypePerson,
					FromUID:     "committed-sender",
					ClientMsgNo: "committed-1",
					Topic:       "committed-topic",
					Payload:     []byte("committed-payload"),
				},
			}},
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		CommittedDispatcher: dispatcher,
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "draft-1",
		Topic:       "draft-topic",
		Payload:     []byte("draft-payload"),
	})

	require.NoError(t, err)
	require.Len(t, dispatcher.calls, 1)
	require.Equal(t, "committed-sender", dispatcher.calls[0].Message.FromUID)
	require.Equal(t, "committed-topic", dispatcher.calls[0].Message.Topic)
	require.Equal(t, []byte("committed-payload"), dispatcher.calls[0].Message.Payload)
}

func TestSendDoesNotPerformSynchronousDeliveryAfterDurableWrite(t *testing.T) {
	reg := &fakeRegistry{
		byUID: map[string][]online.OnlineConn{
			"u2": {
				{SessionID: 2, UID: "u2"},
				{SessionID: 99, UID: "u2"},
			},
		},
	}
	delivery := &recordingDelivery{}
	remote := &recordingRemoteDelivery{}
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 601, MessageSeq: 22}},
		},
	}
	recipients := fakeRecipientDirectory{
		endpointsByUID: map[string][]Endpoint{
			"u2": {
				{NodeID: 1, BootID: 11, SessionID: 2},
				{NodeID: 2, BootID: 22, SessionID: 8},
			},
		},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		Online:              reg,
		Delivery:            delivery,
		Recipients:          recipients,
		RemoteDelivery:      remote,
		LocalNodeID:         1,
		LocalBootID:         11,
		CommittedDispatcher: &recordingCommittedDispatcher{},
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
		ClientSeq:   31,
		ClientMsgNo: "m-remote",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Empty(t, delivery.calls)
	require.Empty(t, remote.calls)
}

func TestSendDurablePersonPropagatesRequestContextToChannelAppender(t *testing.T) {
	type ctxKey string

	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{result: channel.AppendResult{MessageID: 401, MessageSeq: 19}},
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	ctx := context.WithValue(context.Background(), ctxKey("request"), "durable-send")
	result, err := app.Send(ctx, SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "m9",
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Len(t, cluster.sendContexts, 1)
	require.Equal(t, ctx, cluster.sendContexts[0])
}

func TestSendDurablePersonReturnsContextCanceled(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendFn: func(ctx context.Context, _ channel.AppendRequest) (channel.AppendResult, error) {
			<-ctx.Done()
			return channel.AppendResult{}, ctx.Err()
		},
	}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := app.Send(ctx, SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "m10",
		Payload:     []byte("hi"),
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, SendResult{}, result)
	require.Empty(t, cluster.sendContexts)
	require.Empty(t, cluster.sendBatchContexts)
}

func TestSendReturnsProtocolUpgradeRequiredWhenClusterRejectsLegacyClient(t *testing.T) {
	cluster := &fakeChannelAppender{
		sendReplies: []fakeChannelAppenderSendReply{
			{err: channel.ErrProtocolUpgradeRequired},
		},
	}
	delivery := &recordingDelivery{}
	app := New(Options{
		Now:             fixedNowFn,
		ChannelAppender: cluster,
		Online: &fakeRegistry{
			byUID: map[string][]online.OnlineConn{
				"u2": {
					{SessionID: 2, UID: "u2"},
				},
			},
		},
		Delivery: delivery,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:         "u1",
		ChannelID:       "u2",
		ChannelType:     frame.ChannelTypePerson,
		Payload:         []byte("hi"),
		ClientSeq:       23,
		ClientMsgNo:     "m8",
		ProtocolVersion: frame.LegacyMessageSeqVersion,
	})

	require.ErrorIs(t, err, channel.ErrProtocolUpgradeRequired)
	require.Equal(t, SendResult{}, result)
	require.Empty(t, delivery.calls)
}

func TestNewPreservesInjectedCollaborators(t *testing.T) {
	identities := &fakeIdentityStore{}
	channels := &fakeChannelStore{}
	cluster := &fakeChannelAppender{}
	reg := &fakeRegistry{}
	delivery := &recordingDelivery{}
	dispatcher := &recordingCommittedDispatcher{}
	realtime := &recordingRealtimeDispatcher{}
	messageIDs := &sequenceMessageIDGenerator{next: 1001}
	acks := &recordingDeliveryAck{}
	offline := &recordingDeliveryOffline{}
	permissions := newFakePermissionStore()
	systemUIDs := fakeSystemUIDChecker{"system": true}

	app := New(Options{
		IdentityStore:       identities,
		ChannelStore:        channels,
		PermissionStore:     permissions,
		SystemUIDs:          systemUIDs,
		ChannelAppender:     cluster,
		Online:              reg,
		Delivery:            delivery,
		CommittedDispatcher: dispatcher,
		RealtimeDispatcher:  realtime,
		MessageIDs:          messageIDs,
		DeliveryAck:         acks,
		DeliveryOffline:     offline,
		LocalBootID:         9,
		Now:                 fixedNowFn,
	})

	require.Same(t, identities, app.identities)
	require.Same(t, channels, app.channels)
	require.Same(t, permissions, app.permissions)
	require.Equal(t, systemUIDs, app.systemUIDs)
	require.Same(t, cluster, app.appender)
	require.Same(t, reg, app.online)
	require.Same(t, delivery, app.delivery)
	require.Same(t, dispatcher, app.dispatcher)
	require.Same(t, realtime, app.realtime)
	require.Same(t, messageIDs, app.messageIDs)
	require.Same(t, acks, app.deliveryAck)
	require.Same(t, offline, app.deliveryOffline)
	require.Equal(t, uint64(9), app.localBootID)
	require.Same(t, reg, app.OnlineRegistry())
}

func fixedNowFn() time.Time {
	return fixedSendNow
}

type recordingUserSendLimiter struct {
	decision userlimit.Decision
	calls    []userlimit.Request
}

func (l *recordingUserSendLimiter) AllowSend(_ time.Time, req userlimit.Request) userlimit.Decision {
	l.calls = append(l.calls, req)
	return l.decision
}

type fakeRegistry struct {
	byUID map[string][]online.OnlineConn
}

func (f *fakeRegistry) Register(conn online.OnlineConn) error {
	if f.byUID == nil {
		f.byUID = make(map[string][]online.OnlineConn)
	}
	f.byUID[conn.UID] = append(f.byUID[conn.UID], conn)
	return nil
}

func (f *fakeRegistry) Unregister(sessionID uint64) {
	for uid, conns := range f.byUID {
		filtered := conns[:0]
		for _, conn := range conns {
			if conn.SessionID != sessionID {
				filtered = append(filtered, conn)
			}
		}
		if len(filtered) == 0 {
			delete(f.byUID, uid)
			continue
		}
		f.byUID[uid] = filtered
	}
}

func (f *fakeRegistry) MarkClosing(uint64) (online.OnlineConn, bool) {
	return online.OnlineConn{}, false
}

func (f *fakeRegistry) Connection(sessionID uint64) (online.OnlineConn, bool) {
	for _, conns := range f.byUID {
		for _, conn := range conns {
			if conn.SessionID == sessionID {
				return conn, true
			}
		}
	}
	return online.OnlineConn{}, false
}

func (f *fakeRegistry) ConnectionsByUID(uid string) []online.OnlineConn {
	conns := f.byUID[uid]
	out := make([]online.OnlineConn, len(conns))
	copy(out, conns)
	return out
}

func (f *fakeRegistry) ActiveConnectionsBySlot(uint64) []online.OnlineConn {
	return nil
}

func (f *fakeRegistry) ActiveSlots() []online.SlotSnapshot {
	return nil
}

func (f *fakeRegistry) Summary() online.Summary {
	summary := online.Summary{SessionsByListener: make(map[string]int)}
	for _, conns := range f.byUID {
		for _, conn := range conns {
			summary.Total++
			if conn.State == online.LocalRouteStateClosing {
				summary.Closing++
			} else {
				summary.Active++
			}
			summary.SessionsByListener[conn.Listener]++
		}
	}
	return summary
}

type deliveryCall struct {
	recipients []online.OnlineConn
	frame      frame.Frame
}

type recordingDelivery struct {
	calls []deliveryCall
}

func (d *recordingDelivery) Deliver(recipients []online.OnlineConn, f frame.Frame) error {
	copiedRecipients := make([]online.OnlineConn, len(recipients))
	copy(copiedRecipients, recipients)
	d.calls = append(d.calls, deliveryCall{recipients: copiedRecipients, frame: f})
	return nil
}

type fakeRecipientDirectory struct {
	endpointsByUID map[string][]Endpoint
	err            error
}

func (f fakeRecipientDirectory) EndpointsByUID(_ context.Context, uid string) ([]Endpoint, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]Endpoint(nil), f.endpointsByUID[uid]...), nil
}

type recordingRemoteDelivery struct {
	calls []RemoteDeliveryCommand
}

func (d *recordingRemoteDelivery) DeliverRemote(_ context.Context, cmd RemoteDeliveryCommand) error {
	copied := cmd
	copied.SessionIDs = append([]uint64(nil), cmd.SessionIDs...)
	d.calls = append(d.calls, copied)
	return nil
}

type recordingCommittedDispatcher struct {
	calls []deliveryEnvelopeRecord
	err   error
}

func (d *recordingCommittedDispatcher) SubmitCommitted(_ context.Context, env messageevents.MessageCommitted) error {
	copied := env
	copied.Message.Payload = append([]byte(nil), env.Message.Payload...)
	d.calls = append(d.calls, deliveryEnvelopeRecord(copied))
	return d.err
}

type deliveryEnvelopeRecord = messageevents.MessageCommitted

type recordingCMDConversationIntentSink struct {
	intents  []cmdsync.ConversationIntent
	accepted bool
	err      error
}

func (s *recordingCMDConversationIntentSink) PushIntent(_ context.Context, intent cmdsync.ConversationIntent) (bool, error) {
	copied := intent
	copied.UserReadSeqs = make(map[string]uint64, len(intent.UserReadSeqs))
	for uid, readSeq := range intent.UserReadSeqs {
		copied.UserReadSeqs[uid] = readSeq
	}
	s.intents = append(s.intents, copied)
	return s.accepted, s.err
}

type recordingRealtimeDispatcher struct {
	calls []messageevents.MessageRealtime
	err   error
}

func (d *recordingRealtimeDispatcher) SubmitRealtime(_ context.Context, event messageevents.MessageRealtime) error {
	copied := event.Clone()
	d.calls = append(d.calls, copied)
	return d.err
}

type sequenceMessageIDGenerator struct {
	next uint64
}

func (g *sequenceMessageIDGenerator) Next() uint64 {
	id := g.next
	g.next++
	return id
}

type fakeIdentityStore struct{}

func (*fakeIdentityStore) GetUser(context.Context, string) (metadb.User, error) {
	return metadb.User{}, nil
}

type fakeChannelStore struct{}

func (*fakeChannelStore) GetChannel(context.Context, string, int64) (metadb.Channel, error) {
	return metadb.Channel{}, nil
}

type fakePermissionStore struct {
	channels        map[string]metadb.Channel
	channelErrs     map[string]error
	members         map[string]map[string]bool
	hasAny          map[string]bool
	getChannelCalls int
}

func newFakePermissionStore() *fakePermissionStore {
	return &fakePermissionStore{
		channels:    make(map[string]metadb.Channel),
		channelErrs: make(map[string]error),
		members:     make(map[string]map[string]bool),
		hasAny:      make(map[string]bool),
	}
}

func permissionKey(channelID string, channelType int64) string {
	return channelID + "#" + strconv.FormatInt(channelType, 10)
}

func (s *fakePermissionStore) GetChannelForPermission(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	s.getChannelCalls++
	key := permissionKey(channelID, channelType)
	if err, ok := s.channelErrs[key]; ok {
		return metadb.Channel{}, err
	}
	ch, ok := s.channels[key]
	if !ok {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return ch, nil
}

func (s *fakePermissionStore) ContainsChannelSubscriber(_ context.Context, channelID string, channelType int64, uid string) (bool, error) {
	return s.members[permissionKey(channelID, channelType)][uid], nil
}

func (s *fakePermissionStore) HasChannelSubscribers(_ context.Context, channelID string, channelType int64) (bool, error) {
	return s.hasAny[permissionKey(channelID, channelType)], nil
}

type fakeSystemUIDChecker map[string]bool

func (f fakeSystemUIDChecker) IsSystemUID(uid string) bool { return f[uid] }

type fakeChannelAppenderSendReply struct {
	result channel.AppendResult
	err    error
}

type fakeChannelAppenderSendBatchReply struct {
	result channel.AppendBatchResult
	err    error
}

type fakeChannelAppender struct {
	sendRequests      []channel.AppendRequest
	sendBatchRequests []channel.AppendBatchRequest
	sendContexts      []context.Context
	sendBatchContexts []context.Context
	sendAppendCalls   int
	sendReplies       []fakeChannelAppenderSendReply
	sendBatchReplies  []fakeChannelAppenderSendBatchReply
	sendFn            func(context.Context, channel.AppendRequest) (channel.AppendResult, error)
	sendBatchFn       func(context.Context, channel.AppendBatchRequest) (channel.AppendBatchResult, error)
}

func (f *fakeChannelAppender) Append(ctx context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
	f.sendAppendCalls++
	f.sendContexts = append(f.sendContexts, ctx)
	f.sendRequests = append(f.sendRequests, req)
	if f.sendFn != nil {
		return f.sendFn(ctx, req)
	}
	if len(f.sendReplies) == 0 {
		return channel.AppendResult{}, nil
	}
	reply := f.sendReplies[0]
	f.sendReplies = f.sendReplies[1:]
	return reply.result, reply.err
}

func (f *fakeChannelAppender) AppendBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	f.sendBatchContexts = append(f.sendBatchContexts, ctx)
	f.sendBatchRequests = append(f.sendBatchRequests, req)
	if len(req.Messages) == 1 {
		f.sendContexts = append(f.sendContexts, ctx)
		f.sendRequests = append(f.sendRequests, channel.AppendRequest{
			ChannelID:             req.ChannelID,
			Message:               req.Messages[0],
			SupportsMessageSeqU64: req.SupportsMessageSeqU64,
			CommitMode:            req.CommitMode,
			ExpectedChannelEpoch:  req.ExpectedChannelEpoch,
			ExpectedLeaderEpoch:   req.ExpectedLeaderEpoch,
			TraceID:               req.TraceID,
			Attempt:               req.Attempt,
		})
	}
	if f.sendBatchFn != nil {
		return f.sendBatchFn(ctx, req)
	}
	if f.sendFn != nil && len(req.Messages) == 1 {
		result, err := f.sendFn(ctx, f.sendRequests[len(f.sendRequests)-1])
		if err != nil {
			return channel.AppendBatchResult{}, err
		}
		return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{
			MessageID:  result.MessageID,
			MessageSeq: result.MessageSeq,
			Message:    result.Message,
		}}}, nil
	}
	if len(f.sendBatchReplies) == 0 && len(f.sendReplies) > 0 {
		if len(req.Messages) != 1 {
			return channel.AppendBatchResult{}, errors.New("fake channel cluster: sendReplies only support one message")
		}
		reply := f.sendReplies[0]
		f.sendReplies = f.sendReplies[1:]
		if reply.err != nil {
			return channel.AppendBatchResult{}, reply.err
		}
		return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{
			MessageID:  reply.result.MessageID,
			MessageSeq: reply.result.MessageSeq,
			Message:    reply.result.Message,
		}}}, nil
	}
	if len(f.sendBatchReplies) == 0 {
		items := make([]channel.AppendBatchItemResult, len(req.Messages))
		for i, msg := range req.Messages {
			items[i] = channel.AppendBatchItemResult{MessageID: uint64(i + 1), MessageSeq: uint64(i + 1), Message: msg}
		}
		return channel.AppendBatchResult{Items: items}, nil
	}
	reply := f.sendBatchReplies[0]
	f.sendBatchReplies = f.sendBatchReplies[1:]
	return reply.result, reply.err
}

// recordingMessageSendTraceSink captures synchronous sendtrace events emitted by message tests.
type recordingMessageSendTraceSink struct {
	events []sendtrace.Event
}

func (s *recordingMessageSendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.events = append(s.events, event)
}
