package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

const maxPersistedReportBytes int64 = 4 << 20

// LoadConfig reads one strict chat-lifecycle YAML document and validates the
// complete deterministic configuration before any network request.
func LoadConfig(path string) (Config, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read chat-lifecycle config: %w", err)
	}
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(encoded))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse chat-lifecycle config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("parse chat-lifecycle config: multiple documents")
		}
		return Config{}, fmt.Errorf("parse chat-lifecycle config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate chat-lifecycle config: %w", err)
	}
	return cfg, nil
}

// ReadReport reads one strict, fully validated persisted JSON report.
func ReadReport(path string) (Report, error) {
	file, err := os.Open(path)
	if err != nil {
		return Report{}, fmt.Errorf("read chat-lifecycle checkpoint: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxPersistedReportBytes+1))
	if err != nil {
		return Report{}, fmt.Errorf("read chat-lifecycle checkpoint: %w", err)
	}
	if int64(len(encoded)) > maxPersistedReportBytes {
		return Report{}, fmt.Errorf("read chat-lifecycle checkpoint: exceeds %d-byte limit", maxPersistedReportBytes)
	}
	var report Report
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return Report{}, fmt.Errorf("parse chat-lifecycle checkpoint: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Report{}, fmt.Errorf("parse chat-lifecycle checkpoint: trailing document")
		}
		return Report{}, fmt.Errorf("parse chat-lifecycle checkpoint: %w", err)
	}
	if err := validateReport(report); err != nil {
		return Report{}, fmt.Errorf("validate chat-lifecycle checkpoint: %w", err)
	}
	return report, nil
}
