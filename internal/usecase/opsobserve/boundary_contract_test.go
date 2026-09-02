package opsobserve

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceNormalizesBoundedObservationRequests(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	source := &recordingObservationSource{}
	service := mustService(t, source, now)
	ctx := context.Background()

	if _, err := service.NodeInspect(ctx, NodeInspectRequest{NodeID: 7}); err != nil {
		t.Fatalf("NodeInspect(): %v", err)
	}
	if _, err := service.SlotInspect(ctx, SlotInspectRequest{SlotID: 9}); err != nil {
		t.Fatalf("SlotInspect(): %v", err)
	}
	if _, err := service.ControllerTasksQuery(ctx, ControllerTasksQueryRequest{Kind: " migrate ", Status: " running "}); err != nil {
		t.Fatalf("ControllerTasksQuery(): %v", err)
	}
	start := now.Add(-time.Hour)
	metric, err := service.MetricsQueryRange(ctx, MetricsQueryRangeRequest{
		QueryID: MetricQueryTargetsUp, Start: start, End: now, StepSeconds: 60,
	})
	if err != nil {
		t.Fatalf("MetricsQueryRange(): %v", err)
	}
	if metric.Window == nil || !metric.Window.Start.Equal(start) || !metric.Window.End.Equal(now) {
		t.Fatalf("metric window = %+v, want request window", metric.Window)
	}
	if _, err := service.LogsSearch(ctx, LogsSearchRequest{NodeID: 7, Source: " app ", Levels: []string{" INFO ", "warn"}}); err != nil {
		t.Fatalf("LogsSearch(): %v", err)
	}
	if _, err := service.LogsContext(ctx, LogsContextRequest{NodeID: 7, Source: " error ", Cursor: " opaque ", Before: 1}); err != nil {
		t.Fatalf("LogsContext(): %v", err)
	}
	if _, err := service.DiagnosticsQuery(ctx, DiagnosticsQueryRequest{TraceID: " trace-1 ", Stage: " apply ", Result: " ok "}); err != nil {
		t.Fatalf("DiagnosticsQuery(): %v", err)
	}
	if _, err := service.ConfigReadRedacted(ctx, ConfigReadRedactedRequest{NodeID: 7}); err != nil {
		t.Fatalf("ConfigReadRedacted(): %v", err)
	}
	if _, err := service.BackupInspect(ctx, BackupInspectRequest{ArchiveID: " archive-1 "}); err != nil {
		t.Fatalf("BackupInspect(): %v", err)
	}

	if source.node.NodeID != 7 || source.slot.SlotID != 9 || source.config.NodeID != 7 {
		t.Fatalf("point requests = node=%+v slot=%+v config=%+v", source.node, source.slot, source.config)
	}
	if source.tasks.Kind != "migrate" || source.tasks.Status != "running" || source.tasks.Limit != 100 {
		t.Fatalf("controller request = %+v", source.tasks)
	}
	if source.logs.Source != "app" || source.logs.Limit != 100 {
		t.Fatalf("logs request = %+v", source.logs)
	}
	if source.logContext.Source != "error" || source.logContext.Cursor != "opaque" {
		t.Fatalf("log context request = %+v", source.logContext)
	}
	if source.diagnostics.TraceID != "trace-1" || source.diagnostics.Stage != "apply" || source.diagnostics.Result != "ok" || source.diagnostics.Limit != 100 {
		t.Fatalf("diagnostics request = %+v", source.diagnostics)
	}
	if source.backup.ArchiveID != "archive-1" || source.backup.Limit != 50 {
		t.Fatalf("backup request = %+v", source.backup)
	}
}

func TestServiceRejectsMalformedClosedWorldSelectors(t *testing.T) {
	t.Parallel()

	service := mustService(t, sourceStub{}, time.Now())
	now := time.Now()
	tests := []struct {
		name string
		call func() error
	}{
		{name: "missing node", call: func() error { _, err := service.NodeInspect(context.Background(), NodeInspectRequest{}); return err }},
		{name: "missing slot", call: func() error { _, err := service.SlotInspect(context.Background(), SlotInspectRequest{}); return err }},
		{name: "task selector", call: func() error {
			_, err := service.ControllerTasksQuery(context.Background(), ControllerTasksQueryRequest{Kind: "kind*"})
			return err
		}},
		{name: "task limit", call: func() error {
			_, err := service.ControllerTasksQuery(context.Background(), ControllerTasksQueryRequest{Limit: maxControllerTasks + 1})
			return err
		}},
		{name: "metric points", call: func() error {
			_, err := service.MetricsQueryRange(context.Background(), MetricsQueryRangeRequest{
				QueryID: MetricQueryTargetsUp, Start: now.Add(-time.Hour), End: now, StepSeconds: 1,
			})
			return err
		}},
		{name: "log level", call: func() error {
			_, err := service.LogsSearch(context.Background(), LogsSearchRequest{NodeID: 1, Source: "app", Levels: []string{"trace"}})
			return err
		}},
		{name: "empty log context", call: func() error {
			_, err := service.LogsContext(context.Background(), LogsContextRequest{NodeID: 1, Source: "app", Cursor: "cursor"})
			return err
		}},
		{name: "diagnostic half window", call: func() error {
			_, err := service.DiagnosticsQuery(context.Background(), DiagnosticsQueryRequest{Start: now})
			return err
		}},
		{name: "missing config node", call: func() error {
			_, err := service.ConfigReadRedacted(context.Background(), ConfigReadRedactedRequest{})
			return err
		}},
		{name: "backup selector", call: func() error {
			_, err := service.BackupInspect(context.Background(), BackupInspectRequest{ArchiveID: "archive id"})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.call(); !errors.Is(err, ErrInvalidToolInput) {
				t.Fatalf("error = %v, want ErrInvalidToolInput", err)
			}
		})
	}
}

func TestServiceEnforcesConfigurationAndResponseBounds(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{ClusterID: " ", Source: sourceStub{}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(empty cluster) error = %v, want ErrInvalidConfig", err)
	}
	if _, err := New(Config{ClusterID: "cluster-a", Source: sourceStub{}, Timeout: time.Second - 1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(short timeout) error = %v, want ErrInvalidConfig", err)
	}
	service := mustService(t, sourceStub{}, time.Now())
	if _, err := service.finish(SourceResult{Data: strings.Repeat("x", MaxResponseBytes)}); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("finish(oversized) error = %v, want ErrResponseTooLarge", err)
	}
	ids := MetricQueryIDs()
	if len(ids) != len(metricQueryIDs) || ids[0] != MetricQueryTargetsUp || ids[len(ids)-1] != MetricQuerySlotApplyGap {
		t.Fatalf("MetricQueryIDs() = %#v", ids)
	}
}

type recordingObservationSource struct {
	sourceStub
	node        NodeInspectRequest
	slot        SlotInspectRequest
	tasks       ControllerTasksQueryRequest
	logs        LogsSearchRequest
	logContext  LogsContextRequest
	diagnostics DiagnosticsQueryRequest
	config      ConfigReadRedactedRequest
	backup      BackupInspectRequest
}

func (s *recordingObservationSource) NodeInspect(_ context.Context, req NodeInspectRequest) (SourceResult, error) {
	s.node = req
	return SourceResult{}, nil
}

func (s *recordingObservationSource) SlotInspect(_ context.Context, req SlotInspectRequest) (SourceResult, error) {
	s.slot = req
	return SourceResult{}, nil
}

func (s *recordingObservationSource) ControllerTasksQuery(_ context.Context, req ControllerTasksQueryRequest) (SourceResult, error) {
	s.tasks = req
	return SourceResult{}, nil
}

func (*recordingObservationSource) MetricsQueryRange(_ context.Context, _ MetricsQueryRangeRequest) (SourceResult, error) {
	return SourceResult{}, nil
}

func (s *recordingObservationSource) LogsSearch(_ context.Context, req LogsSearchRequest) (SourceResult, error) {
	s.logs = req
	return SourceResult{}, nil
}

func (s *recordingObservationSource) LogsContext(_ context.Context, req LogsContextRequest) (SourceResult, error) {
	s.logContext = req
	return SourceResult{}, nil
}

func (s *recordingObservationSource) DiagnosticsQuery(_ context.Context, req DiagnosticsQueryRequest) (SourceResult, error) {
	s.diagnostics = req
	return SourceResult{}, nil
}

func (s *recordingObservationSource) ConfigReadRedacted(_ context.Context, req ConfigReadRedactedRequest) (SourceResult, error) {
	s.config = req
	return SourceResult{}, nil
}

func (s *recordingObservationSource) BackupInspect(_ context.Context, req BackupInspectRequest) (SourceResult, error) {
	s.backup = req
	return SourceResult{}, nil
}
