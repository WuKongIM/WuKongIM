package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clouddeployinfra "github.com/WuKongIM/WuKongIM/internal/infra/clouddeploy"
	clouddeploy "github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

func TestReadOfflinePlanRejectsOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized-plan.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte{' '}, maxOfflinePlanBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readOfflinePlan(path); err == nil {
		t.Fatal("readOfflinePlan(oversized) succeeded")
	}
}

func TestInstallOfflineHostRendersRoleSpecificNativePayload(t *testing.T) {
	now := time.Now().UTC()
	bundle, manifest := buildOfflineTestBundle(t)
	plan, err := clouddeploy.BuildPlan(offlineLease(now, manifest), manifest, now)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	writeOfflineJSON(t, planPath, plan)

	nodeRuntime := t.TempDir()
	if err := os.WriteFile(filepath.Join(nodeRuntime, "node.env"), []byte("WK_MANAGER_JWT_SECRET=test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nodeRoot := t.TempDir()
	installed, err := installOfflineHost(offlineInstallOptions{bundleRoot: bundle, planPath: planPath, role: "service-1", rootPrefix: nodeRoot, runtimeDir: nodeRuntime, noSystemd: true})
	if err != nil || installed.BundleDigest != manifest.BundleDigest {
		t.Fatalf("installOfflineHost(service) = %#v, %v", installed, err)
	}
	nodeConfig := readOfflineTestFile(t, filepath.Join(nodeRoot, "etc/wukongim/wukongim.toml"))
	if !strings.Contains(nodeConfig, "id = 1") || !strings.Contains(nodeConfig, "10.42.0.11:7000") ||
		!strings.Contains(nodeConfig, "hash_slot_count = 256") || !strings.Contains(nodeConfig, "initial_slot_count = 12") {
		t.Fatalf("node config = %s", nodeConfig)
	}
	for _, path := range []string{"opt/wukongim/bin/wkbench", "etc/systemd/system/wkbench-host-metrics.service"} {
		if _, err := os.Stat(filepath.Join(nodeRoot, path)); err != nil {
			t.Fatalf("service observation file %s: %v", path, err)
		}
	}
	assertOfflineMode(t, filepath.Join(nodeRoot, "etc/wukongim/secrets/node.env"), 0o600)

	loadRuntime := t.TempDir()
	for _, name := range []string{"load.env", "analysis.env", "analysis-cert.pem", "analysis-key.pem"} {
		if err := os.WriteFile(filepath.Join(loadRuntime, name), []byte("test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	loadRoot := t.TempDir()
	_, err = installOfflineHost(offlineInstallOptions{bundleRoot: bundle, planPath: planPath, role: "load", rootPrefix: loadRoot, runtimeDir: loadRuntime, noSystemd: true})
	if err != nil {
		t.Fatalf("installOfflineHost(load) error = %v", err)
	}
	for _, path := range []string{
		"opt/wukongim/bin/wkbench", "opt/wukongim/bin/prometheus", "opt/wukongim/bin/caddy",
		"opt/wukongim/scripts/run-chat-lifecycle-stage.sh",
		"opt/wukongim/assets/manager/index.html", "opt/wukongim/assets/demo/index.html",
		"etc/systemd/system/wkbench-worker@.service", "etc/systemd/system/wkbench-coordinator.service",
		"etc/systemd/system/wkbench-formal.service", "etc/systemd/system/wkbench-rehearsal.service",
		"etc/wukongim/chat-lifecycle-rehearsal.yaml",
	} {
		if _, err := os.Stat(filepath.Join(loadRoot, path)); err != nil {
			t.Fatalf("load file %s: %v", path, err)
		}
	}
	assertOfflineMode(t, filepath.Join(loadRoot, "opt/wukongim/scripts/run-chat-lifecycle-stage.sh"), 0o755)
	workload := readOfflineTestFile(t, filepath.Join(loadRoot, "etc/wukongim/chat-lifecycle.yaml"))
	if strings.Contains(workload, ".invalid") || !strings.Contains(workload, "10.42.0.11:5001") ||
		!strings.Contains(workload, "10.42.0.11:19101") || !strings.Contains(workload, "mountpoint: /var/lib/wukongim-cloud") ||
		!strings.Contains(workload, "minimum_data_filesystem_bytes: 500000000000") {
		t.Fatalf("workload = %s", workload)
	}
	caddy := readOfflineTestFile(t, filepath.Join(loadRoot, "etc/wukongim/Caddyfile"))
	if strings.Contains(caddy, "{{") || !strings.Contains(caddy, "10.42.0.11:5200") || !strings.Contains(caddy, "{$WK_DEMO_BASIC_AUTH_HASH}") {
		t.Fatalf("Caddyfile = %s", caddy)
	}
	for _, name := range []string{"load.env", "analysis.env", "analysis-cert.pem", "analysis-key.pem"} {
		assertOfflineMode(t, filepath.Join(loadRoot, "etc/wukongim/secrets", name), 0o600)
	}
}

func TestActivateOfflineLoadKeepsCoordinatorDormant(t *testing.T) {
	fakeBin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	systemctl := filepath.Join(fakeBin, "systemctl")
	content := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >>\"$WK_TEST_SYSTEMCTL_LOG\"\n"
	if err := os.WriteFile(systemctl, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WK_TEST_SYSTEMCTL_LOG", logPath)
	if err := activateOfflineUnits("load"); err != nil {
		t.Fatal(err)
	}
	log := readOfflineTestFile(t, logPath)
	for _, unit := range []string{"wkbench-worker@1.service", "wkbench-worker@2.service", "wkbench-worker@3.service", "prometheus.service"} {
		if !strings.Contains(log, unit) {
			t.Fatalf("systemctl log omits %s: %s", unit, log)
		}
	}
	if strings.Contains(log, "wkbench-coordinator.service") || strings.Contains(log, "wkbench-formal.service") || strings.Contains(log, "wkbench-rehearsal.service") {
		t.Fatalf("Deployment activated a workload coordinator: %s", log)
	}
}

func buildOfflineTestBundle(t *testing.T) (string, clouddeploy.Manifest) {
	t.Helper()
	root := t.TempDir()
	intent := clouddeploy.DefaultIntent("0123456789012345678901234567890123456789", "abcdefabcdefabcdefabcdefabcdefabcdefabcd")
	for _, name := range intent.OfflineBinaries {
		writeOfflineELF(t, filepath.Join(root, "bin", name))
	}
	for _, relative := range []string{"assets/manager/index.html", "assets/demo/index.html"} {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("<!doctype html>\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	formal, err := os.ReadFile(filepath.Join("..", "..", "configs", "wkbench", "chat-lifecycle", "formal.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	formalPath := filepath.Join(root, "config", "chat-lifecycle.yaml")
	if err := os.MkdirAll(filepath.Dir(formalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(formalPath, formal, 0o644); err != nil {
		t.Fatal(err)
	}
	rehearsal, err := os.ReadFile(filepath.Join("..", "..", "configs", "wkbench", "chat-lifecycle", "rehearsal.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "chat-lifecycle-rehearsal.yaml"), rehearsal, 0o644); err != nil {
		t.Fatal(err)
	}
	directory, err := clouddeployinfra.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := clouddeploy.Seal(directory, intent.SourceSHA, intent.ControlSHA)
	if err != nil {
		t.Fatal(err)
	}
	return root, manifest
}

func offlineLease(now time.Time, manifest clouddeploy.Manifest) clouddeploy.LeaseInventory {
	return clouddeploy.LeaseInventory{
		LeaseID: "lease-offline", RequestID: "request-offline", Repository: "WuKongIM/WuKongIM", Provider: "fake", Region: "local", Zone: "zone-a",
		PlanDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", SourceSHA: manifest.SourceSHA,
		BundleDigest: manifest.BundleDigest, State: "active", CreatedAt: now, ExpiresAt: now.Add(96 * time.Hour),
		Budget: clouddeploy.DeploymentBudget{Currency: "CNY", LimitMicros: clouddeploy.FormalBudgetHardMicros, OperationalStopMicros: clouddeploy.FormalBudgetStopMicros, EstimatedCostMicros: 100_000_000, LineItems: []clouddeploy.DeploymentBudgetLineItem{{Kind: "lease", Role: "all", Quantity: 1, CostMicros: 100_000_000}}},
		Resources: []clouddeploy.LeaseResource{
			{ID: "i-service-1", Kind: "instance", Role: "service", PrivateAddress: "10.42.0.11"},
			{ID: "i-service-2", Kind: "instance", Role: "service", PrivateAddress: "10.42.0.12"},
			{ID: "i-service-3", Kind: "instance", Role: "service", PrivateAddress: "10.42.0.13"},
			{ID: "i-load", Kind: "instance", Role: "load", PrivateAddress: "10.42.0.20"},
			{ID: "d-service-1", Kind: "data_disk", Role: "service", ParentID: "i-service-1", SizeBytes: 500_000_000_000},
			{ID: "d-service-2", Kind: "data_disk", Role: "service", ParentID: "i-service-2", SizeBytes: 500_000_000_000},
			{ID: "d-service-3", Kind: "data_disk", Role: "service", ParentID: "i-service-3", SizeBytes: 500_000_000_000},
			{ID: "d-load", Kind: "data_disk", Role: "load", ParentID: "i-load", SizeBytes: 200_000_000_000},
			{ID: "eip-load", Kind: "public_address", Role: "load", PublicAddress: "203.0.113.10"},
		},
	}
}

func writeOfflineELF(t *testing.T, path string) {
	t.Helper()
	header := make([]byte, 64)
	copy(header, []byte("\x7fELF"))
	header[4], header[5], header[6], header[7] = 2, 1, 1, 0
	binary.LittleEndian.PutUint16(header[16:18], 2)
	binary.LittleEndian.PutUint16(header[18:20], 62)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, header, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeOfflineJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readOfflineTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertOfflineMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
	}
}
