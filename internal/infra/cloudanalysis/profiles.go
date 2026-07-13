package cloudanalysis

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
	pprofprofile "github.com/google/pprof/profile"
)

const (
	defaultMaxStoredProfiles     = 32
	defaultMaxStoredProfileBytes = int64(64 << 20)
	maxSingleProfileBytes        = int64(32 << 20)
)

// ProfileMetadata is the bounded non-raw view of one in-memory capture.
type ProfileMetadata struct {
	// ProfileID is the opaque gateway-issued capture identity.
	ProfileID string `json:"profile_id"`
	// NodeID is the captured cluster node.
	NodeID uint64 `json:"node_id"`
	// Kind is cpu, heap, or goroutine.
	Kind analysis.ProfileKind `json:"kind"`
	// CapturedAt is the completion timestamp.
	CapturedAt time.Time `json:"captured_at"`
	// Window is the active capture or snapshot interval.
	Window analysis.TimeWindow `json:"window"`
	// SizeBytes is the retained raw profile size without exposing its bytes.
	SizeBytes int `json:"size_bytes"`
}

// ProfileTopRow is one bounded symbolic aggregate from a captured profile.
type ProfileTopRow struct {
	// Function is the symbolized function name.
	Function string `json:"function"`
	// Flat is the selected sample value attributed directly to the function.
	Flat int64 `json:"flat"`
	// Cumulative is the selected sample value attributed through its stack.
	Cumulative int64 `json:"cumulative"`
	// Unit is the selected pprof sample unit.
	Unit string `json:"unit"`
}

type storedProfile struct {
	metadata ProfileMetadata
	data     []byte
}

type profileStoreConfig struct {
	nodeURLs    map[uint64]*url.URL
	client      *http.Client
	now         func() time.Time
	maxProfiles int
	maxBytes    int64
}

type profileStore struct {
	mu          sync.RWMutex
	nodeURLs    map[uint64]*url.URL
	client      *http.Client
	now         func() time.Time
	maxProfiles int
	maxBytes    int64
	totalBytes  int64
	sequence    atomic.Uint64
	order       []string
	profiles    map[string]storedProfile
}

func newProfileStore(cfg profileStoreConfig) *profileStore {
	maxProfiles := cfg.maxProfiles
	if maxProfiles <= 0 {
		maxProfiles = defaultMaxStoredProfiles
	}
	maxBytes := cfg.maxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxStoredProfileBytes
	}
	return &profileStore{
		nodeURLs: cfg.nodeURLs, client: cfg.client, now: cfg.now, maxProfiles: maxProfiles, maxBytes: maxBytes,
		order: make([]string, 0, maxProfiles), profiles: make(map[string]storedProfile),
	}
}

func (s *profileStore) capture(ctx context.Context, req analysis.ProfileCaptureRequest) (analysis.SourceResult, error) {
	baseURL, ok := s.nodeURLs[req.NodeID]
	if !ok {
		return analysis.SourceResult{}, analysis.ErrInvalidToolInput
	}
	start := s.now().UTC()
	target := *baseURL
	switch req.Kind {
	case analysis.ProfileCPU:
		target.Path = strings.TrimRight(baseURL.Path, "/") + "/debug/pprof/profile"
		target.RawQuery = url.Values{"seconds": {strconv.Itoa(req.Seconds)}}.Encode()
	case analysis.ProfileHeap:
		target.Path = strings.TrimRight(baseURL.Path, "/") + "/debug/pprof/heap"
		target.RawQuery = "debug=0"
	case analysis.ProfileGoroutine:
		target.Path = strings.TrimRight(baseURL.Path, "/") + "/debug/pprof/goroutine"
		target.RawQuery = "debug=0"
	default:
		return analysis.SourceResult{}, analysis.ErrInvalidToolInput
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return analysis.SourceResult{}, err
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return analysis.SourceResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return analysis.SourceResult{}, fmt.Errorf("node %d profile: status %d: %s", req.NodeID, resp.StatusCode, boundedText(body, 512))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSingleProfileBytes+1))
	if err != nil {
		return analysis.SourceResult{}, err
	}
	if int64(len(data)) > maxSingleProfileBytes {
		return analysis.SourceResult{}, fmt.Errorf("node %d profile exceeds %d bytes", req.NodeID, maxSingleProfileBytes)
	}
	if _, err := pprofprofile.ParseData(data); err != nil {
		return analysis.SourceResult{}, fmt.Errorf("node %d invalid profile: %w", req.NodeID, err)
	}
	end := s.now().UTC()
	profileID := fmt.Sprintf("p-%d-%s-%d", req.NodeID, req.Kind, s.sequence.Add(1))
	metadata := ProfileMetadata{
		ProfileID: profileID, NodeID: req.NodeID, Kind: req.Kind, CapturedAt: end,
		Window: analysis.TimeWindow{Start: start, End: end}, SizeBytes: len(data),
	}
	if !s.store(storedProfile{metadata: metadata, data: data}) {
		return analysis.SourceResult{}, fmt.Errorf("node %d profile exceeds the configured retention budget", req.NodeID)
	}
	window := metadata.Window
	return analysis.SourceResult{
		Node: "node-" + strconv.FormatUint(req.NodeID, 10), Source: "pprof", Window: &window,
		Completeness: analysis.CompletenessComplete, Data: metadata,
	}, nil
}

func (s *profileStore) top(req analysis.ProfileTopRequest) (analysis.SourceResult, error) {
	s.mu.RLock()
	stored, ok := s.profiles[req.ProfileID]
	s.mu.RUnlock()
	if !ok {
		return analysis.SourceResult{}, fmt.Errorf("profile %s not found", req.ProfileID)
	}
	parsed, err := pprofprofile.ParseData(stored.data)
	if err != nil {
		return analysis.SourceResult{}, err
	}
	rows := summarizeProfile(parsed, req.Limit)
	window := stored.metadata.Window
	return analysis.SourceResult{
		Node: "node-" + strconv.FormatUint(stored.metadata.NodeID, 10), Source: "pprof", Window: &window,
		Completeness: analysis.CompletenessComplete, Data: rows,
	}, nil
}

func (s *profileStore) list(req analysis.ProfileListRequest) analysis.SourceResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ProfileMetadata, 0, min(req.Limit, len(s.order)))
	for index := len(s.order) - 1; index >= 0 && len(items) < req.Limit; index-- {
		stored := s.profiles[s.order[index]]
		if req.NodeID == 0 || stored.metadata.NodeID == req.NodeID {
			items = append(items, stored.metadata)
		}
	}
	node := "cluster"
	if req.NodeID != 0 {
		node = "node-" + strconv.FormatUint(req.NodeID, 10)
	}
	return analysis.SourceResult{Node: node, Source: "pprof", Completeness: analysis.CompletenessComplete, Data: items}
}

func (s *profileStore) store(profile storedProfile) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if int64(len(profile.data)) > s.maxBytes {
		return false
	}
	for len(s.order) >= s.maxProfiles || s.totalBytes+int64(len(profile.data)) > s.maxBytes && len(s.order) > 0 {
		oldestID := s.order[0]
		s.order = s.order[1:]
		oldest := s.profiles[oldestID]
		s.totalBytes -= int64(len(oldest.data))
		delete(s.profiles, oldestID)
	}
	s.profiles[profile.metadata.ProfileID] = profile
	s.order = append(s.order, profile.metadata.ProfileID)
	s.totalBytes += int64(len(profile.data))
	return true
}

func summarizeProfile(profile *pprofprofile.Profile, limit int) []ProfileTopRow {
	if profile == nil || len(profile.SampleType) == 0 {
		return []ProfileTopRow{}
	}
	valueIndex := len(profile.SampleType) - 1
	unit := profile.SampleType[valueIndex].Unit
	type aggregate struct {
		flat int64
		cum  int64
	}
	aggregates := make(map[string]aggregate)
	for _, sample := range profile.Sample {
		if valueIndex >= len(sample.Value) {
			continue
		}
		value := sample.Value[valueIndex]
		seen := make(map[string]struct{})
		for locationIndex, location := range sample.Location {
			for _, line := range location.Line {
				if line.Function == nil || line.Function.Name == "" {
					continue
				}
				name := line.Function.Name
				aggregate := aggregates[name]
				if locationIndex == 0 {
					aggregate.flat += value
				}
				if _, exists := seen[name]; !exists {
					aggregate.cum += value
					seen[name] = struct{}{}
				}
				aggregates[name] = aggregate
			}
		}
	}
	rows := make([]ProfileTopRow, 0, len(aggregates))
	for function, aggregate := range aggregates {
		rows = append(rows, ProfileTopRow{Function: function, Flat: aggregate.flat, Cumulative: aggregate.cum, Unit: unit})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Cumulative != rows[j].Cumulative {
			return rows[i].Cumulative > rows[j].Cumulative
		}
		return rows[i].Function < rows[j].Function
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}
