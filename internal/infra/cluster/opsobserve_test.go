package cluster

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	observe "github.com/WuKongIM/WuKongIM/internal/usecase/opsobserve"
)

func TestOpsObservationSafeProjectionsOmitInternalErrorsAndTokens(t *testing.T) {
	task := safeControllerTasks([]management.ControllerTask{{
		TaskID: "task-1", LastError: "dial 10.0.0.4:7000: secret failure",
		Participants: []management.ControllerTaskParticipant{{NodeID: 1, LastError: "stack trace"}},
	}})
	slot := safeSlot(management.Slot{SlotID: 1, Task: &management.SlotTask{
		TaskID: "slot-task", LastError: "raw raft failure",
		Participants: []management.SlotTaskParticipant{{NodeID: 1, LastError: "raw participant failure"}},
	}})
	channel := safeChannelRuntime(management.ChannelRuntimeMeta{
		ChannelID: "channel-a", ChannelType: 1, WriteFenceToken: "secret-fence-token",
	})
	diagnostics := safeDiagnostics(management.DiagnosticsQueryResponse{
		Notes:  []string{"dial 10.0.0.5:7000"},
		Nodes:  []management.DiagnosticsNodeResult{{NodeID: 1, Notes: []string{"internal address"}}},
		Events: []management.DiagnosticsEvent{{NodeID: 1, Error: "implementation stack"}},
	})

	payload, err := json.Marshal([]any{task, slot, channel, diagnostics})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, forbidden := range []string{
		"10.0.0.4", "secret failure", "stack trace", "raw raft failure",
		"raw participant failure", "secret-fence-token", "10.0.0.5", "implementation stack",
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("safe projection leaked %q: %s", forbidden, payload)
		}
	}
}

func TestOpsObservationSafeProjectionsUseStableSnakeCaseJSON(t *testing.T) {
	payload, err := json.Marshal([]any{
		safeNodeHealth(management.Node{
			NodeID: 1,
			Membership: management.NodeMembership{
				Role: "data", JoinState: "active", Schedulable: true,
			},
			Health: management.NodeHealth{RuntimeReady: true, ObservedControlRevision: 7},
		}),
		safeSlot(management.Slot{
			SlotID: 3,
			Assignment: management.SlotAssignment{
				DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 2,
			},
			Runtime: management.SlotRuntime{LeaderID: 2, HasQuorum: true},
		}),
		safeNodeDiagnosticSummary(management.DynamicNodeDiagnosticSummary{SafeToRemove: true}),
		safeNodeDiagnosticSlots([]management.DynamicNodeDiagnosticSlot{{
			SlotID: 3, CurrentLeader: 2,
		}}),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	encoded := string(payload)
	for _, expected := range []string{
		`"join_state":"active"`, `"runtime_ready":true`, `"observed_control_revision":7`,
		`"desired_peers":[1,2,3]`, `"preferred_leader":2`, `"has_quorum":true`,
		`"safe_to_remove":true`, `"current_leader":2`,
	} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("safe projection missing %q: %s", expected, encoded)
		}
	}
	for _, forbidden := range []string{
		`"JoinState"`, `"RuntimeReady"`, `"DesiredPeers"`, `"SafeToRemove"`,
	} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("safe projection leaked unstable field %q: %s", forbidden, encoded)
		}
	}
}

func TestOpsObservationConfigRedactsAddressLikeValues(t *testing.T) {
	result := safeConfig(management.NodeConfigSnapshot{
		GeneratedAt: time.Unix(1, 0).UTC(), NodeID: 1,
		Groups: []management.NodeConfigGroup{{ID: "cluster", Items: []management.NodeConfigItem{
			{Key: "WK_CLUSTER_LISTEN_ADDR", Value: "10.0.0.1:7000"},
			{Key: "WK_CLUSTER_HASH_SLOT_COUNT", Value: "256"},
		}}},
	})
	if got := result.Groups[0].Items[0]; got.Value != "******" || !got.Redacted {
		t.Fatalf("address item = %#v", got)
	}
	if got := result.Groups[0].Items[1]; got.Value != "256" || got.Redacted {
		t.Fatalf("ordinary item = %#v", got)
	}
}

func TestOpsObservationClusterHealthAcceptsReadyMatchedSlotAndChecksMetrics(t *testing.T) {
	source := NewOpsObservationSource(OpsObservationSourceConfig{
		Inventory: healthyOpsInventory{},
		Metrics: opsMetricsStub{responses: map[string]observe.MetricRangeData{
			observe.MetricQueryTargetsUp: {
				Series: []observe.MetricSeries{
					{Values: metricValues("1")},
					{Values: metricValues("0")},
				},
			},
			observe.MetricQueryRuntimeQueuePressure: {
				Series: []observe.MetricSeries{{Values: metricValues("0.90")}},
			},
		}},
	})
	result, err := source.ClusterHealth(context.Background(), observe.ClusterHealthRequest{})
	if err != nil {
		t.Fatalf("ClusterHealth() error = %v", err)
	}
	if result.Status != observe.StatusDegraded || result.Completeness != observe.CompletenessComplete {
		t.Fatalf("result = %#v", result)
	}
	codes := make(map[string]bool, len(result.ReasonCodes))
	for _, reason := range result.ReasonCodes {
		codes[reason.Code] = true
	}
	if !codes["metric_targets_down"] || !codes["runtime_queue_pressure_high"] ||
		codes["slot_health_degraded"] || codes["slot_health_missing"] {
		t.Fatalf("reason codes = %#v", result.ReasonCodes)
	}
}

func TestLatestMetricValueRejectsNonFiniteAndMalformedValues(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "not-a-number"} {
		if _, ok := latestMetricValue(observe.MetricSeries{Values: metricValues(value)}); ok {
			t.Fatalf("latestMetricValue(%q) accepted invalid value", value)
		}
	}
}

func TestOpsObservationBackupInspectUsesBoundedRestorePointPage(t *testing.T) {
	backup := &opsBackupStub{
		status: backupusecase.StatusSnapshot{
			Enabled: true,
			Health:  backupusecase.HealthHealthy,
		},
		page: backupusecase.RestorePointPage{
			Items: []backupusecase.RestorePoint{
				{ID: "rp-other", JobID: "job-other"},
				{ID: "rp-match", JobID: "job-match"},
			},
			NextCursor: "more",
			Total:      201,
		},
	}
	source := NewOpsObservationSource(OpsObservationSourceConfig{Backup: backup})

	result, err := source.BackupInspect(context.Background(), observe.BackupInspectRequest{
		JobID: "job-match",
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("BackupInspect() error = %v", err)
	}
	if backup.request.Limit != backupusecase.MaxRestorePointPageSize || backup.request.Cursor != "" {
		t.Fatalf("restore-point request = %#v", backup.request)
	}
	if result.Completeness != observe.CompletenessPartial || len(result.Warnings) != 1 {
		t.Fatalf("result coverage = %#v, warnings = %#v", result.Completeness, result.Warnings)
	}
	data, ok := result.Data.(backupInspectData)
	if !ok {
		t.Fatalf("result data type = %T", result.Data)
	}
	if len(data.RestorePoints) != 1 || data.RestorePoints[0].ID != "rp-match" {
		t.Fatalf("restore points = %#v", data.RestorePoints)
	}
}

type healthyOpsInventory struct{}

func (healthyOpsInventory) ListNodes(context.Context) (management.NodeList, error) {
	return management.NodeList{
		ControllerLeaderID: 1,
		Items: []management.Node{{
			NodeID: 1, Status: "alive",
			Health: management.NodeHealth{Freshness: "fresh", RuntimeReady: true},
		}},
	}, nil
}

func (healthyOpsInventory) ListSlots(context.Context, management.ListSlotsOptions) ([]management.Slot, error) {
	return []management.Slot{{
		SlotID:  1,
		State:   management.SlotState{Quorum: "ready", Sync: "matched"},
		Runtime: management.SlotRuntime{HasQuorum: true},
	}}, nil
}

func (healthyOpsInventory) DynamicNodeDiagnostics(context.Context, management.DynamicNodeDiagnosticsRequest) (management.DynamicNodeDiagnosticsResponse, error) {
	return management.DynamicNodeDiagnosticsResponse{}, nil
}

func (healthyOpsInventory) GetChannelRuntimeMeta(context.Context, string, int64) (management.ChannelRuntimeMeta, error) {
	return management.ChannelRuntimeMeta{}, nil
}

type opsMetricsStub struct {
	responses map[string]observe.MetricRangeData
}

func (s opsMetricsStub) QueryOpsMetrics(_ context.Context, request observe.MetricsQueryRangeRequest) (observe.MetricRangeData, error) {
	return s.responses[request.QueryID], nil
}

type opsBackupStub struct {
	status  backupusecase.StatusSnapshot
	page    backupusecase.RestorePointPage
	request backupusecase.RestorePointListRequest
}

func (s *opsBackupStub) Status(context.Context) (backupusecase.StatusSnapshot, error) {
	return s.status, nil
}

func (s *opsBackupStub) ListRestorePointsPage(_ context.Context, request backupusecase.RestorePointListRequest) (backupusecase.RestorePointPage, error) {
	s.request = request
	return s.page, nil
}

func metricValues(value string) [][]json.RawMessage {
	encoded, _ := json.Marshal(value)
	return [][]json.RawMessage{{json.RawMessage(`1`), encoded}}
}
