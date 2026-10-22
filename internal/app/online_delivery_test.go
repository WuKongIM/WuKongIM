package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestOnlineDeliveryIncompleteLogPreservesExactBodyFreeIdentity(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result runtimedelivery.ObservationResult
		phase  runtimedelivery.PlanFailurePhase
		mode   onlinedelivery.Mode
		seq    uint64
	}{
		{name: "presence", result: runtimedelivery.ObservationResultError, phase: runtimedelivery.PlanFailurePhasePresence, mode: onlinedelivery.ModeDurable, seq: 9007199254740993},
		{name: "owner push", result: runtimedelivery.ObservationResultRetryExhausted, phase: runtimedelivery.PlanFailurePhaseOwnerPush, mode: onlinedelivery.ModeDurable, seq: 9007199254740993},
		{name: "canceled", result: runtimedelivery.ObservationResultCanceled, phase: runtimedelivery.PlanFailurePhaseContext, mode: onlinedelivery.ModeDurable, seq: 9007199254740993},
		{name: "panic", result: runtimedelivery.ObservationResultPanic, phase: runtimedelivery.PlanFailurePhasePanic, mode: onlinedelivery.ModeDurable, seq: 9007199254740993},
		{name: "transient zero sequence", result: runtimedelivery.ObservationResultError, phase: runtimedelivery.PlanFailurePhasePresence, mode: onlinedelivery.ModeTransient},
		{name: "success remains quiet", result: runtimedelivery.ObservationResultOK, mode: onlinedelivery.ModeDurable, seq: 9007199254740993},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logger := &recordingAppLogger{}
			observer := &onlineDeliveryObserver{app: &App{logger: logger}}
			cause := errors.New("original failure")
			observer.ObservePlanTerminal(runtimedelivery.PlanTerminalEvent{
				MessageID: 18446744073709551614, MessageSeq: tt.seq,
				Result: tt.result, Mode: tt.mode, Recipients: 3,
				Failure: runtimedelivery.PlanFailureSample{Phase: tt.phase, RecipientUID: "existing-recipient", OwnerNodeID: 2, Err: cause},
			})
			entries := appLogEntriesForEvent(logger, "WARN", "internal.app.delivery.plan_incomplete")
			if tt.result == runtimedelivery.ObservationResultOK {
				if len(logger.entriesSnapshot()) != 0 {
					t.Fatal("successful terminal observation added a log record")
				}
				return
			}
			if len(entries) != 1 || len(logger.entriesSnapshot()) != 1 {
				t.Fatalf("failed plan logs = %d/%d, want one existing warning", len(entries), len(logger.entriesSnapshot()))
			}
			entry := entries[0]
			requireAppLogField(t, entry, "messageID", uint64(18446744073709551614))
			requireAppLogField(t, entry, "messageSeq", tt.seq)
			requireAppLogField(t, entry, "result", string(tt.result))
			requireAppLogField(t, entry, "phase", string(tt.phase))
			wantMode := "durable"
			if tt.mode == onlinedelivery.ModeTransient {
				wantMode = "transient"
			}
			requireAppLogField(t, entry, "mode", wantMode)
			requireAppLogField(t, entry, "recipients", 3)
			requireAppLogField(t, entry, "uid", "existing-recipient")
			requireAppLogField(t, entry, "ownerNodeID", uint64(2))
			requireAppLogField(t, entry, "error", cause)
			if len(entry.fields) != 10 {
				t.Fatalf("failure log fields = %d, want eight existing fields plus two scalar identities", len(entry.fields))
			}
			for _, field := range entry.fields {
				switch field.Key {
				case "messageID", "messageSeq":
					if field.Type != wklog.Uint64Type {
						t.Fatalf("identity %s type = %v, want uint64", field.Key, field.Type)
					}
				case "event", "result", "phase", "mode", "recipients", "uid", "ownerNodeID", "error":
				default:
					t.Fatalf("unexpected failure log field %q", field.Key)
				}
			}
		})
	}
	var absent *onlineDeliveryObserver
	absent.ObservePlanTerminal(runtimedelivery.PlanTerminalEvent{})
	(&onlineDeliveryObserver{}).ObservePlanTerminal(runtimedelivery.PlanTerminalEvent{})
	(&onlineDeliveryObserver{app: &App{}}).ObservePlanTerminal(runtimedelivery.PlanTerminalEvent{})
}

func TestOnlineDeliveryTerminalIdentitiesDoNotCreateMetricSeries(t *testing.T) {
	reg := obsmetrics.New(1, "node-1")
	observer := &onlineDeliveryObserver{app: &App{metrics: reg}}
	for _, identity := range []struct{ id, sequence uint64 }{{0, 0}, {18446744073709551614, 9007199254740993}} {
		observer.ObservePlanTerminal(runtimedelivery.PlanTerminalEvent{
			MessageID: identity.id, MessageSeq: identity.sequence, Result: runtimedelivery.ObservationResultError,
			Recipients: 3, Duration: time.Millisecond,
		})
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	process := requireAppMetricFamily(t, families, "wukongim_delivery_recipient_worker_process_total")
	if len(process.Metric) != 7 {
		t.Fatalf("process series = %d, want the seven existing terminal result partitions", len(process.Metric))
	}
	if got := findAppMetricByLabels(t, process, map[string]string{"result": "error"}).GetCounter().GetValue(); got != 2 {
		t.Fatalf("error process count = %v, want both message identities in one result series", got)
	}
	for _, metric := range process.Metric {
		if len(metric.Label) != 3 {
			t.Fatalf("process labels = %#v, want only node_id/node_name/result", metric.Label)
		}
		for _, label := range metric.Label {
			switch label.GetName() {
			case "node_id", "node_name", "result":
			default:
				t.Fatalf("identity leaked into metric label %q", label.GetName())
			}
		}
	}
}

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
