package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	productconfig "github.com/WuKongIM/WuKongIM/internal/config"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
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
	if err := os.WriteFile(scenario, []byte("version: wkbench/v1\nrun:\n  id: cloud-small\n  duration: 2h\n  report_dir: /tmp/reports\nobjectives:\n  scale: small\n"), 0o600); err != nil {
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
	contractData, err := os.ReadFile(filepath.Join(root, "config", effectiveNodeRuntimeContractName))
	if err != nil {
		t.Fatalf("read effective runtime contract: %v", err)
	}
	var contract EffectiveNodeRuntimeContract
	if err := json.Unmarshal(contractData, &contract); err != nil {
		t.Fatalf("decode effective runtime contract: %v", err)
	}
	expectedContract, err := effectiveNodeRuntimeContractForScale("small")
	if err != nil {
		t.Fatal(err)
	}
	if !runtimeContractValuesEqual(contract, expectedContract) {
		t.Fatalf("rendered runtime contract = %#v, want %#v", contract, expectedContract)
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
	if err := os.WriteFile(scenario, []byte("version: wkbench/v1\nrun:\n  id: test\n  duration: 2h\n  report_dir: /tmp/reports\nobjectives:\n  scale: small\n"), 0o600); err != nil {
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
	if !strings.Contains(units["wukongim.service"], "NoNewPrivileges=true") ||
		!strings.Contains(units["wukongim.service"], "ExecStopPost=-/opt/wukongim/bin/wukongim-cgroup-metrics") {
		t.Fatal("wukongim unit lacks hardening or stop-time cgroup evidence capture")
	}
	if !strings.Contains(units["wukongim-cgroup-metrics.service"], "User=wukongim") ||
		!strings.Contains(units["wukongim-cgroup-metrics.service"], "ExecStart=/opt/wukongim/bin/wukongim-cgroup-metrics --watch") {
		t.Fatal("cgroup memory evidence collector is not installed as a bounded long-running service")
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
	mediumContract, err := effectiveNodeRuntimeContractForScale("medium")
	if err != nil {
		t.Fatal(err)
	}
	config := nodeConfig(1, testBundleSpec("unused").PrivateIPv4, mediumContract)
	if !strings.Contains(config, "hash_slot_count = 256") || !strings.Contains(config, "initial_slot_count = 10") || !strings.Contains(config, "channel_replica_n = 3") || strings.Count(config, "[[cluster.nodes]]") != 3 {
		t.Fatalf("node config does not preserve the three-node 256 hash-slot and 10 Slot Group contract:\n%s", config)
	}
	if !strings.Contains(config, "[conversation]") || !strings.Contains(config, "authority_cache_max_rows = 750000") {
		t.Fatalf("cloud node config does not retain the bounded Medium conversation working set:\n%s", config)
	}
	if !strings.Contains(config, "[delivery]") || !strings.Contains(config, fmt.Sprintf("recipient_worker_concurrency = %d", cloudMediumRecipientWorkerConcurrency)) {
		t.Fatalf("cloud Medium node config does not retain the measured recipient worker capacity:\n%s", config)
	}
	for _, required := range []string{
		"channel_reactor_count = 4",
		"channel_store_append_workers = 8",
		"channel_store_apply_workers = 8",
		"channel_rpc_workers = 96",
		"channel_rpc_batch_max_items = 8",
		"gnet_multicore = true",
		"gnet_num_event_loop = 4",
		"runtime_async_send_workers = 128",
		"runtime_async_send_queue_capacity = 131072",
	} {
		if !strings.Contains(config, required) {
			t.Fatalf("cloud node config lacks explicit runtime contract %q:\n%s", required, config)
		}
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

func TestNodeRuntimeProfileUsesReviewedCloudScale(t *testing.T) {
	tests := []struct {
		scale                string
		wantCacheRows        int
		wantRecipientWorkers int
	}{
		{scale: "small", wantCacheRows: cloudSmallAuthorityCacheMaxRows, wantRecipientWorkers: cloudDefaultRecipientWorkers},
		{scale: "medium", wantCacheRows: cloudMediumAuthorityCacheMaxRows, wantRecipientWorkers: cloudMediumRecipientWorkerConcurrency},
		{scale: "large", wantCacheRows: cloudLargeAuthorityCacheMaxRows, wantRecipientWorkers: cloudDefaultRecipientWorkers},
	}
	for _, test := range tests {
		t.Run(test.scale, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scenario.yaml")
			content := "version: wkbench/v1\nobjectives:\n  scale: " + test.scale + "\n"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			profile, err := nodeRuntimeProfileForScenario(path)
			if err != nil {
				t.Fatalf("nodeRuntimeProfileForScenario() error = %v", err)
			}
			if profile.ConversationAuthorityCacheMaxRows != test.wantCacheRows {
				t.Fatalf("authority cache max rows = %d, want %d", profile.ConversationAuthorityCacheMaxRows, test.wantCacheRows)
			}
			if profile.RecipientWorkerConcurrency != test.wantRecipientWorkers {
				t.Fatalf("recipient worker concurrency = %d, want %d", profile.RecipientWorkerConcurrency, test.wantRecipientWorkers)
			}
			config := nodeConfig(1, testBundleSpec("unused").PrivateIPv4, profile)
			if !strings.Contains(config, fmt.Sprintf("authority_cache_max_rows = %d", test.wantCacheRows)) {
				t.Fatalf("node config does not use %s ceiling %d:\n%s", test.scale, test.wantCacheRows, config)
			}
			workerConfigLine := fmt.Sprintf("recipient_worker_concurrency = %d", test.wantRecipientWorkers)
			if !strings.Contains(config, workerConfigLine) {
				t.Fatalf("node config does not use %s recipient worker capacity %d:\n%s", test.scale, test.wantRecipientWorkers, config)
			}
		})
	}
}

func TestRenderedCloudScaleNodeConfigLoadsReviewedRuntimeProfile(t *testing.T) {
	tests := []struct {
		scale                string
		wantCacheRows        int
		wantRecipientWorkers int
		wantRPCWorkers       int
	}{
		{scale: "small", wantCacheRows: cloudSmallAuthorityCacheMaxRows, wantRecipientWorkers: 100, wantRPCWorkers: cloudDefaultChannelRPCWorkers},
		{scale: "medium", wantCacheRows: cloudMediumAuthorityCacheMaxRows, wantRecipientWorkers: cloudMediumRecipientWorkerConcurrency, wantRPCWorkers: cloudMediumChannelRPCWorkers},
		{scale: "large", wantCacheRows: cloudLargeAuthorityCacheMaxRows, wantRecipientWorkers: 100, wantRPCWorkers: cloudDefaultChannelRPCWorkers},
	}
	for _, test := range tests {
		t.Run(test.scale, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"wukongim", "wkbench", "wkanalysis", "wkcloudview", "prometheus", "node_exporter"} {
				if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			scenario := filepath.Join("..", "..", "..", "..", "docker", "sim", "cloud-"+test.scale+".yaml")
			if err := Render(root, testBundleSpec(scenario)); err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			path := filepath.Join(root, "config", "node-1.toml")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			hasRecipientOverride := strings.Contains(string(raw), "recipient_worker_concurrency")
			if !hasRecipientOverride {
				t.Fatalf("rendered %s config omits the explicit recipient worker contract:\n%s", test.scale, raw)
			}
			loaded, err := productconfig.Load(productconfig.Options{
				Args:    []string{"-config", path},
				Environ: []string{"PATH=/usr/bin"},
			})
			if err != nil {
				t.Fatalf("config.Load() error = %v", err)
			}
			if loaded.Delivery.RecipientWorkerConcurrency != test.wantRecipientWorkers {
				t.Fatalf("recipient worker concurrency = %d, want %d", loaded.Delivery.RecipientWorkerConcurrency, test.wantRecipientWorkers)
			}
			if loaded.Conversation.AuthorityCacheMaxRows != test.wantCacheRows {
				t.Fatalf("authority cache max rows = %d, want %d", loaded.Conversation.AuthorityCacheMaxRows, test.wantCacheRows)
			}
			if loaded.Cluster.Slots.HashSlotCount != 256 || loaded.Cluster.Slots.InitialSlotCount != 10 {
				t.Fatalf("cluster slots = hash %d / initial %d, want 256 / 10", loaded.Cluster.Slots.HashSlotCount, loaded.Cluster.Slots.InitialSlotCount)
			}
			if loaded.Cluster.Slots.ReplicaCount != 3 || loaded.Cluster.Channel.ReplicaCount != 3 {
				t.Fatalf("cluster replicas = Slot %d / Channel %d, want 3 / 3", loaded.Cluster.Slots.ReplicaCount, loaded.Cluster.Channel.ReplicaCount)
			}
			if loaded.Cluster.Channel.ReactorCount != 4 || loaded.Cluster.Channel.StoreAppendWorkers != 8 ||
				loaded.Cluster.Channel.StoreApplyWorkers != 8 || loaded.Cluster.Channel.RPCWorkers != test.wantRPCWorkers ||
				loaded.Cluster.Channel.RPCBatchMaxItems != 8 {
				t.Fatalf("channel runtime = %#v, want reactor/append/apply/RPC/batch 4/8/8/%d/8", loaded.Cluster.Channel, test.wantRPCWorkers)
			}
			if !loaded.Gateway.Transport.Gnet.Multicore || loaded.Gateway.Transport.Gnet.NumEventLoop != 4 || loaded.Gateway.Runtime.AsyncSendWorkers != 128 || loaded.Gateway.Runtime.AsyncSendQueueCapacity != 131072 {
				t.Fatalf("gateway runtime = transport %#v runtime %#v, want multicore/loops/workers/queue true/4/128/131072", loaded.Gateway.Transport.Gnet, loaded.Gateway.Runtime)
			}
			sources := startupConfigSources(loaded.StartupConfigSnapshot)
			for _, key := range effectiveRuntimeContractKeys {
				if got := sources[key]; got != managementusecase.NodeConfigValueSourceTOML {
					t.Fatalf("effective startup source for %s = %q, want toml", key, got)
				}
			}
		})
	}
}

func startupConfigSources(snapshot managementusecase.NodeConfigSnapshot) map[string]string {
	sources := make(map[string]string)
	for _, group := range snapshot.Groups {
		for _, item := range group.Items {
			sources[item.Key] = item.Source
		}
	}
	return sources
}

func TestCloudMediumRecipientWorkerCapacityCoversMeasuredPlanRate(t *testing.T) {
	const (
		clusterPlansPerSecond        = 5100
		worstNodePlanShare           = 0.40
		worstMeanPlanSeconds         = 0.113
		capacityHeadroom             = 1.25
		productDefaultWorkerCapacity = 100
	)
	required := int(math.Ceil(float64(clusterPlansPerSecond) * worstNodePlanShare * worstMeanPlanSeconds * capacityHeadroom))
	if productDefaultWorkerCapacity >= required {
		t.Fatalf("test invariant invalid: product default %d unexpectedly covers required capacity %d", productDefaultWorkerCapacity, required)
	}
	if cloudMediumRecipientWorkerConcurrency < required {
		t.Fatalf("Cloud Medium recipient worker capacity = %d, want at least %d", cloudMediumRecipientWorkerConcurrency, required)
	}
}

func TestCloudMediumRPCWorkerCapacityCoversMeasuredIngressWithHeadroom(t *testing.T) {
	const (
		measuredWorkers  = 50
		measuredIngress  = 3846.48
		minimumIngress   = 4500.0
		capacityHeadroom = 1.50
	)
	contract, err := effectiveNodeRuntimeContractForScale("medium")
	if err != nil {
		t.Fatal(err)
	}
	required := int(math.Ceil(float64(measuredWorkers) * minimumIngress / measuredIngress * capacityHeadroom))
	if contract.ChannelRPCWorkers < required {
		t.Fatalf("Cloud Medium Channel RPC workers = %d, want at least %d from measured %.2f/s at %d workers with %.0f%% headroom",
			contract.ChannelRPCWorkers, required, measuredIngress, measuredWorkers, (capacityHeadroom-1)*100)
	}
}

func TestCgroupV2MetricsCollectorPersistsPeakAndResetEventCounters(t *testing.T) {
	root := t.TempDir()
	cgroup := filepath.Join(root, "cgroup")
	state := filepath.Join(root, "state")
	output := filepath.Join(root, "textfile", "cgroup.prom")
	if err := os.MkdirAll(cgroup, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cgroup, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("memory.current", "700")
	write("memory.peak", "800")
	write("memory.max", "max")
	write("memory.swap.current", "10")
	write("memory.swap.max", "max")
	write("memory.events", "low 0\nhigh 0\nmax 2\noom 3\noom_kill 1\noom_group_kill 0")
	script := filepath.Join(root, "collector.sh")
	if err := os.WriteFile(script, []byte(cgroupMetricsCollector()), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		t.Helper()
		command := exec.Command("bash", script)
		command.Env = append(os.Environ(), "WK_CGROUP_PATH="+cgroup, "WK_CGROUP_METRICS_STATE_DIR="+state, "WK_TEXTFILE_OUTPUT="+output)
		if combined, err := command.CombinedOutput(); err != nil {
			t.Fatalf("collector failed: %v\n%s", err, combined)
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	first := run()
	for _, fragment := range []string{
		"wukongim_service_cgroup_available 1",
		"wukongim_service_cgroup_memory_peak_bytes 800",
		"wukongim_service_cgroup_memory_limit_unlimited 1",
		`wukongim_service_cgroup_memory_events_total{event="oom_kill"} 1`,
	} {
		if !strings.Contains(first, fragment) {
			t.Fatalf("first collector output missing %q:\n%s", fragment, first)
		}
	}

	write("memory.current", "400")
	write("memory.peak", "500")
	write("memory.max", "1024")
	write("memory.events", "low 0\nhigh 0\nmax 0\noom 1\noom_kill 0\noom_group_kill 0")
	second := run()
	for _, fragment := range []string{
		"wukongim_service_cgroup_memory_peak_bytes 800",
		"wukongim_service_cgroup_memory_limit_bytes 1024",
		"wukongim_service_cgroup_memory_limit_unlimited 0",
		`wukongim_service_cgroup_memory_events_total{event="oom"} 4`,
		`wukongim_service_cgroup_memory_events_total{event="oom_kill"} 1`,
	} {
		if !strings.Contains(second, fragment) {
			t.Fatalf("second collector output missing %q:\n%s", fragment, second)
		}
	}
}

func TestCgroupV1MetricsCollectorMapsMemoryAndSwapEvidence(t *testing.T) {
	root := t.TempDir()
	cgroupRoot := filepath.Join(root, "cgroup-root")
	cgroup := filepath.Join(cgroupRoot, "memory", "system.slice", "wukongim.service")
	state := filepath.Join(root, "state")
	output := filepath.Join(root, "textfile", "cgroup.prom")
	if err := os.MkdirAll(cgroup, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cgroup, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("memory.usage_in_bytes", "700")
	write("memory.max_usage_in_bytes", "800")
	write("memory.limit_in_bytes", "9223372036854771712")
	write("memory.memsw.usage_in_bytes", "750")
	write("memory.memsw.limit_in_bytes", "9223372036854771712")
	write("memory.oom_control", "oom_kill_disable 0\nunder_oom 0\noom_kill 1")
	script := filepath.Join(root, "collector.sh")
	if err := os.WriteFile(script, []byte(cgroupMetricsCollector()), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		t.Helper()
		command := exec.Command("bash", script)
		command.Env = append(os.Environ(), "WK_CGROUP_ROOT="+cgroupRoot, "WK_CGROUP_METRICS_STATE_DIR="+state, "WK_TEXTFILE_OUTPUT="+output)
		if combined, err := command.CombinedOutput(); err != nil {
			t.Fatalf("collector failed: %v\n%s", err, combined)
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	first := run()
	for _, fragment := range []string{
		"wukongim_service_cgroup_available 1",
		"wukongim_service_cgroup_memory_current_bytes 700",
		"wukongim_service_cgroup_memory_peak_bytes 800",
		"wukongim_service_cgroup_memory_peak_native_available 1",
		"wukongim_service_cgroup_memory_limit_unlimited 1",
		"wukongim_service_cgroup_memory_swap_current_bytes 50",
		"wukongim_service_cgroup_memory_swap_limit_unlimited 1",
		`wukongim_service_cgroup_memory_events_total{event="oom_kill"} 1`,
	} {
		if !strings.Contains(first, fragment) {
			t.Fatalf("first cgroup v1 collector output missing %q:\n%s", fragment, first)
		}
	}

	write("memory.usage_in_bytes", "400")
	write("memory.max_usage_in_bytes", "500")
	write("memory.limit_in_bytes", "1024")
	write("memory.memsw.usage_in_bytes", "500")
	write("memory.memsw.limit_in_bytes", "1536")
	write("memory.oom_control", "oom_kill_disable 0\nunder_oom 0\noom_kill 0")
	second := run()
	for _, fragment := range []string{
		"wukongim_service_cgroup_memory_peak_bytes 800",
		"wukongim_service_cgroup_memory_limit_bytes 1024",
		"wukongim_service_cgroup_memory_limit_unlimited 0",
		"wukongim_service_cgroup_memory_swap_current_bytes 100",
		"wukongim_service_cgroup_memory_swap_limit_bytes 512",
		"wukongim_service_cgroup_memory_swap_limit_unlimited 0",
		`wukongim_service_cgroup_memory_events_total{event="oom_kill"} 1`,
	} {
		if !strings.Contains(second, fragment) {
			t.Fatalf("second cgroup v1 collector output missing %q:\n%s", fragment, second)
		}
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
			"node-1": {"wukongim", "node-exporter", "wukongim-cgroup-metrics"}, "node-2": {"wukongim", "node-exporter", "wukongim-cgroup-metrics"}, "node-3": {"wukongim", "node-exporter", "wukongim-cgroup-metrics"},
			"sim": {"wkbench-worker", "prometheus", "node-exporter", "wkanalysis", "wkcloudview"},
		},
		CgroupMetricsAvailableNodeIDs: []uint64{1, 2, 3},
		ReadyNodeIDs:                  []uint64{1, 2, 3}, ClusterMemberCount: 3, HashSlotCount: 256, SlotGroupCount: 10,
		HealthySlotLeaders: 256, HealthySlotReplicas: 256, PrometheusTargetsUp: 7, PrometheusTargetsWant: 7,
		AnalysisMCPSelfCheck: true, PublicViewEnabled: true, CloudViewSelfCheck: true,
		WKBenchValidate: true, WKBenchDoctor: true,
		RuntimeScale:                "medium",
		ExpectedNodeRuntimeContract: expectedRuntimeContract(t, "medium"),
		NodeRuntimeContracts: map[string]EffectiveNodeRuntimeContract{
			"node-1": observedRuntimeContract(t, "medium"),
			"node-2": observedRuntimeContract(t, "medium"),
			"node-3": observedRuntimeContract(t, "medium"),
		},
	}
	if result := EvaluateBootstrapGate(snapshot, digest); !result.Passed || result.Retryable {
		t.Fatalf("complete gate = %#v", result)
	}
	snapshot.SlotGroupCount = 256
	if result := EvaluateBootstrapGate(snapshot, digest); result.Passed || !slices.Contains(result.Failures, "logical Slot Raft Group count is not ten") {
		t.Fatalf("wrong Slot Raft Group count gate = %#v, want explicit failure", result)
	}
	snapshot.SlotGroupCount = 10
	snapshot.CgroupMetricsAvailableNodeIDs = []uint64{1, 2}
	if result := EvaluateBootstrapGate(snapshot, digest); result.Passed || !slices.Contains(result.Failures, "service cgroup memory evidence is unavailable") {
		t.Fatalf("missing cgroup metric gate = %#v, want explicit failure", result)
	}
	snapshot.CgroupMetricsAvailableNodeIDs = []uint64{1, 2, 3}
	snapshot.NodeRuntimeContracts["node-2"] = observedRuntimeContract(t, "medium")
	driftedRuntime := snapshot.NodeRuntimeContracts["node-2"]
	driftedRuntime.ChannelRPCWorkers = 500
	snapshot.NodeRuntimeContracts["node-2"] = driftedRuntime
	if result := EvaluateBootstrapGate(snapshot, digest); result.Passed || !slices.Contains(result.Failures, "node-2 effective runtime contract mismatch") {
		t.Fatalf("runtime config drift gate = %#v, want explicit failure", result)
	}
	snapshot.NodeRuntimeContracts["node-2"] = observedRuntimeContract(t, "medium")
	snapshot.NodeRuntimeContracts["node-2"].ValueSources["WK_CLUSTER_CHANNEL_RPC_WORKERS"] = "default"
	if result := EvaluateBootstrapGate(snapshot, digest); result.Passed || !slices.Contains(result.Failures, "node-2 effective runtime contract source is not toml") {
		t.Fatalf("runtime config source drift gate = %#v, want explicit failure", result)
	}
	snapshot.NodeRuntimeContracts["node-2"] = observedRuntimeContract(t, "medium")
	driftedExpected := snapshot.ExpectedNodeRuntimeContract
	driftedExpected.ChannelRPCWorkers = 49
	snapshot.ExpectedNodeRuntimeContract = driftedExpected
	if result := EvaluateBootstrapGate(snapshot, digest); result.Passed || result.Retryable || !slices.Contains(result.Failures, "node-1 effective runtime contract mismatch") {
		t.Fatalf("sealed runtime contract drift gate = %#v, want observed mismatch", result)
	}
	snapshot.ExpectedNodeRuntimeContract = expectedRuntimeContract(t, "medium")
	snapshot.RuntimeScale = ""
	if result := EvaluateBootstrapGate(snapshot, digest); result.Passed || result.Retryable || !slices.Contains(result.Failures, `sealed effective runtime contract scale mismatch: contract="medium" scenario=""`) {
		t.Fatalf("missing rendered scenario scale gate = %#v, want terminal mismatch", result)
	}
	snapshot.RuntimeScale = "medium"
	snapshot.PendingControllerTask = 1
	if result := EvaluateBootstrapGate(snapshot, digest); result.Passed || !result.Retryable || len(result.Failures) == 0 {
		t.Fatalf("incomplete gate = %#v, want retryable fail closed result", result)
	}
}

func observedRuntimeContract(t *testing.T, scale string) EffectiveNodeRuntimeContract {
	t.Helper()
	contract, err := effectiveNodeRuntimeContractForScale(scale)
	if err != nil {
		t.Fatal(err)
	}
	contract.ValueSources = make(map[string]string, len(effectiveRuntimeContractKeys))
	for _, key := range effectiveRuntimeContractKeys {
		contract.ValueSources[key] = "toml"
	}
	return contract
}

func expectedRuntimeContract(t *testing.T, scale string) EffectiveNodeRuntimeContract {
	t.Helper()
	contract, err := effectiveNodeRuntimeContractForScale(scale)
	if err != nil {
		t.Fatal(err)
	}
	return contract
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
