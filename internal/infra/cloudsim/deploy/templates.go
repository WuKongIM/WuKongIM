package deploy

import (
	"fmt"
	"strings"
	"time"
)

const (
	// effectiveNodeRuntimeContractName is the bundle-relative machine-readable runtime contract.
	effectiveNodeRuntimeContractName = "effective-node-runtime-contract.json"
	// EffectiveNodeRuntimeContractSchemaV1 is the exact runtime contract schema emitted into bundles and checked at bootstrap.
	EffectiveNodeRuntimeContractSchemaV1 = "wukongim/cloud-effective-node-runtime-contract/v1"
	cloudPhysicalHashSlotCount           = 256
	cloudLogicalSlotGroupCount           = 10
	cloudSlotReplicaCount                = 3
	cloudChannelReplicaCount             = 3
	cloudChannelReactorCount             = 4
	cloudChannelStoreAppendWorkers       = 8
	cloudChannelStoreApplyWorkers        = 8
	cloudChannelRPCWorkers               = 50
	// cloudChannelRPCBatchMaxItems is the bounded value accepted by the real
	// three-process Medium-shaped 256/10/3 gate at 4,500/s actual ingress.
	// The prior 61.75ms cycle and batch-four saturation corroborate the choice
	// but are not treated as an ingress-to-RPC-item capacity equation.
	cloudChannelRPCBatchMaxItems       = 8
	cloudGatewayGnetEventLoops         = 4
	cloudGatewayAsyncSendWorkers       = 128
	cloudGatewayAsyncSendQueueCapacity = 131_072
	cloudDefaultRecipientWorkers       = 100
	// cloudSmallAuthorityCacheMaxRows preserves the default cache ceiling while
	// covering the complete 30,860-row Cloud Small conversation working set.
	cloudSmallAuthorityCacheMaxRows = 100_000
	// cloudMediumAuthorityCacheMaxRows covers the complete 569,520-row Cloud
	// Medium working set plus bounded churn and temporary leader skew.
	cloudMediumAuthorityCacheMaxRows = 750_000
	// cloudLargeAuthorityCacheMaxRows covers the complete 15,593,050-row Cloud
	// Large working set plus bounded churn and temporary leader skew.
	cloudLargeAuthorityCacheMaxRows = 20_000_000
	// cloudMediumRecipientWorkerConcurrency is the measured Cloud Medium plan
	// capacity. At 108-113 ms per plan, 320 workers cover the reviewed 5,100
	// plans/s cluster load when the busiest node owns 40 percent of the ten Slot
	// Groups, with bounded headroom. Other scales keep the product default until
	// they have their own completed calibration.
	cloudMediumRecipientWorkerConcurrency = 320
)

var effectiveRuntimeContractKeys = []string{
	"WK_CLUSTER_HASH_SLOT_COUNT",
	"WK_CLUSTER_INITIAL_SLOT_COUNT",
	"WK_CLUSTER_SLOT_REPLICA_N",
	"WK_CLUSTER_CHANNEL_REPLICA_N",
	"WK_CLUSTER_CHANNEL_REACTOR_COUNT",
	"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS",
	"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS",
	"WK_CLUSTER_CHANNEL_RPC_WORKERS",
	"WK_CLUSTER_CHANNEL_RPC_BATCH_MAX_ITEMS",
	"WK_GATEWAY_GNET_MULTICORE",
	"WK_GATEWAY_GNET_NUM_EVENT_LOOP",
	"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS",
	"WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY",
	"WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY",
	"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS",
}

// EffectiveNodeRuntimeContract is the exact non-secret runtime shape deployed to every cloud node.
type EffectiveNodeRuntimeContract struct {
	// Schema is the versioned machine-readable contract identifier.
	Schema string `json:"schema"`
	// Scale is the reviewed Cloud Simulation scale that selected this contract.
	Scale string `json:"scale"`
	// PhysicalHashSlotCount is the number of stable physical hash-slot fences.
	PhysicalHashSlotCount int `json:"physical_hash_slot_count"`
	// LogicalSlotGroupCount is the number of logical Slot Raft Groups that own hash slots.
	LogicalSlotGroupCount int `json:"logical_slot_group_count"`
	// SlotReplicaCount is the desired voter count for every logical Slot Raft Group.
	SlotReplicaCount int `json:"slot_replica_count"`
	// ChannelReplicaCount is the desired replica count for newly created Channels.
	ChannelReplicaCount int `json:"channel_replica_count"`
	// ChannelReactorCount is the explicit Channel reactor partition count.
	ChannelReactorCount int `json:"channel_reactor_count"`
	// ChannelStoreAppendWorkers is the explicit leader append store-worker bound.
	ChannelStoreAppendWorkers int `json:"channel_store_append_workers"`
	// ChannelStoreApplyWorkers is the explicit follower apply store-worker bound.
	ChannelStoreApplyWorkers int `json:"channel_store_apply_workers"`
	// ChannelRPCWorkers is the explicit Channel replication RPC-worker bound.
	ChannelRPCWorkers int `json:"channel_rpc_workers"`
	// ChannelRPCBatchMaxItems is the bounded same-target replication batch cap.
	ChannelRPCBatchMaxItems int `json:"channel_rpc_batch_max_items"`
	// GatewayGnetMulticore enables the reviewed multi-event-loop gateway shape.
	GatewayGnetMulticore bool `json:"gateway_gnet_multicore"`
	// GatewayGnetEventLoops is the explicit gnet event-loop count.
	GatewayGnetEventLoops int `json:"gateway_gnet_event_loops"`
	// GatewayAsyncSendWorkers is the bounded async SEND worker count.
	GatewayAsyncSendWorkers int `json:"gateway_async_send_workers"`
	// GatewayAsyncSendQueueCapacity is the bounded async SEND queue capacity.
	GatewayAsyncSendQueueCapacity int `json:"gateway_async_send_queue_capacity"`
	// RecipientWorkerConcurrency is the reviewed delivery recipient worker count.
	RecipientWorkerConcurrency int `json:"recipient_worker_concurrency"`
	// ConversationAuthorityCacheMaxRows is the reviewed per-node authority cache ceiling.
	ConversationAuthorityCacheMaxRows int `json:"conversation_authority_cache_max_rows"`
	// ValueSources records the observed Manager source for every critical key during bootstrap.
	// It is omitted from the immutable expected contract stored in the bundle.
	ValueSources map[string]string `json:"value_sources,omitempty"`
}

// effectiveNodeRuntimeContractForScale returns the immutable reviewed runtime
// shape embedded in a bundle for one supported Cloud Simulation scale.
func effectiveNodeRuntimeContractForScale(scale string) (EffectiveNodeRuntimeContract, error) {
	contract := EffectiveNodeRuntimeContract{
		Schema:                        EffectiveNodeRuntimeContractSchemaV1,
		Scale:                         strings.ToLower(strings.TrimSpace(scale)),
		PhysicalHashSlotCount:         cloudPhysicalHashSlotCount,
		LogicalSlotGroupCount:         cloudLogicalSlotGroupCount,
		SlotReplicaCount:              cloudSlotReplicaCount,
		ChannelReplicaCount:           cloudChannelReplicaCount,
		ChannelReactorCount:           cloudChannelReactorCount,
		ChannelStoreAppendWorkers:     cloudChannelStoreAppendWorkers,
		ChannelStoreApplyWorkers:      cloudChannelStoreApplyWorkers,
		ChannelRPCWorkers:             cloudChannelRPCWorkers,
		ChannelRPCBatchMaxItems:       cloudChannelRPCBatchMaxItems,
		GatewayGnetMulticore:          true,
		GatewayGnetEventLoops:         cloudGatewayGnetEventLoops,
		GatewayAsyncSendWorkers:       cloudGatewayAsyncSendWorkers,
		GatewayAsyncSendQueueCapacity: cloudGatewayAsyncSendQueueCapacity,
		RecipientWorkerConcurrency:    cloudDefaultRecipientWorkers,
	}
	switch contract.Scale {
	case "small":
		contract.ConversationAuthorityCacheMaxRows = cloudSmallAuthorityCacheMaxRows
	case "medium":
		contract.ConversationAuthorityCacheMaxRows = cloudMediumAuthorityCacheMaxRows
		contract.RecipientWorkerConcurrency = cloudMediumRecipientWorkerConcurrency
	case "large":
		contract.ConversationAuthorityCacheMaxRows = cloudLargeAuthorityCacheMaxRows
	default:
		return EffectiveNodeRuntimeContract{}, fmt.Errorf("%w: scenario objectives.scale must be small, medium, or large", ErrInvalidBundle)
	}
	return contract, nil
}

// runtimeContractValuesEqual compares immutable runtime dimensions while
// leaving each observed node's source evidence to the Bootstrap Gate.
func runtimeContractValuesEqual(left, right EffectiveNodeRuntimeContract) bool {
	return left.Schema == right.Schema &&
		left.Scale == right.Scale &&
		left.PhysicalHashSlotCount == right.PhysicalHashSlotCount &&
		left.LogicalSlotGroupCount == right.LogicalSlotGroupCount &&
		left.SlotReplicaCount == right.SlotReplicaCount &&
		left.ChannelReplicaCount == right.ChannelReplicaCount &&
		left.ChannelReactorCount == right.ChannelReactorCount &&
		left.ChannelStoreAppendWorkers == right.ChannelStoreAppendWorkers &&
		left.ChannelStoreApplyWorkers == right.ChannelStoreApplyWorkers &&
		left.ChannelRPCWorkers == right.ChannelRPCWorkers &&
		left.ChannelRPCBatchMaxItems == right.ChannelRPCBatchMaxItems &&
		left.GatewayGnetMulticore == right.GatewayGnetMulticore &&
		left.GatewayGnetEventLoops == right.GatewayGnetEventLoops &&
		left.GatewayAsyncSendWorkers == right.GatewayAsyncSendWorkers &&
		left.GatewayAsyncSendQueueCapacity == right.GatewayAsyncSendQueueCapacity &&
		left.RecipientWorkerConcurrency == right.RecipientWorkerConcurrency &&
		left.ConversationAuthorityCacheMaxRows == right.ConversationAuthorityCacheMaxRows
}

func cloudViewConfig(runID string, addresses map[string]string) string {
	return fmt.Sprintf(`{
  "listen_addr": "0.0.0.0:19443",
  "run_id": %q,
  "public_base_url": "http://127.0.0.1:19443",
  "prometheus_url": "http://127.0.0.1:9090",
  "state_path": "/var/lib/wukongim-cloud/cloud-view-state.json",
  "metrics_path": "/var/lib/wukongim/textfile/cloud-view.prom",
  "nodes": [
    {"id": 1, "api_base_url": "http://%s:5001", "manager_base_url": "http://%s:5301", "websocket_base_url": "http://%s:5200"},
    {"id": 2, "api_base_url": "http://%s:5001", "manager_base_url": "http://%s:5301", "websocket_base_url": "http://%s:5200"},
    {"id": 3, "api_base_url": "http://%s:5001", "manager_base_url": "http://%s:5301", "websocket_base_url": "http://%s:5200"}
  ],
  "limits": {
    "http_requests_per_second_per_ip": 30,
    "http_burst_per_ip": 60,
    "http_requests_per_second_global": 200,
    "http_burst_global": 400,
    "websocket_connections_per_ip": 20,
    "websocket_connections_global": 64
  }
}
`, runID,
		addresses["node-1"], addresses["node-1"], addresses["node-1"],
		addresses["node-2"], addresses["node-2"], addresses["node-2"],
		addresses["node-3"], addresses["node-3"], addresses["node-3"])
}

func nodeConfig(nodeID int, addresses map[string]string, contract EffectiveNodeRuntimeContract) string {
	deliveryConfig := fmt.Sprintf(`
[delivery]
# Runs recipient-authority delivery plans outside channel append writers.
# Cloud Medium uses its measured capacity; other reviewed scales pin the
# product default explicitly so paid runs never inherit loader drift.
recipient_worker_concurrency = %d
`, contract.RecipientWorkerConcurrency)
	return fmt.Sprintf(`[node]
id = %d
data_dir = "/var/lib/wukongim-cloud/node"

[cluster]
listen_addr = "0.0.0.0:7000"
hash_slot_count = %d
initial_slot_count = %d
slot_replica_n = %d
channel_replica_n = %d
slot_tick_interval = "50ms"
slot_heartbeat_tick = 2
slot_election_tick = 20
channel_reactor_count = %d
channel_store_append_workers = %d
channel_store_apply_workers = %d
channel_rpc_workers = %d
channel_rpc_batch_max_items = %d

[[cluster.nodes]]
id = 1
addr = "%s:7000"

[[cluster.nodes]]
id = 2
addr = "%s:7000"

[[cluster.nodes]]
id = 3
addr = "%s:7000"

[api]
listen_addr = "0.0.0.0:5001"
external_tcp_addr = "%s:5100"
external_ws_addr = "ws://%s:5200"

[bench]
api_enable = true
api_max_batch_size = 10000
api_max_payload_bytes = 10485760

[manager]
listen_addr = "0.0.0.0:5301"
auth_on = true
jwt_issuer = "wukongim-cloud-sim"
jwt_expire = "1h"

[gateway]
gnet_multicore = %t
gnet_num_event_loop = %d
runtime_async_send_workers = %d
runtime_async_send_queue_capacity = %d

%s
[conversation]
# Bounds per-node active-conversation authority rows for the selected reviewed
# scale. Entries allocate on demand; the ceiling includes bounded churn
# headroom even when actual Raft leaders are temporarily skewed.
authority_cache_max_rows = %d

[[gateway.listeners]]
name = "tcp-wkproto"
network = "tcp"
address = "0.0.0.0:5100"
transport = "gnet"
protocol = "wkproto"

[[gateway.listeners]]
name = "ws-gateway"
network = "websocket"
address = "0.0.0.0:5200"
transport = "gnet"
protocol = "wsmux"

[log]
dir = "/var/lib/wukongim-cloud/logs"
level = "info"

[observability]
debug_api_enable = true
metrics_enable = true

[prometheus]
query_base_url = "http://%s:9090"

[diagnostics]
enable = true
`, nodeID,
		contract.PhysicalHashSlotCount, contract.LogicalSlotGroupCount,
		contract.SlotReplicaCount, contract.ChannelReplicaCount,
		contract.ChannelReactorCount, contract.ChannelStoreAppendWorkers,
		contract.ChannelStoreApplyWorkers, contract.ChannelRPCWorkers,
		contract.ChannelRPCBatchMaxItems,
		addresses["node-1"], addresses["node-2"], addresses["node-3"],
		addresses[fmt.Sprintf("node-%d", nodeID)], addresses[fmt.Sprintf("node-%d", nodeID)],
		contract.GatewayGnetMulticore, contract.GatewayGnetEventLoops,
		contract.GatewayAsyncSendWorkers, contract.GatewayAsyncSendQueueCapacity,
		deliveryConfig, contract.ConversationAuthorityCacheMaxRows, addresses["sim"])
}

func targetConfig(addresses map[string]string) string {
	return fmt.Sprintf(`name: cloud-three-node-cluster
api:
  addrs:
    - http://%s:5001
    - http://%s:5001
    - http://%s:5001
gateway:
  tcp:
    addrs:
      - %s:5100
      - %s:5100
      - %s:5100
bench_api:
  enabled: true
  addrs:
    - http://%s:5001
    - http://%s:5001
    - http://%s:5001
  token: ${WK_BENCH_API_TOKEN}
metrics:
  enabled: true
  addrs:
    - http://%s:5001/metrics
    - http://%s:5001/metrics
    - http://%s:5001/metrics
`, addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses["node-1"], addresses["node-2"], addresses["node-3"])
}

func workerConfig(sourceAddresses []string) string {
	addresses := ""
	for _, address := range sourceAddresses {
		addresses += fmt.Sprintf("      - %s\n", address)
	}
	return fmt.Sprintf(`workers:
  - id: simulator-worker
    addr: http://127.0.0.1:19090
    weight: 1
    control_token: ${WK_BENCH_WORKER_TOKEN}
    client:
      send_queue_capacity: 16
      max_inflight: 1
      read_buffer_size: 1024
      frame_buffer_size: 4
    tcp_source:
      ipv4_addrs:
%s      port_min: 1024
      port_max: 65535
`, addresses)
}

func prometheusConfig(addresses map[string]string) string {
	return fmt.Sprintf(`global:
  scrape_interval: 15s
  evaluation_interval: 15s
scrape_configs:
  - job_name: wukongim
    static_configs:
      - targets: ["%s:5001"]
        labels: {role: "node-1"}
      - targets: ["%s:5001"]
        labels: {role: "node-2"}
      - targets: ["%s:5001"]
        labels: {role: "node-3"}
  - job_name: hosts
    static_configs:
      - targets: ["%s:9100"]
        labels: {role: "node-1"}
      - targets: ["%s:9100"]
        labels: {role: "node-2"}
      - targets: ["%s:9100"]
        labels: {role: "node-3"}
      - targets: ["%s:9100"]
        labels: {role: "sim"}
`, addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses["sim"])
}

func cgroupMetricsCollector() string {
	return `#!/usr/bin/env bash
set -euo pipefail

umask 022

output="${WK_TEXTFILE_OUTPUT:-/var/lib/wukongim/textfile/wukongim-cgroup.prom}"
state_dir="${WK_CGROUP_METRICS_STATE_DIR:-/var/lib/wukongim/cgroup-metrics-state}"
cgroup_path="${WK_CGROUP_PATH:-}"
cgroup_root="${WK_CGROUP_ROOT:-/sys/fs/cgroup}"
watch=false
if [[ "${1:-}" == --watch ]]; then
  watch=true
fi

collect() {
mkdir -p "$(dirname "$output")" "$state_dir"
lock_dir="$state_dir/collector.lock"
lock_acquired=false
for ((attempt = 0; attempt < 40; attempt += 1)); do
  if mkdir "$lock_dir" 2>/dev/null; then
    printf '%s\n' "$$" >"$lock_dir/pid"
    lock_acquired=true
    break
  fi
  owner_pid=""
  if [[ -r "$lock_dir/pid" ]]; then
    IFS= read -r owner_pid <"$lock_dir/pid" || true
  fi
  if [[ "$owner_pid" =~ ^[0-9]+$ ]] && ! kill -0 "$owner_pid" 2>/dev/null; then
    rm -rf "$lock_dir"
    continue
  fi
  sleep 0.05
done
[[ "$lock_acquired" == true ]] || exit 0
trap 'rm -rf "$lock_dir"' EXIT

read_number() {
  local path="$1"
  local value=""
  if [[ -r "$path" ]]; then
    IFS= read -r value <"$path" || true
  fi
  if [[ "$value" =~ ^[0-9]+$ ]]; then
    printf '%s\n' "$value"
    return 0
  fi
  return 1
}

read_state_number() {
  local path="$1"
  local fallback="$2"
  local value=""
  if [[ -r "$path" ]]; then
    IFS= read -r value <"$path" || true
  fi
  if [[ "$value" =~ ^[0-9]+$ ]]; then
    printf '%s\n' "$value"
  else
    printf '%s\n' "$fallback"
  fi
}

write_state() {
  local path="$1"
  local value="$2"
  local existing=""
  if [[ -r "$path" ]]; then
    IFS= read -r existing <"$path" || true
  fi
  if [[ "$existing" == "$value" ]]; then
    return 0
  fi
  local temporary="${path}.tmp.$$"
  printf '%s\n' "$value" >"$temporary"
  mv "$temporary" "$path"
}

cgroup_version=0
detect_cgroup_version() {
  local path="$1"
  if [[ -r "$path/memory.current" ]]; then
    cgroup_version=2
    cgroup_path="$path"
    return 0
  fi
  if [[ -r "$path/memory.usage_in_bytes" ]]; then
    cgroup_version=1
    cgroup_path="$path"
    return 0
  fi
  return 1
}

if [[ -n "$cgroup_path" ]]; then
  detect_cgroup_version "$cgroup_path" || true
fi
if [[ "$cgroup_version" == 0 ]]; then
  control_group="$(systemctl show --property=ControlGroup --value wukongim.service 2>/dev/null || true)"
  if [[ "$control_group" == /* ]]; then
    detect_cgroup_version "${cgroup_root}${control_group}" || \
      detect_cgroup_version "${cgroup_root}/memory${control_group}" || true
  fi
fi
if [[ "$cgroup_version" == 0 ]]; then
  detect_cgroup_version "${cgroup_root}/system.slice/wukongim.service" || \
    detect_cgroup_version "${cgroup_root}/memory/system.slice/wukongim.service" || true
fi

available=0
current=""
raw_peak=""
native_peak_available=0
if [[ "$cgroup_version" == 2 ]]; then
  available=1
  current="$(read_number "$cgroup_path/memory.current" || true)"
  raw_peak="$(read_number "$cgroup_path/memory.peak" || true)"
  if [[ "$raw_peak" =~ ^[0-9]+$ ]]; then
    native_peak_available=1
  else
    raw_peak="$current"
  fi
elif [[ "$cgroup_version" == 1 ]]; then
  available=1
  current="$(read_number "$cgroup_path/memory.usage_in_bytes" || true)"
  raw_peak="$(read_number "$cgroup_path/memory.max_usage_in_bytes" || true)"
  if [[ "$raw_peak" =~ ^[0-9]+$ ]]; then
    native_peak_available=1
  else
    raw_peak="$current"
  fi
fi

peak_state="$state_dir/memory_peak_bytes"
persistent_peak="$(read_state_number "$peak_state" 0)"
if [[ "$raw_peak" =~ ^[0-9]+$ ]] && ((raw_peak > persistent_peak)); then
  persistent_peak="$raw_peak"
  write_state "$peak_state" "$persistent_peak"
fi

limit_state="$state_dir/memory_limit"
memory_limit_path=""
if [[ "$cgroup_version" == 2 ]]; then
  memory_limit_path="$cgroup_path/memory.max"
elif [[ "$cgroup_version" == 1 ]]; then
  memory_limit_path="$cgroup_path/memory.limit_in_bytes"
fi
if [[ "$available" == 1 && -r "$memory_limit_path" ]]; then
  IFS= read -r memory_limit <"$memory_limit_path" || true
  if [[ "$cgroup_version" == 1 && "$memory_limit" =~ ^[0-9]+$ ]] && ((memory_limit >= 1152921504606846976)); then
    memory_limit=max
  fi
  if [[ "$memory_limit" == max || "$memory_limit" =~ ^[0-9]+$ ]]; then
    write_state "$limit_state" "$memory_limit"
  fi
fi
memory_limit=""
if [[ -r "$limit_state" ]]; then
  IFS= read -r memory_limit <"$limit_state" || true
fi

swap_current=""
swap_limit=""
if [[ "$cgroup_version" == 2 ]]; then
  swap_current="$(read_number "$cgroup_path/memory.swap.current" || true)"
  if [[ -r "$cgroup_path/memory.swap.max" ]]; then
    IFS= read -r swap_limit <"$cgroup_path/memory.swap.max" || true
    if [[ "$swap_limit" != max && ! "$swap_limit" =~ ^[0-9]+$ ]]; then
      swap_limit=""
    fi
    if [[ -n "$swap_limit" ]]; then
      write_state "$state_dir/memory_swap_limit" "$swap_limit"
    fi
  fi
elif [[ "$cgroup_version" == 1 ]]; then
  memsw_current="$(read_number "$cgroup_path/memory.memsw.usage_in_bytes" || true)"
  if [[ "$current" =~ ^[0-9]+$ && "$memsw_current" =~ ^[0-9]+$ ]]; then
    if ((memsw_current >= current)); then
      swap_current=$((memsw_current - current))
    else
      swap_current=0
    fi
  fi
  memsw_limit="$(read_number "$cgroup_path/memory.memsw.limit_in_bytes" || true)"
  if [[ "$memsw_limit" =~ ^[0-9]+$ ]]; then
    if ((memsw_limit >= 1152921504606846976)); then
      swap_limit=max
    elif [[ "$memory_limit" =~ ^[0-9]+$ ]]; then
      if ((memsw_limit >= memory_limit)); then
        swap_limit=$((memsw_limit - memory_limit))
      else
        swap_limit=0
      fi
    fi
    if [[ -n "$swap_limit" ]]; then
      write_state "$state_dir/memory_swap_limit" "$swap_limit"
    fi
  fi
fi
if [[ -z "$swap_limit" && -r "$state_dir/memory_swap_limit" ]]; then
  IFS= read -r swap_limit <"$state_dir/memory_swap_limit" || true
fi

events_available=false
event_low=0
event_high=0
event_max=0
event_oom=0
event_oom_kill=0
event_oom_group_kill=0
if [[ "$available" == 1 && -r "$cgroup_path/memory.events" ]]; then
  events_available=true
  while read -r event_name event_value; do
    [[ "$event_value" =~ ^[0-9]+$ ]] || continue
    case "$event_name" in
      low) event_low="$event_value" ;;
      high) event_high="$event_value" ;;
      max) event_max="$event_value" ;;
      oom) event_oom="$event_value" ;;
      oom_kill) event_oom_kill="$event_value" ;;
      oom_group_kill) event_oom_group_kill="$event_value" ;;
    esac
  done <"$cgroup_path/memory.events"
elif [[ "$cgroup_version" == 1 && -r "$cgroup_path/memory.oom_control" ]]; then
  events_available=true
  while read -r event_name event_value; do
    [[ "$event_value" =~ ^[0-9]+$ ]] || continue
    case "$event_name" in
      oom_kill) event_oom_kill="$event_value" ;;
    esac
  done <"$cgroup_path/memory.oom_control"
fi

temporary="${output}.tmp.$$"
trap 'rm -f "$temporary"; rm -rf "$lock_dir"' EXIT
{
  printf '# HELP wukongim_service_cgroup_available Whether the WuKongIM systemd service cgroup is readable.\n'
  printf '# TYPE wukongim_service_cgroup_available gauge\n'
  printf 'wukongim_service_cgroup_available %s\n' "$available"
  if [[ "$current" =~ ^[0-9]+$ ]]; then
    printf 'wukongim_service_cgroup_memory_current_bytes %s\n' "$current"
  fi
  printf 'wukongim_service_cgroup_memory_peak_bytes %s\n' "$persistent_peak"
  printf 'wukongim_service_cgroup_memory_peak_native_available %s\n' "$native_peak_available"
  if [[ "$memory_limit" == max ]]; then
    printf 'wukongim_service_cgroup_memory_limit_unlimited 1\n'
  elif [[ "$memory_limit" =~ ^[0-9]+$ ]]; then
    printf 'wukongim_service_cgroup_memory_limit_bytes %s\n' "$memory_limit"
    printf 'wukongim_service_cgroup_memory_limit_unlimited 0\n'
  fi
  if [[ "$swap_current" =~ ^[0-9]+$ ]]; then
    printf 'wukongim_service_cgroup_memory_swap_current_bytes %s\n' "$swap_current"
  fi
  if [[ "$swap_limit" == max ]]; then
    printf 'wukongim_service_cgroup_memory_swap_limit_unlimited 1\n'
  elif [[ "$swap_limit" =~ ^[0-9]+$ ]]; then
    printf 'wukongim_service_cgroup_memory_swap_limit_bytes %s\n' "$swap_limit"
    printf 'wukongim_service_cgroup_memory_swap_limit_unlimited 0\n'
  fi
  for event in low high max oom oom_kill oom_group_kill; do
    if [[ "$events_available" == true ]]; then
      case "$event" in
        low) raw="$event_low" ;;
        high) raw="$event_high" ;;
        max) raw="$event_max" ;;
        oom) raw="$event_oom" ;;
        oom_kill) raw="$event_oom_kill" ;;
        oom_group_kill) raw="$event_oom_group_kill" ;;
      esac
      previous="$(read_state_number "$state_dir/event_${event}_previous" 0)"
      total="$(read_state_number "$state_dir/event_${event}_total" 0)"
      if ((raw >= previous)); then
        delta=$((raw - previous))
      else
        delta=$raw
      fi
      total=$((total + delta))
      write_state "$state_dir/event_${event}_previous" "$raw"
      write_state "$state_dir/event_${event}_total" "$total"
    else
      total="$(read_state_number "$state_dir/event_${event}_total" 0)"
    fi
    printf 'wukongim_service_cgroup_memory_events_total{event="%s"} %s\n' "$event" "$total"
  done
  if [[ "$available" == 1 ]]; then
    printf 'wukongim_service_cgroup_collector_last_success_unixtime_seconds %s\n' "$(date +%s)"
  fi
} >"$temporary"
chmod 0644 "$temporary"
mv "$temporary" "$output"
rm -rf "$lock_dir"
trap - EXIT
}

if [[ "$watch" == true ]]; then
  while true; do
    collect
    sleep 1
  done
else
  collect
fi
`
}

func systemdUnits(publicViewEnabled bool, duration time.Duration) map[string]string {
	prometheusRetention := "72h"
	if duration == 168*time.Hour {
		prometheusRetention = "192h"
	}
	units := map[string]string{
		"wukongim.service": `[Unit]
Description=WuKongIM cloud simulation node
After=network-online.target local-fs.target
Wants=network-online.target

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/node.env
ExecStart=/opt/wukongim/bin/wukongim -config /etc/wukongim/wukongim.toml
ExecStopPost=-/opt/wukongim/bin/wukongim-cgroup-metrics
Restart=on-failure
RestartSec=2s
LimitNOFILE=1048576
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`,
		"wkbench-worker.service": `[Unit]
Description=WuKongIM cloud simulation worker
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/sim.env
ExecStart=/opt/wukongim/bin/wkbench worker --listen 127.0.0.1:19090 --work-dir /var/lib/wukongim-cloud/worker --control-token ${WK_BENCH_WORKER_TOKEN}
Restart=on-failure
RestartSec=2s
LimitNOFILE=1048576
TasksMax=infinity
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`,
		"wkbench-run.service": `[Unit]
Description=WuKongIM cloud simulation one-shot coordinator
After=wkbench-worker.service wkanalysis.service prometheus.service
Requires=wkbench-worker.service

[Service]
Type=oneshot
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/sim.env
ExecStart=/opt/wukongim/bin/wkbench validate --target /etc/wukongim/target.yaml --workers /etc/wukongim/workers.yaml --scenario /etc/wukongim/scenario.yaml
ExecStart=/opt/wukongim/bin/wkbench doctor --target /etc/wukongim/target.yaml --workers /etc/wukongim/workers.yaml --scenario /etc/wukongim/scenario.yaml
ExecStart=/opt/wukongim/bin/wkbench run --target /etc/wukongim/target.yaml --workers /etc/wukongim/workers.yaml --scenario /etc/wukongim/scenario.yaml
ExecStopPost=/opt/wukongim/bin/wkcloudview annotate-report --status-url http://127.0.0.1:19443/cloud-view/status --report /var/lib/wukongim-cloud/reports/${WK_CLOUD_RUN_ID}/report.json
Restart=no
TimeoutStartSec=infinity

[Install]
WantedBy=multi-user.target
`,
		"prometheus.service": fmt.Sprintf(`[Unit]
Description=Run-scoped Prometheus
After=network-online.target local-fs.target

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/sim.env
ExecStart=/opt/wukongim/bin/prometheus --config.file=/etc/wukongim/prometheus.yml --storage.tsdb.path=/var/lib/wukongim-cloud/prometheus --storage.tsdb.retention.time=%s --web.external-url=${WK_CLOUD_VIEW_PROMETHEUS_EXTERNAL_URL} --web.route-prefix=/
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`, prometheusRetention),
	}
	for name, content := range baseSystemdUnits() {
		units[name] = content
	}
	if !publicViewEnabled {
		units["wkbench-run.service"] = strings.ReplaceAll(units["wkbench-run.service"], "ExecStopPost=/opt/wukongim/bin/wkcloudview annotate-report --status-url http://127.0.0.1:19443/cloud-view/status --report /var/lib/wukongim-cloud/reports/${WK_CLOUD_RUN_ID}/report.json\n", "")
		return units
	}
	units["wkcloudview.service"] = `[Unit]
Description=WuKongIM run-scoped public Cloud View on TCP/19443
After=network-online.target prometheus.service
Requires=prometheus.service

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/sim.env
ExecStart=/opt/wukongim/bin/wkcloudview serve --config /etc/wukongim/cloud-view.json
Restart=on-failure
RestartSec=2s
LimitNOFILE=1048576
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`
	return units
}

func baseSystemdUnits() map[string]string {
	return map[string]string{
		"node-exporter.service": `[Unit]
Description=Run-scoped host metrics exporter
After=network-online.target

[Service]
Type=simple
User=wukongim
Group=wukongim
ExecStart=/opt/wukongim/bin/node_exporter --web.listen-address=0.0.0.0:9100 --collector.textfile.directory=/var/lib/wukongim/textfile
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`,
		"wkanalysis.service": `[Unit]
Description=WuKongIM run-scoped Analysis MCP
After=network-online.target prometheus.service
Requires=prometheus.service

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/analysis.env
ExecStart=/opt/wukongim/bin/wkanalysis
Restart=on-failure
RestartSec=2s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`,
		"wukongim-cgroup-metrics.service": `[Unit]
Description=Persist WuKongIM service cgroup memory evidence
After=wukongim.service

[Service]
Type=simple
User=wukongim
Group=wukongim
ExecStart=/opt/wukongim/bin/wukongim-cgroup-metrics --watch
Restart=on-failure
RestartSec=1s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`,
	}
}
