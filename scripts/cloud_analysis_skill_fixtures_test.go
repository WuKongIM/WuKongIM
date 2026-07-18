package scripts_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestCloudAnalysisSkillDocumentsProcessLossGuard(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "SKILL.md"))
	contract := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-cloud-analysis", "references", "tool-contract.md"))
	for _, queryID := range []string{
		"node_memory_percent", "node_oom_kills", "process_start_time_seconds",
		"gateway_active_connections", "channel_active_channels",
	} {
		if !strings.Contains(skill, "`"+queryID+"`") {
			t.Fatalf("SKILL.md must route process-loss analysis through %q", queryID)
		}
		if !strings.Contains(contract, "`"+queryID+"`") {
			t.Fatalf("tool-contract.md must list metric query ID %q", queryID)
		}
	}
	if !strings.Contains(skill, "invalidates performance and storage calibration") {
		t.Fatal("SKILL.md must reject calibration evidence after a node OOM or process restart")
	}
	for _, required := range []string{"step_seconds=5", "`inuse_space`", "`alloc_space`"} {
		if !strings.Contains(skill, required) {
			t.Fatalf("SKILL.md must document bounded transient-memory evidence %q", required)
		}
		if required != "step_seconds=5" && !strings.Contains(contract, required) {
			t.Fatalf("tool-contract.md must document heap sample view %q", required)
		}
	}
}

func TestCloudAnalysisSkillFixturesCoverEveryVerdict(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot(t), ".agents", "skills", "wukongim-cloud-analysis", "fixtures")
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	want := []string{"healthy", "infrastructure_interrupted", "insufficient_evidence", "product_defect", "scenario_invalid"}
	allowedVerdicts := make(map[string]struct{}, len(want))
	for _, verdict := range want {
		allowedVerdicts[verdict] = struct{}{}
	}
	got := make([]string, 0, len(paths))
	for _, path := range paths {
		fixture := decodeCloudAnalysisSkillFixture(t, path)
		if fixture.Schema != "wukongim.analysis.skill_fixture/v1" || fixture.Name == "" || fixture.ExpectedVerdict == "" {
			t.Fatalf("fixture %s identity = %#v", path, fixture)
		}
		if _, ok := allowedVerdicts[fixture.ExpectedVerdict]; !ok {
			t.Fatalf("fixture %s verdict = %q, want one of %v", path, fixture.ExpectedVerdict, want)
		}
		if fixture.RunID == "" || len(fixture.Observations) < 2 || fixture.Observations[0].Tool != "run_inspect" {
			t.Fatalf("fixture %s must begin with run_inspect", path)
		}
		if fixture.Observations[1].Tool != "workload_inspect" {
			t.Fatalf("fixture %s must inspect the final workload before other live sources", path)
		}
		for _, observation := range fixture.Observations {
			if observation.RunID != fixture.RunID || observation.Node == "" || observation.Source == "" ||
				observation.ObservedAt.IsZero() || observation.Window.Start.IsZero() || observation.Window.End.IsZero() ||
				observation.Window.End.Before(observation.Window.Start) || observation.Completeness == "" || observation.Warnings == nil {
				t.Fatalf("fixture %s observation is not run-bound: %#v", path, observation)
			}
		}
		if fixture.Name == "scenario_invalid" {
			if fixture.ExpectedRoute != "worker_failure" {
				t.Fatalf("fixture %s route = %q, want worker_failure", path, fixture.ExpectedRoute)
			}
			failedWorkers, ok := fixture.Observations[1].Data["failed_workers"].([]any)
			if !ok || len(failedWorkers) == 0 {
				t.Fatalf("fixture %s must expose structured worker failure evidence", path)
			}
		}
		if fixture.Name == "worker_status_mismatch" {
			if fixture.ExpectedVerdict != "scenario_invalid" || fixture.ExpectedRoute != "worker_status_mismatch" {
				t.Fatalf("fixture %s mismatch route = %q/%q", path, fixture.ExpectedVerdict, fixture.ExpectedRoute)
			}
			failedWorkers, ok := fixture.Observations[1].Data["failed_workers"].([]any)
			if !ok || len(failedWorkers) != 1 {
				t.Fatalf("fixture %s must expose one status mismatch", path)
			}
			failure, ok := failedWorkers[0].(map[string]any)
			if !ok || failure["reason_code"] != "worker_status_mismatch" {
				t.Fatalf("fixture %s reason = %#v, want worker_status_mismatch", path, failedWorkers[0])
			}
		}
		if fixture.Name == "process_loss_ambiguous" {
			if fixture.ExpectedVerdict != "insufficient_evidence" || fixture.ExpectedRoute != "process_loss" {
				t.Fatalf("fixture %s process-loss route = %q/%q", path, fixture.ExpectedVerdict, fixture.ExpectedRoute)
			}
			queryIDs := make(map[string]struct{})
			for _, observation := range fixture.Observations {
				if queryID, ok := observation.Data["query_id"].(string); ok {
					queryIDs[queryID] = struct{}{}
				}
			}
			for _, queryID := range []string{"node_oom_kills", "process_start_time_seconds", "channel_active_channels"} {
				if _, ok := queryIDs[queryID]; !ok {
					t.Fatalf("fixture %s missing process-loss query %q", path, queryID)
				}
			}
		}
		got = append(got, fixture.ExpectedVerdict)
	}
	sort.Strings(got)
	for _, verdict := range want {
		index := sort.SearchStrings(got, verdict)
		if index == len(got) || got[index] != verdict {
			t.Fatalf("fixture verdicts = %v, missing %q", got, verdict)
		}
	}
}

type cloudAnalysisSkillFixture struct {
	Schema          string                          `json:"schema"`
	Name            string                          `json:"name"`
	RunID           string                          `json:"run_id"`
	Observations    []cloudAnalysisSkillObservation `json:"observations"`
	ExpectedVerdict string                          `json:"expected_verdict"`
	ExpectedRoute   string                          `json:"expected_route,omitempty"`
}

type cloudAnalysisSkillObservation struct {
	Tool         string                   `json:"tool"`
	RunID        string                   `json:"run_id"`
	Node         string                   `json:"node"`
	Source       string                   `json:"source"`
	ObservedAt   time.Time                `json:"observed_at"`
	Window       cloudAnalysisSkillWindow `json:"window"`
	Completeness string                   `json:"completeness"`
	Warnings     []string                 `json:"warnings"`
	Data         map[string]any           `json:"data"`
}

type cloudAnalysisSkillWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func decodeCloudAnalysisSkillFixture(t *testing.T, path string) cloudAnalysisSkillFixture {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var fixture cloudAnalysisSkillFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("fixture %s contains trailing JSON", path)
	}
	return fixture
}
