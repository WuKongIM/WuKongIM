package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

func TestOnlineDeliveryOfflineObserverPreservesOrdinaryCommitEligibility(t *testing.T) {
	tests := []struct {
		name  string
		event channelappend.CommittedEnvelope
		want  int
	}{
		{name: "ordinary durable", event: channelappend.CommittedEnvelope{MessageSeq: 1}, want: 1},
		{name: "transient", event: channelappend.CommittedEnvelope{}, want: 0},
		{name: "sync once", event: channelappend.CommittedEnvelope{MessageSeq: 1, SyncOnce: true}, want: 0},
		{name: "request scoped", event: channelappend.CommittedEnvelope{MessageSeq: 1, MessageScopedUIDs: []string{"u1"}}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := &recordingOnlineDeliveryOfflineObserver{}
			observer := onlineDeliveryOfflineObserver{next: next}

			observer.ObserveOfflineRecipients(context.Background(), runtimedelivery.OfflineRecipientsEvent{
				Event: tt.event,
				UIDs:  []string{"u1"},
			})

			if len(next.events) != tt.want {
				t.Fatalf("offline observer calls = %d, want %d", len(next.events), tt.want)
			}
		})
	}
}

func TestOnlineDeliveryObserverRecordsRemotePushAndNormalizedError(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := onlineDeliveryObserver{app: &App{metrics: reg}}
	observer.ObserveOwnerPush(runtimedelivery.OwnerPushEvent{
		OwnerNodeID: 2,
		Result:      runtimedelivery.ObservationResultError,
		Routes:      3,
		Retryable:   3,
		Duration:    time.Millisecond,
		Failure:     runtimedelivery.OwnerPushFailureSample{Err: errors.New("remote transport unavailable")},
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	pushes := requireAppMetricFamily(t, families, "wukongim_delivery_push_rpc_total")
	if got := findAppMetricByLabels(t, pushes, map[string]string{"target_node": "2", "result": "error"}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("remote push attempts = %v, want 1", got)
	}
	routes := requireAppMetricFamily(t, families, "wukongim_delivery_push_rpc_routes_total")
	if got := findAppMetricByLabels(t, routes, map[string]string{"target_node": "2", "result": "error"}).GetCounter().GetValue(); got != 3 {
		t.Fatalf("remote push routes = %v, want 3", got)
	}
	errorsTotal := requireAppMetricFamily(t, families, "wukongim_delivery_errors_total")
	if got := findAppMetricByLabels(t, errorsTotal, map[string]string{"class": runtimedelivery.DeliveryErrorClassError}).GetCounter().GetValue(); got != 1 {
		t.Fatalf("normalized delivery errors = %v, want 1", got)
	}
}

type recordingOnlineDeliveryOfflineObserver struct {
	events []channelappend.OfflineRecipientsEvent
}

func (o *recordingOnlineDeliveryOfflineObserver) ObserveOfflineRecipients(_ context.Context, event channelappend.OfflineRecipientsEvent) {
	o.events = append(o.events, event)
}
