package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	deliverytagruntime "github.com/WuKongIM/WuKongIM/internal/runtime/deliverytag"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gatewaysession "github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
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

func TestCommittedDispatchQueuePreservesPerChannelOrder(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		Delivery:    delivery,
		ShardCount:  1,
		QueueDepth:  8,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	for seq := uint64(1); seq <= 3; seq++ {
		require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			MessageID:   seq,
			MessageSeq:  seq,
		}}))
	}

	require.Eventually(t, func() bool { return delivery.Len() == 3 }, time.Second, time.Millisecond)
	require.Equal(t, []uint64{1, 2, 3}, delivery.messageSeqs())
}

func TestCommittedDispatchQueueOverflowFallbackDoesNotFlushConversation(t *testing.T) {
	conversation := &recordingFlushingConversationSubmitter{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID:           1,
		Conversation:          conversation,
		ShardCount:            1,
		QueueDepth:            1,
		DisableWorkersForTest: true,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}}))
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 2}}))
	require.Eventually(t, func() bool { return conversation.SubmitCalls() == 1 }, time.Second, time.Millisecond)
	require.Equal(t, 0, conversation.FlushCalls())
}

func TestCommittedDispatchQueueRejectsBeforeStart(t *testing.T) {
	conversation := &recordingFlushingConversationSubmitter{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID:  1,
		Conversation: conversation,
		ShardCount:   1,
		QueueDepth:   1,
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}})

	require.ErrorIs(t, err, errCommittedDispatcherStopped)
	require.Equal(t, 0, conversation.SubmitCalls())
	require.Equal(t, 0, conversation.FlushCalls())
}

func TestCommittedDispatchQueueRejectsAfterStop(t *testing.T) {
	conversation := &recordingFlushingConversationSubmitter{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID:  1,
		Conversation: conversation,
		ShardCount:   1,
		QueueDepth:   1,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	require.NoError(t, dispatcher.Stop())

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}})

	require.ErrorIs(t, err, errCommittedDispatcherStopped)
	require.Equal(t, 0, conversation.SubmitCalls())
	require.Equal(t, 0, conversation.FlushCalls())
}

func TestCommittedDispatchQueueStopContextBoundsBlockedWorker(t *testing.T) {
	delivery := newBlockingCommittedSubmitter()
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		Delivery:    delivery,
		ShardCount:  1,
		QueueDepth:  1,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}}))
	delivery.WaitEntered(t)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err := dispatcher.StopContext(stopCtx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	delivery.Release()
	require.Eventually(t, func() bool { return dispatcher.isStoppedForTest() }, time.Second, time.Millisecond)
}

func TestCommittedDispatchQueueStopContextCancelsInFlightRoute(t *testing.T) {
	delivery := newContextAwareBlockingCommittedSubmitter()
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		Delivery:    delivery,
		ShardCount:  1,
		QueueDepth:  1,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}}))
	delivery.WaitEntered(t)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, dispatcher.StopContext(stopCtx))
	require.ErrorIs(t, delivery.Err(), context.Canceled)
}

func TestCommittedDispatchQueueStopContextCanBeRetriedAfterTimeout(t *testing.T) {
	delivery := newBlockingCommittedSubmitter()
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		Delivery:    delivery,
		ShardCount:  1,
		QueueDepth:  1,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}}))
	delivery.WaitEntered(t)

	firstCtx, firstCancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer firstCancel()
	require.ErrorIs(t, dispatcher.StopContext(firstCtx), context.DeadlineExceeded)

	secondCtx, secondCancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer secondCancel()
	require.ErrorIs(t, dispatcher.StopContext(secondCtx), context.DeadlineExceeded)

	delivery.Release()
	require.Eventually(t, func() bool { return dispatcher.isStoppedForTest() }, time.Second, time.Millisecond)
	require.NoError(t, dispatcher.StopContext(context.Background()))
}

func TestCommittedDispatchQueueRecordsMetrics(t *testing.T) {
	metrics := &recordingCommittedDispatchMetrics{}
	conversation := &recordingFlushingConversationSubmitter{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID:           1,
		Conversation:          conversation,
		Metrics:               metrics,
		ShardCount:            1,
		QueueDepth:            1,
		DisableWorkersForTest: true,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}}))
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 2}}))

	require.Equal(t, []string{"0:ok", "0:overflow"}, metrics.enqueues)
	require.Equal(t, []string{"0"}, metrics.overflows)
	require.Contains(t, metrics.depths, "0:1")
}

func TestAsyncCommittedDispatcherRecordsQueueMetrics(t *testing.T) {
	metrics := &recordingCommittedDispatchMetrics{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID:           1,
		Conversation:          &recordingFlushingConversationSubmitter{},
		Metrics:               metrics,
		ShardCount:            1,
		QueueDepth:            1,
		DisableWorkersForTest: true,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}}))
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 2}}))

	require.Equal(t, []string{"0:ok", "0:overflow"}, metrics.enqueues)
	require.NotEmpty(t, metrics.DepthSnapshots())
}

func TestCommittedDispatchQueueOverflowFallbackDoesNotBlockSubmit(t *testing.T) {
	conversation := newBlockingConversationSubmitter()
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID:           1,
		Conversation:          conversation,
		ShardCount:            1,
		QueueDepth:            1,
		DisableWorkersForTest: true,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() {
		conversation.Release()
		require.NoError(t, dispatcher.Stop())
	}()

	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}}))
	done := make(chan error, 1)
	go func() {
		done <- dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 2}})
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		require.FailNow(t, "overflow fallback blocked SubmitCommitted")
	}
	conversation.WaitEntered(t)
}

func TestCommittedDispatchQueueResetsDepthOnStop(t *testing.T) {
	metrics := &recordingCommittedDispatchMetrics{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID:           1,
		Conversation:          &recordingFlushingConversationSubmitter{},
		Metrics:               metrics,
		ShardCount:            1,
		QueueDepth:            1,
		DisableWorkersForTest: true,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 1}}))

	require.NoError(t, dispatcher.Stop())

	require.Contains(t, metrics.DepthSnapshots(), "0:0")
}

func TestAsyncCommittedDispatcherFallsBackToLocalConversationWhenOwnerIsUnknown(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	conversation := &recordingFlushingConversationSubmitter{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		ChannelLog: &stubChannelLogCluster{
			statusErr: errors.New("leader unknown"),
		},
		Delivery:     delivery,
		Conversation: conversation,
		ShardCount:   1,
		QueueDepth:   8,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		Message: channel.Message{
			ChannelID:   "g1",
			ChannelType: 2,
			MessageID:   101,
			MessageSeq:  1,
		},
	}))

	require.Eventually(t, func() bool {
		return delivery.SubmitCalls() == 0 && conversation.SubmitCalls() == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 0, conversation.FlushCalls())
}

func TestAsyncCommittedDispatcherReturnsScopedRemoteSubmitError(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	conversation := &recordingConversationSubmitter{}
	nodeClient := &recordingCommittedNodeSubmitter{err: accessnode.ErrMessageScopedDeliverySubmitUnsupported}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		ChannelLog: &stubChannelLogCluster{
			status: channel.ChannelRuntimeStatus{
				Leader: 2,
			},
		},
		Delivery:     delivery,
		Conversation: conversation,
		NodeClient:   nodeClient,
		ShardCount:   1,
		QueueDepth:   8,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		Message: channel.Message{
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			MessageID:   101,
			MessageSeq:  1,
		},
		MessageScopedUIDs: []string{"u1"},
	})

	require.ErrorIs(t, err, accessnode.ErrMessageScopedDeliverySubmitUnsupported)
	require.Equal(t, 0, delivery.SubmitCalls())
	require.Equal(t, 0, conversation.Len())
	require.Len(t, nodeClient.calls, 1)
	require.Equal(t, uint64(2), nodeClient.calls[0].nodeID)
	require.Equal(t, []string{"u1"}, nodeClient.calls[0].env.MessageScopedUIDs)
}

func TestAsyncCommittedDispatcherSubmitsDurableMessageToLocalDelivery(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		ChannelLog: &stubChannelLogCluster{
			status: channel.ChannelRuntimeStatus{
				Leader: 1,
			},
		},
		Delivery:   delivery,
		ShardCount: 1,
		QueueDepth: 8,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
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
		return delivery.Len() == 1
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
	}, delivery.Calls()[0].Message)
}

func TestAsyncCommittedDispatcherDoesNotWarnWhenTransientStatusFailureRecovers(t *testing.T) {
	logger := newCapturedLogger("")
	delivery := &recordingCommittedSubmitter{}
	channelLog := &transientStatusChannelLog{
		results: []statusResult{
			{err: channel.ErrNotReady},
			{status: channel.ChannelRuntimeStatus{Leader: 1}},
		},
	}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		Logger:      logger,
		ChannelLog:  channelLog,
		Delivery:    delivery,
		ShardCount:  1,
		QueueDepth:  8,
	})

	dispatcher.routeCommitted(context.Background(), deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "u1@u2",
			ChannelType: frame.ChannelTypePerson,
			MessageID:   89,
			MessageSeq:  8,
		},
	})

	require.Equal(t, 2, channelLog.Calls())
	require.Equal(t, 1, delivery.Len())
	for _, entry := range logger.entries() {
		require.NotEqual(t, "WARN", entry.level, "transient status retry should not warn: %#v", entry)
	}
}

func TestAsyncCommittedDispatcherSubmitsToConversationProjector(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	conversation := &recordingConversationSubmitter{}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		ChannelLog: &stubChannelLogCluster{
			status: channel.ChannelRuntimeStatus{
				Leader: 1,
			},
		},
		Delivery:     delivery,
		Conversation: conversation,
		ShardCount:   1,
		QueueDepth:   8,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	msg := channel.Message{
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		MessageID:   99,
		MessageSeq:  8,
		ClientMsgNo: "c1",
		FromUID:     "u1",
		Payload:     []byte("hello projector"),
	}
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: msg}))

	require.Eventually(t, func() bool {
		return delivery.Len() == 1 && conversation.Len() == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, msg, delivery.Calls()[0].Message)
	require.Equal(t, msg, conversation.Calls()[0])
}

func TestAsyncCommittedDispatcherRoutesDurableMessageToRemoteOwner(t *testing.T) {
	delivery := &recordingCommittedSubmitter{}
	conversation := &recordingConversationSubmitter{}
	nodeClient := &recordingCommittedNodeSubmitter{}
	channelLog := &stubChannelLogCluster{
		status: channel.ChannelRuntimeStatus{
			Leader: 2,
		},
	}
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID:  1,
		ChannelLog:   channelLog,
		Delivery:     delivery,
		Conversation: conversation,
		NodeClient:   nodeClient,
		ShardCount:   1,
		QueueDepth:   8,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() { require.NoError(t, dispatcher.Stop()) }()

	msg := channel.Message{
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		MessageID:   100,
		MessageSeq:  9,
		ClientMsgNo: "c2",
		FromUID:     "u1",
		Payload:     []byte("hello local"),
	}
	require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: msg}))

	require.Eventually(t, func() bool {
		return nodeClient.Len() == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 1, channelLog.StatusCalls())
	require.Equal(t, 0, delivery.Len())
	require.Equal(t, 0, conversation.Len())
	calls := nodeClient.Calls()
	require.Equal(t, uint64(2), calls[0].nodeID)
	require.Equal(t, msg, calls[0].env.Message)
}

func TestDeliverySuccessDiagnosticsUseDebugLevel(t *testing.T) {
	logger := newCapturedLogger("")

	dispatcher := asyncCommittedDispatcher{logger: logger}
	dispatcher.logCommittedRoute(deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "g1",
			ChannelType: 2,
			MessageID:   101,
			MessageSeq:  1,
		},
	}, "local_owner", 1, nil)
	requireCapturedLogEntry(t, logger, "DEBUG", "", "delivery.diag.committed_route")

	store := &resolverSnapshotStore{uids: []string{"u2"}}
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: store,
		}),
		authority: &recordingAuthoritative{
			batches: map[string][]presence.Route{
				"u2": {{UID: "u2", NodeID: 1, BootID: 11, SessionID: 2}},
			},
		},
		pageSize: 8,
		logger:   logger,
	}
	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "g1",
		ChannelType: 2,
	}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)
	_, err = resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	requireCapturedLogEntry(t, logger, "DEBUG", "", "delivery.diag.resolve_page")

	push := localDeliveryPush{
		online:        online.NewRegistry(),
		localNodeID:   1,
		gatewayBootID: 11,
		logger:        logger,
	}
	_, err = push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				ChannelID:   "g1",
				ChannelType: 2,
				MessageID:   102,
				MessageSeq:  2,
			},
		},
	})
	require.NoError(t, err)
	requireCapturedLogEntry(t, logger, "DEBUG", "", "delivery.diag.local_push")
}

func TestLocalDeliveryPushSkipsDiagnosticsWhenDebugDisabled(t *testing.T) {
	logger := &debugDisabledCapturedLogger{capturedLogger: newCapturedLogger("")}
	push := localDeliveryPush{
		online:        online.NewRegistry(),
		localNodeID:   1,
		gatewayBootID: 11,
		logger:        logger,
	}

	_, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				ChannelID:   "g1",
				ChannelType: 2,
				MessageID:   102,
				MessageSeq:  2,
			},
		},
	})
	require.NoError(t, err)
	require.Empty(t, logger.entries())
}

func TestDeliveryResolverMissingAuthoritativeEndpointsUseDebugLevel(t *testing.T) {
	logger := newCapturedLogger("")
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverSnapshotStore{uids: []string{"offline-u2"}},
		}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{}},
		pageSize:  8,
		logger:    logger,
	}
	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "g1",
		ChannelType: 2,
	}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.Empty(t, page.Routes)
	require.True(t, page.Done)
	entry := requireCapturedLogEntry(t, logger, "DEBUG", "", "delivery.diag.resolve_page")
	require.Equal(t, "offline-u2", requireCapturedFieldValue[string](t, entry, "missingUIDs"))
}

func TestLocalDeliveryResolverUsesMessageScopedSubscribers(t *testing.T) {
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"u1": {{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}},
			"u2": {{UID: "u2", NodeID: 2, BootID: 22, SessionID: 202}},
		}},
		pageSize: 8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "tmp1____cmd",
		ChannelType: frame.ChannelTypeTemp,
	}, deliveryruntime.CommittedEnvelope{MessageScopedUIDs: []string{"u1", "u2"}})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101},
		{UID: "u2", NodeID: 2, BootID: 22, SessionID: 202},
	}, page.Routes)
}

func TestLocalDeliveryResolverNotifiesUIDObserverBeforePresenceExpansion(t *testing.T) {
	observer := &recordingResolvedUIDObserver{}
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverSnapshotStore{uids: []string{"offline", "online"}},
		}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"online": {{UID: "online", NodeID: 1, BootID: 11, SessionID: 101}},
		}},
		uidObserver: observer,
		pageSize:    8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{Message: channel.Message{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		MessageSeq:  9,
	}})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, []string{"offline", "online"}, observer.pages[0].UIDs)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "online", NodeID: 1, BootID: 11, SessionID: 101}}, page.Routes)
}

func TestTagDeliveryResolverNotifiesUIDObserverBeforePresenceExpansion(t *testing.T) {
	observer := &recordingResolvedUIDObserver{}
	manager := deliverytagruntime.NewManager(deliverytagruntime.Options{
		LocalNodeID: 1,
		NewTagKey:   func() string { return "tag-observer" },
	})
	resolver := tagDeliveryResolver{
		localNodeID: 1,
		tags:        manager,
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverVersionStore{uids: []string{"offline", "online"}, version: 4},
		}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"online": {{UID: "online", NodeID: 1, BootID: 11, SessionID: 101}},
		}},
		topology:    staticDeliveryTagTopology{version: testDeliveryTagTopology(1)},
		uidObserver: observer,
		pageSize:    8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{Message: channel.Message{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		MessageSeq:  9,
	}})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, []string{"offline", "online"}, observer.pages[0].UIDs)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "online", NodeID: 1, BootID: 11, SessionID: 101}}, page.Routes)
}

func TestDeliveryUIDObserverSkipsWhenRequestScopedIntentAlreadySubmitted(t *testing.T) {
	observer := &recordingResolvedUIDObserver{}
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"u1": {{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}},
		}},
		uidObserver: observer,
		pageSize:    8,
	}
	env := deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   "temp____cmd",
			ChannelType: frame.ChannelTypeTemp,
			MessageSeq:  1,
		},
		MessageScopedUIDs:              []string{"u1"},
		CMDConversationIntentSubmitted: true,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   env.ChannelID,
		ChannelType: env.ChannelType,
	}, env)
	require.NoError(t, err)
	page, err := resolver.ResolvePage(context.Background(), token, "", 8)

	require.NoError(t, err)
	require.True(t, page.Done)
	require.Empty(t, observer.pages)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}}, page.Routes)
}

func TestDeliveryUIDObserverDoesNotAffectResolvePage(t *testing.T) {
	observer := &recordingResolvedUIDObserver{internalErr: errors.New("observer side effect failed")}
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverSnapshotStore{uids: []string{"u1"}},
		}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"u1": {{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}},
		}},
		uidObserver: observer,
		pageSize:    8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{Message: channel.Message{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		MessageSeq:  9,
	}})
	require.NoError(t, err)
	page, err := resolver.ResolvePage(context.Background(), token, "", 8)

	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, observer.internalErr, observer.errs[0])
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}}, page.Routes)
}

func TestTagDeliveryResolverFallbackNotifiesUIDObserver(t *testing.T) {
	observer := &recordingResolvedUIDObserver{}
	metrics := &recordingDeliveryRoutingMetrics{}
	resolver := tagDeliveryResolver{
		localNodeID: 1,
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverSnapshotStore{uids: []string{"u1"}},
		}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"u1": {{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}},
		}},
		uidObserver: observer,
		metrics:     metrics,
		pageSize:    8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{Message: channel.Message{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		MessageSeq:  9,
	}})
	require.NoError(t, err)
	page, err := resolver.ResolvePage(context.Background(), token, "", 8)

	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, []string{"u1"}, observer.pages[0].UIDs)
	require.Equal(t, []string{"group:ok:1:1"}, metrics.resolves)
}

func TestLocalDeliveryResolverRoutesNonDurableCommandGroupToRemoteSessions(t *testing.T) {
	store := &resolverSnapshotStore{
		uids: []string{"u-local", "u-remote"},
	}
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{Store: store}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"u-local":  {{UID: "u-local", NodeID: 1, BootID: 11, SessionID: 101}},
			"u-remote": {{UID: "u-remote", NodeID: 2, BootID: 22, SessionID: 202}},
		}},
		pageSize: 8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{Message: channel.Message{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
		MessageID:   950,
		MessageSeq:  0,
	}})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u-local", NodeID: 1, BootID: 11, SessionID: 101},
		{UID: "u-remote", NodeID: 2, BootID: 22, SessionID: 202},
	}, page.Routes)
}

func TestDeliveryRoutingUsesTagPartition(t *testing.T) {
	manager := deliverytagruntime.NewManager(deliverytagruntime.Options{
		LocalNodeID: 1,
		NewTagKey:   func() string { return "tag-1" },
	})
	resolver := tagDeliveryResolver{
		localNodeID: 1,
		tags:        manager,
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverVersionStore{
				uids:    []string{"u1", "u2", "u3"},
				version: 4,
			},
		}),
		authority: &recordingAuthoritative{
			batches: map[string][]presence.Route{
				"u1": {{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}},
				"u3": {{UID: "u3", NodeID: 1, BootID: 11, SessionID: 103}},
			},
		},
		topology: staticDeliveryTagTopology{version: deliverytagruntime.PartitionTopologyVersion{
			HashSlotTableVersion: 9,
			SlotAuthorityRefs: []deliverytagruntime.SlotAuthorityRef{
				{SlotID: 1, LeaderNodeID: 1, ConfigEpoch: 2, BalanceVersion: 3},
				{SlotID: 2, LeaderNodeID: 2, ConfigEpoch: 2, BalanceVersion: 3},
			},
		}},
		pageSize: 8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101},
		{UID: "u3", NodeID: 1, BootID: 11, SessionID: 103},
	}, page.Routes)
	require.Equal(t, "u2", page.NextCursor)
	require.True(t, page.Done)
	require.Equal(t, [][]string{{"u1", "u3", "u2"}}, resolver.authority.(*recordingAuthoritative).uidBatches)

	ref, ok := manager.CurrentRef("2:g1")
	require.True(t, ok)
	require.Equal(t, "tag-1", ref.TagKey)
	require.Equal(t, uint64(1), ref.TagVersion)
	require.Equal(t, uint64(4), ref.SubscriberMutationVersion)
	tag, ok := manager.LookupTag("tag-1")
	require.True(t, ok)
	require.Equal(t, []deliverytagruntime.NodePartition{
		{NodeID: 1, UIDs: []string{"u1", "u3"}},
		{NodeID: 2, UIDs: []string{"u2"}},
	}, tag.Partitions)
}

func TestDeliveryRoutingExpandsAllLeaderTagPartitionsForFanout(t *testing.T) {
	manager := deliverytagruntime.NewManager(deliverytagruntime.Options{
		LocalNodeID: 1,
		NewTagKey:   func() string { return "tag-all" },
	})
	resolver := tagDeliveryResolver{
		localNodeID: 1,
		tags:        manager,
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverVersionStore{
				uids:    []string{"u-local", "u-remote"},
				version: 4,
			},
		}),
		authority: &recordingAuthoritative{
			batches: map[string][]presence.Route{
				"u-local":  {{UID: "u-local", NodeID: 1, BootID: 11, SessionID: 101}},
				"u-remote": {{UID: "u-remote", NodeID: 2, BootID: 22, SessionID: 202}},
			},
		},
		topology: staticDeliveryTagTopology{version: deliverytagruntime.PartitionTopologyVersion{
			HashSlotTableVersion: 9,
			SlotAuthorityRefs: []deliverytagruntime.SlotAuthorityRef{
				{SlotID: 1, LeaderNodeID: 1, ConfigEpoch: 2, BalanceVersion: 3},
				{SlotID: 2, LeaderNodeID: 2, ConfigEpoch: 2, BalanceVersion: 3},
			},
		}},
		pageSize: 8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u-local", NodeID: 1, BootID: 11, SessionID: 101},
		{UID: "u-remote", NodeID: 2, BootID: 22, SessionID: 202},
	}, page.Routes)
	require.Equal(t, [][]string{{"u-local", "u-remote"}}, resolver.authority.(*recordingAuthoritative).uidBatches)
}

func TestDeliveryRoutingBypassesTagPartitionForPersonChannels(t *testing.T) {
	manager := deliverytagruntime.NewManager(deliverytagruntime.Options{
		LocalNodeID: 2,
		NewTagKey:   func() string { return "tag-person" },
	})
	resolver := tagDeliveryResolver{
		localNodeID: 2,
		tags:        manager,
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{}),
		authority: &recordingAuthoritative{single: map[string][]presence.Route{
			"u2": {{UID: "u2", NodeID: 3, BootID: 33, SessionID: 202}},
		}},
		topology: staticDeliveryTagTopology{version: deliverytagruntime.PartitionTopologyVersion{
			HashSlotTableVersion: 9,
			SlotAuthorityRefs: []deliverytagruntime.SlotAuthorityRef{
				{SlotID: 1, LeaderNodeID: 1, ConfigEpoch: 2, BalanceVersion: 3},
			},
		}},
		pageSize: 8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   deliveryusecase.EncodePersonChannel("u1", "u2"),
		ChannelType: frame.ChannelTypePerson,
	}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u2", NodeID: 3, BootID: 33, SessionID: 202}}, page.Routes)
	require.Equal(t, "u1", page.NextCursor)
	require.True(t, page.Done)
	require.Equal(t, []string{"u2", "u1"}, resolver.authority.(*recordingAuthoritative).uidCalls)
	require.Empty(t, resolver.authority.(*recordingAuthoritative).uidBatches)

	_, ok := manager.CurrentRef("1:" + deliveryusecase.EncodePersonChannel("u1", "u2"))
	require.False(t, ok)
}

func TestDeliveryRoutingMessageScopedTagDoesNotReplaceReusableRef(t *testing.T) {
	var tagSeq int
	manager := deliverytagruntime.NewManager(deliverytagruntime.Options{
		LocalNodeID: 1,
		NewTagKey: func() string {
			tagSeq++
			return fmt.Sprintf("tag-scoped-%d", tagSeq)
		},
	})
	topology := deliverytagruntime.PartitionTopologyVersion{
		HashSlotTableVersion: 9,
		SlotAuthorityRefs: []deliverytagruntime.SlotAuthorityRef{
			{SlotID: 1, LeaderNodeID: 1, ConfigEpoch: 2, BalanceVersion: 3},
		},
	}
	reusable, created := manager.BuildLeaderTag(deliverytagruntime.BuildRequest{
		ChannelKey:                "8:tmp1____cmd",
		SubscriberMutationVersion: 7,
		Topology:                  topology,
		Partitions:                []deliverytagruntime.NodePartition{{NodeID: 1, UIDs: []string{"ordinary"}}},
	})
	require.True(t, created)

	resolver := tagDeliveryResolver{
		localNodeID: 1,
		tags:        manager,
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"scoped": {{UID: "scoped", NodeID: 1, BootID: 11, SessionID: 101}},
		}},
		topology: staticDeliveryTagTopology{version: topology},
		pageSize: 8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "tmp1____cmd",
		ChannelType: frame.ChannelTypeTemp,
	}, deliveryruntime.CommittedEnvelope{MessageScopedUIDs: []string{"scoped"}})
	require.NoError(t, err)
	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "scoped", NodeID: 1, BootID: 11, SessionID: 101}}, page.Routes)

	ref, ok := manager.CurrentRef("8:tmp1____cmd")
	require.True(t, ok)
	require.Equal(t, reusable.Key, ref.TagKey)
}

func TestNextDeliveryTagUIDPageAtUsesOffsetWithoutAllocating(t *testing.T) {
	uids := []string{"u1", "u2", "u3", "u4"}

	allocs := testing.AllocsPerRun(100, func() {
		page, nextIndex, done := nextDeliveryTagUIDPageAt(uids, 1, 2)
		_, _, _ = page, nextIndex, done
	})
	require.Zero(t, allocs)

	page, nextIndex, done := nextDeliveryTagUIDPageAt(uids, 1, 2)
	require.Equal(t, []string{"u2", "u3"}, page)
	require.Equal(t, 3, nextIndex)
	require.False(t, done)

	page[0] = "changed"
	require.Equal(t, "changed", uids[1])
}

func TestDeliveryRoutingUsesCachedTagWithoutListingSubscribers(t *testing.T) {
	topology := deliverytagruntime.PartitionTopologyVersion{
		HashSlotTableVersion: 9,
		SlotAuthorityRefs: []deliverytagruntime.SlotAuthorityRef{
			{SlotID: 1, LeaderNodeID: 1, ConfigEpoch: 2, BalanceVersion: 3},
			{SlotID: 2, LeaderNodeID: 2, ConfigEpoch: 2, BalanceVersion: 3},
		},
	}
	manager := deliverytagruntime.NewManager(deliverytagruntime.Options{
		LocalNodeID: 1,
		NewTagKey:   func() string { return "tag-cached" },
	})
	_, created := manager.BuildLeaderTag(deliverytagruntime.BuildRequest{
		ChannelKey:                      "2:g-cached",
		SubscriberMutationVersion:       4,
		SourceChannelKey:                "2:g-cached",
		SourceSubscriberMutationVersion: 4,
		Topology:                        topology,
		Partitions: []deliverytagruntime.NodePartition{
			{NodeID: 1, UIDs: []string{"u1", "u3"}},
			{NodeID: 2, UIDs: []string{"u2"}},
		},
	})
	require.True(t, created)

	store := &resolverVersionStore{
		uids:    []string{"u1", "u2", "u3"},
		version: 4,
	}
	resolver := tagDeliveryResolver{
		localNodeID: 1,
		tags:        manager,
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{Store: store}),
		authority: &recordingAuthoritative{
			batches: map[string][]presence.Route{
				"u1": {{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}},
				"u3": {{UID: "u3", NodeID: 1, BootID: 11, SessionID: 103}},
			},
		},
		topology: staticDeliveryTagTopology{version: topology},
		pageSize: 8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "g-cached",
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)
	require.Zero(t, store.pageCalls)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101},
		{UID: "u3", NodeID: 1, BootID: 11, SessionID: 103},
	}, page.Routes)
	require.Equal(t, "u2", page.NextCursor)
	require.True(t, page.Done)
	require.Zero(t, store.pageCalls)
}

func TestDeliveryRoutingSkipsCachedTagFastPathWithoutVersionFence(t *testing.T) {
	topology := deliverytagruntime.PartitionTopologyVersion{
		HashSlotTableVersion: 9,
		SlotAuthorityRefs: []deliverytagruntime.SlotAuthorityRef{
			{SlotID: 1, LeaderNodeID: 1, ConfigEpoch: 2, BalanceVersion: 3},
			{SlotID: 2, LeaderNodeID: 2, ConfigEpoch: 2, BalanceVersion: 3},
		},
	}
	manager := deliverytagruntime.NewManager(deliverytagruntime.Options{
		LocalNodeID: 1,
		NewTagKey:   func() string { return "tag-unversioned" },
	})
	_, created := manager.BuildLeaderTag(deliverytagruntime.BuildRequest{
		ChannelKey: "2:g-unversioned",
		Topology:   topology,
		Partitions: []deliverytagruntime.NodePartition{
			{NodeID: 1, UIDs: []string{"u1", "u3"}},
			{NodeID: 2, UIDs: []string{"u2"}},
		},
	})
	require.True(t, created)

	store := &resolverVersionStore{
		uids:    []string{"u1", "u2", "u3"},
		version: 0,
	}
	resolver := tagDeliveryResolver{
		localNodeID: 1,
		tags:        manager,
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{Store: store}),
		authority: &recordingAuthoritative{
			batches: map[string][]presence.Route{
				"u1": {{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}},
				"u3": {{UID: "u3", NodeID: 1, BootID: 11, SessionID: 103}},
			},
		},
		topology: staticDeliveryTagTopology{version: topology},
		pageSize: 8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "g-unversioned",
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)
	require.NotZero(t, store.pageCalls)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.Len(t, page.Routes, 2)
	require.True(t, page.Done)
}

func TestDeliveryTagTopologyReaderUsesCachedAssignmentsWithoutControllerRefresh(t *testing.T) {
	cluster := &recordingDeliveryTagCluster{
		slotByKey: map[string]multiraft.SlotID{
			"u1": 2,
			"u2": 1,
			"u3": 2,
		},
		leaderBySlot: map[multiraft.SlotID]multiraft.NodeID{
			1: 11,
			2: 12,
		},
		hashSlotTableVersion: 9,
		cachedAssignments: []controllermeta.SlotAssignment{
			{SlotID: 1, ConfigEpoch: 21, BalanceVersion: 31},
			{SlotID: 2, ConfigEpoch: 22, BalanceVersion: 32},
		},
		listAssignments: []controllermeta.SlotAssignment{
			{SlotID: 1, ConfigEpoch: 99, BalanceVersion: 99},
			{SlotID: 2, ConfigEpoch: 99, BalanceVersion: 99},
		},
	}
	reader := deliveryTagTopologyReaderAdapter{cluster: cluster}

	topology, err := reader.CurrentDeliveryTagTopology(context.Background(), []string{"u1", "u2", "u3"})

	require.NoError(t, err)
	require.Zero(t, cluster.ListSlotAssignmentsCalls())
	require.Equal(t, deliverytagruntime.PartitionTopologyVersion{
		HashSlotTableVersion: 9,
		SlotAuthorityRefs: []deliverytagruntime.SlotAuthorityRef{
			{SlotID: 1, LeaderNodeID: 11, ConfigEpoch: 21, BalanceVersion: 31},
			{SlotID: 2, LeaderNodeID: 12, ConfigEpoch: 22, BalanceVersion: 32},
		},
	}, topology)
}

func TestCurrentDeliveryTagAssignmentBySlotFiltersCachedAssignmentsToRequiredSlots(t *testing.T) {
	assignments := make([]controllermeta.SlotAssignment, 128)
	for i := range assignments {
		assignments[i] = controllermeta.SlotAssignment{
			SlotID:         uint32(i),
			ConfigEpoch:    uint64(i + 100),
			BalanceVersion: uint64(i + 200),
		}
	}
	cluster := &recordingDeliveryTagCluster{cachedAssignments: assignments}

	got := currentDeliveryTagAssignmentBySlot(context.Background(), cluster, []uint32{3, 7})

	require.Zero(t, cluster.ListSlotAssignmentsCalls())
	require.Len(t, got, 2)
	require.Equal(t, uint64(103), got[3].ConfigEpoch)
	require.Equal(t, uint64(207), got[7].BalanceVersion)
	require.NotContains(t, got, uint32(0))
}

func TestDeliveryTagTopologyValidatorUsesCachedAssignmentsWithoutControllerRefresh(t *testing.T) {
	cluster := &recordingDeliveryTagCluster{
		leaderBySlot: map[multiraft.SlotID]multiraft.NodeID{
			1: 11,
			2: 12,
		},
		hashSlotTableVersion: 9,
		cachedAssignments: []controllermeta.SlotAssignment{
			{SlotID: 1, ConfigEpoch: 21, BalanceVersion: 31},
			{SlotID: 2, ConfigEpoch: 22, BalanceVersion: 32},
		},
		listAssignments: []controllermeta.SlotAssignment{
			{SlotID: 1, ConfigEpoch: 99, BalanceVersion: 99},
			{SlotID: 2, ConfigEpoch: 99, BalanceVersion: 99},
		},
	}
	reader := deliveryTagTopologyReaderAdapter{cluster: cluster}
	topology := deliverytagruntime.PartitionTopologyVersion{
		HashSlotTableVersion: 9,
		SlotAuthorityRefs: []deliverytagruntime.SlotAuthorityRef{
			{SlotID: 1, LeaderNodeID: 11, ConfigEpoch: 21, BalanceVersion: 31},
			{SlotID: 2, LeaderNodeID: 12, ConfigEpoch: 22, BalanceVersion: 32},
		},
	}

	valid, err := reader.ValidateCurrentDeliveryTagTopology(context.Background(), topology)

	require.NoError(t, err)
	require.True(t, valid)
	require.Zero(t, cluster.ListSlotAssignmentsCalls())
}

func TestDeliveryTagTopologyReaderFallsBackToListingAssignmentsWhenCacheMisses(t *testing.T) {
	cluster := &recordingDeliveryTagCluster{
		slotByKey: map[string]multiraft.SlotID{"u1": 1},
		leaderBySlot: map[multiraft.SlotID]multiraft.NodeID{
			1: 11,
		},
		hashSlotTableVersion: 9,
		listAssignments: []controllermeta.SlotAssignment{
			{SlotID: 1, ConfigEpoch: 21, BalanceVersion: 31},
		},
	}
	reader := deliveryTagTopologyReaderAdapter{cluster: cluster}

	topology, err := reader.CurrentDeliveryTagTopology(context.Background(), []string{"u1"})

	require.NoError(t, err)
	require.Equal(t, 1, cluster.ListSlotAssignmentsCalls())
	require.Equal(t, []deliverytagruntime.SlotAuthorityRef{
		{SlotID: 1, LeaderNodeID: 11, ConfigEpoch: 21, BalanceVersion: 31},
	}, topology.SlotAuthorityRefs)
}

func TestDeliveryRoutingRejectsStaleTagResponse(t *testing.T) {
	manager := deliverytagruntime.NewManager(deliverytagruntime.Options{LocalNodeID: 1})
	_, stored := manager.StoreFollowerPartition(deliverytagruntime.DeliveryTag{
		Key:                       "tag-1",
		ChannelKey:                "2:g1",
		TagVersion:                2,
		SubscriberMutationVersion: 2,
		Topology:                  testDeliveryTagTopology(1),
		Partitions: []deliverytagruntime.NodePartition{
			{NodeID: 1, UIDs: []string{"fresh"}},
		},
	})
	require.True(t, stored)
	resolver := tagDeliveryResolver{
		localNodeID: 1,
		tags:        manager,
		authority: &recordingAuthoritative{
			batches: map[string][]presence.Route{
				"fresh": {{UID: "fresh", NodeID: 1, BootID: 11, SessionID: 22}},
			},
		},
	}
	token := &tagResolveToken{
		tag: deliverytagruntime.DeliveryTag{
			Key:                       "tag-1",
			ChannelKey:                "2:g1",
			TagVersion:                1,
			SubscriberMutationVersion: 1,
			Topology:                  testDeliveryTagTopology(1),
			Partitions: []deliverytagruntime.NodePartition{
				{NodeID: 1, UIDs: []string{"stale"}},
			},
		},
		routableUIDs: []string{"stale"},
	}

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "fresh", NodeID: 1, BootID: 11, SessionID: 22}}, page.Routes)
	require.Equal(t, "fresh", page.NextCursor)
	require.True(t, page.Done)
	require.Equal(t, [][]string{{"fresh"}}, resolver.authority.(*recordingAuthoritative).uidBatches)

	tag, ok := manager.LookupTag("tag-1")
	require.True(t, ok)
	require.Equal(t, uint64(2), tag.TagVersion)
	require.Equal(t, []deliverytagruntime.NodePartition{{NodeID: 1, UIDs: []string{"fresh"}}}, tag.Partitions)
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

func TestBuildRealtimeRecvPacketStripsCommandSuffixFromClientChannelView(t *testing.T) {
	t.Run("group", func(t *testing.T) {
		packet := buildRealtimeRecvPacket(channel.Message{
			MessageID:   89,
			MessageSeq:  8,
			ChannelID:   channelid.ToCommandChannel("g1"),
			ChannelType: frame.ChannelTypeGroup,
			FromUID:     "u1",
			Payload:     []byte("hello group cmd"),
		}, "")

		require.Equal(t, "g1", packet.ChannelID)
		require.Equal(t, frame.ChannelTypeGroup, packet.ChannelType)
	})

	t.Run("person", func(t *testing.T) {
		packet := buildRealtimeRecvPacket(channel.Message{
			MessageID:   90,
			MessageSeq:  9,
			ChannelID:   channelid.ToCommandChannel("u1@u2"),
			ChannelType: frame.ChannelTypePerson,
			FromUID:     "u1",
			Payload:     []byte("hello person cmd"),
		}, "u2")

		require.Equal(t, "u1", packet.ChannelID)
		require.Equal(t, frame.ChannelTypePerson, packet.ChannelType)
	})

	t.Run("temp", func(t *testing.T) {
		packet := buildRealtimeRecvPacket(channel.Message{
			MessageID:   91,
			MessageSeq:  0,
			Framer:      frame.Framer{SyncOnce: true, NoPersist: true},
			ChannelID:   channelid.ToCommandChannel("tmp1"),
			ChannelType: frame.ChannelTypeTemp,
			FromUID:     "system",
			Payload:     []byte("hello temp cmd"),
		}, "")

		require.Equal(t, "tmp1", packet.ChannelID)
		require.Equal(t, frame.ChannelTypeTemp, packet.ChannelType)
		require.True(t, packet.Framer.SyncOnce)
		require.True(t, packet.Framer.NoPersist)
	})
}

func TestBuildRealtimeRecvPacketStripsCommandSuffixForNonDurableCommandGroup(t *testing.T) {
	packet := buildRealtimeRecvPacket(channel.Message{
		MessageID:   950,
		MessageSeq:  0,
		Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		FromUID:     "u1",
		Payload:     []byte("online cmd"),
	}, "")

	require.Equal(t, "g1", packet.ChannelID)
	require.Equal(t, frame.ChannelTypeGroup, packet.ChannelType)
	require.True(t, packet.Framer.NoPersist)
	require.True(t, packet.Framer.SyncOnce)
	require.Zero(t, packet.MessageSeq)
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
		Session:   sender,
	}))
	require.NoError(t, registry.Register(online.OnlineConn{
		SessionID: 2,
		UID:       "u2",
		State:     online.LocalRouteStateActive,
		Session:   recipient,
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
				ChannelID:   channelid.ToCommandChannel("u1@u2"),
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

func TestLocalDeliveryPushReusesGroupFrameForAllRoutes(t *testing.T) {
	first := newOptionRecordingSession(1, "tcp")
	second := newOptionRecordingSession(2, "tcp")
	registry := online.NewRegistry()
	require.NoError(t, registry.Register(online.OnlineConn{
		SessionID: 1,
		UID:       "u1",
		State:     online.LocalRouteStateActive,
		Session:   first,
	}))
	require.NoError(t, registry.Register(online.OnlineConn{
		SessionID: 2,
		UID:       "u2",
		State:     online.LocalRouteStateActive,
		Session:   second,
	}))
	push := localDeliveryPush{online: registry, localNodeID: 1, gatewayBootID: 11}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   120,
				MessageSeq:  12,
				ChannelID:   "g-reuse",
				ChannelType: frame.ChannelTypeGroup,
				FromUID:     "u1",
				Payload:     []byte("hello group reuse"),
			},
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u1", NodeID: 1, BootID: 11, SessionID: 1},
			{UID: "u2", NodeID: 1, BootID: 11, SessionID: 2},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Accepted, 2)

	firstWrites := first.Writes()
	secondWrites := second.Writes()
	require.Len(t, firstWrites, 1)
	require.Len(t, secondWrites, 1)
	require.Same(t, firstWrites[0].f, secondWrites[0].f)
}

func TestDistributedDeliveryPushSingleRemoteNodeUsesCallerGoroutine(t *testing.T) {
	client := &sameGoroutineDeliveryPushClient{}
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
	}
	callerGoroutineID := currentTestGoroutineID(t)

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   121,
				MessageSeq:  13,
				ChannelID:   "g-single-remote",
				ChannelType: frame.ChannelTypeGroup,
				FromUID:     "u1",
				Payload:     []byte("hello single remote"),
			},
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
		},
	})

	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
	}, result.Accepted)
	require.NoError(t, client.err)
	require.Equal(t, callerGoroutineID, client.goroutineID)
}

func TestDistributedDeliveryPushRunsRemoteNodesWithBoundedParallelism(t *testing.T) {
	client := newBlockingRemoteDeliveryPushClient()
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
	}

	done := make(chan error, 1)
	go func() {
		_, err := push.Push(context.Background(), deliveryruntime.PushCommand{
			Envelope: deliveryruntime.CommittedEnvelope{
				Message: channel.Message{
					MessageID:   121,
					MessageSeq:  13,
					ChannelID:   "g-remote-parallel",
					ChannelType: frame.ChannelTypeGroup,
					FromUID:     "u1",
					Payload:     []byte("hello remote parallel"),
				},
			},
			Routes: []deliveryruntime.RouteKey{
				{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
				{UID: "u3", NodeID: 3, BootID: 11, SessionID: 31},
				{UID: "u4", NodeID: 4, BootID: 11, SessionID: 41},
				{UID: "u5", NodeID: 5, BootID: 11, SessionID: 51},
				{UID: "u6", NodeID: 6, BootID: 11, SessionID: 61},
			},
		})
		done <- err
	}()
	client.WaitForStarted(t, 1)

	select {
	case <-client.started:
	case <-time.After(50 * time.Millisecond):
		client.Release()
		<-done
		t.Fatal("remote node pushes ran serially instead of concurrently")
	}
	require.Eventually(t, func() bool { return client.MaxActive() >= 2 }, time.Second, time.Millisecond)
	require.LessOrEqual(t, client.MaxActive(), 4)

	client.Release()
	require.NoError(t, <-done)
}

type sameGoroutineDeliveryPushClient struct {
	goroutineID uint64
	err         error
}

func (c *sameGoroutineDeliveryPushClient) PushBatch(_ context.Context, _ uint64, cmd accessnode.DeliveryPushCommand) (accessnode.DeliveryPushResponse, error) {
	return accessnode.DeliveryPushResponse{Accepted: append([]deliveryruntime.RouteKey(nil), cmd.Routes...)}, nil
}

func (c *sameGoroutineDeliveryPushClient) PushBatchItems(_ context.Context, _ uint64, cmd accessnode.DeliveryPushBatchCommand) (accessnode.DeliveryPushResponse, error) {
	c.goroutineID, c.err = currentGoroutineID()
	resp := accessnode.DeliveryPushResponse{}
	for _, item := range cmd.Items {
		resp.Accepted = append(resp.Accepted, item.Routes...)
	}
	return resp, nil
}

func currentTestGoroutineID(t require.TestingT) uint64 {
	id, err := currentGoroutineID()
	require.NoError(t, err)
	return id
}

func currentGoroutineID() (uint64, error) {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := bytes.Fields(buf[:n])
	if len(fields) < 2 {
		return 0, fmt.Errorf("parse goroutine id from %q", string(buf[:n]))
	}
	id, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return id, nil
}

type blockingRemoteDeliveryPushClient struct {
	started chan uint64
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	active  int
	max     int
}

func newBlockingRemoteDeliveryPushClient() *blockingRemoteDeliveryPushClient {
	return &blockingRemoteDeliveryPushClient{
		started: make(chan uint64, 16),
		release: make(chan struct{}),
	}
}

func (c *blockingRemoteDeliveryPushClient) PushBatch(_ context.Context, _ uint64, cmd accessnode.DeliveryPushCommand) (accessnode.DeliveryPushResponse, error) {
	return accessnode.DeliveryPushResponse{Accepted: append([]deliveryruntime.RouteKey(nil), cmd.Routes...)}, nil
}

func (c *blockingRemoteDeliveryPushClient) PushBatchItems(_ context.Context, nodeID uint64, cmd accessnode.DeliveryPushBatchCommand) (accessnode.DeliveryPushResponse, error) {
	c.mu.Lock()
	c.active++
	if c.active > c.max {
		c.max = c.active
	}
	c.mu.Unlock()
	c.started <- nodeID
	<-c.release
	c.mu.Lock()
	c.active--
	c.mu.Unlock()
	resp := accessnode.DeliveryPushResponse{}
	for _, item := range cmd.Items {
		resp.Accepted = append(resp.Accepted, item.Routes...)
	}
	return resp, nil
}

func (c *blockingRemoteDeliveryPushClient) WaitForStarted(t *testing.T, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case <-c.started:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for remote push %d", i+1)
		}
	}
}

func (c *blockingRemoteDeliveryPushClient) MaxActive() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max
}

func (c *blockingRemoteDeliveryPushClient) Release() {
	c.once.Do(func() { close(c.release) })
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
		Session:   origin,
	}))
	require.NoError(t, registry.Register(online.OnlineConn{
		SessionID: 3,
		UID:       "u1",
		State:     online.LocalRouteStateActive,
		Session:   mirror,
	}))
	require.NoError(t, registry.Register(online.OnlineConn{
		SessionID: 2,
		UID:       "u2",
		State:     online.LocalRouteStateActive,
		Session:   recipient,
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
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u1", NodeID: 1, BootID: 11, SessionID: 1},
	}, result.Dropped)

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

func TestDistributedDeliveryPushSkipsRemoteOriginSessionOnly(t *testing.T) {
	client := &recordingDeliveryPushClient{}
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
	}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   100,
				MessageSeq:  9,
				ChannelID:   "u1@u2",
				ChannelType: frame.ChannelTypePerson,
				FromUID:     "u1",
				Payload:     []byte("hello remote"),
			},
			SenderSessionID: 1,
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u1", NodeID: 2, BootID: 22, SessionID: 1},
			{UID: "u2", NodeID: 3, BootID: 33, SessionID: 1},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u2", NodeID: 3, BootID: 33, SessionID: 1},
	}, result.Accepted)
	require.Empty(t, result.Retryable)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u1", NodeID: 2, BootID: 22, SessionID: 1},
	}, result.Dropped)
	require.Len(t, client.batchCalls, 1)
	require.Equal(t, uint64(3), client.batchCalls[0].nodeID)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u2", NodeID: 3, BootID: 33, SessionID: 1},
	}, client.batchCalls[0].cmd.Items[0].Routes)
}

func TestDistributedDeliveryPushBatchesGroupRoutesPerTargetNode(t *testing.T) {
	client := &recordingDeliveryPushClient{}
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
	}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   101,
				MessageSeq:  9,
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				FromUID:     "u1",
				Payload:     []byte("hello group"),
			},
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
			{UID: "u3", NodeID: 2, BootID: 11, SessionID: 22},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Accepted, 2)
	require.Empty(t, result.Retryable)
	require.Empty(t, result.Dropped)

	require.Empty(t, client.singleCalls)
	require.Len(t, client.batchCalls, 1)
	call := client.batchCalls[0]
	require.Equal(t, uint64(2), call.nodeID)
	require.Equal(t, uint64(1), call.cmd.OwnerNodeID)
	require.Len(t, call.cmd.Items, 1)
	require.Equal(t, "g1", call.cmd.Items[0].ChannelID)
	require.Equal(t, frame.ChannelTypeGroup, call.cmd.Items[0].ChannelType)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
		{UID: "u3", NodeID: 2, BootID: 11, SessionID: 22},
	}, call.cmd.Items[0].Routes)
}

func TestDistributedDeliveryPushChunksGroupRoutesPerTargetNode(t *testing.T) {
	client := &recordingDeliveryPushClient{}
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
	}
	routes := make([]deliveryruntime.RouteKey, 1001)
	for i := range routes {
		routes[i] = deliveryruntime.RouteKey{UID: fmt.Sprintf("u%d", i), NodeID: 2, BootID: 11, SessionID: uint64(i + 1)}
	}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   101,
				MessageSeq:  9,
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				FromUID:     "u1",
				Payload:     []byte("hello group"),
			},
		},
		Routes: routes,
	})
	require.NoError(t, err)
	require.Len(t, result.Accepted, len(routes))

	require.Empty(t, client.singleCalls)
	require.Len(t, client.batchCalls, 2)
	require.Len(t, client.batchCalls[0].cmd.Items, 1)
	require.Len(t, client.batchCalls[0].cmd.Items[0].Routes, 1000)
	require.Len(t, client.batchCalls[1].cmd.Items, 1)
	require.Len(t, client.batchCalls[1].cmd.Items[0].Routes, 1)
	require.Equal(t, routes[:1000], client.batchCalls[0].cmd.Items[0].Routes)
	require.Equal(t, routes[1000:], client.batchCalls[1].cmd.Items[0].Routes)
}

func TestDistributedDeliveryPushRecordsPushRouteMetrics(t *testing.T) {
	metrics := &recordingDeliveryRoutingMetrics{}
	client := &recordingDeliveryPushClient{}
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
		metrics:     metrics,
	}

	_, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   101,
				MessageSeq:  9,
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				FromUID:     "u1",
				Payload:     []byte("hello group"),
			},
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
			{UID: "u3", NodeID: 2, BootID: 11, SessionID: 22},
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"2:ok:2"}, metrics.pushRPCs)
}

func TestDistributedDeliveryPushReconstructsAcceptedRoutesFromAcceptedCount(t *testing.T) {
	routes := []deliveryruntime.RouteKey{
		{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
		{UID: "u3", NodeID: 2, BootID: 11, SessionID: 22},
		{UID: "u4", NodeID: 2, BootID: 11, SessionID: 23},
	}
	client := &recordingDeliveryPushClient{
		batchResponse: &accessnode.DeliveryPushResponse{
			Status:           "ok",
			AcceptedCount:    1,
			AcceptedCountSet: true,
			Retryable:        []deliveryruntime.RouteKey{routes[1]},
			Dropped:          []deliveryruntime.RouteKey{routes[2]},
		},
	}
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
	}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   101,
				MessageSeq:  9,
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				FromUID:     "u1",
				Payload:     []byte("hello group"),
			},
		},
		Routes: routes,
	})

	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{routes[0]}, result.Accepted)
	require.Equal(t, []deliveryruntime.RouteKey{routes[1]}, result.Retryable)
	require.Equal(t, []deliveryruntime.RouteKey{routes[2]}, result.Dropped)
	require.Empty(t, client.singleCalls)
}

func TestAcceptedCountAllAcceptedAvoidsAllocations(t *testing.T) {
	routes := []deliveryruntime.RouteKey{
		{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
		{UID: "u3", NodeID: 2, BootID: 11, SessionID: 22},
	}
	resp := accessnode.DeliveryPushResponse{
		AcceptedCount:    uint64(len(routes)),
		AcceptedCountSet: true,
	}

	allocs := testing.AllocsPerRun(1000, func() {
		accepted, missing := acceptedAndUnreportedDeliveryRoutes(routes, resp)
		if len(accepted) != len(routes) || len(missing) != 0 {
			t.Fatalf("accepted=%d missing=%d, want all accepted", len(accepted), len(missing))
		}
	})

	require.Equal(t, float64(0), allocs)
}

func TestDistributedDeliveryPushRecordsPartialPushRouteMetrics(t *testing.T) {
	routes := []deliveryruntime.RouteKey{
		{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
		{UID: "u3", NodeID: 2, BootID: 11, SessionID: 22},
	}
	metrics := &recordingDeliveryRoutingMetrics{}
	client := &recordingDeliveryPushClient{
		batchResponse: &accessnode.DeliveryPushResponse{
			Accepted: []deliveryruntime.RouteKey{routes[0]},
			Dropped:  []deliveryruntime.RouteKey{routes[1]},
		},
	}
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
		metrics:     metrics,
	}

	_, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   101,
				MessageSeq:  9,
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				FromUID:     "u1",
				Payload:     []byte("hello group"),
			},
		},
		Routes: routes,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"2:partial:2"}, metrics.pushRPCs)
}

func TestDistributedDeliveryPushBatchesPersonRoutesByRecipientChannelView(t *testing.T) {
	client := &recordingDeliveryPushClient{}
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
	}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   102,
				MessageSeq:  10,
				ChannelID:   channelid.ToCommandChannel("u1@u2"),
				ChannelType: frame.ChannelTypePerson,
				FromUID:     "u1",
				Payload:     []byte("hello person"),
			},
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u1", NodeID: 2, BootID: 11, SessionID: 21},
			{UID: "u2", NodeID: 2, BootID: 11, SessionID: 22},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Accepted, 2)

	require.Empty(t, client.singleCalls)
	require.Len(t, client.batchCalls, 1)
	call := client.batchCalls[0]
	require.Equal(t, uint64(2), call.nodeID)
	require.Len(t, call.cmd.Items, 2)

	viewsByUID := make(map[string]string)
	for _, item := range call.cmd.Items {
		require.Equal(t, channelid.ToCommandChannel("u1@u2"), item.ChannelID)
		require.Len(t, item.Routes, 1)
		packet := mustDecodeRecvPacket(t, item.Frame)
		viewsByUID[item.Routes[0].UID] = packet.ChannelID
	}
	require.Equal(t, map[string]string{
		"u1": "u2",
		"u2": "u1",
	}, viewsByUID)
}

func TestDistributedDeliveryPushSinglePersonRouteAllocationBudget(t *testing.T) {
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      benchmarkAcceptAllDeliveryPushClient{},
		codec:       codec.New(),
	}
	cmd := deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   103,
				MessageSeq:  11,
				ChannelID:   channelid.ToCommandChannel("u1@u2"),
				ChannelType: frame.ChannelTypePerson,
				FromUID:     "u1",
				Payload:     []byte("hello single person route"),
			},
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u2", NodeID: 2, BootID: 11, SessionID: 22},
		},
	}

	allocs := testing.AllocsPerRun(1000, func() {
		result, err := push.Push(context.Background(), cmd)
		require.NoError(t, err)
		require.Len(t, result.Accepted, 1)
	})
	require.LessOrEqual(t, allocs, float64(20), "single-route person delivery should not allocate grouping maps")
}

func TestDistributedDeliveryPushFallsBackToLegacyForUnreportedBatchRoutes(t *testing.T) {
	metrics := &recordingDeliveryRoutingMetrics{}
	client := &recordingDeliveryPushClient{
		batchResponse: &accessnode.DeliveryPushResponse{},
	}
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
		metrics:     metrics,
	}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   103,
				MessageSeq:  11,
				ChannelID:   "u1@u2",
				ChannelType: frame.ChannelTypePerson,
				FromUID:     "u1",
				Payload:     []byte("hello legacy"),
			},
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u1", NodeID: 2, BootID: 11, SessionID: 21},
			{UID: "u2", NodeID: 2, BootID: 11, SessionID: 22},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Accepted, 2)
	require.Empty(t, result.Retryable)
	require.Empty(t, result.Dropped)

	require.Len(t, client.batchCalls, 1)
	require.Len(t, client.singleCalls, 2)
	require.Equal(t, []string{"2:partial:2"}, metrics.pushRPCs)
	viewsByUID := make(map[string]string)
	for _, call := range client.singleCalls {
		require.Equal(t, uint64(2), call.nodeID)
		require.Len(t, call.cmd.Routes, 1)
		packet := mustDecodeRecvPacket(t, call.cmd.Frame)
		viewsByUID[call.cmd.Routes[0].UID] = packet.ChannelID
	}
	require.Equal(t, map[string]string{
		"u1": "u2",
		"u2": "u1",
	}, viewsByUID)
}

func TestDistributedDeliveryPushFallsBackToLegacyWhenBatchRejected(t *testing.T) {
	metrics := &recordingDeliveryRoutingMetrics{}
	client := &recordingDeliveryPushClient{
		batchErr: errors.New("legacy node rejected batch shape"),
	}
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      client,
		codec:       codec.New(),
		metrics:     metrics,
	}

	result, err := push.Push(context.Background(), deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				MessageID:   104,
				MessageSeq:  12,
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				FromUID:     "u1",
				Payload:     []byte("hello legacy node"),
			},
		},
		Routes: []deliveryruntime.RouteKey{
			{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
			{UID: "u3", NodeID: 2, BootID: 11, SessionID: 22},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Accepted, 2)
	require.Empty(t, result.Retryable)
	require.Empty(t, result.Dropped)

	require.Len(t, client.batchCalls, 1)
	require.Len(t, client.singleCalls, 1)
	require.Equal(t, []string{"2:error:2"}, metrics.pushRPCs)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u2", NodeID: 2, BootID: 11, SessionID: 21},
		{UID: "u3", NodeID: 2, BootID: 11, SessionID: 22},
	}, client.singleCalls[0].cmd.Routes)
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
	metrics := &recordingDeliveryRoutingMetrics{}
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: store,
		}),
		authority: authority,
		metrics:   metrics,
		pageSize:  8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "g1",
		ChannelType: 2,
	}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)

	page1, err := resolver.ResolvePage(context.Background(), token, "", 1)
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 11, SessionID: 2}}, page1.Routes)
	require.Equal(t, "u2", page1.NextCursor)
	require.False(t, page1.Done)

	page2, err := resolver.ResolvePage(context.Background(), token, page1.NextCursor, 1)
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 11, SessionID: 3}}, page2.Routes)
	require.Equal(t, "u2", page2.NextCursor)
	require.True(t, page2.Done)

	require.Equal(t, 0, store.snapshotCalls)
	require.Equal(t, [][]string{{"u2"}}, authority.uidBatches)
	require.Equal(t, []string{"group:ok:1:1", "group:ok:0:1"}, metrics.resolves)
}

func TestLocalDeliveryResolverUsesDirectLookupForPersonChannels(t *testing.T) {
	authority := &recordingAuthoritative{
		single: map[string][]presence.Route{
			"u2": {{UID: "u2", NodeID: 1, BootID: 11, SessionID: 2}},
		},
	}
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{}),
		authority:   authority,
		pageSize:    8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   deliveryusecase.EncodePersonChannel("u1", "u2"),
		ChannelType: frame.ChannelTypePerson,
	}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "u2", NodeID: 1, BootID: 11, SessionID: 2}}, page.Routes)
	require.Equal(t, "u1", page.NextCursor)
	require.True(t, page.Done)
	require.Equal(t, []string{"u2", "u1"}, authority.uidCalls)
	require.Empty(t, authority.uidBatches)
}

func TestLocalDeliveryResolverRecordsResolvePageErrors(t *testing.T) {
	pageErr := errors.New("subscriber page failed")
	metrics := &recordingDeliveryRoutingMetrics{}
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverSnapshotStore{pageErr: pageErr},
		}),
		authority: &recordingAuthoritative{},
		metrics:   metrics,
		pageSize:  8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{})

	require.NoError(t, err)

	_, err = resolver.ResolvePage(context.Background(), token, "", 8)
	require.ErrorIs(t, err, pageErr)
	require.Equal(t, []string{"group:error:1:0"}, metrics.resolves)
}

type recordingDeliveryOwnerNotifier struct {
	acks        []deliveryevents.RouteAck
	offlines    []deliveryevents.SessionClosed
	ackErr      error
	offlineErr  error
	localClosed bool
}

func (r *recordingDeliveryOwnerNotifier) NotifyAck(_ context.Context, _ uint64, cmd deliveryevents.RouteAck) error {
	r.acks = append(r.acks, cmd)
	return r.ackErr
}

func (r *recordingDeliveryOwnerNotifier) NotifyOffline(_ context.Context, _ uint64, cmd deliveryevents.SessionClosed) error {
	r.offlines = append(r.offlines, cmd)
	return r.offlineErr
}

type recordingDeliveryPushClient struct {
	singleCalls   []recordingDeliveryPushSingleCall
	batchCalls    []recordingDeliveryPushBatchCall
	batchResponse *accessnode.DeliveryPushResponse
	batchErr      error
}

type recordingDeliveryPushSingleCall struct {
	nodeID uint64
	cmd    accessnode.DeliveryPushCommand
}

type recordingDeliveryPushBatchCall struct {
	nodeID uint64
	cmd    accessnode.DeliveryPushBatchCommand
}

func (r *recordingDeliveryPushClient) PushBatch(_ context.Context, nodeID uint64, cmd accessnode.DeliveryPushCommand) (accessnode.DeliveryPushResponse, error) {
	r.singleCalls = append(r.singleCalls, recordingDeliveryPushSingleCall{nodeID: nodeID, cmd: cmd})
	return accessnode.DeliveryPushResponse{Accepted: append([]deliveryruntime.RouteKey(nil), cmd.Routes...)}, nil
}

func (r *recordingDeliveryPushClient) PushBatchItems(_ context.Context, nodeID uint64, cmd accessnode.DeliveryPushBatchCommand) (accessnode.DeliveryPushResponse, error) {
	r.batchCalls = append(r.batchCalls, recordingDeliveryPushBatchCall{nodeID: nodeID, cmd: cmd})
	if r.batchErr != nil {
		return accessnode.DeliveryPushResponse{}, r.batchErr
	}
	if r.batchResponse != nil {
		return *r.batchResponse, nil
	}
	resp := accessnode.DeliveryPushResponse{}
	for _, item := range cmd.Items {
		resp.Accepted = append(resp.Accepted, item.Routes...)
	}
	return resp, nil
}

func mustDecodeRecvPacket(t *testing.T, body []byte) *frame.RecvPacket {
	t.Helper()
	f, _, err := codec.New().DecodeFrame(body, frame.LatestVersion)
	require.NoError(t, err)
	packet, ok := f.(*frame.RecvPacket)
	require.True(t, ok)
	return packet
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

func (s *optionRecordingSession) WriteFrame(f frame.Frame) error {
	return s.Session.WriteFrame(f)
}

func (s *optionRecordingSession) Writes() []outboundWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]outboundWrite, len(s.writes))
	copy(out, s.writes)
	return out
}

type recordingRouteAcker struct {
	acks []deliveryevents.RouteAck
	err  error
}

func (r *recordingRouteAcker) AckRoute(_ context.Context, cmd deliveryevents.RouteAck) error {
	r.acks = append(r.acks, cmd)
	return r.err
}

type recordingSessionCloser struct {
	closed []deliveryevents.SessionClosed
	err    error
}

func (r *recordingSessionCloser) SessionClosed(_ context.Context, cmd deliveryevents.SessionClosed) error {
	r.closed = append(r.closed, cmd)
	return r.err
}

type recordingCommittedSubmitter struct {
	mu          sync.Mutex
	submitCalls int
	calls       []deliveryruntime.CommittedEnvelope
}

type recordingCommittedNodeSubmitter struct {
	mu    sync.Mutex
	calls []recordingCommittedNodeSubmitCall
	err   error
}

type recordingCommittedNodeSubmitCall struct {
	nodeID uint64
	env    deliveryruntime.CommittedEnvelope
}

func (r *recordingCommittedNodeSubmitter) SubmitCommitted(_ context.Context, nodeID uint64, env deliveryruntime.CommittedEnvelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := env
	copied.Payload = append([]byte(nil), env.Payload...)
	copied.MessageScopedUIDs = append([]string(nil), env.MessageScopedUIDs...)
	r.calls = append(r.calls, recordingCommittedNodeSubmitCall{nodeID: nodeID, env: copied})
	return r.err
}

func (r *recordingCommittedNodeSubmitter) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingCommittedNodeSubmitter) Calls() []recordingCommittedNodeSubmitCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordingCommittedNodeSubmitCall, len(r.calls))
	for i, call := range r.calls {
		copied := call
		copied.env.Payload = append([]byte(nil), call.env.Payload...)
		copied.env.MessageScopedUIDs = append([]string(nil), call.env.MessageScopedUIDs...)
		out[i] = copied
	}
	return out
}

func (r *recordingCommittedSubmitter) SubmitCommitted(_ context.Context, env deliveryruntime.CommittedEnvelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.submitCalls++
	copied := env
	copied.Payload = append([]byte(nil), env.Payload...)
	copied.MessageScopedUIDs = append([]string(nil), env.MessageScopedUIDs...)
	r.calls = append(r.calls, copied)
	return nil
}

func (r *recordingCommittedSubmitter) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingCommittedSubmitter) SubmitCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.submitCalls
}

func (r *recordingCommittedSubmitter) Calls() []deliveryruntime.CommittedEnvelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]deliveryruntime.CommittedEnvelope, len(r.calls))
	for i, call := range r.calls {
		copied := call
		copied.Payload = append([]byte(nil), call.Payload...)
		copied.MessageScopedUIDs = append([]string(nil), call.MessageScopedUIDs...)
		out[i] = copied
	}
	return out
}

func (r *recordingCommittedSubmitter) messageSeqs() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]uint64, 0, len(r.calls))
	for _, call := range r.calls {
		out = append(out, call.MessageSeq)
	}
	return out
}

type recordingConversationSubmitter struct {
	mu          sync.Mutex
	submitCalls int
	calls       []channel.Message
}

func (r *recordingConversationSubmitter) SubmitCommitted(_ context.Context, msg channel.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.submitCalls++
	copied := msg
	copied.Payload = append([]byte(nil), msg.Payload...)
	r.calls = append(r.calls, copied)
	return nil
}

func (r *recordingConversationSubmitter) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingConversationSubmitter) SubmitCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.submitCalls
}

func (r *recordingConversationSubmitter) Calls() []channel.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]channel.Message, len(r.calls))
	for i, call := range r.calls {
		copied := call
		copied.Payload = append([]byte(nil), call.Payload...)
		out[i] = copied
	}
	return out
}

type recordingFlushingConversationSubmitter struct {
	recordingConversationSubmitter
	flushCalls int
}

func (r *recordingFlushingConversationSubmitter) Flush(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushCalls++
	return nil
}

func (r *recordingFlushingConversationSubmitter) FlushCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushCalls
}

type blockingConversationSubmitter struct {
	recordingConversationSubmitter
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingConversationSubmitter() *blockingConversationSubmitter {
	return &blockingConversationSubmitter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *blockingConversationSubmitter) SubmitCommitted(ctx context.Context, msg channel.Message) error {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.recordingConversationSubmitter.SubmitCommitted(ctx, msg)
}

func (b *blockingConversationSubmitter) WaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for blocking conversation submit")
	}
}

func (b *blockingConversationSubmitter) Release() {
	select {
	case <-b.release:
	default:
		close(b.release)
	}
}

type recordingCommittedDispatchMetrics struct {
	mu        sync.Mutex
	depths    []string
	enqueues  []string
	overflows []string
}

func (m *recordingCommittedDispatchMetrics) SetCommittedDispatchQueueDepth(shard string, depth int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.depths = append(m.depths, shard+":"+strconv.Itoa(depth))
}

func (m *recordingCommittedDispatchMetrics) ObserveCommittedDispatchEnqueue(shard, result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enqueues = append(m.enqueues, shard+":"+result)
}

func (m *recordingCommittedDispatchMetrics) ObserveCommittedDispatchOverflow(shard string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.overflows = append(m.overflows, shard)
}

func (m *recordingCommittedDispatchMetrics) DepthSnapshots() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.depths...)
}

type recordingDeliveryRoutingMetrics struct {
	pushRPCs []string
	resolves []string
}

func (m *recordingDeliveryRoutingMetrics) ObserveResolve(channelType, result string, _ time.Duration, pages, routes int) {
	m.resolves = append(m.resolves, channelType+":"+result+":"+strconv.Itoa(pages)+":"+strconv.Itoa(routes))
}

func (m *recordingDeliveryRoutingMetrics) ObservePushRPC(targetNode, result string, _ time.Duration, routes int) {
	m.pushRPCs = append(m.pushRPCs, targetNode+":"+result+":"+strconv.Itoa(routes))
}

type blockingCommittedSubmitter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingCommittedSubmitter() *blockingCommittedSubmitter {
	return &blockingCommittedSubmitter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *blockingCommittedSubmitter) SubmitCommitted(context.Context, deliveryruntime.CommittedEnvelope) error {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil
}

func (b *blockingCommittedSubmitter) WaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for blocked committed submitter")
	}
}

func (b *blockingCommittedSubmitter) Release() {
	close(b.release)
}

type contextAwareBlockingCommittedSubmitter struct {
	entered chan struct{}
	once    sync.Once
	mu      sync.Mutex
	err     error
}

func newContextAwareBlockingCommittedSubmitter() *contextAwareBlockingCommittedSubmitter {
	return &contextAwareBlockingCommittedSubmitter{entered: make(chan struct{})}
}

func (b *contextAwareBlockingCommittedSubmitter) SubmitCommitted(ctx context.Context, _ deliveryruntime.CommittedEnvelope) error {
	b.once.Do(func() { close(b.entered) })
	err := ctx.Err()
	if err == nil {
		<-ctx.Done()
		err = ctx.Err()
	}
	b.mu.Lock()
	b.err = err
	b.mu.Unlock()
	return err
}

func (b *contextAwareBlockingCommittedSubmitter) WaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for context-aware committed submitter")
	}
}

func (b *contextAwareBlockingCommittedSubmitter) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

func (d *asyncCommittedDispatcher) isStoppedForTest() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.running && !d.stopping && d.done == nil
}

type resolverSnapshotStore struct {
	snapshotCalls int
	pageCalls     int
	uids          []string
	snapshotErr   error
	pageErr       error
}

func (s *resolverSnapshotStore) SnapshotChannelSubscribers(_ context.Context, _ string, _ int64) ([]string, error) {
	s.snapshotCalls++
	if s.snapshotErr != nil {
		return nil, s.snapshotErr
	}
	return append([]string(nil), s.uids...), nil
}

func (s *resolverSnapshotStore) ListChannelSubscribers(_ context.Context, _ string, _ int64, afterUID string, limit int) ([]string, string, bool, error) {
	s.pageCalls++
	if s.pageErr != nil {
		return nil, "", false, s.pageErr
	}
	if limit <= 0 {
		return nil, afterUID, true, nil
	}
	start := 0
	if afterUID != "" {
		for i, uid := range s.uids {
			if uid == afterUID {
				start = i + 1
				break
			}
		}
	}
	if start >= len(s.uids) {
		return nil, afterUID, true, nil
	}
	end := start + limit
	if end > len(s.uids) {
		end = len(s.uids)
	}
	page := append([]string(nil), s.uids[start:end]...)
	cursor := afterUID
	if len(page) > 0 {
		cursor = page[len(page)-1]
	}
	return page, cursor, end >= len(s.uids), nil
}

type resolverVersionStore struct {
	resolverSnapshotStore
	uids    []string
	version uint64
}

func (s *resolverVersionStore) GetChannel(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	return metadb.Channel{
		ChannelID:                 channelID,
		ChannelType:               channelType,
		SubscriberMutationVersion: s.version,
	}, nil
}

func (s *resolverVersionStore) SnapshotChannelSubscribers(ctx context.Context, channelID string, channelType int64) ([]string, error) {
	s.snapshotCalls++
	if s.snapshotErr != nil {
		return nil, s.snapshotErr
	}
	return append([]string(nil), s.uids...), nil
}

func (s *resolverVersionStore) ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	s.pageCalls++
	if s.pageErr != nil {
		return nil, "", false, s.pageErr
	}
	if limit <= 0 {
		return nil, afterUID, true, nil
	}
	start := 0
	if afterUID != "" {
		for i, uid := range s.uids {
			if uid == afterUID {
				start = i + 1
				break
			}
		}
	}
	if start >= len(s.uids) {
		return nil, afterUID, true, nil
	}
	end := start + limit
	if end > len(s.uids) {
		end = len(s.uids)
	}
	page := append([]string(nil), s.uids[start:end]...)
	cursor := afterUID
	if len(page) > 0 {
		cursor = page[len(page)-1]
	}
	return page, cursor, end >= len(s.uids), nil
}

type recordingResolvedUIDObserver struct {
	pages       []resolvedUIDPage
	internalErr error
	errs        []error
}

func (o *recordingResolvedUIDObserver) OnResolvedUIDPage(_ context.Context, page resolvedUIDPage) {
	page.UIDs = append([]string(nil), page.UIDs...)
	page.Envelope.Payload = append([]byte(nil), page.Envelope.Payload...)
	page.Envelope.MessageScopedUIDs = append([]string(nil), page.Envelope.MessageScopedUIDs...)
	o.pages = append(o.pages, page)
	if o.internalErr != nil {
		o.errs = append(o.errs, o.internalErr)
	}
}

type staticDeliveryTagTopology struct {
	version deliverytagruntime.PartitionTopologyVersion
}

func (s staticDeliveryTagTopology) CurrentDeliveryTagTopology(context.Context, []string) (deliverytagruntime.PartitionTopologyVersion, error) {
	return s.version.Clone(), nil
}

func (s staticDeliveryTagTopology) ValidateCurrentDeliveryTagTopology(_ context.Context, topology deliverytagruntime.PartitionTopologyVersion) (bool, error) {
	return s.version.Equal(topology), nil
}

type recordingDeliveryTagCluster struct {
	slotByKey            map[string]multiraft.SlotID
	leaderBySlot         map[multiraft.SlotID]multiraft.NodeID
	hashSlotTableVersion uint64
	cachedAssignments    []controllermeta.SlotAssignment
	listAssignments      []controllermeta.SlotAssignment
	listAssignmentsCalls int
}

func (c *recordingDeliveryTagCluster) SlotForKey(key string) multiraft.SlotID {
	if c.slotByKey == nil {
		return 1
	}
	return c.slotByKey[key]
}

func (c *recordingDeliveryTagCluster) HashSlotTableVersion() uint64 {
	return c.hashSlotTableVersion
}

func (c *recordingDeliveryTagCluster) LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	if c.leaderBySlot == nil {
		return multiraft.NodeID(slotID), nil
	}
	return c.leaderBySlot[slotID], nil
}

func (c *recordingDeliveryTagCluster) ListSlotAssignments(context.Context) ([]controllermeta.SlotAssignment, error) {
	c.listAssignmentsCalls++
	return append([]controllermeta.SlotAssignment(nil), c.listAssignments...), nil
}

func (c *recordingDeliveryTagCluster) ListCachedAssignments() []controllermeta.SlotAssignment {
	return append([]controllermeta.SlotAssignment(nil), c.cachedAssignments...)
}

func (c *recordingDeliveryTagCluster) ListSlotAssignmentsCalls() int {
	return c.listAssignmentsCalls
}

func testDeliveryTagTopology(version uint64) deliverytagruntime.PartitionTopologyVersion {
	return deliverytagruntime.PartitionTopologyVersion{
		HashSlotTableVersion: version,
		SlotAuthorityRefs: []deliverytagruntime.SlotAuthorityRef{
			{SlotID: 1, LeaderNodeID: 1, ConfigEpoch: version, BalanceVersion: version},
		},
	}
}

type recordingAuthoritative struct {
	uidBatches [][]string
	uidCalls   []string
	single     map[string][]presence.Route
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

func (r *recordingAuthoritative) EndpointsByUID(_ context.Context, uid string) ([]presence.Route, error) {
	r.uidCalls = append(r.uidCalls, uid)
	return append([]presence.Route(nil), r.single[uid]...), nil
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

type statusResult struct {
	status channel.ChannelRuntimeStatus
	err    error
}

type transientStatusChannelLog struct {
	mu      sync.Mutex
	results []statusResult
	calls   int
}

func (s *transientStatusChannelLog) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(s.results) == 0 {
		return channel.ChannelRuntimeStatus{}, nil
	}
	idx := s.calls - 1
	if idx >= len(s.results) {
		idx = len(s.results) - 1
	}
	result := s.results[idx]
	return result.status, result.err
}

func (s *transientStatusChannelLog) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestLocalDeliveryResolverReturnsOfflineUIDsAfterPresenceClassification(t *testing.T) {
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverSnapshotStore{uids: []string{"offline", "online"}},
		}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"online": {{UID: "online", NodeID: 1, BootID: 11, SessionID: 101}},
		}},
		collectOfflineUIDs: true,
		pageSize:           8,
	}
	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)

	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, []string{"offline"}, page.OfflineUIDs)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "online", NodeID: 1, BootID: 11, SessionID: 101}}, page.Routes)
}

func TestLocalDeliveryResolverSkipsOfflineUIDCollectionByDefault(t *testing.T) {
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverSnapshotStore{uids: []string{"offline", "online"}},
		}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"online": {{UID: "online", NodeID: 1, BootID: 11, SessionID: 101}},
		}},
		pageSize: 8,
	}
	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)

	require.NoError(t, err)
	require.True(t, page.Done)
	require.Empty(t, page.OfflineUIDs)
	require.Equal(t, []deliveryruntime.RouteKey{{UID: "online", NodeID: 1, BootID: 11, SessionID: 101}}, page.Routes)
}

func TestLocalDeliveryResolverPaginatesOfflineUIDsWhenCollecting(t *testing.T) {
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
			Store: &resolverSnapshotStore{uids: []string{"offline-1", "offline-2", "offline-3"}},
		}),
		authority:          &recordingAuthoritative{batches: map[string][]presence.Route{}},
		collectOfflineUIDs: true,
		pageSize:           1,
	}
	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}, deliveryruntime.CommittedEnvelope{})
	require.NoError(t, err)

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)

	require.NoError(t, err)
	require.False(t, page.Done)
	require.Equal(t, []string{"offline-1"}, page.OfflineUIDs)
	require.Empty(t, page.Routes)
}

func TestTagDeliveryResolverPaginatesOfflineUIDsWhenCollecting(t *testing.T) {
	resolver := tagDeliveryResolver{
		authority:          &recordingAuthoritative{batches: map[string][]presence.Route{}},
		collectOfflineUIDs: true,
		pageSize:           1,
	}
	token := &tagResolveToken{
		routableUIDs: []string{"offline-1", "offline-2", "offline-3"},
		channelType:  frame.ChannelTypeGroup,
		ephemeral:    true,
	}

	page, err := resolver.ResolvePage(context.Background(), token, "", 8)

	require.NoError(t, err)
	require.False(t, page.Done)
	require.Equal(t, []string{"offline-1"}, page.OfflineUIDs)
	require.Empty(t, page.Routes)
}
