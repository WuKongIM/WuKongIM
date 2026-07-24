package app

import (
	"context"
	"testing"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	runtimewebhook "github.com/WuKongIM/WuKongIM/internal/runtime/webhook"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

func TestNewSkipsWebhookSubsystemWhenDisabled(t *testing.T) {
	app, err := newTestApp(t, Config{DataDir: t.TempDir()}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)
	require.Nil(t, app.webhook)
	require.Nil(t, app.webhookNotify)
	require.Nil(t, app.webhookOffline)
	require.Nil(t, app.webhookPresence)
}

func TestNewWiresWebhookSubsystemWhenEnabled(t *testing.T) {
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Webhook: WebhookConfig{
			HTTPAddr: "http://127.0.0.1:8080/webhook",
		},
	}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)
	require.NotNil(t, app.webhook)
	require.NotNil(t, app.webhookNotify)
	require.NotNil(t, app.webhookOffline)
	require.NotNil(t, app.webhookPresence)
}

func TestWebhookConfigSnapshotUsesNormalizedStartupConfig(t *testing.T) {
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Webhook: WebhookConfig{
			HTTPAddr:    "http://127.0.0.1:8080/webhook",
			FocusEvents: []string{runtimewebhook.EventMsgNotify},
			QueueSize:   2048,
			Workers:     8,
		},
	}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)

	snapshot, err := app.WebhookConfigSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, accessmanager.WebhookConfigSnapshot{
		Enabled:                   true,
		HTTPAddr:                  "http://127.0.0.1:8080/webhook",
		FocusEvents:               []string{runtimewebhook.EventMsgNotify},
		SupportedEvents:           []string{runtimewebhook.EventMsgNotify, runtimewebhook.EventMsgOffline, runtimewebhook.EventUserOnlineStatus},
		QueueSize:                 2048,
		Workers:                   8,
		MsgNotifyBatchMaxItems:    100,
		MsgNotifyBatchMaxWait:     "500ms",
		OnlineStatusBatchMaxItems: 512,
		OnlineStatusBatchMaxWait:  "2s",
		OfflineUIDBatchSize:       512,
		RequestTimeout:            "5s",
		RetryMaxAttempts:          3,
		Source:                    "startup_config",
		RequiresRestart:           true,
	}, snapshot)

	snapshot.FocusEvents[0] = runtimewebhook.EventMsgOffline
	second, err := app.WebhookConfigSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{runtimewebhook.EventMsgNotify}, second.FocusEvents)
}

func TestWebhookConfigSnapshotReportsDisabledDefaults(t *testing.T) {
	app, err := newTestApp(t, Config{DataDir: t.TempDir()}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)

	snapshot, err := app.WebhookConfigSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, accessmanager.WebhookConfigSnapshot{
		Enabled:                   false,
		HTTPAddr:                  "",
		FocusEvents:               []string{},
		SupportedEvents:           []string{runtimewebhook.EventMsgNotify, runtimewebhook.EventMsgOffline, runtimewebhook.EventUserOnlineStatus},
		QueueSize:                 1024,
		Workers:                   16,
		MsgNotifyBatchMaxItems:    100,
		MsgNotifyBatchMaxWait:     "500ms",
		OnlineStatusBatchMaxItems: 512,
		OnlineStatusBatchMaxWait:  "2s",
		OfflineUIDBatchSize:       512,
		RequestTimeout:            "5s",
		RetryMaxAttempts:          3,
		Source:                    "startup_config",
		RequiresRestart:           true,
	}, snapshot)
}

func TestWirePresencePassesWebhookOnlineStatusObserver(t *testing.T) {
	cluster := newFakePresenceCluster(1, nil)
	cluster.snapshot = readyFakeClusterSnapshot(1, 16)
	observer := &recordingOnlineStatusObserverForWebhookTest{}
	app := &App{
		cluster:         cluster,
		online:          online.NewRegistry(online.RegistryOptions{}),
		webhookPresence: observer,
		logger:          wklog.NewNop(),
	}
	app.wirePresence()
	require.NotNil(t, app.presence)
	app.presenceDirectory.BecomeAuthority(presence.RouteTarget{
		HashSlot:       9,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  3,
		AuthorityEpoch: 2,
	})

	err := app.presence.Activate(context.Background(), presence.ActivateCommand{
		UID:       "u1",
		SessionID: 11,
	})

	require.NoError(t, err)
	require.Equal(t, []presence.OnlineStatusEvent{{UID: "u1", Online: true, Value: "u1-0-1-11-1-1"}}, observer.events)
}

func TestComposePersistAfterEnqueuersCallsBoth(t *testing.T) {
	left := &recordingPersistAfterEnqueuerForWebhookTest{}
	right := &recordingPersistAfterEnqueuerForWebhookTest{}
	composed := composePersistAfterEnqueuers(left, right)

	composed.EnqueuePersistAfter(context.Background(), channelappend.CommittedEnvelope{MessageID: 101})

	require.Equal(t, []uint64{101}, left.messageIDs)
	require.Equal(t, []uint64{101}, right.messageIDs)
}

func TestComposeOfflineRecipientObserversKeepsPluginSingleAndWebhookBatch(t *testing.T) {
	plugin := &recordingOfflineRecipientObserverForWebhookTest{}
	webhook := &recordingOfflineRecipientsObserverForWebhookTest{}
	single, batch := composeOfflineRecipientObservers(plugin, webhook)

	processor := channelappend.NewRecipientProcessor(channelappend.RecipientProcessorOptions{
		PresenceResolver:            staticChannelAppendPresenceResolver{},
		OfflineRecipientObserver:    single,
		OfflineRecipientsObserver:   batch,
		DeliveryRetryMaxAttempts:    1,
		DeliveryRetryInitialBackoff: 1,
		DeliveryRetryMaxBackoff:     1,
	})
	err := processor.ProcessRecipientBatch(context.Background(), channelappend.RecipientBatch{
		Event:      channelappend.CommittedEnvelope{MessageID: 101, MessageSeq: 1, ChannelID: "g1", ChannelType: 2},
		Recipients: []channelappend.Recipient{{UID: "u1"}, {UID: "u2"}},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2"}, plugin.uids)
	require.Equal(t, [][]string{{"u1", "u2"}}, webhook.uidBatches)
}

func TestComposeOfflineRecipientObserversUsesPluginBatchWhenAvailable(t *testing.T) {
	plugin := &recordingBatchOfflineRecipientObserverForWebhookTest{}
	webhook := &recordingOfflineRecipientsObserverForWebhookTest{}
	single, batch := composeOfflineRecipientObservers(plugin, webhook)

	processor := channelappend.NewRecipientProcessor(channelappend.RecipientProcessorOptions{
		PresenceResolver:            staticChannelAppendPresenceResolver{},
		OfflineRecipientObserver:    single,
		OfflineRecipientsObserver:   batch,
		DeliveryRetryMaxAttempts:    1,
		DeliveryRetryInitialBackoff: 1,
		DeliveryRetryMaxBackoff:     1,
	})
	err := processor.ProcessRecipientBatch(context.Background(), channelappend.RecipientBatch{
		Event:      channelappend.CommittedEnvelope{MessageID: 101, MessageSeq: 1, ChannelID: "g1", ChannelType: 2},
		Recipients: []channelappend.Recipient{{UID: "u1"}, {UID: "u2"}},
	})

	require.NoError(t, err)
	require.Empty(t, plugin.singleUIDs)
	require.Equal(t, [][]string{{"u1", "u2"}}, plugin.uidBatches)
	require.Equal(t, [][]string{{"u1", "u2"}}, webhook.uidBatches)
}

func TestWebhookNotifyEnqueuerMapsCommittedEnvelopeAndCopiesSlices(t *testing.T) {
	runtime := &recordingWebhookRuntime{}
	enqueuer := webhookNotifyEnqueuer{runtime: runtime}
	source := channelappend.CommittedEnvelope{
		MessageID:         101,
		MessageSeq:        202,
		ChannelID:         "room-a",
		ChannelType:       2,
		Setting:           9,
		Topic:             "topic-a",
		Expire:            3600,
		FromUID:           "sender-u1",
		SenderNodeID:      7,
		ClientMsgNo:       "client-1",
		ServerTimestampMS: 123456789,
		Payload:           []byte("payload"),
		RedDot:            true,
		SyncOnce:          true,
		MessageScopedUIDs: []string{"u2"},
	}

	enqueuer.EnqueuePersistAfter(context.Background(), source)
	source.Payload[0] = 'X'

	require.Equal(t, []runtimewebhook.Message{{
		MessageID:         101,
		MessageSeq:        202,
		ChannelID:         "room-a",
		ChannelType:       2,
		Setting:           9,
		Topic:             "topic-a",
		Expire:            3600,
		SourceID:          7,
		FromUID:           "sender-u1",
		ClientMsgNo:       "client-1",
		ServerTimestampMS: 123456789,
		Payload:           []byte("payload"),
		RedDot:            true,
		SyncOnce:          true,
	}}, runtime.notify)
}

func TestWebhookOfflineObserverChunksRecipientsAndCopiesSlices(t *testing.T) {
	runtime := &recordingWebhookRuntime{}
	observer := webhookOfflineObserver{runtime: runtime, uidBatchSize: 2}
	source := channelappend.OfflineRecipientsEvent{
		Event: channelappend.CommittedEnvelope{
			MessageID:   101,
			MessageSeq:  202,
			ChannelID:   "room-a",
			ChannelType: 2,
			FromUID:     "sender-u1",
			Payload:     []byte("payload"),
		},
		UIDs: []string{"u1", "u2", "u3"},
	}

	observer.ObserveOfflineRecipients(context.Background(), source)
	source.Event.Payload[0] = 'X'
	source.UIDs[0] = "mutated"

	require.Len(t, runtime.offline, 2)
	require.Equal(t, []string{"u1", "u2"}, runtime.offline[0].ToUIDs)
	require.Equal(t, []string{"u3"}, runtime.offline[1].ToUIDs)
	require.Equal(t, []byte("payload"), runtime.offline[0].Message.Payload)
}

func TestWebhookPresenceObserverIsBestEffort(t *testing.T) {
	runtime := &recordingWebhookRuntime{}
	observer := webhookPresenceObserver{runtime: runtime}

	err := observer.ObserveOnlineStatus(context.Background(), presence.OnlineStatusEvent{Value: "u1-1"})

	require.NoError(t, err)
	require.Equal(t, []runtimewebhook.OnlineStatus{{Value: "u1-1"}}, runtime.online)
}

func TestWebhookLifecycleStartsBeforeChannelAppendAndStopsAfterDelivery(t *testing.T) {
	var calls []string
	var app *App
	webhook := &recordingWorkerRuntime{
		name:  "webhook",
		calls: &calls,
		onStart: func() {
			require.False(t, app.deliveryStarted)
			require.False(t, app.channelAppendStarted)
		},
		onStop: func() {
			require.False(t, app.deliveryStarted)
			require.False(t, app.channelAppendStarted)
		},
	}
	app = &App{
		cluster:        &fakeCluster{calls: &calls},
		pluginHook:     &recordingWorkerRuntime{name: "plugin_hook", calls: &calls},
		webhook:        webhook,
		deliveryWorker: &recordingWorkerRuntime{name: "delivery_worker", calls: &calls},
		channelAppends: newNoopStartedChannelAppendGroupForLifecycleTest(),
	}

	require.NoError(t, app.Start(context.Background()))
	require.True(t, app.webhookStarted)
	require.True(t, app.channelAppendStarted)
	require.NoError(t, app.Stop(context.Background()))
	require.False(t, app.webhookStarted)
	requireLifecycleOrder(t, calls, []string{
		"cluster.start",
		"plugin_hook.start",
		"webhook.start",
		"delivery_worker.start",
		"delivery_worker.stop",
		"webhook.stop",
		"plugin_hook.stop",
		"cluster.stop",
	})
}

type recordingWebhookRuntime struct {
	notify  []runtimewebhook.Message
	offline []runtimewebhook.OfflineMessage
	online  []runtimewebhook.OnlineStatus
}

func (r *recordingWebhookRuntime) Notify(_ context.Context, msg runtimewebhook.Message) {
	r.notify = append(r.notify, msg)
}

func (r *recordingWebhookRuntime) Offline(_ context.Context, msg runtimewebhook.OfflineMessage) {
	r.offline = append(r.offline, msg)
}

func (r *recordingWebhookRuntime) OnlineStatus(_ context.Context, status runtimewebhook.OnlineStatus) {
	r.online = append(r.online, status)
}

type recordingOnlineStatusObserverForWebhookTest struct {
	events []presence.OnlineStatusEvent
}

func (r *recordingOnlineStatusObserverForWebhookTest) ObserveOnlineStatus(_ context.Context, event presence.OnlineStatusEvent) error {
	r.events = append(r.events, event)
	return nil
}

type recordingPersistAfterEnqueuerForWebhookTest struct {
	messageIDs []uint64
}

func (r *recordingPersistAfterEnqueuerForWebhookTest) EnqueuePersistAfter(_ context.Context, event channelappend.CommittedEnvelope) {
	r.messageIDs = append(r.messageIDs, event.MessageID)
}

type recordingOfflineRecipientObserverForWebhookTest struct {
	uids []string
}

func (r *recordingOfflineRecipientObserverForWebhookTest) ObserveOfflineRecipient(_ context.Context, event channelappend.OfflineRecipientEvent) {
	r.uids = append(r.uids, event.UID)
}

type recordingOfflineRecipientsObserverForWebhookTest struct {
	uidBatches [][]string
}

type recordingBatchOfflineRecipientObserverForWebhookTest struct {
	singleUIDs []string
	uidBatches [][]string
}

func (r *recordingBatchOfflineRecipientObserverForWebhookTest) ObserveOfflineRecipient(_ context.Context, event channelappend.OfflineRecipientEvent) {
	r.singleUIDs = append(r.singleUIDs, event.UID)
}

func (r *recordingBatchOfflineRecipientObserverForWebhookTest) ObserveOfflineRecipients(_ context.Context, event channelappend.OfflineRecipientsEvent) {
	r.uidBatches = append(r.uidBatches, append([]string(nil), event.UIDs...))
}

func (r *recordingOfflineRecipientsObserverForWebhookTest) ObserveOfflineRecipients(_ context.Context, event channelappend.OfflineRecipientsEvent) {
	r.uidBatches = append(r.uidBatches, append([]string(nil), event.UIDs...))
}

type staticChannelAppendPresenceResolver struct{}

func (staticChannelAppendPresenceResolver) EndpointsByUIDs(context.Context, []string) ([]channelappend.Route, error) {
	return nil, nil
}
