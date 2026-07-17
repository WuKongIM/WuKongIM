package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	for _, name := range []string{"wukongim", "wkbench", "wkanalysis", "wkcloudview", "prometheus", "node_exporter"} {
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
	if !strings.Contains(string(renderedScenario), "id: run-1") || !strings.Contains(string(renderedScenario), "duration: 30m0s") || !strings.Contains(string(renderedScenario), "report_dir: /var/lib/wukongim-cloud/reports/run-1") {
		t.Fatalf("scenario does not bind requested duration and run report directory:\n%s", renderedScenario)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "wkbench"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(root); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("Verify(tampered) error = %v, want ErrInvalidBundle", err)
	}
}

func TestRenderOmitsCloudViewWhenPublicObservationDisabled(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wukongim", "wkbench", "wkanalysis", "prometheus", "node_exporter"} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scenario := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(scenario, []byte("version: wkbench/v1\nrun:\n  id: test\n  duration: 2h\n  report_dir: /tmp/reports\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := testBundleSpec(scenario)
	spec.PublicViewEnabled = false
	if err := Render(root, spec); err != nil {
		t.Fatalf("Render() with Cloud View disabled: %v", err)
	}
	for _, path := range []string{"config/cloud-view.json", "systemd/wkcloudview.service"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("%s exists while Cloud View disabled: %v", path, err)
		}
	}
	unit, err := os.ReadFile(filepath.Join(root, "systemd/wkbench-run.service"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unit), "wkcloudview") {
		t.Fatalf("disabled wkbench unit depends on wkcloudview:\n%s", unit)
	}
}

func TestRenderedContractsUseSystemdAndThreeNode256Slots(t *testing.T) {
	units := systemdUnits(true, 48*time.Hour)
	if !strings.Contains(units["wkbench-run.service"], "Restart=no") || strings.Contains(units["wkbench-run.service"], "Restart=always") {
		t.Fatal("wkbench run is not a non-restarting systemd unit")
	}
	if !strings.Contains(units["wkbench-run.service"], "benchmark_purity") &&
		!strings.Contains(units["wkbench-run.service"], "annotate-report") {
		t.Fatal("wkbench run does not annotate the final report with Cloud View purity")
	}
	if !strings.Contains(units["wukongim.service"], "NoNewPrivileges=true") {
		t.Fatal("wukongim unit lacks hardening")
	}
	if !strings.Contains(units["wkcloudview.service"], "TCP/19443") ||
		!strings.Contains(units["wkcloudview.service"], "EnvironmentFile=/etc/wukongim/sim.env") {
		t.Fatalf("Cloud View unit does not preserve public listener and dynamic public URL contract:\n%s", units["wkcloudview.service"])
	}
	if !strings.Contains(units["prometheus.service"], "WK_CLOUD_VIEW_PROMETHEUS_EXTERNAL_URL") ||
		!strings.Contains(units["prometheus.service"], "--web.route-prefix=/") ||
		!strings.Contains(units["prometheus.service"], "--storage.tsdb.retention.time=72h") {
		t.Fatalf("Prometheus unit lacks proxied public path contract:\n%s", units["prometheus.service"])
	}
	weekUnits := systemdUnits(true, 168*time.Hour)
	if !strings.Contains(weekUnits["prometheus.service"], "--storage.tsdb.retention.time=192h") {
		t.Fatalf("week-long Prometheus unit lacks eight-day retention:\n%s", weekUnits["prometheus.service"])
	}
	cloudView := cloudViewConfig(testBundleSpec("unused").RunID, testBundleSpec("unused").PrivateIPv4)
	for _, required := range []string{`"listen_addr": "0.0.0.0:19443"`, `"id": 1`, `"id": 2`, `"id": 3`,
		`"prometheus_url": "http://127.0.0.1:9090"`} {
		if !strings.Contains(cloudView, required) {
			t.Fatalf("Cloud View config lacks %s:\n%s", required, cloudView)
		}
	}
	config := nodeConfig(1, testBundleSpec("unused").PrivateIPv4)
	if !strings.Contains(config, "hash_slot_count = 256") || !strings.Contains(config, "initial_slot_count = 10") || !strings.Contains(config, "channel_replica_n = 3") || strings.Count(config, "[[cluster.nodes]]") != 3 {
		t.Fatalf("node config does not preserve the three-node 256 hash-slot and 10 Slot Group contract:\n%s", config)
	}
	prometheus := prometheusConfig(testBundleSpec("unused").PrivateIPv4)
	if !strings.Contains(prometheus, "scrape_interval: 15s") ||
		!strings.Contains(prometheus, "evaluation_interval: 15s") ||
		!strings.Contains(prometheus, `labels: {role: "sim"}`) || strings.Count(prometheus, "labels: {role:") != 7 {
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
	client := workerSet.Workers[0].Client
	if client == nil || client.SendQueueCapacity != 16 || client.MaxInflight != 1 ||
		client.ReadBufferSize != 1024 || client.FrameBufferSize != 4 {
		t.Fatalf("cloud worker must use the bounded stability client profile: client=%#v", client)
	}
}

func TestBundleSpecAllowsWeekLongStabilityDuration(t *testing.T) {
	spec := testBundleSpec("scenario.yaml")
	spec.Duration = 168 * time.Hour
	if err := validateSpec(spec); err != nil {
		t.Fatalf("validateSpec(168h) error = %v", err)
	}
}

func TestBootstrapGateFailsClosedAndPassesOnlyCompleteSnapshot(t *testing.T) {
	digest := "sha256:bundle"
	snapshot := BootstrapSnapshot{
		BundleDigests: map[string]string{"node-1": digest, "node-2": digest, "node-3": digest, "sim": digest},
		ActiveServices: map[string][]string{
			"node-1": {"wukongim", "node-exporter"}, "node-2": {"wukongim", "node-exporter"}, "node-3": {"wukongim", "node-exporter"},
			"sim": {"wkbench-worker", "prometheus", "node-exporter", "wkanalysis", "wkcloudview"},
		},
		ReadyNodeIDs: []uint64{1, 2, 3}, ClusterMemberCount: 3, HashSlotCount: 256, SlotGroupCount: 10,
		HealthySlotLeaders: 256, HealthySlotReplicas: 256, PrometheusTargetsUp: 7, PrometheusTargetsWant: 7,
		AnalysisMCPSelfCheck: true, PublicViewEnabled: true, CloudViewSelfCheck: true,
		WKBenchValidate: true, WKBenchDoctor: true,
	}
	if result := EvaluateBootstrapGate(snapshot, digest); !result.Passed {
		t.Fatalf("complete gate = %#v", result)
	}
	snapshot.SlotGroupCount = 256
	if result := EvaluateBootstrapGate(snapshot, digest); result.Passed || !slices.Contains(result.Failures, "logical Slot Raft Group count is not ten") {
		t.Fatalf("wrong Slot Raft Group count gate = %#v, want explicit failure", result)
	}
	snapshot.SlotGroupCount = 10
	snapshot.PendingControllerTask = 1
	if result := EvaluateBootstrapGate(snapshot, digest); result.Passed || len(result.Failures) == 0 {
		t.Fatalf("incomplete gate = %#v, want fail closed", result)
	}
}

func testBundleSpec(scenario string) BundleSpec {
	return BundleSpec{
		RunID: "run-1", SourceSHA: "0123456789012345678901234567890123456789", ScenarioPath: scenario,
		ScenarioDigest: "sha256:scenario", Duration: 30 * time.Minute,
		PrivateIPv4:         map[string]string{"node-1": "10.42.0.11", "node-2": "10.42.0.12", "node-3": "10.42.0.13", "sim": "10.42.0.20"},
		SimulatorSourceIPv4: []string{"10.42.0.20", "10.42.0.21", "10.42.0.22"},
		PublicViewEnabled:   true,
	}
}
