package chatlifecycle

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadConfigAcceptsOneValidatedStrictYAMLDocument(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "strict-loader"
	path := filepath.Join(t.TempDir(), "chat-lifecycle.yaml")
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunID != cfg.RunID || loaded.Profile != cfg.Profile || loaded.Mode != cfg.Mode {
		t.Fatalf("loaded config identity = %+v, want %+v", loaded, cfg)
	}
}

func TestLoadConfigRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "strict-rejection"
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body []byte
		want string
	}{
		{name: "unknown", body: append(append([]byte(nil), encoded...), []byte("unknown_contract_field: true\n")...), want: "field unknown_contract_field not found"},
		{name: "multiple", body: append(append([]byte(nil), encoded...), []byte("---\nrun_id: another\n")...), want: "multiple documents"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.yaml")
			if err := os.WriteFile(path, test.body, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestReadReportAcceptsOnlyBoundedStrictValidatedJSON(t *testing.T) {
	report := reportFixture(t)
	encoded, err := MarshalReport(report, ReportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	validPath := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := os.WriteFile(validPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadReport(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConfigDigest != report.ConfigDigest || loaded.Kind != report.Kind {
		t.Fatalf("loaded report identity = %+v, want %+v", loaded, report)
	}

	unknown := bytes.TrimSuffix(encoded, []byte("\n"))
	unknown = append(bytes.TrimSuffix(unknown, []byte("}")), []byte(",\"secret_extension\":true}\n")...)
	unknownPath := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknownPath, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReport(unknownPath); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field report error = %v", err)
	}

	oversizedPath := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(oversizedPath, bytes.Repeat([]byte{' '}, int(maxPersistedReportBytes)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReport(oversizedPath); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized report error = %v", err)
	}
}
