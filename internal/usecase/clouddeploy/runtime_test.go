package clouddeploy_test

import (
	"strings"
	"testing"
	"time"

	clouddeploy "github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

func TestRenderHostFilesUsesPrivateTopologyAndPublicHTTPHost(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan, err := clouddeploy.BuildPlan(deploymentLease(now), deploymentManifest(), now)
	if err != nil {
		t.Fatal(err)
	}
	templates := map[string]string{
		"wukongim.toml":  `id={{NODE_ID}} private={{PRIVATE_IPV4}} nodes={{CLUSTER_NODES}} public={{PUBLIC_HTTP_HOST}} load={{LOAD_PRIVATE_IPV4}}`,
		"prometheus.yml": `wk=[{{WUKONGIM_METRICS_TARGETS}}] hosts=[{{NODE_EXPORTER_TARGETS}}]`,
		"Caddyfile":      `ws={{DEMO_WS_UPSTREAMS}} api={{DEMO_API_UPSTREAMS}} manager={{MANAGER_UPSTREAMS}} {$WK_DEMO_BASIC_AUTH_HASH}`,
		"chat-lifecycle.yaml": `run_id: replace-with-unique-formal-run-id
profile: formal
workload:
  workers: 3
  topology: {logical_slot_groups: 12, hash_slots: 256, slot_replicas: 3, channel_replicas: 3}
  sync: {version: 0}
observation:
  service_nodes: [{address: "http://service-1.invalid"}, {address: "http://service-2.invalid"}, {address: "http://service-3.invalid"}]
  workers: [{address: "http://worker-1.invalid"}, {address: "http://worker-2.invalid"}, {address: "http://worker-3.invalid"}]
  host_metrics: [{address: "http://host-metrics-1.invalid"}, {address: "http://host-metrics-2.invalid"}, {address: "http://host-metrics-3.invalid"}]
  api_addrs: ["http://api-1.invalid", "http://api-2.invalid", "http://api-3.invalid"]
  gateway_tcp_addrs: ["gateway-1.invalid:5100", "gateway-2.invalid:5100", "gateway-3.invalid:5100"]
thresholds:
  minimum_data_filesystem_bytes: 500000000000
`,
		"chat-lifecycle-rehearsal.yaml": `run_id: replace-with-unique-rehearsal-run-id
profile: formal
stage: rehearsal
workload:
  workers: 3
  topology: {logical_slot_groups: 12, hash_slots: 256, slot_replicas: 3, channel_replicas: 3}
  sync: {version: 0}
observation:
  service_nodes: [{address: "http://service-1.invalid"}, {address: "http://service-2.invalid"}, {address: "http://service-3.invalid"}]
  workers: [{address: "http://worker-1.invalid"}, {address: "http://worker-2.invalid"}, {address: "http://worker-3.invalid"}]
  host_metrics: [{address: "http://host-metrics-1.invalid"}, {address: "http://host-metrics-2.invalid"}, {address: "http://host-metrics-3.invalid"}]
  api_addrs: ["http://api-1.invalid", "http://api-2.invalid", "http://api-3.invalid"]
  gateway_tcp_addrs: ["gateway-1.invalid:5100", "gateway-2.invalid:5100", "gateway-3.invalid:5100"]
thresholds:
  minimum_data_filesystem_bytes: 500000000000
`,
	}
	node, err := clouddeploy.RenderHostFiles(plan, "service-1", templates)
	if err != nil || len(node) != 1 {
		t.Fatalf("RenderHostFiles(service) = %#v, %v", node, err)
	}
	nodeText := string(node[0].Content)
	if !strings.Contains(nodeText, "id=1") || !strings.Contains(nodeText, "10.42.0.11:7000") ||
		!strings.Contains(nodeText, "public=203.0.113.10") || strings.Contains(nodeText, "{{") {
		t.Fatalf("node config = %s", nodeText)
	}
	load, err := clouddeploy.RenderHostFiles(plan, "load", templates)
	if err != nil || len(load) != 5 {
		t.Fatalf("RenderHostFiles(load) = %#v, %v", load, err)
	}
	joined := ""
	for _, file := range load {
		joined += string(file.Content)
	}
	if strings.Contains(joined, ".invalid") || strings.Contains(joined, "replace-with") ||
		!strings.Contains(joined, "10.42.0.11:5001") || !strings.Contains(joined, "127.0.0.1:19091") ||
		!strings.Contains(joined, "10.42.0.11:19101") ||
		!strings.Contains(joined, "{$WK_DEMO_BASIC_AUTH_HASH}") {
		t.Fatalf("load config = %s", joined)
	}
}
