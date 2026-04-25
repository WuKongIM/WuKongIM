package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	gatewaysession "github.com/WuKongIM/WuKongIM/internal/gateway/session"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestAckRoutingKeepsRemoteBindingWhenOwnerNotifyFails(t *testing.T) {
	remoteAcks := deliveryruntime.NewAckIndex()
	remoteAcks.Bind(deliveryruntime.AckBinding{
		SessionID:   7,
		MessageID:   101,
		ChannelID:   "c1",
		ChannelType: 2,
		OwnerNodeID: 9,
		Route:       deliveryruntime.RouteKey{UID: "u2", SessionID: 7},
	})
	notifier := &recordingDeliveryOwnerNotifier{
		ackErr: errors.New("owner unavailable"),
	}

	router := ackRouting{
		localNodeID: 1,
		remoteAcks:  remoteAcks,
		notifier:    notifier,
	}

	err := router.AckRoute(context.Background(), message.RouteAckCommand{
		UID:        "u2",
		SessionID:  7,
		MessageID:  101,
		MessageSeq: 1,
	})

	require.Error(t, err)
	require.Len(t, notifier.acks, 1)
	require.True(t, remoteAcksHas(remoteAcks, 7, 101))
}

func TestAckRoutingRemovesRemoteBindingAfterOwnerNotifySucceeds(t *testing.T) {
	remoteAcks := deliveryruntime.NewAckIndex()
	remoteAcks.Bind(deliveryruntime.AckBinding{
		SessionID:   7,
		MessageID:   101,
		ChannelID:   "c1",
		ChannelType: 2,
		OwnerNodeID: 9,
		Route:       deliveryruntime.RouteKey{UID: "u2", SessionID: 7},
	})
	notifier := &recordingDeliveryOwnerNotifier{}

	router := ackRouting{
		localNodeID: 1,
		remoteAcks:  remoteAcks,
		notifier:    notifier,
	}

	require.NoError(t, router.AckRoute(context.Background(), message.RouteAckCommand{
		UID:        "u2",
		SessionID:  7,
		MessageID:  101,
		MessageSeq: 1,
	}))

	require.Len(t, notifier.acks, 1)
	require.False(t, remoteAcksHas(remoteAcks, 7, 101))
}

func TestAckRoutingKeepsRemoteBindingWhenNotifierMissing(t *testing.T) {
	remoteAcks := deliveryruntime.NewAckIndex()
	remoteAcks.Bind(deliveryruntime.AckBinding{
		SessionID:   7,
		MessageID:   101,
		ChannelID:   "c1",
		ChannelType: 2,
		OwnerNodeID: 9,
		Route:       deliveryruntime.RouteKey{UID: "u2", SessionID: 7},
	})
	local := &recordingRouteAcker{}

	router := ackRouting{
		localNodeID: 1,
		local:       local,
		remoteAcks:  remoteAcks,
	}

	err := router.AckRoute(context.Background(), message.RouteAckCommand{
		UID:        "u2",
		SessionID:  7,
		MessageID:  101,
		MessageSeq: 1,
	})

	require.Error(t, err)
	require.True(t, remoteAcksHas(remoteAcks, 7, 101))
	require.Empty(t, local.acks)
}

func TestAckRoutingKeepsLocalBindingWhenLocalAckFails(t *testing.T) {
	remoteAcks := deliveryruntime.NewAckIndex()
	remoteAcks.Bind(deliveryruntime.AckBinding{
		SessionID:   7,
		MessageID:   101,
		ChannelID:   "c1",
		ChannelType: 2,
		OwnerNodeID: 1,
		Route:       deliveryruntime.RouteKey{UID: "u2", SessionID: 7},
	})
	local := &recordingRouteAcker{err: errors.New("local ack failed")}

	router := ackRouting{
		localNodeID: 1,
		local:       local,
		remoteAcks:  remoteAcks,
	}

	err := router.AckRoute(context.Background(), message.RouteAckCommand{
		UID:        "u2",
		SessionID:  7,
		MessageID:  101,
		MessageSeq: 1,
	})

	require.Error(t, err)
	require.Len(t, local.acks, 1)
	require.True(t, remoteAcksHas(remoteAcks, 7, 101))
}

func TestOfflineRoutingKeepsRemoteBindingsWhenOwnerNotifyFails(t *testing.T) {
	remoteAcks := deliveryruntime.NewAckIndex()
	remoteAcks.Bind(deliveryruntime.AckBinding{
		SessionID:   8,
		MessageID:   201,
		ChannelID:   "c2",
		ChannelType: 2,
		OwnerNodeID: 9,
		Route:       deliveryruntime.RouteKey{UID: "u2", SessionID: 8},
	})
	notifier := &recordingDeliveryOwnerNotifier{
		offlineErr: errors.New("owner unavailable"),
	}

	router := offlineRouting{
		localNodeID: 1,
		remoteAcks:  remoteAcks,
		notifier:    notifier,
	}

	err := router.SessionClosed(context.Background(), message.SessionClosedCommand{
		UID:       "u2",
		SessionID: 8,
	})

	require.Error(t, err)
	require.Len(t, notifier.offlines, 1)
	require.False(t, notifier.localClosed)
	require.True(t, remoteAcksHas(remoteAcks, 8, 201))
}

func TestOfflineRoutingRemovesRemoteBindingsAfterOwnerNotifySucceeds(t *testing.T) {
	remoteAcks := deliveryruntime.NewAckIndex()
	remoteAcks.Bind(deliveryruntime.AckBinding{
		SessionID:   8,
		MessageID:   201,
		ChannelID:   "c2",
		ChannelType: 2,
		OwnerNodeID: 9,
		Route:       deliveryruntime.RouteKey{UID: "u2", SessionID: 8},
	})
	notifier := &recordingDeliveryOwnerNotifier{}

	router := offlineRouting{
		localNodeID: 1,
		remoteAcks:  remoteAcks,
		notifier:    notifier,
	}

	require.NoError(t, router.SessionClosed(context.Background(), message.SessionClosedCommand{
		UID:       "u2",
		SessionID: 8,
	}))

	require.Len(t, notifier.offlines, 1)
	require.False(t, remoteAcksHas(remoteAcks, 8, 201))
}

func TestOfflineRoutingKeepsRemoteBindingsWhenNotifierMissing(t *testing.T) {
	remoteAcks := deliveryruntime.NewAckIndex()
	remoteAcks.Bind(deliveryruntime.AckBinding{
		SessionID:   8,
		MessageID:   201,
		ChannelID:   "c2",
		ChannelType: 2,
		OwnerNodeID: 9,
		Route:       deliveryruntime.RouteKey{UID: "u2", SessionID: 8},
	})
	local := &recordingSessionCloser{}

	router := offlineRouting{
		localNodeID: 1,
		local:       local,
		remoteAcks:  remoteAcks,
	}

	err := router.SessionClosed(context.Background(), message.SessionClosedCommand{
		UID:       "u2",
		SessionID: 8,
	})

	require.Error(t, err)
	require.True(t, remoteAcksHas(remoteAcks, 8, 201))
	require.Len(t, local.closed, 1)
}

func TestOfflineRoutingKeepsLocalBindingsWhenLocalCloseFails(t *testing.T) {
	remoteAcks := deliveryruntime.NewAckIndex()
	remoteAcks.Bind(deliveryruntime.AckBinding{
		SessionID:   8,
		MessageID:   201,
		ChannelID:   "c2",
		ChannelType: 2,
		OwnerNodeID: 1,
		Route:       deliveryruntime.RouteKey{UID: "u2", SessionID: 8},
	})
	local := &recordingSessionCloser{err: errors.New("local close failed")}

	router := offlineRouting{
		localNodeID: 1,
		local:       local,
		remoteAcks:  remoteAcks,
	}

	err := router.SessionClosed(context.Background(), message.SessionClosedCommand{
		UID:       "u2",
		SessionID: 8,
	})

	require.Error(t, err)
	require.Len(t, local.closed, 1)
	require.True(t, remoteAcksHas(remoteAcks, 8, 201))
}

func TestAsyncCommittedDispatcherFallsBackToLocalConversationWhenOwnerIsUnknown(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	conversation := &recordingFlushingConversationSubmitter{}
	dispatcher := asyncCommittedDispatcher{
		localNodeID: 1,
		channelLog: &stubChannelLogCluster{
			statusErr: errors.New("leader unknown"),
		},
		delivery:     delivery,
		conversation: conversation,
	}

	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "g1",
			ChannelType: 2,
			MessageID:   101,
			MessageSeq:  1,
		},
	}))

	require.Eventually(t, func() bool {
		return delivery.submitCalls == 0 && conversation.submitCalls == 1 && conversation.flushCalls == 1
	}, time.Second, 10*time.Millisecond)
}

func TestAsyncCommittedDispatcherSubmitsDurableMessageToLocalDelivery(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	dispatcher := asyncCommittedDispatcher{
		localNodeID: 1,
		channelLog: &stubChannelLogCluster{
			status: channel.ChannelRuntimeStatus{
				Leader: 1,
			},
		},
		delivery: delivery,
	}

	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			MessageID:   88,
			MessageSeq:  7,
			Framer:      message.SendCommand{}.Framer,
			Setting:     3,
			MsgKey:      "k1",
			Expire:      60,
			ClientSeq:   9,
			ClientMsgNo: "m1",
			ChannelID:   "u1@u2",
			ChannelType: 1,
			Topic:       "chat",
			FromUID:     "u1",
			Payload:     []byte("hello"),
		},
	}))

	require.Eventually(t, func() bool {
		return len(delivery.calls) == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, channel.Message{
		ChannelID:   "u1@u2",
		ChannelType: 1,
		MessageID:   88,
		MessageSeq:  7,
		FromUID:     "u1",
		ClientMsgNo: "m1",
		Topic:       "chat",
		Payload:     []byte("hello"),
		Framer:      message.SendCommand{}.Framer,
		Setting:     3,
		MsgKey:      "k1",
		Expire:      60,
		ClientSeq:   9,
	}, delivery.calls[0].Message)
}

func TestAsyncCommittedDispatcherSubmitsToConversationProjector(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	conversation := &recordingConversationSubmitter{}
	dispatcher := asyncCommittedDispatcher{
		localNodeID: 1,
		channelLog: &stubChannelLogCluster{
			status: channel.ChannelRuntimeStatus{
				Leader: 1,
			},
		},
		delivery:     delivery,
		conversation: conversation,
	}

	msg := channel.Message{
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		MessageID:   99,
		MessageSeq:  8,
		ClientMsgNo: "c1",
		FromUID:     "u1",
		Payload:     []byte("hello projector"),
	}
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), deliveryruntime.CommittedEnvelope{Message: msg}))

	require.Eventually(t, func() bool {
		return len(delivery.calls) == 1 && len(conversation.calls) == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, msg, delivery.calls[0].Message)
	require.Equal(t, msg, conversation.calls[0])
}

func TestAsyncCommittedDispatcherPrefersLocalDeliveryWithoutOwnerLookup(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	conversation := &recordingConversationSubmitter{}
	channelLog := &stubChannelLogCluster{
		statusErr: errors.New("status should not be called in local-preferred mode"),
	}
	dispatcher := asyncCommittedDispatcher{
		localNodeID:  1,
		preferLocal:  true,
		channelLog:   channelLog,
		delivery:     delivery,
		conversation: conversation,
	}

	msg := channel.Message{
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		MessageID:   100,
		MessageSeq:  9,
		ClientMsgNo: "c2",
		FromUID:     "u1",
		Payload:     []byte("hello local"),
	}
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), deliveryruntime.CommittedEnvelope{Message: msg}))

	require.Eventually(t, func() bool {
		return len(delivery.calls) == 1 && len(conversation.calls) == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 0, channelLog.StatusCalls())
	require.Equal(t, msg, delivery.calls[0].Message)
	require.Equal(t, msg, conversation.calls[0])
}

func TestBuildRealtimeRecvPacketUsesDurableTimestampAndPersonChannelView(t *testing.T) {
	packet := buildRealtimeRecvPacket(channel.Message{
		MessageID:   88,
		MessageSeq:  7,
		Setting:     1,
		MsgKey:      "k1",
		Expire:      60,
		ClientSeq:   9,
		ClientMsgNo: "m1",
		Timestamp:   123,
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		Topic:       "chat",
		FromUID:     "u1",
		Payload:     []byte("hello"),
	}, "u2")

	require.Equal(t, int32(123), packet.Timestamp)
	require.Equal(t, "u1", packet.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, packet.ChannelType)
	require.Equal(t, "u1", packet.FromUID)
	require.Equal(t, []byte("hello"), packet.Payload)
}

func TestLocalDeliveryPushBuildsPersonChannelViewPerRouteUID(t *testing.T) {
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue("uid", "u1")
	recipient := newOptionRecordingSession(2, "tcp")
	recipient.SetValue("uid", "u2")
	registry := online.NewRegistry()
	require.NoError(t, registry.Register(online.OnlineConn{
		SessionID: 1,
		UID:       "u1",
		State:     online.LocalRouteStateActive,
		Session:   sender.Session,
	}))
	require.NoError(t, registry.Register(online.OnlineConn{
		SessionID: 2,
		UID:       "u2",
		State:     online.LocalRouteStateActive,
		Session:   recipient.Session,
	}))

	push := localDeliveryPush{
		online:        registry,
		localNodeID:   1,
		gatewayBootID: 11,
	}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   88,
				MessageSeq:  7,
				ChannelID:   "u1@u2",
				ChannelType: frame.ChannelTypePerson,
				FromUID:     "u1",
				Payload:     []byte("hello"),
			},
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u1", NodeID: 1, BootID: 11, SessionID: 1},
			{UID: "u2", NodeID: 1, BootID: 11, SessionID: 2},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Accepted, 2)

	senderWrites := sender.Writes()
	recipientWrites := recipient.Writes()
	require.Len(t, senderWrites, 1)
	require.Len(t, recipientWrites, 1)

	senderPacket, ok := senderWrites[0].f.(*frame.RecvPacket)
	require.True(t, ok)
	recipientPacket, ok := recipientWrites[0].f.(*frame.RecvPacket)
	require.True(t, ok)

	require.Equal(t, "u2", senderPacket.ChannelID)
	require.Equal(t, "u1", recipientPacket.ChannelID)
}

func TestLocalDeliveryPushSkipsOriginSessionButKeepsOtherSenderSessions(t *testing.T) {
	origin := newOptionRecordingSession(1, "tcp")
	origin.SetValue("uid", "u1")
	mirror := newOptionRecordingSession(3, "tcp")
	mirror.SetValue("uid", "u1")
	recipient := newOptionRecordingSession(2, "tcp")
	recipient.SetValue("uid", "u2")
	registry := online.NewRegistry()
	require.NoError(t, registry.Register(online.OnlineConn{
		SessionID: 1,
		UID:       "u1",
		State:     online.LocalRouteStateActive,
		Session:   origin.Session,
	}))
	require.NoError(t, registry.Register(online.OnlineConn{
		SessionID: 3,
		UID:       "u1",
		State:     online.LocalRouteStateActive,
		Session:   mirror.Session,
	}))
	require.NoError(t, registry.Register(online.OnlineConn{
		SessionID: 2,
		UID:       "u2",
		State:     online.LocalRouteStateActive,
		Session:   recipient.Session,
	}))

	push := localDeliveryPush{
		online:        registry,
		localNodeID:   1,
		gatewayBootID: 11,
	}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   99,
				MessageSeq:  8,
				ChannelID:   "u1@u2",
				ChannelType: frame.ChannelTypePerson,
				FromUID:     "u1",
				Payload:     []byte("hello"),
			},
			SenderSessionID: 1,
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u1", NodeID: 1, BootID: 11, SessionID: 1},
			{UID: "u1", NodeID: 1, BootID: 11, SessionID: 3},
			{UID: "u2", NodeID: 1, BootID: 11, SessionID: 2},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Accepted, 2)

	require.Empty(t, origin.Writes())
	require.Len(t, mirror.Writes(), 1)
	require.Len(t, recipient.Writes(), 1)

	mirrorPacket, ok := mirror.Writes()[0].f.(*frame.RecvPacket)
	require.True(t, ok)
	recipientPacket, ok := recipient.Writes()[0].f.(*frame.RecvPacket)
	require.True(t, ok)
	require.Equal(t, "u2", mirrorPacket.ChannelID)
	require.Equal(t, "u1", recipientPacket.ChannelID)
}

func TestLocalDeliveryResolverSplitsExpandedRoutesAcrossPages(t *testing.T) {
	store := &resolverSnapshotStore{
		uids: []string{"u2"},
	}
	authority := &recordingAuthoritative{
		batches: map[string][]presence.Route{
			"u2": {
				{UID: "u2", NodeID: 1, BootID: 11, SessionID: 2},
				{UID: "u2", NodeID: 1, BootID: 11, SessionID: 3},
			},
		},
	}
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: store,
		}),
		authority: authority,
		pageSize:  8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "g1",
		ChannelType: 2,
	}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)

	page1, cursor, done, err := resolver.ResolvePage(context.Background(), token, "", 1)
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 11, SessionID: 2}}, page1)
	require.Equal(t, "u2", cursor)
	require.False(t, done)

	page2, cursor, done, err := resolver.ResolvePage(context.Background(), token, cursor, 1)
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 11, SessionID: 3}}, page2)
	require.Equal(t, "u2", cursor)
	require.True(t, done)

	require.Equal(t, 1, store.snapshotCalls)
	require.Equal(t, [][]string{{"u2"}}, authority.uidBatches)
}

type recordingDeliveryOwnerNotifier struct {
	acks        []message.RouteAckCommand
	offlines    []message.SessionClosedCommand
	ackErr      error
	offlineErr  error
	localClosed bool
}

func (r *recordingDeliveryOwnerNotifier) NotifyAck(_ context.Context, _ uint64, cmd message.RouteAckCommand) error {
	r.acks = append(r.acks, cmd)
	return r.ackErr
}

func (r *recordingDeliveryOwnerNotifier) NotifyOffline(_ context.Context, _ uint64, cmd message.SessionClosedCommand) error {
	r.offlines = append(r.offlines, cmd)
	return r.offlineErr
}

func remoteAcksHas(idx *deliveryruntime.AckIndex, sessionID, messageID uint64) bool {
	if idx == nil {
		return false
	}
	_, ok := idx.Lookup(sessionID, messageID)
	return ok
}

type outboundWrite struct {
	f    frame.Frame
	meta gatewaysession.OutboundMeta
}

type optionRecordingSession struct {
	gatewaysession.Session
	mu     sync.Mutex
	writes []outboundWrite
}

func newOptionRecordingSession(id uint64, listener string) *optionRecordingSession {
	recorder := &optionRecordingSession{}
	recorder.Session = gatewaysession.New(gatewaysession.Config{
		ID:       id,
		Listener: listener,
		WriteFrameFn: func(f frame.Frame, meta gatewaysession.OutboundMeta) error {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.writes = append(recorder.writes, outboundWrite{f: f, meta: meta})
			return nil
		},
	})
	return recorder
}

func (s *optionRecordingSession) Writes() []outboundWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]outboundWrite, len(s.writes))
	copy(out, s.writes)
	return out
}

type recordingRouteAcker struct {
	acks []message.RouteAckCommand
	err  error
}

func (r *recordingRouteAcker) AckRoute(_ context.Context, cmd message.RouteAckCommand) error {
	r.acks = append(r.acks, cmd)
	return r.err
}

type recordingSessionCloser struct {
	closed []message.SessionClosedCommand
	err    error
}

func (r *recordingSessionCloser) SessionClosed(_ context.Context, cmd message.SessionClosedCommand) error {
	r.closed = append(r.closed, cmd)
	return r.err
}

type recordingCommittedSubmitter struct {
	submitCalls int
	calls       []deliveryruntime.CommittedEnvelope
}

func (r *recordingCommittedSubmitter) SubmitCommitted(_ context.Context, env deliveryruntime.CommittedEnvelope) error {
	r.submitCalls++
	copied := env
	copied.Payload = append([]byte(nil), env.Payload...)
	r.calls = append(r.calls, copied)
	return nil
}

type recordingConversationSubmitter struct {
	submitCalls int
	calls       []channel.Message
}

func (r *recordingConversationSubmitter) SubmitCommitted(_ context.Context, msg channel.Message) error {
	r.submitCalls++
	copied := msg
	copied.Payload = append([]byte(nil), msg.Payload...)
	r.calls = append(r.calls, copied)
	return nil
}

type recordingFlushingConversationSubmitter struct {
	recordingConversationSubmitter
	flushCalls int
}

func (r *recordingFlushingConversationSubmitter) Flush(context.Context) error {
	r.flushCalls++
	return nil
}

type resolverSnapshotStore struct {
	snapshotCalls int
	uids          []string
}

func (s *resolverSnapshotStore) SnapshotChannelSubscribers(_ context.Context, _ string, _ int64) ([]string, error) {
	s.snapshotCalls++
	return append([]string(nil), s.uids...), nil
}

func (s *resolverSnapshotStore) ListChannelSubscribers(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	return nil, "", true, nil
}

type recordingAuthoritative struct {
	uidBatches [][]string
	batches    map[string][]presence.Route
}

func (r *recordingAuthoritative) RegisterAuthoritative(context.Context, presence.RegisterAuthoritativeCommand) (presence.RegisterAuthoritativeResult, error) {
	return presence.RegisterAuthoritativeResult{}, nil
}

func (r *recordingAuthoritative) UnregisterAuthoritative(context.Context, presence.UnregisterAuthoritativeCommand) error {
	return nil
}

func (r *recordingAuthoritative) HeartbeatAuthoritative(context.Context, presence.HeartbeatAuthoritativeCommand) (presence.HeartbeatAuthoritativeResult, error) {
	return presence.HeartbeatAuthoritativeResult{}, nil
}

func (r *recordingAuthoritative) ReplayAuthoritative(context.Context, presence.ReplayAuthoritativeCommand) error {
	return nil
}

func (r *recordingAuthoritative) EndpointsByUID(context.Context, string) ([]presence.Route, error) {
	return nil, nil
}

func (r *recordingAuthoritative) EndpointsByUIDs(_ context.Context, uids []string) (map[string][]presence.Route, error) {
	r.uidBatches = append(r.uidBatches, append([]string(nil), uids...))
	out := make(map[string][]presence.Route, len(uids))
	for _, uid := range uids {
		out[uid] = append([]presence.Route(nil), r.batches[uid]...)
	}
	return out, nil
}

type stubChannelLogCluster struct {
	mu          sync.Mutex
	status      channel.ChannelRuntimeStatus
	statusErr   error
	statusCalls int
}

func (s *stubChannelLogCluster) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	s.mu.Lock()
	s.statusCalls++
	s.mu.Unlock()
	return s.status, s.statusErr
}

func (s *stubChannelLogCluster) StatusCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusCalls
}
