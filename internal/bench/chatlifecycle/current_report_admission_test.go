package chatlifecycle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Legacy diagnostic decoding never grants current capacity authority. These
// cases use existing APIs so the missing eligibility check is semantic RED.
func TestCurrentReportCapacityPreparationRequiresV8(t *testing.T) {
	_, admission, _ := capacityTestAdmission(t)
	formal := FormalConfig()
	formal.RunID = "capacity-aged-soak"
	for _, schema := range []string{ReportSchemaVersion, "wukongim/chat-lifecycle-report/v7", "", "wukongim/chat-lifecycle-report/v9"} {
		t.Run(schema, func(t *testing.T) {
			checkpoint := admission.Checkpoint
			checkpoint.SchemaVersion = schema
			_, err := PrepareCapacityConfig(formal, checkpoint, admission.Reference)
			if schema == ReportSchemaVersion {
				if err != nil {
					t.Fatalf("current capacity preparation rejected: %v", err)
				}
			} else if !errors.Is(err, ErrCapacityAdmission) {
				t.Fatalf("noncurrent report %q granted capacity preparation: %v", schema, err)
			}
		})
	}
}

func TestCurrentReportCapacityAdmissionRequiresV8(t *testing.T) {
	cfg, admission, _ := capacityTestAdmission(t)
	for _, schema := range []string{ReportSchemaVersion, "wukongim/chat-lifecycle-report/v7", "", "wukongim/chat-lifecycle-report/v9"} {
		t.Run(schema, func(t *testing.T) {
			candidate := admission
			candidate.Checkpoint.SchemaVersion = schema
			err := validateCapacityCheckpoint(cfg, candidate)
			if schema == ReportSchemaVersion {
				if err != nil {
					t.Fatalf("current aged checkpoint rejected: %v", err)
				}
			} else if !errors.Is(err, ErrCapacityAdmission) {
				t.Fatalf("noncurrent report %q granted current capacity admission: %v", schema, err)
			}
		})
	}
}

func TestCurrentReportLegacyDiagnosticReadRemainsUnknown(t *testing.T) {
	_, admission, _ := capacityTestAdmission(t)
	legacy := admission.Checkpoint
	legacy.SchemaVersion = "wukongim/chat-lifecycle-report/v7"
	legacy.Verdict.LatencyGate = nil
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	for _, wholeNull := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "null"}[wholeNull], func(t *testing.T) {
			body := encoded
			if wholeNull {
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(encoded, &raw); err != nil {
					t.Fatal(err)
				}
				var verdict map[string]json.RawMessage
				if err := json.Unmarshal(raw["verdict"], &verdict); err != nil {
					t.Fatal(err)
				}
				verdict["latency_gate"] = json.RawMessage("null")
				raw["verdict"], err = json.Marshal(verdict)
				if err != nil {
					t.Fatal(err)
				}
				body, err = json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "legacy.json")
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			read, err := ReadReport(path)
			if err != nil || read.SchemaVersion != legacy.SchemaVersion || read.Verdict.LatencyGate != nil {
				t.Fatalf("legacy diagnostic was rejected, upgraded or given fabricated gate proof: %q/%+v/%v", read.SchemaVersion, read.Verdict.LatencyGate, err)
			}
		})
	}
}
