//go:build integration

package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/metrics"
)

// The real warmup boundary must retain why a retryable SENDACK was emitted;
// channel/storage timing alone cannot distinguish unavailable authority from busy admission.
func TestMixedSendBoundaryRetainsPermissionFailureEvidence(t *testing.T) {
	apps := []*App{{metrics: metrics.New(1, "one")}, {metrics: metrics.New(2, "two")}}
	for index, app := range apps {
		app.metrics.Message.ObserveSendPermissionStage("admission", "busy", time.Millisecond)
		app.metrics.Message.ObserveSendPermissionStage("rpc", "unavailable", 2*time.Millisecond)
		app.metrics.Message.ObserveSendPermissionStage("barrier", "stale_route", 3*time.Millisecond)
		app.metrics.Message.ObserveSendPermissionCount("cohort_busy", index+1)
		app.metrics.Message.ObserveSendPermissionCohortOwned("calls", index+1)
		app.metrics.Message.ObserveSendPermissionInflight(index + 1)
		app.metrics.Gateway.Sendack("node_not_match", "single_error", "route_not_ready")
		app.metrics.Slot.ObserveReadBarrier("not_leader", 4*time.Millisecond)
	}
	before := mixedSendDiagnosticSnapshot(t, apps)
	encoded, err := json.Marshal(before)
	if err != nil || len(encoded) > 4<<20 {
		t.Fatalf("bounded boundary serialization: size=%d error=%v", len(encoded), err)
	}
	counts := make(map[string]int)
	for _, family := range before.Families {
		counts[family.GetName()]++
	}
	for _, family := range []string{
		"wukongim_message_permission_duration_seconds",
		"wukongim_message_permission_counts_total",
		"wukongim_message_permission_cohort_owned",
		"wukongim_message_permission_inflight",
		"wukongim_gateway_sendacks_total",
		"wukongim_slot_read_barrier_duration_seconds",
	} {
		if counts[family] != len(apps) {
			t.Errorf("boundary family %s has %d node projections, want %d", family, counts[family], len(apps))
		}
	}

	for index, app := range apps {
		app.metrics.Message.ObserveSendPermissionStage("admission", "busy", time.Second)
		app.metrics.Message.ObserveSendPermissionCount("cohort_busy", 1)
		app.metrics.Message.ObserveSendPermissionCohortOwned("calls", -(index + 1))
		app.metrics.Message.ObserveSendPermissionInflight(-(index + 1))
	}
	after := mixedSendDiagnosticSnapshot(t, apps)
	unchanged, err := json.Marshal(before)
	if err != nil || string(unchanged) != string(encoded) {
		t.Fatal("later live mutations changed the retained boundary")
	}
	for _, snapshot := range []mixedSendSnapshot{before, after} {
		stages := 0
		for _, family := range snapshot.Families {
			if family.GetName() != "wukongim_message_permission_duration_seconds" {
				continue
			}
			for _, metric := range family.Metric {
				stage := threeNodeMixedMetricLabel(metric, "stage")
				result := threeNodeMixedMetricLabel(metric, "result")
				if stage == "admission" && result == "busy" || stage == "rpc" && result == "unavailable" || stage == "barrier" && result == "stale_route" {
					if metric.Histogram == nil || metric.Histogram.GetSampleCount() == 0 {
						t.Fatal("permission classification lost its counter")
					}
					stages++
				}
			}
		}
		if stages != 3*len(apps) {
			t.Errorf("retained stage/result projections = %d, want %d", stages, 3*len(apps))
		}
	}
}
