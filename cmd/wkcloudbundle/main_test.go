package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOfflineCommandsSealAndVerifyVersionedBundle(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"caddy", "node_exporter", "prometheus", "wkanalysis", "wkbench", "wkcloudbundle", "wkcloudgate", "wkcloudhost", "wukongim"} {
		path := filepath.Join(root, "bin", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		header := make([]byte, 64)
		copy(header, []byte("\x7fELF"))
		header[4], header[5], header[6] = 2, 1, 1
		binary.LittleEndian.PutUint16(header[16:18], 2)
		binary.LittleEndian.PutUint16(header[18:20], 62)
		if err := os.WriteFile(path, header, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"assets/manager/index.html", "assets/demo/index.html"} {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("<!doctype html>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const sourceSHA = "0123456789012345678901234567890123456789"
	const controlSHA = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	var stdout bytes.Buffer
	command := newRootCommand(&stdout, &bytes.Buffer{})
	command.SetArgs([]string{"seal-offline", "--root", root, "--source-sha", sourceSHA, "--control-sha", controlSHA})
	if err := command.Execute(); err != nil {
		t.Fatalf("seal-offline error = %v", err)
	}
	var sealed struct {
		Schema       string `json:"schema"`
		BundleDigest string `json:"bundle_digest"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sealed); err != nil || sealed.Schema != "wukongim.cloud_deployment.bundle/v1" || sealed.BundleDigest == "" {
		t.Fatalf("sealed = %#v, %v", sealed, err)
	}
	stdout.Reset()
	command = newRootCommand(&stdout, &bytes.Buffer{})
	command.SetArgs([]string{"verify-offline", "--root", root})
	if err := command.Execute(); err != nil {
		t.Fatalf("verify-offline error = %v", err)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(sealed.BundleDigest)) {
		t.Fatalf("verify output = %s", stdout.String())
	}
}

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
