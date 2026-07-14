// Package cloudanalysis adapts the Analysis MCP usecase to WuKongIM manager,
// Prometheus, and node-private pprof HTTP surfaces.
package cloudanalysis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

const defaultHTTPTimeout = 70 * time.Second

// ErrInvalidHTTPConfig reports an unsafe or incomplete private source configuration.
var ErrInvalidHTTPConfig = errors.New("internal/infra/cloudanalysis: invalid HTTP config")

// RunInspector proves live or released state independently of observability endpoints.
type RunInspector interface {
	// InspectRun returns current inventory proof for one exact Run Identity.
	InspectRun(context.Context, string) (analysis.RunInspection, error)
}

// StaticRunInspector provides explicit runtime-local state after an external
// provider preflight, or local-Compose state in development.
type StaticRunInspector struct {
	// Inspection is the fixed local run identity and state.
	Inspection analysis.RunInspection
}

// InspectRun returns the fixed local inspection only for its exact Run Identity.
func (s StaticRunInspector) InspectRun(_ context.Context, runID string) (analysis.RunInspection, error) {
	if strings.TrimSpace(runID) == "" || runID != s.Inspection.RunID {
		return analysis.RunInspection{}, analysis.ErrRunIdentityMismatch
	}
	if s.Inspection.State == analysis.RunStateReleased && s.Inspection.InventoryCount == 0 {
		return analysis.RunInspection{}, analysis.ErrRunContractMismatch
	}
	inspection := s.Inspection
	inspection.Warnings = append([]string(nil), inspection.Warnings...)
	return inspection, nil
}

// ManagerAuth configures one run-scoped manager capability credential.
type ManagerAuth struct {
	// BearerToken uses a pre-issued short-lived manager token when set.
	BearerToken string
	// Username is the dedicated analysis capability username when login is required.
	Username string
	// Password is the run-scoped internal capability secret.
	Password string
}

// HTTPConfig binds trusted private endpoints at gateway startup.
type HTTPConfig struct {
	// Inspector proves current live or released run inventory.
	Inspector RunInspector
	// ManagerBaseURL is the fixed private WuKongIM manager origin.
	ManagerBaseURL string
	// ManagerAuth is the dedicated run-scoped manager capability credential.
	ManagerAuth ManagerAuth
	// PrometheusBaseURL is the fixed simulator-local Prometheus origin.
	PrometheusBaseURL string
	// NodeAPIBaseURLs maps allowlisted node IDs to private WuKongIM API origins.
	NodeAPIBaseURLs map[uint64]string
	// WorkloadReportDir is the fixed effective wkbench report directory.
	WorkloadReportDir string
	// HTTPClient is an optional bounded client used for all private sources.
	HTTPClient *http.Client
	// Now supplies deterministic capture timestamps.
	Now func() time.Time
	// MaxStoredProfiles bounds in-memory raw profile retention.
	MaxStoredProfiles int
	// MaxStoredProfileBytes bounds total in-memory raw profile bytes.
	MaxStoredProfileBytes int64
}

// HTTPSources implements the complete cloudanalysis.Sources port over private HTTP.
type HTTPSources struct {
	inspector  RunInspector
	manager    *managerClient
	prometheus *prometheusClient
	profiles   *profileStore
	workload   *workloadSummarySource
}

// NewHTTPSources validates fixed private endpoints and creates bounded adapters.
func NewHTTPSources(cfg HTTPConfig) (*HTTPSources, error) {
	if cfg.Inspector == nil || len(cfg.NodeAPIBaseURLs) == 0 {
		return nil, ErrInvalidHTTPConfig
	}
	workload := newWorkloadSummarySource(cfg.WorkloadReportDir)
	managerURL, err := parseBaseURL("manager", cfg.ManagerBaseURL)
	if err != nil {
		return nil, err
	}
	prometheusURL, err := parseBaseURL("prometheus", cfg.PrometheusBaseURL)
	if err != nil {
		return nil, err
	}
	nodeURLs := make(map[uint64]*url.URL, len(cfg.NodeAPIBaseURLs))
	for nodeID, raw := range cfg.NodeAPIBaseURLs {
		if nodeID == 0 {
			return nil, fmt.Errorf("%w: node id", ErrInvalidHTTPConfig)
		}
		parsed, parseErr := parseBaseURL(fmt.Sprintf("node %d", nodeID), raw)
		if parseErr != nil {
			return nil, parseErr
		}
		nodeURLs[nodeID] = parsed
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &HTTPSources{
		inspector:  cfg.Inspector,
		manager:    newManagerClient(managerURL, cfg.ManagerAuth, client, now),
		prometheus: &prometheusClient{baseURL: prometheusURL, client: client},
		profiles: newProfileStore(profileStoreConfig{
			nodeURLs: nodeURLs, client: client, now: now,
			maxProfiles: cfg.MaxStoredProfiles, maxBytes: cfg.MaxStoredProfileBytes,
		}),
		workload: workload,
	}, nil
}

// WorkloadInspect returns the simulator-local bounded wkbench final summary.
func (s *HTTPSources) WorkloadInspect(ctx context.Context, runID string) (analysis.SourceResult, error) {
	return s.workload.inspect(ctx, runID)
}

// InspectRun proves current state using the independently configured run authority.
func (s *HTTPSources) InspectRun(ctx context.Context, runID string) (analysis.RunInspection, error) {
	return s.inspector.InspectRun(ctx, runID)
}

// ClusterSnapshot combines manager node inventory and bounded workqueue status.
func (s *HTTPSources) ClusterSnapshot(ctx context.Context) (analysis.SourceResult, error) {
	return s.manager.clusterSnapshot(ctx)
}

// MetricsQueryRange executes one pre-resolved allowlisted PromQL query.
func (s *HTTPSources) MetricsQueryRange(ctx context.Context, req analysis.MetricsQueryRangeRequest, query string) (analysis.SourceResult, error) {
	return s.prometheus.queryRange(ctx, req, query)
}

// LogsSearch returns bounded ordinary application log entries.
func (s *HTTPSources) LogsSearch(ctx context.Context, req analysis.LogsSearchRequest) (analysis.SourceResult, error) {
	return s.manager.logsSearch(ctx, req)
}

// LogsContext returns one bounded manager cursor page.
func (s *HTTPSources) LogsContext(ctx context.Context, req analysis.LogsContextRequest) (analysis.SourceResult, error) {
	return s.manager.logsContext(ctx, req)
}

// DiagnosticsQuery returns bounded retained diagnostic events.
func (s *HTTPSources) DiagnosticsQuery(ctx context.Context, req analysis.DiagnosticsQueryRequest) (analysis.SourceResult, error) {
	return s.manager.diagnosticsQuery(ctx, req)
}

// TaskAuditsQuery returns bounded retained Controller task histories.
func (s *HTTPSources) TaskAuditsQuery(ctx context.Context, req analysis.TaskAuditsQueryRequest) (analysis.SourceResult, error) {
	return s.manager.taskAuditsQuery(ctx, req)
}

// TraceStart installs one expiring diagnostics tracking rule.
func (s *HTTPSources) TraceStart(ctx context.Context, req analysis.TraceStartRequest) (analysis.SourceResult, error) {
	return s.manager.traceStart(ctx, req)
}

// TraceQuery returns retained events for one exact diagnostics trace.
func (s *HTTPSources) TraceQuery(ctx context.Context, req analysis.TraceQueryRequest) (analysis.SourceResult, error) {
	return s.manager.traceQuery(ctx, req)
}

// ProfileCapture captures and retains one bounded private-node profile in memory.
func (s *HTTPSources) ProfileCapture(ctx context.Context, req analysis.ProfileCaptureRequest) (analysis.SourceResult, error) {
	return s.profiles.capture(ctx, req)
}

// ProfileTop returns a bounded symbolic profile summary without raw bytes.
func (s *HTTPSources) ProfileTop(ctx context.Context, req analysis.ProfileTopRequest) (analysis.SourceResult, error) {
	return s.profiles.top(req)
}

// ProfileList returns bounded profile metadata without raw bytes.
func (s *HTTPSources) ProfileList(_ context.Context, req analysis.ProfileListRequest) (analysis.SourceResult, error) {
	return s.profiles.list(req), nil
}

// ConfigReadRedacted returns manager-owned allowlisted effective config.
func (s *HTTPSources) ConfigReadRedacted(ctx context.Context, req analysis.ConfigReadRequest) (analysis.SourceResult, error) {
	return s.manager.configReadRedacted(ctx, req)
}

func parseBaseURL(label, raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: %s base URL", ErrInvalidHTTPConfig, label)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}
