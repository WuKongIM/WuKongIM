package delivery

import (
	"context"
	"errors"
	"testing"
)

func TestDeliveryErrorClassifiesRetryableErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantClass string
		retryable bool
	}{
		{name: "nil", wantClass: DeliveryErrorClassNone},
		{name: "retryable fanout", err: ErrRetryableFanoutTask, wantClass: DeliveryErrorClassRetryable, retryable: true},
		{name: "retryable push", err: ErrRetryablePushRoutes, wantClass: DeliveryErrorClassRetryable, retryable: true},
		{name: "route not ready", err: ErrRouteNotReady, wantClass: DeliveryErrorClassRouteNotReady, retryable: true},
		{name: "queue full", err: errors.Join(ErrRetryQueueFull, ErrRetryableFanoutTask), wantClass: DeliveryErrorClassQueueFull},
		{name: "invalid cursor", err: ErrInvalidSubscriberCursor, wantClass: DeliveryErrorClassInvalidCursor},
		{name: "context canceled", err: context.Canceled, wantClass: DeliveryErrorClassCanceled},
		{name: "unknown", err: errors.New("other"), wantClass: DeliveryErrorClassError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeliveryErrorClass(tt.err); got != tt.wantClass {
				t.Fatalf("DeliveryErrorClass() = %q, want %q", got, tt.wantClass)
			}
			if got := IsRetryableDeliveryError(tt.err); got != tt.retryable {
				t.Fatalf("IsRetryableDeliveryError() = %v, want %v", got, tt.retryable)
			}
		})
	}
}

func TestClassifyPushObservation(t *testing.T) {
	tests := []struct {
		name       string
		retryable  int
		dropped    int
		err        error
		wantResult string
		wantClass  string
	}{
		{name: "success", wantResult: DeliveryResultOK, wantClass: DeliveryErrorClassNone},
		{name: "retryable routes take precedence", retryable: 1, dropped: 1, wantResult: DeliveryResultRetryable, wantClass: DeliveryErrorClassRetryable},
		{name: "mixed accepted and dropped", dropped: 1, wantResult: DeliveryResultDropped, wantClass: DeliveryErrorClassNone},
		{name: "retryable error", err: ErrRouteNotReady, wantResult: DeliveryResultRetryable, wantClass: DeliveryErrorClassRouteNotReady},
		{name: "hard error", err: errors.New("push failed"), wantResult: DeliveryResultError, wantClass: DeliveryErrorClassError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotResult, gotClass := ClassifyPushObservation(tt.retryable, tt.dropped, tt.err)
			if gotResult != tt.wantResult || gotClass != tt.wantClass {
				t.Fatalf("ClassifyPushObservation() = (%q, %q), want (%q, %q)", gotResult, gotClass, tt.wantResult, tt.wantClass)
			}
		})
	}
}

func TestFanoutWorkerObserverRecordsResolveAndRetryablePush(t *testing.T) {
	observer := &recordingDeliveryObserver{}
	worker := NewFanoutWorker(FanoutWorkerOptions{
		Presence: &fakePresenceResolver{
			routes: map[string][]Route{
				"u1": {{UID: "u1", OwnerNodeID: 10, SessionID: 101}},
			},
		},
		Push:     &fakePusher{result: PushResult{Retryable: []Route{{UID: "u1", OwnerNodeID: 10, SessionID: 101}}}},
		Observer: observer,
	})

	err := worker.RunTask(context.Background(), FanoutTask{
		Envelope:  Envelope{MessageID: 1001, ChannelType: 2, MessageScopedUIDs: []string{"u1"}},
		Partition: Partition{ID: 1},
	})
	if !errors.Is(err, ErrRetryablePushRoutes) {
		t.Fatalf("RunTask() error = %v, want ErrRetryablePushRoutes", err)
	}
	if len(observer.resolves) != 1 {
		t.Fatalf("resolve events = %d, want 1", len(observer.resolves))
	}
	resolve := observer.resolves[0]
	if resolve.Result != DeliveryResultOK || resolve.UIDs != 1 || resolve.Routes != 1 {
		t.Fatalf("resolve event = %#v, want ok with 1 uid and 1 route", resolve)
	}
	if len(observer.pushes) != 1 {
		t.Fatalf("push events = %d, want 1", len(observer.pushes))
	}
	push := observer.pushes[0]
	if push.OwnerNodeID != 10 || push.Result != DeliveryResultRetryable || push.Routes != 1 || push.Retryable != 1 {
		t.Fatalf("push event = %#v, want retryable owner 10", push)
	}
}

func TestFanoutTaskRouterObserverRecordsRemoteForwardFailure(t *testing.T) {
	observer := &recordingDeliveryObserver{}
	remote := &recordingFanoutForwarder{err: errors.New("rpc failed")}
	router := NewFanoutTaskRouter(FanoutTaskRouterOptions{
		LocalNodeID: 1,
		Local:       &recordingFanoutRunner{},
		Remote:      remote,
		Observer:    observer,
	})

	err := router.RunTask(context.Background(), FanoutTask{Partition: Partition{ID: 2, LeaderNodeID: 3}})
	if !errors.Is(err, ErrRetryableFanoutTask) {
		t.Fatalf("RunTask() error = %v, want ErrRetryableFanoutTask", err)
	}
	if len(observer.tasks) != 1 {
		t.Fatalf("task events = %d, want 1", len(observer.tasks))
	}
	event := observer.tasks[0]
	if event.TargetNodeID != 3 || event.Path != DeliveryFanoutPathRemote || event.Result != DeliveryResultRetryable ||
		event.ErrorClass != DeliveryErrorClassRetryable {
		t.Fatalf("task event = %#v, want retryable remote node 3", event)
	}
}

type recordingDeliveryObserver struct {
	tasks    []FanoutTaskEvent
	resolves []FanoutResolveEvent
	pushes   []FanoutPushEvent
}

func (o *recordingDeliveryObserver) ObserveFanoutTask(event FanoutTaskEvent) {
	o.tasks = append(o.tasks, event)
}

func (o *recordingDeliveryObserver) ObserveFanoutResolve(event FanoutResolveEvent) {
	o.resolves = append(o.resolves, event)
}

func (o *recordingDeliveryObserver) ObserveFanoutPush(event FanoutPushEvent) {
	o.pushes = append(o.pushes, event)
}
