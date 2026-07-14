package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"gopkg.in/yaml.v3"
)

func TestRenderSealVerifyAndTamperDetection(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wukongim", "wkbench", "wkanalysis", "prometheus", "node_exporter"} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte("static-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scenario := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(scenario, []byte("version: wkbench/v1\nrun:\n  id: cloud-small\n  duration: 2h\n  report_dir: /tmp/reports\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := testBundleSpec(scenario)
	if err := Render(root, spec); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	manifest, err := Seal(root, spec)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if !strings.HasPrefix(manifest.BundleDigest, "sha256:") {
		t.Fatalf("bundle digest = %q", manifest.BundleDigest)
	}
	if _, err := Verify(root); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	renderedScenario, err := os.ReadFile(filepath.Join(root, "config", "scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(renderedScenario), "id: run-1") || !strings.Contains(string(renderedScenario), "duration: 24h0m0s") || !strings.Contains(string(renderedScenario), "report_dir: /var/lib/wukongim-cloud/reports/run-1") {
		t.Fatalf("scenario does not bind requested duration and run report directory:\n%s", renderedScenario)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "wkbench"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(root); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("Verify(tampered) error = %v, want ErrInvalidBundle", err)
	}
}

func TestRenderedContractsUseSystemdAndThreeNode256Slots(t *testing.T) {
	units := systemdUnits()
	if !strings.Contains(units["wkbench-run.service"], "Restart=no") || strings.Contains(units["wkbench-run.service"], "Restart=always") {
		t.Fatal("wkbench run is not a non-restarting systemd unit")
	}
	if !strings.Contains(units["wukongim.service"], "NoNewPrivileges=true") {
		t.Fatal("wukongim unit lacks hardening")
	}
	config := nodeConfig(1, testBundleSpec("unused").PrivateIPv4)
	if !strings.Contains(config, "hash_slot_count = 256") || strings.Count(config, "[[cluster.nodes]]") != 3 {
		t.Fatalf("node config does not preserve three-node 256-slot contract:\n%s", config)
	}
	prometheus := prometheusConfig(testBundleSpec("unused").PrivateIPv4)
	if !strings.Contains(prometheus, `labels: {role: "sim"}`) || strings.Count(prometheus, "labels: {role:") != 7 {
		t.Fatalf("Prometheus targets do not preserve per-role attribution:\n%s", prometheus)
	}
	workers := workerConfig(testBundleSpec("unused").SimulatorSourceIPv4)
	if !strings.Contains(workers, "10.42.0.22") || !strings.Contains(workers, "port_max: 65535") {
		t.Fatalf("worker source pool is incomplete:\n%s", workers)
	}
	var workerSet model.WorkerSet
	if err := yaml.Unmarshal([]byte(workers), &workerSet); err != nil || len(workerSet.Workers) != 1 || model.TCPSourceCapacity(workerSet.Workers[0].TCPSource) < 100000 {
		t.Fatalf("worker source capacity cannot sustain the 100k profile: workers=%#v err=%v", workerSet, err)
	}
}

func TestBootstrapGateFailsClosedAndPassesOnlyCompleteSnapshot(t *testing.T) {
	digest := "sha256:bundle"
	snapshot := BootstrapSnapshot{
		BundleDigests: map[string]string{"node-1": digest, "node-2": digest, "node-3": digest, "sim": digest},
		ActiveServices: map[string][]string{
			"node-1": {"wukongim", "node-exporter"}, "node-2": {"wukongim", "node-exporter"}, "node-3": {"wukongim", "node-exporter"},
			"sim": {"wkbench-worker", "prometheus", "node-exporter", "wkanalysis"},
		},
		ReadyNodeIDs: []uint64{1, 2, 3}, ClusterMemberCount: 3, HashSlotCount: 256,
		HealthySlotLeaders: 256, HealthySlotReplicas: 256, PrometheusTargetsUp: 7, PrometheusTargetsWant: 7,
		AnalysisMCPSelfCheck: true, WKBenchValidate: true, WKBenchDoctor: true,
	}
	if result := EvaluateBootstrapGate(snapshot, digest); !result.Passed {
		t.Fatalf("complete gate = %#v", result)
	}
	snapshot.PendingControllerTask = 1
	if result := EvaluateBootstrapGate(snapshot, digest); result.Passed || len(result.Failures) == 0 {
		t.Fatalf("incomplete gate = %#v, want fail closed", result)
	}
}

func testBundleSpec(scenario string) BundleSpec {
	return BundleSpec{
		RunID: "run-1", SourceSHA: "0123456789012345678901234567890123456789", ScenarioPath: scenario,
		ScenarioDigest: "sha256:scenario", Duration: 24 * time.Hour,
		PrivateIPv4:         map[string]string{"node-1": "10.42.0.11", "node-2": "10.42.0.12", "node-3": "10.42.0.13", "sim": "10.42.0.20"},
		SimulatorSourceIPv4: []string{"10.42.0.20", "10.42.0.21", "10.42.0.22"},
	}
}
