package app

import (
	"context"
	"sync"
	"testing"
	"time"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/delivery"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestPluginReceiveObserverMapsOfflineResolvedEventAndDetachesCancellation(t *testing.T) {
	plugins := &recordingPluginReceiveUsecase{done: make(chan struct{})}
	observer := startTestPluginReceiveObserver(t, plugins, time.Second, 1, 8)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	observer.OfflineResolved(ctx, deliveryruntime.OfflineResolvedEvent{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message:           channel.Message{MessageID: 77, FromUID: "alice", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("hello")},
			MessageScopedUIDs: []string{"bot"},
		},
		UID: "bot",
	})

	plugins.waitDone(t)
	require.Len(t, plugins.events, 1)
	require.Equal(t, uint64(77), plugins.events[0].Message.MessageID)
	require.Equal(t, "bot", plugins.events[0].UID)
	require.True(t, plugins.events[0].RequestScoped)
	require.Len(t, plugins.ctxErrs, 1)
	require.NoError(t, plugins.ctxErrs[0])
}

func TestPluginReceiveObserverDoesNotBlockDeliveryPath(t *testing.T) {
	plugins := newBlockingPluginReceiveUsecase()
	observer := startTestPluginReceiveObserver(t, plugins, time.Second, 1, 1)
	defer plugins.release()

	done := make(chan struct{})
	go func() {
		observer.OfflineResolved(context.Background(), deliveryruntime.OfflineResolvedEvent{
			Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{MessageID: 78, FromUID: "alice", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}},
			UID:      "bot",
		})
		close(done)
	}()
	plugins.waitStarted(t)

	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Receive observer blocked the delivery path")
	}
}

func TestPluginReceiveObserverDoesNotDropBehindBusyWorker(t *testing.T) {
	first := newBlockingPluginReceiveUsecase()
	second := &recordingPluginReceiveUsecase{done: make(chan struct{})}
	observer := newPluginReceiveObserverWithLimits(chainedPluginReceiveUsecase{first: first, second: second}, time.Second, nil, 1, 1)
	require.NoError(t, observer.Start(context.Background()))
	defer func() { require.NoError(t, observer.Stop(context.Background())) }()
	defer first.release()

	observer.OfflineResolved(context.Background(), deliveryruntime.OfflineResolvedEvent{
		Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{MessageID: 80, FromUID: "alice", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}},
		UID:      "first",
	})
	first.waitStarted(t)
	observer.OfflineResolved(context.Background(), deliveryruntime.OfflineResolvedEvent{
		Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{MessageID: 81, FromUID: "alice", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}},
		UID:      "second",
	})

	first.release()
	second.waitDone(t)
	require.Len(t, second.events, 1)
	require.Equal(t, "second", second.events[0].UID)
}

func TestPluginReceiveObserverBoundsQueuedWork(t *testing.T) {
	first := newBlockingPluginReceiveUsecase()
	second := &recordingPluginReceiveUsecase{}
	observer := newPluginReceiveObserverWithLimits(chainedPluginReceiveUsecase{first: first, second: second}, time.Second, nil, 1, 1)
	require.NoError(t, observer.Start(context.Background()))
	defer func() { require.NoError(t, observer.Stop(context.Background())) }()
	defer first.release()

	observer.OfflineResolved(context.Background(), deliveryruntime.OfflineResolvedEvent{
		Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{MessageID: 82, FromUID: "alice", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}},
		UID:      "first",
	})
	first.waitStarted(t)
	observer.OfflineResolved(context.Background(), deliveryruntime.OfflineResolvedEvent{
		Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{MessageID: 83, FromUID: "alice", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}},
		UID:      "second",
	})
	blocked := make(chan struct{})
	go func() {
		observer.OfflineResolved(context.Background(), deliveryruntime.OfflineResolvedEvent{
			Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{MessageID: 84, FromUID: "alice", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}},
			UID:      "third",
		})
		close(blocked)
	}()

	select {
	case <-blocked:
		t.Fatal("Receive observer dropped or accepted work beyond the configured queue bound")
	case <-time.After(50 * time.Millisecond):
	}

	first.release()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("Receive observer stayed blocked after queue capacity was released")
	}
	second.waitEventCount(t, 2)
}

func TestPluginReceiveObserverStopCancelsActiveWorkers(t *testing.T) {
	plugins := newCancelAwarePluginReceiveUsecase()
	observer := newPluginReceiveObserverWithLimits(plugins, time.Hour, nil, 1, 1)
	require.NoError(t, observer.Start(context.Background()))

	observer.OfflineResolved(context.Background(), deliveryruntime.OfflineResolvedEvent{
		Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{MessageID: 84, FromUID: "alice", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}},
		UID:      "bot",
	})
	plugins.waitStarted(t)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, observer.Stop(stopCtx))
	plugins.waitCanceled(t)
}

func TestPluginReceiveObserverUsesBoundedContext(t *testing.T) {
	plugins := &recordingPluginReceiveUsecase{done: make(chan struct{})}
	observer := startTestPluginReceiveObserver(t, plugins, time.Second, 1, 8)

	observer.OfflineResolved(context.Background(), deliveryruntime.OfflineResolvedEvent{
		Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{MessageID: 79, FromUID: "alice", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}},
		UID:      "bot",
	})

	plugins.waitDone(t)
	require.Len(t, plugins.deadlineSet, 1)
	require.True(t, plugins.deadlineSet[0])
}

func startTestPluginReceiveObserver(t *testing.T, plugins pluginOfflineReceiveUsecase, timeout time.Duration, concurrency int, queueDepth int) *pluginReceiveObserver {
	t.Helper()
	observer := newPluginReceiveObserverWithLimits(plugins, timeout, nil, concurrency, queueDepth)
	require.NoError(t, observer.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, observer.Stop(context.Background())) })
	return observer
}

func TestNewBuildsPluginReceiveObserverWhenPluginEnabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.Plugin.Enable = true
	cfg.Plugin.Timeout = 9 * time.Second

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.Stop()) })

	observer, ok := unexportedFieldForTest(t, app.deliveryRuntime, "offlineResolvedObserver").(deliveryruntime.OfflineResolvedObserver)
	require.True(t, ok)
	receiveObserver, ok := observer.(*pluginReceiveObserver)
	require.Truef(t, ok, "offline observer should be pluginReceiveObserver, got %T", observer)
	require.Same(t, app.pluginApp, receiveObserver.pluginUsecase)
	require.Same(t, app.pluginReceiveObserver, receiveObserver)
	require.Equal(t, cfg.Plugin.Timeout, receiveObserver.timeout)
	resolver := unexportedFieldForTest(t, app.deliveryRuntime, "resolver").(tagDeliveryResolver)
	require.True(t, resolver.collectOfflineUIDs)
}

func TestNewSkipsPluginReceiveObserverAndOfflineUIDCollectionWhenPluginDisabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.Plugin.Enable = false

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.Stop()) })

	observer := unexportedFieldForTest(t, app.deliveryRuntime, "offlineResolvedObserver")
	require.Nil(t, observer)
	resolver := unexportedFieldForTest(t, app.deliveryRuntime, "resolver").(tagDeliveryResolver)
	require.False(t, resolver.collectOfflineUIDs)
}

type recordingPluginReceiveUsecase struct {
	mu          sync.Mutex
	events      []pluginusecase.OfflineReceiveEvent
	ctxErrs     []error
	deadlineSet []bool
	done        chan struct{}
	err         error
}

func (r *recordingPluginReceiveUsecase) ReceiveOffline(ctx context.Context, event pluginusecase.OfflineReceiveEvent) error {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.ctxErrs = append(r.ctxErrs, ctx.Err())
	_, ok := ctx.Deadline()
	r.deadlineSet = append(r.deadlineSet, ok)
	done := r.done
	r.mu.Unlock()
	if done != nil {
		close(done)
	}
	return r.err
}

func (r *recordingPluginReceiveUsecase) waitDone(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReceiveOffline")
	}
}

func (r *recordingPluginReceiveUsecase) waitEventCount(t *testing.T, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return r.eventCount() >= want
	}, time.Second, 5*time.Millisecond)
}

func (r *recordingPluginReceiveUsecase) eventCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

type blockingPluginReceiveUsecase struct {
	started  chan struct{}
	releaseC chan struct{}
}

func newBlockingPluginReceiveUsecase() *blockingPluginReceiveUsecase {
	return &blockingPluginReceiveUsecase{started: make(chan struct{}), releaseC: make(chan struct{})}
}

func (b *blockingPluginReceiveUsecase) ReceiveOffline(context.Context, pluginusecase.OfflineReceiveEvent) error {
	close(b.started)
	<-b.releaseC
	return nil
}

func (b *blockingPluginReceiveUsecase) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReceiveOffline to start")
	}
}

func (b *blockingPluginReceiveUsecase) release() {
	select {
	case <-b.releaseC:
	default:
		close(b.releaseC)
	}
}

type chainedPluginReceiveUsecase struct {
	first  *blockingPluginReceiveUsecase
	second *recordingPluginReceiveUsecase
}

func (c chainedPluginReceiveUsecase) ReceiveOffline(ctx context.Context, event pluginusecase.OfflineReceiveEvent) error {
	if event.UID == "first" {
		return c.first.ReceiveOffline(ctx, event)
	}
	return c.second.ReceiveOffline(ctx, event)
}

type cancelAwarePluginReceiveUsecase struct {
	started  chan struct{}
	canceled chan struct{}
}

func newCancelAwarePluginReceiveUsecase() *cancelAwarePluginReceiveUsecase {
	return &cancelAwarePluginReceiveUsecase{started: make(chan struct{}), canceled: make(chan struct{})}
}

func (c *cancelAwarePluginReceiveUsecase) ReceiveOffline(ctx context.Context, _ pluginusecase.OfflineReceiveEvent) error {
	close(c.started)
	<-ctx.Done()
	close(c.canceled)
	return ctx.Err()
}

func (c *cancelAwarePluginReceiveUsecase) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-c.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReceiveOffline to start")
	}
}

func (c *cancelAwarePluginReceiveUsecase) waitCanceled(t *testing.T) {
	t.Helper()
	select {
	case <-c.canceled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReceiveOffline cancellation")
	}
}
