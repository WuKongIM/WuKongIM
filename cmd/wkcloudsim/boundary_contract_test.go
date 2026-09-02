package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cloudsimalibaba "github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/alibaba"
	cloudsim "github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

func TestCreateRejectsIncompleteLocatorFlagsBeforeMutation(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	directory := t.TempDir()
	statePath := filepath.Join(directory, "inventory.json")
	requestPath := writeCloudSimJSON(t, directory, "request.json", validCreateRequest(now, "run-no-locator"))
	locatorPath := filepath.Join(directory, "locator.json")

	var stdout, stderr bytes.Buffer
	code := execute([]string{
		"--state", statePath,
		"create", "--request", requestPath,
		"--locator", locatorPath,
	}, &stdout, &stderr, func() time.Time { return now })
	if code != 1 || !strings.Contains(stderr.String(), "--workflow-run-id is required with --locator") {
		t.Fatalf("create code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("create stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(locatorPath); !os.IsNotExist(err) {
		t.Fatalf("locator stat error = %v, want not created", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state store stat error = %v, want provider factory not invoked", err)
	}
}

func TestBoundedJSONReadersRejectOversizedTrailingWhitespace(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		read  func(string) error
	}{
		{
			name:  "create request",
			limit: maxCreateRequestBytes,
			read: func(path string) error {
				_, err := readCreateRequest(path)
				return err
			},
		},
		{
			name:  "Alibaba provider config",
			limit: maxProviderConfigBytes,
			read: func(path string) error {
				_, err := readAlibabaConfig(path)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := append([]byte("{}"), bytes.Repeat([]byte(" "), test.limit)...)
			path := filepath.Join(t.TempDir(), "oversized.json")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatalf("WriteFile(): %v", err)
			}
			if err := test.read(path); err == nil || !strings.Contains(err.Error(), "at most") {
				t.Fatalf("read oversized document error = %v, want size-limit rejection", err)
			}
		})
	}
}

func TestStrictJSONReadersRejectUnknownFieldsAndTrailingValues(t *testing.T) {
	t.Run("create request", func(t *testing.T) {
		tests := []struct {
			name string
			path string
			want string
		}{
			{name: "missing", path: filepath.Join(t.TempDir(), "missing.json"), want: "no such file"},
			{name: "unknown field", path: writeRawCloudSimDocument(t, []byte(`{"unknown":true}`)), want: "unknown field"},
			{name: "trailing value", path: writeRawCloudSimDocument(t, []byte("{}\n{}\n")), want: "trailing data"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				if _, err := readCreateRequest(test.path); err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("readCreateRequest() error = %v, want %q", err, test.want)
				}
			})
		}
	})

	t.Run("Alibaba config", func(t *testing.T) {
		if _, err := readAlibabaConfig(""); err == nil || !strings.Contains(err.Error(), "--provider-config is required") {
			t.Fatalf("readAlibabaConfig(empty) error = %v", err)
		}
		if _, err := readAlibabaConfig(filepath.Join(t.TempDir(), "missing.json")); err == nil {
			t.Fatal("readAlibabaConfig(missing) error = nil")
		}
		if _, err := readAlibabaConfig(writeRawCloudSimDocument(t, []byte(`{"unknown":true}`))); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("readAlibabaConfig(unknown) error = %v", err)
		}
		if _, err := readAlibabaConfig(writeRawCloudSimDocument(t, []byte("{}\n{}\n"))); err == nil || !strings.Contains(err.Error(), "trailing data") {
			t.Fatalf("readAlibabaConfig(trailing) error = %v", err)
		}

		path := writeRawCloudSimDocument(t, []byte(`{
			"region":"cn-hangzhou",
			"zone_id":"cn-hangzhou-a",
			"private_ipv4":{"sim":"10.0.0.5"},
			"simulator_source_ipv4":["10.0.0.5","10.0.0.6"]
		}`))
		config, err := readAlibabaConfig(path)
		if err != nil {
			t.Fatalf("readAlibabaConfig(valid): %v", err)
		}
		if config.Region != "cn-hangzhou" || config.ZoneID != "cn-hangzhou-a" || config.PrivateIPv4["sim"] != "10.0.0.5" ||
			len(config.SimulatorSourceIPv4) != 2 || config.SimulatorSourceIPv4[1] != "10.0.0.6" {
			t.Fatalf("decoded config = %#v", config)
		}
	})
}

func TestPreflightProjectsExactLocatorBoundInventory(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	directory := t.TempDir()
	statePath := filepath.Join(directory, "inventory.json")
	locatorPath := filepath.Join(directory, "locator.json")
	requestPath := writeCloudSimJSON(t, directory, "request.json", validCreateRequest(now, "run-preflight"))

	var stdout, stderr bytes.Buffer
	if code := execute([]string{
		"--state", statePath, "create", "--request", requestPath,
		"--locator", locatorPath, "--workflow-run-id", "77",
	}, &stdout, &stderr, func() time.Time { return now }); code != 0 {
		t.Fatalf("create code=%d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"--state", statePath, "preflight", "--locator", locatorPath}, &stdout, &stderr, func() time.Time { return now }); code != 0 {
		t.Fatalf("preflight code=%d stderr=%q", code, stderr.String())
	}
	var result cloudsim.PreflightResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode preflight: %v; output=%q", err, stdout.String())
	}
	if result.State != cloudsim.PreflightLive || result.Run == nil || result.Run.ID != "run-preflight" || len(result.Resources) == 0 ||
		len(result.Resources) != len(result.Run.Resources) {
		t.Fatalf("preflight result = %#v, want exact live projection", result)
	}
}

func TestSweepProjectsExpiredRunCleanup(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	directory := t.TempDir()
	statePath := filepath.Join(directory, "inventory.json")
	requestPath := writeCloudSimJSON(t, directory, "request.json", validCreateRequest(now, "run-expired"))

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"--state", statePath, "create", "--request", requestPath}, &stdout, &stderr, func() time.Time { return now }); code != 0 {
		t.Fatalf("create code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"--state", statePath, "sweep"}, &stdout, &stderr, func() time.Time { return now.Add(3 * time.Hour) }); code != 0 {
		t.Fatalf("sweep code=%d stderr=%q", code, stderr.String())
	}
	var result cloudsim.SweepResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode sweep: %v; output=%q", err, stdout.String())
	}
	if len(result.Destroyed) != 1 || result.Destroyed[0] != "run-expired" || len(result.Retained) != 0 || len(result.Failed) != 0 {
		t.Fatalf("sweep result = %#v, want exact expired cleanup", result)
	}
}

func TestRootProviderSelectionFailsClosedBeforeCloudAccess(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "fake requires state", args: []string{"inventory"}, want: "--state is required"},
		{name: "unknown provider", args: []string{"--provider", "unknown", "inventory"}, want: "unsupported --provider"},
		{name: "Alibaba requires config", args: []string{"--provider", cloudsimalibaba.ProviderName, "inventory"}, want: "--provider-config is required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := execute(test.args, &stdout, &stderr, time.Now); code != 1 {
				t.Fatalf("execute() code = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestDiscoverConfigRejectsWrongProviderAndPreservesFactoryError(t *testing.T) {
	provider := "fake"
	factoryCalls := 0
	command := newDiscoverConfigCommand(&bytes.Buffer{}, &provider, func(string) (cloudsimalibaba.ConfigDiscoveryAPI, error) {
		factoryCalls++
		return nil, nil
	})
	command.SetArgs([]string{"--region", "cn-hangzhou"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "requires --provider alibaba") {
		t.Fatalf("discover fake error = %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d, want zero", factoryCalls)
	}

	provider = cloudsimalibaba.ProviderName
	sentinel := errors.New("credential scope denied")
	command = newDiscoverConfigCommand(&bytes.Buffer{}, &provider, func(string) (cloudsimalibaba.ConfigDiscoveryAPI, error) {
		return nil, sentinel
	})
	command.SetArgs([]string{"--region", "cn-hangzhou"})
	if err := command.Execute(); !errors.Is(err, sentinel) {
		t.Fatalf("discover factory error = %v, want sentinel", err)
	}
}

func validCreateRequest(now time.Time, runID string) cloudsim.CreateRequest {
	return cloudsim.CreateRequest{
		RunID: runID, Provider: "fake", Region: "local", AccountIDHash: "sha256:account",
		Repository: "WuKongIM/WuKongIM", SourceSHA: "0123456789012345678901234567890123456789",
		ScenarioDigest: "sha256:scenario", DeploymentBundleDigest: "sha256:bundle",
		MCPCertificateFingerprint: "sha256:certificate", Preset: cloudsim.PresetSmall,
		ExpiresAt: now.Add(2 * time.Hour), MaxTotalCostMicros: 20_000_000, Currency: "CNY",
	}
}

func writeCloudSimJSON(t *testing.T, directory, name string, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(%s): %v", name, err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
	return path
}

func writeRawCloudSimDocument(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "document.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(document): %v", err)
	}
	return path
}
