package delivery

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestDeliveryRuntimeReportsOfflineResolvedUIDsOutsideActorLock(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)}
	online := testRoute("online", 1, 11, 2)
	resolver := &offlineResolvedResolver{page: ResolvePageResult{
		Routes:      []RouteKey{online},
		OfflineUIDs: []string{"offline"},
		Done:        true,
	}}
	pusher := &recordingPusher{}
	var runtime *Manager
	observer := &recordingOfflineResolvedObserver{
		onOffline: func(OfflineResolvedEvent) {
			_ = runtime.ActorLane(testChannelID, frame.ChannelTypeGroup)
		},
	}
	runtime = NewManager(Config{
		Resolver:                resolver,
		Push:                    pusher,
		Clock:                   clock,
		OfflineResolvedObserver: observer,
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Submit(context.Background(), testEnvelopeFor(testChannelID, frame.ChannelTypeGroup, 501, 7, "payload"))
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("offline resolved observer was called while actor lock was held")
	}
	require.Len(t, observer.events, 1)
	require.Equal(t, "offline", observer.events[0].UID)
	require.Equal(t, uint64(501), observer.events[0].Envelope.MessageID)
	require.Equal(t, 1, observer.events[0].Attempt)
	require.Equal(t, "", observer.events[0].PageCursor)
	require.True(t, observer.events[0].Done)
	require.Equal(t, []RouteKey{online}, pusher.calls[0].routes)
}

func TestDeliveryRuntimeDoesNotReportOnlineRoutesAsOfflineResolved(t *testing.T) {
	online := testRoute("online", 1, 11, 2)
	observer := &recordingOfflineResolvedObserver{}
	runtime := NewManager(Config{
		Resolver:                &offlineResolvedResolver{page: ResolvePageResult{Routes: []RouteKey{online}, Done: true}},
		Push:                    &recordingPusher{},
		Clock:                   &testClock{now: time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)},
		OfflineResolvedObserver: observer,
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor(testChannelID, frame.ChannelTypeGroup, 502, 8, "payload")))

	require.Empty(t, observer.events)
}

func TestDeliveryRuntimeFlushesOfflineResolvedEventsBetweenResolverPages(t *testing.T) {
	resolver := newBlockingSecondOfflinePageResolver()
	observer := &recordingOfflineResolvedObserver{
		onOffline: func(event OfflineResolvedEvent) {
			if event.UID == "offline-1" {
				resolver.ReleaseSecondPage()
			}
		},
	}
	runtime := NewManager(Config{
		Resolver:                resolver,
		Push:                    &recordingPusher{},
		Clock:                   &testClock{now: time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)},
		OfflineResolvedObserver: observer,
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Submit(context.Background(), testEnvelopeFor(testChannelID, frame.ChannelTypeGroup, 503, 9, "payload"))
	}()
	resolver.WaitForSecondPage(t)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("offline resolved observer was not flushed before the next resolver page")
	}
	require.Equal(t, []string{"offline-1", "offline-2"}, observer.uids())
}

type offlineResolvedResolver struct {
	page ResolvePageResult
}

func (r *offlineResolvedResolver) BeginResolve(_ context.Context, _ ChannelKey, _ CommittedEnvelope) (any, error) {
	return "token", nil
}

func (r *offlineResolvedResolver) ResolvePage(_ context.Context, _ any, cursor string, limit int) (ResolvePageResult, error) {
	page := r.page
	page.Routes = append([]RouteKey(nil), r.page.Routes...)
	page.OfflineUIDs = append([]string(nil), r.page.OfflineUIDs...)
	return page, nil
}

type recordingOfflineResolvedObserver struct {
	events    []OfflineResolvedEvent
	onOffline func(OfflineResolvedEvent)
}

func (r *recordingOfflineResolvedObserver) OfflineResolved(_ context.Context, event OfflineResolvedEvent) {
	if r.onOffline != nil {
		r.onOffline(event)
	}
	r.events = append(r.events, event)
}

func (r *recordingOfflineResolvedObserver) uids() []string {
	uids := make([]string, 0, len(r.events))
	for _, event := range r.events {
		uids = append(uids, event.UID)
	}
	return uids
}

type blockingSecondOfflinePageResolver struct {
	secondStarted chan struct{}
	releaseSecond chan struct{}
	pageCalls     int
}

func newBlockingSecondOfflinePageResolver() *blockingSecondOfflinePageResolver {
	return &blockingSecondOfflinePageResolver{
		secondStarted: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
}

func (r *blockingSecondOfflinePageResolver) BeginResolve(_ context.Context, _ ChannelKey, _ CommittedEnvelope) (any, error) {
	return "token", nil
}

func (r *blockingSecondOfflinePageResolver) ResolvePage(_ context.Context, _ any, _ string, _ int) (ResolvePageResult, error) {
	switch r.pageCalls {
	case 0:
		r.pageCalls++
		return ResolvePageResult{OfflineUIDs: []string{"offline-1"}, NextCursor: "offline-1"}, nil
	default:
		r.pageCalls++
		close(r.secondStarted)
		<-r.releaseSecond
		return ResolvePageResult{OfflineUIDs: []string{"offline-2"}, NextCursor: "offline-2", Done: true}, nil
	}
}

func (r *blockingSecondOfflinePageResolver) WaitForSecondPage(t *testing.T) {
	t.Helper()
	select {
	case <-r.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second resolver page")
	}
}

func (r *blockingSecondOfflinePageResolver) ReleaseSecondPage() {
	select {
	case <-r.releaseSecond:
	default:
		close(r.releaseSecond)
	}
}
