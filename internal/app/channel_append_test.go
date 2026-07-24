package app

import (
	"context"
	"errors"
	"testing"

	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
)

func TestChannelAppendSubscriberMutationObserverRefreshesMetadataCache(t *testing.T) {
	cache := clusterinfra.NewChannelAppendMetadataCache()
	app := &App{channelAppendMetadata: cache}
	observer := channelAppendSubscriberMutationObserver{app: app}

	observer.ObserveSubscriberMutation(context.Background(), channelusecase.SubscriberMutationEvent{
		ChannelKey: channelusecase.ChannelKey{
			ChannelID:   "g1",
			ChannelType: 2,
		},
		Large:                     true,
		SubscriberMutationVersion: 7,
	})

	metadata, ok := cache.Lookup(channelappend.ChannelID{ID: "g1", Type: 2})
	if !ok || !metadata.Large || metadata.SubscriberMutationVersion != 7 {
		t.Fatalf("metadata cache = %#v ok=%v, want large version 7", metadata, ok)
	}
}

func TestChannelAppendOwnerPusherObservesActualPushAttempt(t *testing.T) {
	runtimeRoute := runtimedelivery.Route{UID: "u1", OwnerNodeID: 3, SessionID: 10}
	channelRoute := channelappend.Route{UID: "u1", OwnerNodeID: 3, SessionID: 10}
	next := &recordingRuntimeOwnerPusherForChannelAppendTest{result: runtimedelivery.PushResult{Accepted: []runtimedelivery.Route{runtimeRoute}}}
	observer := &recordingDeliveryObserverForChannelAppendTest{}
	pusher := channelAppendOwnerPusher{next: next, observer: observer}

	result, err := pusher.Push(context.Background(), channelappend.PushCommand{
		OwnerNodeID: 3,
		Envelope:    channelappend.CommittedEnvelope{MessageID: 10},
		Routes:      []channelappend.Route{channelRoute},
	})

	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 || result.Accepted[0] != channelRoute {
		t.Fatalf("Push() result = %#v, want accepted route", result)
	}
	if len(observer.pushes) != 1 {
		t.Fatalf("push observations = %d, want 1", len(observer.pushes))
	}
	got := observer.pushes[0]
	if got.OwnerNodeID != 3 || got.Result != runtimedelivery.DeliveryResultOK ||
		got.ErrorClass != runtimedelivery.DeliveryErrorClassNone || got.Routes != 1 || got.Accepted != 1 || got.Duration < 0 {
		t.Fatalf("push observation = %+v, want successful owner 3 route", got)
	}
}

func TestChannelAppendOwnerPusherClassifiesPushOutcomes(t *testing.T) {
	firstRuntimeRoute := runtimedelivery.Route{UID: "u1", OwnerNodeID: 3, SessionID: 10}
	secondRuntimeRoute := runtimedelivery.Route{UID: "u2", OwnerNodeID: 3, SessionID: 20}
	command := channelappend.PushCommand{
		OwnerNodeID: 3,
		Envelope:    channelappend.CommittedEnvelope{MessageID: 10},
		Routes: []channelappend.Route{
			{UID: "u1", OwnerNodeID: 3, SessionID: 10},
			{UID: "u2", OwnerNodeID: 3, SessionID: 20},
		},
	}
	hardErr := errors.New("push failed")
	tests := []struct {
		name          string
		next          *recordingRuntimeOwnerPusherForChannelAppendTest
		wantResult    string
		wantClass     string
		wantAccepted  int
		wantRetryable int
		wantDropped   int
	}{
		{
			name: "mixed accepted and dropped",
			next: &recordingRuntimeOwnerPusherForChannelAppendTest{result: runtimedelivery.PushResult{
				Accepted: []runtimedelivery.Route{firstRuntimeRoute},
				Dropped:  []runtimedelivery.Route{secondRuntimeRoute},
			}},
			wantResult: runtimedelivery.DeliveryResultDropped, wantClass: runtimedelivery.DeliveryErrorClassNone,
			wantAccepted: 1, wantDropped: 1,
		},
		{
			name: "retryable routes",
			next: &recordingRuntimeOwnerPusherForChannelAppendTest{result: runtimedelivery.PushResult{
				Retryable: []runtimedelivery.Route{secondRuntimeRoute},
			}},
			wantResult: runtimedelivery.DeliveryResultRetryable, wantClass: runtimedelivery.DeliveryErrorClassRetryable,
			wantRetryable: 1,
		},
		{
			name:       "retryable error",
			next:       &recordingRuntimeOwnerPusherForChannelAppendTest{err: runtimedelivery.ErrRouteNotReady},
			wantResult: runtimedelivery.DeliveryResultRetryable,
			wantClass:  runtimedelivery.DeliveryErrorClassRouteNotReady,
		},
		{
			name:       "hard error",
			next:       &recordingRuntimeOwnerPusherForChannelAppendTest{err: hardErr},
			wantResult: runtimedelivery.DeliveryResultError,
			wantClass:  runtimedelivery.DeliveryErrorClassError,
		},
		{
			name:        "missing pusher drops routes",
			wantResult:  runtimedelivery.DeliveryResultDropped,
			wantClass:   runtimedelivery.DeliveryErrorClassNone,
			wantDropped: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer := &recordingDeliveryObserverForChannelAppendTest{}
			var next runtimedelivery.Pusher
			if tt.next != nil {
				next = tt.next
			}
			pusher := channelAppendOwnerPusher{next: next, observer: observer}
			_, _ = pusher.Push(context.Background(), command)
			if len(observer.pushes) != 1 {
				t.Fatalf("push observations = %d, want 1", len(observer.pushes))
			}
			got := observer.pushes[0]
			if got.Result != tt.wantResult || got.ErrorClass != tt.wantClass || got.Routes != 2 ||
				got.Accepted != tt.wantAccepted || got.Retryable != tt.wantRetryable || got.Dropped != tt.wantDropped {
				t.Fatalf("push observation = %+v, want result=%s class=%s accepted=%d retryable=%d dropped=%d",
					got, tt.wantResult, tt.wantClass, tt.wantAccepted, tt.wantRetryable, tt.wantDropped)
			}
		})
	}
}

func TestChannelAppendOwnerPusherObservesPanicAndRepanics(t *testing.T) {
	panicValue := &struct{}{}
	observer := &recordingDeliveryObserverForChannelAppendTest{}
	pusher := channelAppendOwnerPusher{
		next:     &recordingRuntimeOwnerPusherForChannelAppendTest{panicValue: panicValue},
		observer: observer,
	}
	command := channelappend.PushCommand{
		OwnerNodeID: 3,
		Routes:      []channelappend.Route{{UID: "u1", OwnerNodeID: 3, SessionID: 10}},
	}

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = pusher.Push(context.Background(), command)
	}()

	if recovered != panicValue {
		t.Fatalf("recovered panic = %#v, want original panic value", recovered)
	}
	if len(observer.pushes) != 1 {
		t.Fatalf("push observations = %d, want panic attempt", len(observer.pushes))
	}
	got := observer.pushes[0]
	if got.Result != runtimedelivery.DeliveryResultError || got.ErrorClass != runtimedelivery.DeliveryErrorClassError || got.Routes != 1 {
		t.Fatalf("panic observation = %+v, want error result for one route", got)
	}
}

type recordingRuntimeOwnerPusherForChannelAppendTest struct {
	result     runtimedelivery.PushResult
	err        error
	panicValue any
}

func (p *recordingRuntimeOwnerPusherForChannelAppendTest) Push(context.Context, runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	if p.panicValue != nil {
		panic(p.panicValue)
	}
	return runtimedelivery.PushResult{
		Accepted:  append([]runtimedelivery.Route(nil), p.result.Accepted...),
		Retryable: append([]runtimedelivery.Route(nil), p.result.Retryable...),
		Dropped:   append([]runtimedelivery.Route(nil), p.result.Dropped...),
	}, p.err
}

type recordingDeliveryObserverForChannelAppendTest struct {
	pushes []runtimedelivery.FanoutPushEvent
}

func (o *recordingDeliveryObserverForChannelAppendTest) ObserveFanoutTask(runtimedelivery.FanoutTaskEvent) {
}

func (o *recordingDeliveryObserverForChannelAppendTest) ObserveFanoutResolve(runtimedelivery.FanoutResolveEvent) {
}

func (o *recordingDeliveryObserverForChannelAppendTest) ObserveFanoutPush(event runtimedelivery.FanoutPushEvent) {
	o.pushes = append(o.pushes, event)
}
