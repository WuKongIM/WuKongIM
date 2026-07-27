package opsobserve

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
)

func TestServiceReturnsCommonEnvelopeAndDoesNotFabricateUnavailableEvidence(t *testing.T) {
	now := time.Date(2026, 7, 24, 3, 4, 5, 0, time.UTC)
	service := mustService(t, sourceStub{clusterHealthErr: errors.New("private address must not leak")}, now)

	got, err := service.ClusterHealth(context.Background(), ClusterHealthRequest{})
	if err != nil {
		t.Fatalf("ClusterHealth() error = %v", err)
	}
	if got.Schema != ObservationSchema || got.ClusterID != "cluster-a" || !got.ObservedAt.Equal(now) {
		t.Fatalf("envelope = %#v", got)
	}
	if got.Freshness != FreshnessMissing || got.Completeness != CompletenessUnavailable || got.Status != StatusUnknown {
		t.Fatalf("unavailable classifications = %#v", got)
	}
	if len(got.ReasonCodes) != 1 || got.ReasonCodes[0].Code != "cluster_health_unavailable" {
		t.Fatalf("reason codes = %#v", got.ReasonCodes)
	}
	if strings.Contains(strings.Join(got.Warnings, " "), "private address") {
		t.Fatalf("warning leaks implementation error: %#v", got.Warnings)
	}
}

func TestServiceRejectsOpenWorldAndUnboundedInputsBeforeCallingSource(t *testing.T) {
	source := &countingSource{}
	service := mustService(t, source, time.Now())
	tests := []struct {
		name string
		call func() error
	}{
		{name: "channel wildcard", call: func() error {
			_, err := service.ChannelRuntimeInspect(context.Background(), ChannelRuntimeInspectRequest{ChannelID: "", ChannelType: 1})
			return err
		}},
		{name: "promql", call: func() error {
			_, err := service.MetricsQueryRange(context.Background(), MetricsQueryRangeRequest{
				QueryID: `rate(http_requests_total[5m])`, Start: time.Now().Add(-time.Hour), End: time.Now(), StepSeconds: 60,
			})
			return err
		}},
		{name: "log source path", call: func() error {
			_, err := service.LogsSearch(context.Background(), LogsSearchRequest{NodeID: 1, Source: "/var/log/app.log"})
			return err
		}},
		{name: "pprof duration", call: func() error {
			_, err := service.PprofAnalyze(context.Background(), PprofAnalyzeRequest{NodeID: 1, Kind: "cpu", Seconds: 31})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrInvalidToolInput) {
				t.Fatalf("error = %v, want ErrInvalidToolInput", err)
			}
		})
	}
	if source.calls != 0 {
		t.Fatalf("source calls = %d, want 0", source.calls)
	}
}

func TestServiceNormalizesPprofDefaults(t *testing.T) {
	source := &countingSource{}
	service := mustService(t, source, time.Now())
	if _, err := service.PprofAnalyze(context.Background(), PprofAnalyzeRequest{NodeID: 2, Kind: "cpu"}); err != nil {
		t.Fatalf("PprofAnalyze() error = %v", err)
	}
	if source.pprof.Seconds != 10 || source.pprof.Rows != 30 {
		t.Fatalf("pprof request = %#v", source.pprof)
	}
}

func TestServiceCachesHealthForThreeSecondsAndReportsCacheHit(t *testing.T) {
	source := &countingSource{}
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	service, err := New(Config{
		ClusterID: "cluster-a", Source: source, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	hits := make([]bool, 0, 3)
	ctx := opscontract.WithCacheObserver(context.Background(), func(hit bool) { hits = append(hits, hit) })
	if _, err := service.ClusterHealth(ctx, ClusterHealthRequest{}); err != nil {
		t.Fatalf("first ClusterHealth() error = %v", err)
	}
	if _, err := service.ClusterHealth(ctx, ClusterHealthRequest{}); err != nil {
		t.Fatalf("second ClusterHealth() error = %v", err)
	}
	now = now.Add(4 * time.Second)
	if _, err := service.ClusterHealth(ctx, ClusterHealthRequest{}); err != nil {
		t.Fatalf("third ClusterHealth() error = %v", err)
	}
	if source.calls != 2 || len(hits) != 3 || hits[0] || !hits[1] || hits[2] {
		t.Fatalf("source calls=%d cache hits=%v", source.calls, hits)
	}
}

func mustService(t *testing.T, source Source, now time.Time) *Service {
	t.Helper()
	service, err := New(Config{ClusterID: "cluster-a", Source: source, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

type sourceStub struct {
	clusterHealthErr error
}

func (s sourceStub) ClusterHealth(context.Context, ClusterHealthRequest) (SourceResult, error) {
	return SourceResult{}, s.clusterHealthErr
}
func (sourceStub) NodeInspect(context.Context, NodeInspectRequest) (SourceResult, error) {
	return SourceResult{}, nil
}
func (sourceStub) SlotInspect(context.Context, SlotInspectRequest) (SourceResult, error) {
	return SourceResult{}, nil
}
func (sourceStub) ChannelRuntimeInspect(context.Context, ChannelRuntimeInspectRequest) (SourceResult, error) {
	return SourceResult{}, nil
}
func (sourceStub) ControllerTasksQuery(context.Context, ControllerTasksQueryRequest) (SourceResult, error) {
	return SourceResult{}, nil
}
func (sourceStub) MetricsQueryRange(context.Context, MetricsQueryRangeRequest) (SourceResult, error) {
	return SourceResult{}, nil
}
func (sourceStub) LogsSearch(context.Context, LogsSearchRequest) (SourceResult, error) {
	return SourceResult{}, nil
}
func (sourceStub) LogsContext(context.Context, LogsContextRequest) (SourceResult, error) {
	return SourceResult{}, nil
}
func (sourceStub) DiagnosticsQuery(context.Context, DiagnosticsQueryRequest) (SourceResult, error) {
	return SourceResult{}, nil
}
func (sourceStub) ConfigReadRedacted(context.Context, ConfigReadRedactedRequest) (SourceResult, error) {
	return SourceResult{}, nil
}
func (sourceStub) BackupInspect(context.Context, BackupInspectRequest) (SourceResult, error) {
	return SourceResult{}, nil
}
func (sourceStub) PprofAnalyze(context.Context, PprofAnalyzeRequest) (SourceResult, error) {
	return SourceResult{}, nil
}

type countingSource struct {
	sourceStub
	calls int
	pprof PprofAnalyzeRequest
}

func (s *countingSource) ClusterHealth(context.Context, ClusterHealthRequest) (SourceResult, error) {
	s.calls++
	return SourceResult{}, nil
}
func (s *countingSource) ChannelRuntimeInspect(context.Context, ChannelRuntimeInspectRequest) (SourceResult, error) {
	s.calls++
	return SourceResult{}, nil
}
func (s *countingSource) MetricsQueryRange(context.Context, MetricsQueryRangeRequest) (SourceResult, error) {
	s.calls++
	return SourceResult{}, nil
}
func (s *countingSource) LogsSearch(context.Context, LogsSearchRequest) (SourceResult, error) {
	s.calls++
	return SourceResult{}, nil
}
func (s *countingSource) PprofAnalyze(_ context.Context, req PprofAnalyzeRequest) (SourceResult, error) {
	s.calls++
	s.pprof = req
	return SourceResult{}, nil
}
