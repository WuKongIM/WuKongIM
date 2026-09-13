package conversation_qps

import (
	"encoding/json"
	"maps"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHiddenLayoutsAndSecondPages(t *testing.T) {
	p, _, err := loadProfile()
	require.NoError(t, err)
	for cohort, want := range []int{100, 20, 100} {
		all := visiblePage(p, cohort, 1, 200)
		require.Len(t, all, want)
		require.Equal(t, all[:min(want, 100)], visiblePage(p, cohort, 1, 100))
		if want > 50 {
			require.Equal(t, all[50:], visiblePage(p, cohort, 2, 50))
		} else {
			require.Empty(t, visiblePage(p, cohort, 2, 50))
		}
	}
	ids := cohortChannels(p, 2)
	page := visiblePage(p, 2, 1, 100)
	require.Equal(t, ids[:99], page[:99])
	require.Equal(t, ids[199], page[99])
}

func TestHiddenResponseRequiresExactPageAndMessageIdentities(t *testing.T) {
	ids := []string{"a", "b"}
	messages := expectedMessages{"a": {{MessageSeq: 1, ClientMsgNo: "a-1", Payload: "eA=="}}, "b": {{MessageSeq: 1, ClientMsgNo: "b-1", Payload: "eQ=="}}}
	rows := []map[string]any{{"channel_id": "a", "channel_type": 2, "recents": messages["a"]}, {"channel_id": "b", "channel_type": 2, "recents": messages["b"]}}
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NoError(t, validateHiddenSync(raw, ids, messages))
	for _, test := range []struct {
		name string
		raw  string
	}{
		{"missing row", `[]`},
		{"wrong page", `[{"channel_id":"b","channel_type":2},{"channel_id":"a","channel_type":2}]`},
		{"wrong type", `[{"channel_id":"a","channel_type":1},{"channel_id":"b","channel_type":2}]`},
		{"missing recents", `[{"channel_id":"a","channel_type":2},{"channel_id":"b","channel_type":2}]`},
		{"malformed", `{`},
	} {
		t.Run(test.name, func(t *testing.T) { require.Error(t, validateHiddenSync([]byte(test.raw), ids, messages)) })
	}
	rows[0]["recents"] = []messageIdentity{{MessageSeq: 2, ClientMsgNo: "a-1", Payload: "eA=="}}
	raw, err = json.Marshal(rows)
	require.NoError(t, err)
	require.Error(t, validateHiddenSync(raw, ids, messages))
}

func TestStressGateRejectsIncompleteAndMisattributedWindows(t *testing.T) {
	p, _, err := loadProfile()
	require.NoError(t, err)
	cfg := p.StressDiagnosis
	cfg.MaxAllocatedBytesPerRequest = map[string]float64{"mixed": 1000}
	start := time.Unix(123, 0)
	good := stressWindow{Name: "mixed", Start: start, End: start.Add(time.Duration(cfg.MixedSeconds) * time.Second), CPUSeconds: 1, AllocatedBytes: 1, HeapBytes: 1}
	for _, work := range []workloadCase{{Endpoint: "/conversation/list", PageSize: 100, OfferedQPS: cfg.MixedListQPS}, {Endpoint: "/conversation/sync", PageSize: 100, OfferedQPS: cfg.MixedSyncQPS}} {
		count := work.OfferedQPS * cfg.MixedSeconds
		phaseProfile := p
		phaseProfile.Workers = cfg.MixedWorkersPerEndpoint
		good.Phases = append(good.Phases, phaseResult{Nodes: 3, Case: work, DriverWorkers: phaseProfile.Workers, QueueCapacity: queuedArrivalLimit(phaseProfile, work), Scheduled: count, Completed: count, CompletedInWindow: count, DurationSeconds: cfg.MixedSeconds, ActualQPS: float64(work.OfferedQPS), P99MS: 10, Verdict: "pass"})
	}
	require.NoError(t, evaluateStressWindow(p, cfg, good))
	for name, mutate := range map[string]func(*stressWindow){
		"failed verdict":               func(w *stressWindow) { w.Phases[0].Verdict = "failed" },
		"missing endpoint":             func(w *stressWindow) { w.Phases = w.Phases[:1] },
		"duplicated endpoint":          func(w *stressWindow) { w.Phases[1] = w.Phases[0] },
		"wrong interval":               func(w *stressWindow) { w.End = w.End.Add(-time.Second) },
		"refusal":                      func(w *stressWindow) { w.Phases[0].Errors++ },
		"late completions":             func(w *stressWindow) { w.Phases[1].CompletedInWindow = 0; w.Phases[1].ActualQPS = 0 },
		"shared allocation regression": func(w *stressWindow) { w.AllocatedBytes = 1e20 },
		"duplicated allocation":        func(w *stressWindow) { w.Phases[0].AllocatedBytes = w.AllocatedBytes },
		"duplicated cpu":               func(w *stressWindow) { w.Phases[0].CPUSeconds = &w.CPUSeconds },
		"missing cpu":                  func(w *stressWindow) { w.CPUSeconds = 0 },
		"activation":                   func(w *stressWindow) { w.RuntimeLoads = 1 },
		"writes":                       func(w *stressWindow) { w.MembershipWrites = 1 },
		"wrong page":                   func(w *stressWindow) { w.Page = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			w := good
			w.Phases = append([]phaseResult(nil), good.Phases...)
			mutate(&w)
			require.Error(t, evaluateStressWindow(p, cfg, w))
		})
	}
}

func TestStressGateRequiresAllFiniteAllocationCeilings(t *testing.T) {
	p, _, err := loadProfile()
	require.NoError(t, err)
	require.NoError(t, validateStressGate(p.StressGate))
	for _, value := range []float64{0, -1, math.NaN(), math.Inf(1), 1e20} {
		cfg := p.StressGate
		cfg.MaxAllocatedBytesPerRequest = maps.Clone(cfg.MaxAllocatedBytesPerRequest)
		cfg.MaxAllocatedBytesPerRequest["mixed"] = value
		require.Error(t, validateStressGate(cfg))
	}
	cfg := p.StressGate
	delete(cfg.MaxAllocatedBytesPerRequest, "mixed")
	require.Error(t, validateStressGate(cfg))
}
