package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadBundleSpecParsesAllowlistedDuration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.json")
	content := `{"run_id":"r","source_sha":"0123456789012345678901234567890123456789","scenario_path":"s.yaml","scenario_digest":"sha256:s","duration":"24h","private_ipv4":{"node-1":"1","node-2":"2","node-3":"3","sim":"4"},"simulator_source_ipv4":["4","5","6"],"public_observation":true}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := readBundleSpec(path)
	if err != nil {
		t.Fatalf("readBundleSpec() error = %v", err)
	}
	if spec.Duration != 24*time.Hour || len(spec.SimulatorSourceIPv4) != 3 || !spec.PublicViewEnabled {
		t.Fatalf("spec = %#v", spec)
	}
}
