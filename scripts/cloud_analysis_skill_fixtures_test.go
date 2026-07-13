package scripts_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestCloudAnalysisSkillFixturesCoverEveryVerdict(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot(t), ".agents", "skills", "wukongim-cloud-analysis", "fixtures")
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	want := []string{"healthy", "infrastructure_interrupted", "insufficient_evidence", "product_defect", "scenario_invalid"}
	got := make([]string, 0, len(paths))
	for _, path := range paths {
		fixture := decodeCloudAnalysisSkillFixture(t, path)
		if fixture.Schema != "wukongim.analysis.skill_fixture/v1" || fixture.Name == "" || fixture.Name != fixture.ExpectedVerdict {
			t.Fatalf("fixture %s identity = %#v", path, fixture)
		}
		if fixture.RunID == "" || len(fixture.Observations) == 0 || fixture.Observations[0].Tool != "run_inspect" {
			t.Fatalf("fixture %s must begin with run_inspect", path)
		}
		for _, observation := range fixture.Observations {
			if observation.RunID != fixture.RunID || observation.Node == "" || observation.Source == "" ||
				observation.ObservedAt.IsZero() || observation.Window.Start.IsZero() || observation.Window.End.IsZero() ||
				observation.Window.End.Before(observation.Window.Start) || observation.Completeness == "" || observation.Warnings == nil {
				t.Fatalf("fixture %s observation is not run-bound: %#v", path, observation)
			}
		}
		got = append(got, fixture.ExpectedVerdict)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("fixture verdicts = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("fixture verdicts = %v, want %v", got, want)
		}
	}
}

type cloudAnalysisSkillFixture struct {
	Schema          string                          `json:"schema"`
	Name            string                          `json:"name"`
	RunID           string                          `json:"run_id"`
	Observations    []cloudAnalysisSkillObservation `json:"observations"`
	ExpectedVerdict string                          `json:"expected_verdict"`
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
