package conversation_qps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"
)

type messageIdentity struct {
	MessageSeq  uint64 `json:"message_seq"`
	ClientMsgNo string `json:"client_msg_no"`
	Payload     string `json:"payload"`
}
type expectedMessages map[string][]messageIdentity

// hiddenAt fixes three deterministic layouts in activation/ID order.
func hiddenAt(cohort, rank int) bool {
	switch cohort {
	case 0:
		return rank%2 == 1
	case 1:
		return rank%10 != 0
	case 2:
		return rank >= 99 && rank < 199
	default:
		panic("unsupported hidden fixture cohort")
	}
}

func cohortChannels(p profile, cohort int) []string {
	ids := make([]string, p.ChannelsPerCohort)
	for i := range ids {
		ids[i] = fmt.Sprintf("qps-%d-%d", cohort, i)
	}
	sort.Strings(ids)
	return ids
}

func visiblePage(p profile, cohort, page, size int) []string {
	var visible []string
	for rank, id := range cohortChannels(p, cohort) {
		if !hiddenAt(cohort, rank) {
			visible = append(visible, id)
		}
	}
	start := (page - 1) * size
	if start >= len(visible) {
		return nil
	}
	return visible[start:min(start+size, len(visible))]
}

// validateHiddenSync rejects wrong pages and incomplete or stale persisted data.
func validateHiddenSync(raw []byte, ids []string, expected expectedMessages) error {
	if data := bytes.TrimSpace(raw); len(data) == 0 || data[0] != '[' {
		return fmt.Errorf("hidden sync response must be an array")
	}
	var rows []struct {
		ChannelID   string            `json:"channel_id"`
		ChannelType int               `json:"channel_type"`
		Recents     []messageIdentity `json:"recents"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return fmt.Errorf("hidden page rows=%d want=%d", len(rows), len(ids))
	}
	for i, row := range rows {
		if row.ChannelID != ids[i] || row.ChannelType != 2 {
			return fmt.Errorf("wrong hidden page/order at row %d", i)
		}
		want, ok := expected[ids[i]]
		if !ok || len(want) == 0 || len(row.Recents) != len(want) {
			return fmt.Errorf("missing hidden-page recents")
		}
		for j, got := range row.Recents {
			if got != want[len(want)-1-j] {
				return fmt.Errorf("incorrect hidden-page message")
			}
		}
	}
	return nil
}

// stressConfig fixes offered diagnostic loads independently of release floors.
type stressConfig struct {
	// MaxAllocatedBytesPerRequest applies once to each whole named gate window.
	MaxAllocatedBytesPerRequest map[string]float64 `json:"max_allocated_bytes_per_request,omitempty"`
	// MixedSeconds is one uninterrupted shared measurement window.
	MixedSeconds int `json:"mixed_seconds"`
	MixedListQPS int `json:"mixed_list_qps"`
	MixedSyncQPS int `json:"mixed_sync_qps"`
	// MixedWorkersPerEndpoint bounds each endpoint pool; the mixed total is twice this.
	MixedWorkersPerEndpoint int `json:"mixed_workers_per_endpoint"`
	HiddenSeconds           int `json:"hidden_seconds"`
	HiddenQPS               int `json:"hidden_qps"`
	HiddenWorkers           int `json:"hidden_workers"`
}

func validateStressConfig(c stressConfig) error {
	if c.MixedSeconds < 15 || c.MixedSeconds > 600 || c.MixedListQPS < 1 || c.MixedListQPS > 1200 || c.MixedSyncQPS < 1 || c.MixedSyncQPS > 420 || c.MixedWorkersPerEndpoint < 1 || c.MixedWorkersPerEndpoint > 24 || c.HiddenSeconds < 15 || c.HiddenSeconds > 180 || c.HiddenQPS < 1 || c.HiddenQPS > 100 || c.HiddenWorkers < 1 || c.HiddenWorkers > 48 {
		return fmt.Errorf("invalid bounded conversation stress profile")
	}
	return nil
}

// stressWindow attributes shared CPU/allocations once, outside endpoint results.
type stressWindow struct {
	Name             string               `json:"name"`
	Page             int                  `json:"page,omitempty"`
	Cohort           int                  `json:"cohort,omitempty"`
	Start            time.Time            `json:"start"`
	End              time.Time            `json:"end"`
	Phases           []phaseResult        `json:"phases"`
	Before           []map[string]float64 `json:"metrics_before"`
	After            []map[string]float64 `json:"metrics_after"`
	CPUSeconds       float64              `json:"cpu_seconds"`
	AllocatedBytes   float64              `json:"allocated_bytes"`
	HeapBytes        float64              `json:"heap_bytes"`
	RuntimeLoads     float64              `json:"runtime_loads"`
	MembershipWrites float64              `json:"membership_writes"`
	ActiveBefore     float64              `json:"active_runtimes_before"`
	ActiveAfter      float64              `json:"active_runtimes_after"`
}

type stressReport struct {
	Stage            string         `json:"stage"`
	Schema           string         `json:"schema"`
	SourceSHA        string         `json:"source_sha"`
	SourceDirty      bool           `json:"source_dirty"`
	ProfileSHA       string         `json:"profile_sha256"`
	BinarySHA        string         `json:"binary_sha256"`
	OS               string         `json:"os"`
	Arch             string         `json:"arch"`
	CPUs             int            `json:"cpus"`
	DriverGOMAXPROCS int            `json:"driver_gomaxprocs"`
	NodeGOMAXPROCS   int            `json:"node_gomaxprocs"`
	StartedAt        time.Time      `json:"started_at"`
	Config           stressConfig   `json:"config"`
	Windows          []stressWindow `json:"windows"`
	Complete         bool           `json:"complete"`
	Passed           bool           `json:"passed"`
}

// evaluateStressWindow requires both simultaneous endpoints and exact hidden
// coverage, with CPU/allocations owned by the shared window only.
func evaluateStressWindow(p profile, cfg stressConfig, w stressWindow) error {
	if err := validateStressConfig(cfg); err != nil {
		return err
	}
	seconds, workers := cfg.HiddenSeconds, cfg.HiddenWorkers
	var cases []workloadCase
	switch w.Name {
	case "mixed":
		if w.Page != 0 || w.Cohort != 0 {
			return fmt.Errorf("invalid mixed window")
		}
		seconds, workers = cfg.MixedSeconds, cfg.MixedWorkersPerEndpoint
		cases = []workloadCase{{Endpoint: "/conversation/list", PageSize: 100, OfferedQPS: cfg.MixedListQPS}, {Endpoint: "/conversation/sync", PageSize: 100, OfferedQPS: cfg.MixedSyncQPS}}
	case "hidden_50_percent", "hidden_90_percent", "hidden_after_99_visible":
		cohort := map[string]int{"hidden_50_percent": 0, "hidden_90_percent": 1, "hidden_after_99_visible": 2}[w.Name]
		if w.Cohort != cohort || (w.Page != 1 && w.Page != 2) {
			return fmt.Errorf("invalid hidden window")
		}
		size := 100
		if w.Page == 2 {
			size = 50
		}
		cases = []workloadCase{{Endpoint: "/conversation/sync", PageSize: size, OfferedQPS: cfg.HiddenQPS}}
	default:
		return fmt.Errorf("unknown stress window")
	}
	if w.Start.IsZero() || w.End.Sub(w.Start) != time.Duration(seconds)*time.Second || len(w.Phases) != len(cases) {
		return fmt.Errorf("missing shared interval or endpoint")
	}
	if w.RuntimeLoads != 0 || w.MembershipWrites != 0 || w.ActiveBefore != 0 || w.ActiveAfter != 0 {
		return fmt.Errorf("stress reads activated runtimes or mutated memberships")
	}
	if w.CPUSeconds <= 0 || math.IsNaN(w.CPUSeconds) || math.IsInf(w.CPUSeconds, 0) || w.HeapBytes <= 0 || math.IsNaN(w.HeapBytes) || math.IsInf(w.HeapBytes, 0) || w.AllocatedBytes <= 0 || math.IsNaN(w.AllocatedBytes) || math.IsInf(w.AllocatedBytes, 0) {
		return fmt.Errorf("missing stress resource evidence")
	}
	p.DurationSeconds, p.Workers = seconds, workers
	completed := 0
	for i, phase := range w.Phases {
		if phase.Verdict != "pass" || phase.Nodes != 3 || !reflect.DeepEqual(phase.Case, cases[i]) || phase.CPUSeconds != nil || phase.AllocatedBytes != 0 || phase.HeapBytes != 0 {
			return fmt.Errorf("wrong workload or duplicated resource attribution")
		}
		if err := evaluate(p, phase); err != nil {
			return err
		}
		completed += phase.Completed
	}
	ceiling := cfg.MaxAllocatedBytesPerRequest[w.Name]
	if ceiling <= 0 || math.IsNaN(ceiling) || math.IsInf(ceiling, 0) || w.AllocatedBytes/float64(completed) > ceiling {
		return fmt.Errorf("stress allocation ceiling exceeded")
	}
	return nil
}

// validateStressGate requires a reviewed allocation ceiling for every window.
func validateStressGate(c stressConfig) error {
	if err := validateStressConfig(c); err != nil {
		return err
	}
	names := []string{"mixed", "hidden_50_percent", "hidden_90_percent", "hidden_after_99_visible"}
	if len(c.MaxAllocatedBytesPerRequest) != len(names) {
		return fmt.Errorf("missing stress gate allocation ceilings")
	}
	for _, name := range names {
		value := c.MaxAllocatedBytesPerRequest[name]
		if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) || value > 128<<20 {
			return fmt.Errorf("invalid stress gate allocation ceiling")
		}
	}
	return nil
}
