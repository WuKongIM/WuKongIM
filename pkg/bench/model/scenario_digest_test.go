package model

import "testing"

func TestDigestScenarioIsStableAndSensitiveToEffectiveValues(t *testing.T) {
	scenario := Scenario{Version: "wkbench/v1", Run: RunConfig{ID: "run-1", RandomSeed: 42}}
	first, err := DigestScenario(scenario)
	if err != nil {
		t.Fatalf("DigestScenario() error = %v", err)
	}
	second, err := DigestScenario(scenario)
	if err != nil {
		t.Fatalf("DigestScenario() second error = %v", err)
	}
	if first != second || len(first) != len("sha256:")+64 {
		t.Fatalf("digests = %q, %q", first, second)
	}
	scenario.Run.RandomSeed++
	changed, err := DigestScenario(scenario)
	if err != nil {
		t.Fatalf("DigestScenario() changed error = %v", err)
	}
	if changed == first {
		t.Fatal("DigestScenario() did not change with the effective scenario")
	}
}
