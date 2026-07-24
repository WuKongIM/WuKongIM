package opsobserve

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
)

const (
	defaultTimeout     = 10 * time.Second
	maxSelectorLength  = 256
	maxLogCursorLength = 2048
	maxControllerTasks = 200
	maxBackupItems     = 100
)

var selectorPattern = regexp.MustCompile(`^[A-Za-z0-9._:@/-]+$`)

// Config configures one cluster-scoped observation service.
type Config struct {
	// ClusterID is the stable cluster identity returned in every observation.
	ClusterID string
	// Source implements the narrow read ports.
	Source Source
	// Timeout bounds ordinary source calls.
	Timeout time.Duration
	// Now provides deterministic timestamps for tests.
	Now func() time.Time
}

// Service validates requests and produces bounded common envelopes.
type Service struct {
	clusterID string
	source    Source
	timeout   time.Duration
	now       func() time.Time

	cacheMu sync.Mutex
	cache   map[string]cacheEntry
	calls   map[string]*cacheCall
}

// New constructs a cluster-scoped observation service.
func New(cfg Config) (*Service, error) {
	if strings.TrimSpace(cfg.ClusterID) == "" || cfg.Source == nil {
		return nil, ErrInvalidConfig
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if timeout < time.Second || timeout > time.Minute {
		return nil, ErrInvalidConfig
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		clusterID: strings.TrimSpace(cfg.ClusterID), source: cfg.Source, timeout: timeout, now: now,
		cache: make(map[string]cacheEntry), calls: make(map[string]*cacheCall),
	}, nil
}

// ClusterHealth returns aggregate health without accepting scan selectors.
func (s *Service) ClusterHealth(ctx context.Context, req ClusterHealthRequest) (Observation, error) {
	return s.readCached(ctx, "cluster_health", "cluster_health", 3*time.Second, func(callCtx context.Context) (SourceResult, error) {
		return s.source.ClusterHealth(callCtx, req)
	})
}

// NodeInspect returns one exact node observation.
func (s *Service) NodeInspect(ctx context.Context, req NodeInspectRequest) (Observation, error) {
	if req.NodeID == 0 {
		return Observation{}, ErrInvalidToolInput
	}
	return s.readCached(ctx, "node_inspect", "node:"+strconv.FormatUint(req.NodeID, 10), 3*time.Second, func(callCtx context.Context) (SourceResult, error) {
		return s.source.NodeInspect(callCtx, req)
	})
}

// SlotInspect returns one exact physical Slot observation.
func (s *Service) SlotInspect(ctx context.Context, req SlotInspectRequest) (Observation, error) {
	if req.SlotID == 0 {
		return Observation{}, ErrInvalidToolInput
	}
	return s.readCached(ctx, "slot_inspect", "slot:"+strconv.FormatUint(uint64(req.SlotID), 10), 3*time.Second, func(callCtx context.Context) (SourceResult, error) {
		return s.source.SlotInspect(callCtx, req)
	})
}

// ChannelRuntimeInspect performs one exact point lookup.
func (s *Service) ChannelRuntimeInspect(ctx context.Context, req ChannelRuntimeInspectRequest) (Observation, error) {
	req.ChannelID = strings.TrimSpace(req.ChannelID)
	if req.ChannelID == "" || len(req.ChannelID) > maxSelectorLength || req.ChannelType == 0 {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, "channel_runtime_inspect", func(callCtx context.Context) (SourceResult, error) {
		return s.source.ChannelRuntimeInspect(callCtx, req)
	})
}

// ControllerTasksQuery returns bounded task evidence.
func (s *Service) ControllerTasksQuery(ctx context.Context, req ControllerTasksQueryRequest) (Observation, error) {
	req.Kind = strings.TrimSpace(req.Kind)
	req.Status = strings.TrimSpace(req.Status)
	if !validOptionalSelector(req.Kind) || !validOptionalSelector(req.Status) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.Limit < 1 || req.Limit > maxControllerTasks {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, "controller_tasks_query", func(callCtx context.Context) (SourceResult, error) {
		return s.source.ControllerTasksQuery(callCtx, req)
	})
}

// MetricsQueryRange executes one server-owned query ID.
func (s *Service) MetricsQueryRange(ctx context.Context, req MetricsQueryRangeRequest) (Observation, error) {
	req.QueryID = strings.TrimSpace(req.QueryID)
	if _, allowed := metricQueryIDs[req.QueryID]; !allowed || !validRequiredSelector(req.QueryID) || req.Start.IsZero() || req.End.IsZero() || !req.End.After(req.Start) ||
		req.End.Sub(req.Start) > MaxMetricRange || req.StepSeconds < 1 ||
		int(req.End.Sub(req.Start)/(time.Duration(req.StepSeconds)*time.Second))+1 > MaxMetricPointsPerSeries {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, "metrics_query_range", func(callCtx context.Context) (SourceResult, error) {
		result, err := s.source.MetricsQueryRange(callCtx, req)
		if result.Window == nil {
			result.Window = &TimeWindow{Start: req.Start, End: req.End}
		}
		return result, err
	})
}

// LogsSearch returns bounded raw ordinary application logs.
func (s *Service) LogsSearch(ctx context.Context, req LogsSearchRequest) (Observation, error) {
	req.Source = strings.TrimSpace(req.Source)
	if req.NodeID == 0 || !validLogSource(req.Source) || len(req.Keyword) > maxSelectorLength || !validLogLevels(req.Levels) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.Limit < 1 || req.Limit > MaxLogLines {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, "logs_search", func(callCtx context.Context) (SourceResult, error) {
		return s.source.LogsSearch(callCtx, req)
	})
}

// LogsContext returns bounded raw log context around an opaque cursor.
func (s *Service) LogsContext(ctx context.Context, req LogsContextRequest) (Observation, error) {
	req.Source = strings.TrimSpace(req.Source)
	req.Cursor = strings.TrimSpace(req.Cursor)
	if req.NodeID == 0 || !validLogSource(req.Source) || req.Cursor == "" || len(req.Cursor) > maxLogCursorLength ||
		req.Before < 0 || req.After < 0 || req.Before+req.After < 1 || req.Before+req.After > MaxLogLines {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, "logs_context", func(callCtx context.Context) (SourceResult, error) {
		return s.source.LogsContext(callCtx, req)
	})
}

// DiagnosticsQuery returns bounded retained diagnostic events.
func (s *Service) DiagnosticsQuery(ctx context.Context, req DiagnosticsQueryRequest) (Observation, error) {
	req.TraceID = strings.TrimSpace(req.TraceID)
	req.Stage = strings.TrimSpace(req.Stage)
	req.Result = strings.TrimSpace(req.Result)
	if !validOptionalSelector(req.TraceID) || !validOptionalSelector(req.Stage) || !validOptionalSelector(req.Result) ||
		(req.Start.IsZero() != req.End.IsZero()) || (!req.Start.IsZero() && !req.End.After(req.Start)) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.Limit < 1 || req.Limit > MaxDiagnosticsEvents {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, "diagnostics_query", func(callCtx context.Context) (SourceResult, error) {
		return s.source.DiagnosticsQuery(callCtx, req)
	})
}

// ConfigReadRedacted returns one node's allowlisted redacted configuration.
func (s *Service) ConfigReadRedacted(ctx context.Context, req ConfigReadRedactedRequest) (Observation, error) {
	if req.NodeID == 0 {
		return Observation{}, ErrInvalidToolInput
	}
	return s.readCached(ctx, "config_read_redacted", "config:"+strconv.FormatUint(req.NodeID, 10), 30*time.Second, func(callCtx context.Context) (SourceResult, error) {
		return s.source.ConfigReadRedacted(callCtx, req)
	})
}

// BackupInspect returns bounded backup evidence.
func (s *Service) BackupInspect(ctx context.Context, req BackupInspectRequest) (Observation, error) {
	req.JobID = strings.TrimSpace(req.JobID)
	if !validOptionalSelector(req.JobID) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Limit == 0 {
		req.Limit = 50
	}
	if req.Limit < 1 || req.Limit > maxBackupItems {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, "backup_inspect", func(callCtx context.Context) (SourceResult, error) {
		return s.source.BackupInspect(callCtx, req)
	})
}

// PprofAnalyze captures and parses one bounded runtime profile.
func (s *Service) PprofAnalyze(ctx context.Context, req PprofAnalyzeRequest) (Observation, error) {
	req.Kind = strings.TrimSpace(req.Kind)
	req.SampleType = strings.TrimSpace(req.SampleType)
	if req.NodeID == 0 || !validPprofKind(req.Kind) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Kind == "cpu" && req.Seconds == 0 {
		req.Seconds = 10
	}
	if req.Rows == 0 {
		req.Rows = 30
	}
	if (req.Kind == "cpu" && (req.Seconds < 1 || req.Seconds > MaxPprofSeconds)) ||
		(req.Kind != "cpu" && req.Seconds != 0) || req.Rows < 1 || req.Rows > MaxPprofRows ||
		(req.Kind == "heap" && req.SampleType != "" && req.SampleType != "inuse_space" && req.SampleType != "alloc_space") ||
		(req.Kind != "heap" && req.SampleType != "") {
		return Observation{}, ErrInvalidToolInput
	}
	timeout := s.timeout
	if req.Kind == "cpu" {
		timeout += time.Duration(req.Seconds) * time.Second
	}
	return s.readWithTimeout(ctx, "pprof_analyze", timeout, func(callCtx context.Context) (SourceResult, error) {
		return s.source.PprofAnalyze(callCtx, req)
	})
}

func (s *Service) read(ctx context.Context, sourceName string, read func(context.Context) (SourceResult, error)) (Observation, error) {
	return s.readWithTimeout(ctx, sourceName, s.timeout, read)
}

type cacheEntry struct {
	result    SourceResult
	expiresAt time.Time
}

type cacheCall struct {
	done   chan struct{}
	result SourceResult
	err    error
}

func (s *Service) readCached(ctx context.Context, sourceName, key string, ttl time.Duration, read func(context.Context) (SourceResult, error)) (Observation, error) {
	now := s.now().UTC()
	s.cacheMu.Lock()
	if entry, ok := s.cache[key]; ok && entry.expiresAt.After(now) {
		s.cacheMu.Unlock()
		opscontract.ReportCacheHit(ctx, true)
		return s.finish(entry.result)
	}
	if call, ok := s.calls[key]; ok {
		s.cacheMu.Unlock()
		select {
		case <-ctx.Done():
			return Observation{}, ctx.Err()
		case <-call.done:
			opscontract.ReportCacheHit(ctx, true)
			if call.err != nil {
				return s.readUnavailable(sourceName)
			}
			return s.finish(call.result)
		}
	}
	call := &cacheCall{done: make(chan struct{})}
	s.calls[key] = call
	s.cacheMu.Unlock()
	opscontract.ReportCacheHit(ctx, false)

	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	result, err := read(callCtx)
	cancel()
	s.cacheMu.Lock()
	call.result, call.err = result, err
	if err == nil {
		s.cache[key] = cacheEntry{result: result, expiresAt: s.now().UTC().Add(ttl)}
	}
	delete(s.calls, key)
	close(call.done)
	s.cacheMu.Unlock()
	if err != nil {
		return s.readUnavailable(sourceName)
	}
	return s.finish(result)
}

func (s *Service) readWithTimeout(ctx context.Context, sourceName string, timeout time.Duration, read func(context.Context) (SourceResult, error)) (Observation, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := read(callCtx)
	if err != nil {
		return s.readUnavailable(sourceName)
	}
	return s.finish(result)
}

func (s *Service) readUnavailable(sourceName string) (Observation, error) {
	return s.finish(SourceResult{
		Freshness: FreshnessMissing, Completeness: CompletenessUnavailable, Status: StatusUnknown,
		ReasonCodes: []ReasonCode{{Code: sourceName + "_unavailable"}},
		Warnings:    []string{"requested evidence is currently unavailable"},
		Data:        map[string]any{},
	})
}

func (s *Service) finish(result SourceResult) (Observation, error) {
	if result.Freshness == "" {
		result.Freshness = FreshnessFresh
	}
	if result.Completeness == "" {
		result.Completeness = CompletenessComplete
	}
	if result.Status == "" {
		result.Status = StatusUnknown
	}
	if result.ReasonCodes == nil {
		result.ReasonCodes = []ReasonCode{}
	}
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	if result.Data == nil {
		result.Data = map[string]any{}
	}
	observation := Observation{
		Schema: ObservationSchema, ClusterID: s.clusterID, ObservedAt: s.now().UTC(),
		Window: result.Window, Freshness: result.Freshness, Completeness: result.Completeness,
		Status: result.Status, ReasonCodes: result.ReasonCodes, Warnings: result.Warnings, Data: result.Data,
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return Observation{}, err
	}
	if len(encoded) > MaxResponseBytes {
		return Observation{}, ErrResponseTooLarge
	}
	return observation, nil
}

func validLogSource(source string) bool {
	return source == "app" || source == "error"
}

func validLogLevels(levels []string) bool {
	for _, level := range levels {
		switch strings.ToLower(strings.TrimSpace(level)) {
		case "debug", "info", "warn", "error", "dpanic", "panic", "fatal":
		default:
			return false
		}
	}
	return true
}

func validPprofKind(kind string) bool {
	return kind == "cpu" || kind == "heap" || kind == "goroutine"
}

func validOptionalSelector(value string) bool {
	return value == "" || validRequiredSelector(value)
}

func validRequiredSelector(value string) bool {
	return value != "" && len(value) <= maxSelectorLength && selectorPattern.MatchString(value)
}
