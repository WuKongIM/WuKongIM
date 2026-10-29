package chatlifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHotAttemptBudgetDefaultsKeepIndependentLatencySLO(t *testing.T) {
	for name, cfg := range map[string]Config{"formal": FormalConfig(), "rehearsal": RehearsalConfig(), "local": LocalConfig()} {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			latency := hotAttemptBudgetLatencyJSON(t, body)
			if string(latency["hot_attempt_timeout"]) != "2000000000" {
				t.Fatalf("default hot attempt budget is not explicit2s: %s", latency["hot_attempt_timeout"])
			}
			if cfg.Thresholds.Latency.HotSendACK != (LatencyLimit{P99: 400 * time.Millisecond, P999: time.Second}) || cfg.Thresholds.Latency.Cold.P999 != 5*time.Second {
				t.Fatal("attempt budget changed latency percentile gates/cold budget")
			}
		})
	}
}

func TestHotAttemptBudgetFormalRequiresExplicitTwoSeconds(t *testing.T) {
	for _, budget := range []time.Duration{2 * time.Second, 0, time.Second, 3 * time.Second, -time.Nanosecond} {
		t.Run(budget.String(), func(t *testing.T) {
			cfg := hotAttemptBudgetConfig(t, FormalConfig(), &budget)
			err := cfg.Validate()
			if budget == 2*time.Second {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatalf("noncurrent Formal hot attempt budget %s was accepted", budget)
			}
		})
	}
}

func TestHotAttemptBudgetCurrentYAMLExamplesDeclareTwoSeconds(t *testing.T) {
	for _, name := range []string{"formal.yaml", "rehearsal.yaml", "local-shakeout.yaml"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadConfig(filepath.Join("..", "..", "..", "configs", "wkbench", "chat-lifecycle", name))
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if string(hotAttemptBudgetLatencyJSON(t, body)["hot_attempt_timeout"]) != "2000000000" {
				t.Fatal("current YAML omitted explicit2s policy")
			}
		})
	}
}

func TestHotAttemptBudgetReportAndDigestTracePolicy(t *testing.T) {
	report := reportFixture(t)
	body, err := MarshalReport(report, ReportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if string(hotAttemptBudgetLatencyJSON(t, body)["hot_attempt_timeout"]) != "2000000000" {
		t.Fatal("report cannot distinguish new2s policy from original1s")
	}
	legacy := hotAttemptBudgetConfig(t, LocalConfig(), nil)
	two := 2 * time.Second
	current := hotAttemptBudgetConfig(t, legacy, &two)
	a, err := digestCheckpointConfig(legacy)
	if err != nil {
		t.Fatal(err)
	}
	b, err := digestCheckpointConfig(current)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("different attempt policies have the same config digest")
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := hotAttemptBudgetLatencyJSON(t, legacyJSON)["hot_attempt_timeout"]; exists {
		t.Fatal("zero fallback inserted a new key into legacy config digest input")
	}
}

func TestHotAttemptBudgetLegacyV7RemainsOriginalForEveryProfile(t *testing.T) {
	for _, profile := range []Profile{ProfileFormal, ProfileLocal} {
		for _, budget := range []string{"absent", "0", "1000000000", "2000000000"} {
			t.Run(string(profile)+"/"+budget, func(t *testing.T) {
				report := reportFixture(t)
				report.SchemaVersion = legacyReportSchemaVersion
				if profile == ProfileLocal {
					report.Profile, report.Stage = ProfileLocal, StageShakeout
					report.Qualification = reportQualification(report.Profile, report.Stage)
				}
				body, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				var value *json.RawMessage
				if budget != "absent" {
					raw := json.RawMessage(budget)
					value = &raw
				}
				body = hotAttemptBudgetRewriteJSON(t, body, value)
				path := filepath.Join(t.TempDir(), "legacy.json")
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
				read, err := ReadReport(path)
				if budget == "absent" || budget == "0" {
					if err != nil || read.ConfigDigest != report.ConfigDigest || read.SchemaVersion != legacyReportSchemaVersion {
						t.Fatalf("legacy report/digest was rejected or upgraded: %v", err)
					}
					encoded, err := json.Marshal(read)
					if err != nil {
						t.Fatal(err)
					}
					if _, exists := hotAttemptBudgetLatencyJSON(t, encoded)["hot_attempt_timeout"]; exists {
						t.Fatal("historical zero was normalized into new policy")
					}
				} else if err == nil {
					t.Fatal("v7 accepted a nonzero policy that its historical writer never emitted")
				}
			})
		}
	}
	zero := json.RawMessage("0")
	body, err := json.Marshal(reportFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "current-with-legacy-budget.json")
	if err := os.WriteFile(path, hotAttemptBudgetRewriteJSON(t, body, &zero), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReport(path); err == nil {
		t.Fatal("currentv8 Formal silently inherited original1s budget")
	}
}

// These JSON helpers deliberately avoid a future Go field so unchanged
// production compiles and fails semantically before the new policy exists.
func hotAttemptBudgetConfig(t *testing.T, cfg Config, budget *time.Duration) Config {
	t.Helper()
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var value *json.RawMessage
	if budget != nil {
		raw, err := json.Marshal(*budget)
		if err != nil {
			t.Fatal(err)
		}
		message := json.RawMessage(raw)
		value = &message
	}
	body = hotAttemptBudgetRewriteJSON(t, body, value)
	var result Config
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func hotAttemptBudgetLatencyJSON(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var root, thresholds, latency map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(root["thresholds"], &thresholds); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(thresholds["latency"], &latency); err != nil {
		t.Fatal(err)
	}
	return latency
}

func hotAttemptBudgetRewriteJSON(t *testing.T, body []byte, value *json.RawMessage) []byte {
	t.Helper()
	var root, thresholds map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(root["thresholds"], &thresholds); err != nil {
		t.Fatal(err)
	}
	latency := hotAttemptBudgetLatencyJSON(t, body)
	if value == nil {
		delete(latency, "hot_attempt_timeout")
	} else {
		latency["hot_attempt_timeout"] = *value
	}
	var err error
	thresholds["latency"], err = json.Marshal(latency)
	if err != nil {
		t.Fatal(err)
	}
	root["thresholds"], err = json.Marshal(thresholds)
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
