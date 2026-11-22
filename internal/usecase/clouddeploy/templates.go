package clouddeploy

func scaffoldFiles() map[string]string {
	return map[string]string{
		"config/wukongim.toml.tmpl": `mode = "release"

[node]
id = {{NODE_ID}}
data_dir = "/var/lib/wukongim-cloud/wukongim"

[cluster]
listen_addr = "{{PRIVATE_IPV4}}:7000"
id = "chat-lifecycle"
nodes = {{CLUSTER_NODES}}
initial_slot_count = 12
hash_slot_count = 256
slot_replica_n = 3
channel_replica_n = 3
# A 100ms heartbeat and two-second minimum election window keep transient
# Cloud Medium storage/transport tails from causing unnecessary Slot terms.
slot_tick_interval = "50ms"
slot_heartbeat_tick = 2
slot_election_tick = 40
max_channels = 50000
# These bounded Cloud Medium capacities are calibrated for the fixed
# 10,000-online, 2,000-SEND/s chat-lifecycle workload. In particular, the
# append pool must absorb the initial cold-Channel quorum-write wave without
# consuming the five-second gateway SEND deadline.
channel_store_append_workers = 128
channel_store_apply_workers = 8
channel_rpc_workers = 96
channel_rpc_batch_max_items = 8

[api]
listen_addr = "0.0.0.0:5001"
external_tcp_addr = "{{PRIVATE_IPV4}}:5100"
external_ws_addr = "ws://{{PUBLIC_HTTP_HOST}}"

[manager]
listen_addr = "0.0.0.0:5301"
auth_on = true

[bench]
api_enable = true

[gateway]
gnet_multicore = true
gnet_num_event_loop = 4
runtime_async_send_workers = 1000
runtime_async_send_queue_capacity = 131072

[delivery]
recipient_worker_concurrency = 320

[observability]
metrics_enable = true
debug_api_enable = true

[prometheus]
enable = false
query_base_url = "http://{{LOAD_PRIVATE_IPV4}}:9090"
`,
		"config/prometheus.yml.tmpl": `global:
  scrape_interval: 15s
scrape_configs:
  - job_name: wukongim
    static_configs:
      - targets: [{{WUKONGIM_METRICS_TARGETS}}]
  - job_name: hosts
    static_configs:
{{NODE_EXPORTER_STATIC_CONFIGS}}
`,
		"config/Caddyfile.tmpl": `{
  auto_https off
  admin off
}
:80 {
  handle_path /demo/* {
    basic_auth {
      {$WK_DEMO_BASIC_AUTH_USER} {$WK_DEMO_BASIC_AUTH_HASH}
    }
    root * /opt/wukongim/assets/demo
    try_files {path} /index.html
    file_server
  }
  @demo_websocket {
    header Connection *Upgrade*
    header Upgrade websocket
  }
  handle @demo_websocket {
    basic_auth {
      {$WK_DEMO_BASIC_AUTH_USER} {$WK_DEMO_BASIC_AUTH_HASH}
    }
    reverse_proxy {{DEMO_WS_UPSTREAMS}} {
      health_uri /readyz
      health_port 5001
      health_interval 5s
      health_timeout 2s
      health_status 2xx
      transport http {
        keepalive off
      }
    }
  }
  @demo_api_safe {
    method GET HEAD
    path /route /user/* /channel/* /message/* /conversation/* /conversations/* /streammessage/* /web/*
  }
  handle @demo_api_safe {
    basic_auth {
      {$WK_DEMO_BASIC_AUTH_USER} {$WK_DEMO_BASIC_AUTH_HASH}
    }
    reverse_proxy {{DEMO_API_UPSTREAMS}} {
      health_uri /readyz
      health_interval 5s
      health_timeout 2s
      health_status 2xx
      lb_try_duration 3s
      lb_try_interval 250ms
      lb_retry_match {
        method GET HEAD
      }
    }
  }
  @demo_api path /route /user/* /channel/* /message/* /conversation/* /conversations/* /streammessage/* /web/*
  handle @demo_api {
    basic_auth {
      {$WK_DEMO_BASIC_AUTH_USER} {$WK_DEMO_BASIC_AUTH_HASH}
    }
    reverse_proxy {{DEMO_API_UPSTREAMS}} {
      health_uri /readyz
      health_interval 5s
      health_timeout 2s
      health_status 2xx
      transport http {
        keepalive off
      }
    }
  }
  @manager_safe {
    method GET HEAD
  }
  handle @manager_safe {
    reverse_proxy {{MANAGER_UPSTREAMS}} {
      health_uri /readyz
      health_port 5001
      health_interval 5s
      health_timeout 2s
      health_status 2xx
      lb_try_duration 3s
      lb_try_interval 250ms
      lb_retry_match {
        method GET HEAD
      }
    }
  }
  handle {
    reverse_proxy {{MANAGER_UPSTREAMS}} {
      health_uri /readyz
      health_port 5001
      health_interval 5s
      health_timeout 2s
      health_status 2xx
      transport http {
        keepalive off
      }
    }
  }
}
`,
		"scripts/verify-base-tools.sh": `#!/usr/bin/env bash
set -euo pipefail
. /etc/os-release
[[ "$ID" == ubuntu && "$VERSION_ID" == 24.04 ]]
[[ "$(uname -m)" == x86_64 ]]
for tool in awk bash blkid cat chmod chown curl date df dirname findmnt getconf grep head id install lsblk mkdir mkfs.ext4 mount mv rm scp sed sha256sum sleep ssh stat sudo systemctl tail tar timedatectl timeout uname useradd; do
  command -v "$tool" >/dev/null
done
`,
		"scripts/wait-coordinator-dependencies.sh": `#!/usr/bin/env bash
set -euo pipefail
config="${WK_CHAT_LIFECYCLE_CONFIG:-/etc/wukongim/chat-lifecycle.yaml}"
[[ "${WK_BENCH_WORKER_TOKEN:-}" =~ ^[0-9a-f]{64}$ ]]
mapfile -t services < <(sed -n '/^  service_nodes:/,/^  workers:/ s/.*address: "http:\/\/\([^"]*\)".*/\1/p' "$config")
mapfile -t host_metrics < <(sed -n '/^  host_metrics:/,/^  load_host_metrics:/ s/^    - .*address: "http:\/\/\([^"]*\)".*/\1/p' "$config")
load_host_metrics="$(sed -n 's/^  load_host_metrics:.*address: "http:\/\/\([^"]*\)".*/\1/p' "$config")"
((${#services[@]} == 3 && ${#host_metrics[@]} == 3))
[[ -n "$load_host_metrics" ]]
deadline=$(( $(date -u +%s) + 900 ))
while (( $(date -u +%s) < deadline )); do
  ready=true
  for address in "${services[@]}"; do
    curl --fail --silent --show-error --max-time 5 "http://${address}/readyz" >/dev/null || ready=false
  done
  for address in "${host_metrics[@]}"; do
    curl --fail --silent --show-error --max-time 5 "http://${address}/healthz" >/dev/null || ready=false
  done
  curl --fail --silent --show-error --max-time 5 "http://${load_host_metrics}/healthz" >/dev/null || ready=false
  for port in 19091 19092 19093; do
    curl --fail --silent --show-error --max-time 5 -H "Authorization: Bearer ${WK_BENCH_WORKER_TOKEN}" "http://127.0.0.1:${port}/healthz" >/dev/null || ready=false
  done
  curl --fail --silent --show-error --max-time 5 http://127.0.0.1:9090/-/ready >/dev/null || ready=false
  if [[ "$ready" == true ]]; then
    exit 0
  fi
  sleep 5
done
exit 1
`,
		"scripts/run-chat-lifecycle-stage.sh": `#!/usr/bin/env bash
set -euo pipefail

[[ "$#" -eq 1 ]]
stage="$1"
case "$stage" in
  coordinator)
    config=/etc/wukongim/chat-lifecycle.yaml
    stage_unit=wkbench-coordinator.service
    command=(/opt/wukongim/bin/wkcli bench soak chat-lifecycle --config "$config" --output-dir /var/lib/wukongim-cloud/reports)
    ;;
  rehearsal)
    config=/etc/wukongim/chat-lifecycle-rehearsal.yaml
    stage_unit=wkbench-rehearsal.service
    command=(/opt/wukongim/bin/wkcli bench soak chat-lifecycle --config "$config" --output-dir /var/lib/wukongim-cloud/reports/rehearsal)
    if [[ -n "${WK_CHAT_LAB_MAX_DURATION_SECONDS:-}" ]]; then
      [[ "$WK_CHAT_LAB_MAX_DURATION_SECONDS" =~ ^[1-9][0-9]*$ ]]
      (( WK_CHAT_LAB_MAX_DURATION_SECONDS >= 960 && WK_CHAT_LAB_MAX_DURATION_SECONDS <= 260100 ))
      command+=(--duration "${WK_CHAT_LAB_MAX_DURATION_SECONDS}s")
    fi
    ;;
  formal)
    config=/etc/wukongim/chat-lifecycle.yaml
    stage_unit=wkbench-formal.service
    command=(/opt/wukongim/bin/wkcli bench formal-chain chat-lifecycle --config "$config" --output-dir /var/lib/wukongim-cloud/reports)
    ;;
  *) exit 1 ;;
esac

load_host_metrics="$(sed -n 's/^  load_host_metrics:.*address: "http:\/\/\([^"]*\)".*/\1/p' "$config")"
[[ -n "$load_host_metrics" ]]
deadline=$(( $(date -u +%s) + 120 ))
while (( $(date -u +%s) < deadline )); do
  if curl --fail --silent --show-error --max-time 5 "http://${load_host_metrics}/metrics" | awk -v unit="$stage_unit" '
    $1 == "wukongim_process_up{unit=\"" unit "\"}" && NF == 2 && $2 == "1" { up++ }
    $1 == "wukongim_process_cpu_jiffies_total{unit=\"" unit "\"}" && NF == 2 && $2 ~ /^[0-9]+$/ { cpu++ }
    $1 == "wukongim_process_resident_memory_bytes{unit=\"" unit "\"}" && NF == 2 && $2 ~ /^[0-9]+$/ && $2 + 0 > 0 { memory++ }
    END { exit !(up == 1 && cpu == 1 && memory == 1) }
  '; then
    exec "${command[@]}"
  fi
  sleep 2
done
exit 1
`,
		"scripts/collect-evidence.sh": `#!/usr/bin/env bash
set -euo pipefail
output="${WK_EVIDENCE_OUTPUT:-/var/lib/wukongim-cloud/evidence/host.txt}"
install -d -m 0750 "$(dirname "$output")"
{
  date -u +%Y-%m-%dT%H:%M:%SZ
  timedatectl show --property=NTPSynchronized --property=TimeUSec
  df -B1 / /var/lib/wukongim-cloud
  systemctl --no-pager --plain --type=service --state=running
} >"$output"
`,
		"scripts/collect-process-metrics.sh": `#!/usr/bin/env bash
set -euo pipefail
output="${WK_PROCESS_METRICS_OUTPUT:-/var/lib/wukongim/textfile/processes.prom}"
interval="${WK_PROCESS_METRICS_INTERVAL_SECONDS:-15}"
units=(
  wukongim.service
  wkbench-host-metrics.service
  wkbench-worker@1.service
  wkbench-worker@2.service
  wkbench-worker@3.service
  wkbench-coordinator.service
  wkbench-formal.service
  wkbench-rehearsal.service
  prometheus.service
  caddy.service
  wkanalysis.service
  wukongim-process-metrics.service
  node-exporter.service
)

unknown_process() {
  printf 'process metrics: %s for %s; snapshot skipped\n' "$2" "$1" >&2
}

emit_snapshot() {
  local page_size observed_at
  page_size="$(getconf PAGESIZE)" || return 1
  printf '# HELP wukongim_process_up Whether the exact systemd process is running.\n' || return 1
  printf '# TYPE wukongim_process_up gauge\n' || return 1
  local unit properties name value load_state active_state pid stat_line current_stat rss_pages read_bytes write_bytes
  local -a fields current_fields descriptors
  for unit in "${units[@]}"; do
    if ! properties="$(systemctl show --property=LoadState --property=ActiveState --property=MainPID "$unit" 2>/dev/null)"; then
      unknown_process "$unit" systemd_query_failed
      return 1
    fi
    load_state= active_state= pid=
    while IFS='=' read -r name value; do
      case "$name" in
        LoadState) load_state="$value" ;;
        ActiveState) active_state="$value" ;;
        MainPID) pid="$value" ;;
      esac
    done <<<"$properties"

    case "$load_state:$active_state:$pid" in
      loaded:inactive:0|loaded:failed:0|not-found:inactive:0|not-found:inactive:)
        printf 'wukongim_process_up{unit="%s"} 0\n' "$unit" || return 1
        continue
        ;;
    esac
    if [[ "$load_state" != loaded || -z "$active_state" || ! "$pid" =~ ^[1-9][0-9]*$ ]]; then
      unknown_process "$unit" systemd_state_incomplete
      return 1
    fi
    if ! { IFS= read -r stat_line <"/proc/$pid/stat"; } 2>/dev/null; then
      unknown_process "$unit" process_stat_unreadable
      return 1
    fi
    read -r -a fields <<<"${stat_line##*) }"
    if (( ${#fields[@]} < 22 )) || [[ ! "${fields[11]}" =~ ^[0-9]+$ || ! "${fields[12]}" =~ ^[0-9]+$ ||
          ! "${fields[17]}" =~ ^[0-9]+$ || ! "${fields[19]}" =~ ^[0-9]+$ ||
          ! "${fields[21]}" =~ ^[1-9][0-9]*$ ]]; then
      unknown_process "$unit" process_stat_incomplete
      return 1
    fi
    rss_pages="${fields[21]}"
    read_bytes=0
    write_bytes=0
    if [[ -r "/proc/$pid/io" ]]; then
      while read -r name value; do
        case "$name" in
          read_bytes:) read_bytes="$value" ;;
          write_bytes:) write_bytes="$value" ;;
        esac
      done <"/proc/$pid/io" || return 1
    fi
    shopt -s nullglob
    descriptors=(/proc/"$pid"/fd/*)
    shopt -u nullglob
    # Bind the resource reads to the same live process using its kernel start time.
    if ! { IFS= read -r current_stat <"/proc/$pid/stat"; } 2>/dev/null; then
      unknown_process "$unit" process_stat_unreadable
      return 1
    fi
    read -r -a current_fields <<<"${current_stat##*) }"
    if [[ "${current_fields[19]:-}" != "${fields[19]}" || "${current_fields[0]}" == [ZXx] ]]; then
      unknown_process "$unit" process_identity_changed
      return 1
    fi
    printf 'wukongim_process_up{unit="%s"} 1\n' "$unit" || return 1
    printf 'wukongim_process_cpu_jiffies_total{unit="%s"} %s\n' "$unit" "$((fields[11] + fields[12]))" || return 1
    printf 'wukongim_process_resident_memory_bytes{unit="%s"} %s\n' "$unit" "$((rss_pages * page_size))" || return 1
    printf 'wukongim_process_threads{unit="%s"} %s\n' "$unit" "${fields[17]}" || return 1
    printf 'wukongim_process_open_fds{unit="%s"} %s\n' "$unit" "${#descriptors[@]}" || return 1
    printf 'wukongim_process_read_bytes_total{unit="%s"} %s\n' "$unit" "$read_bytes" || return 1
    printf 'wukongim_process_write_bytes_total{unit="%s"} %s\n' "$unit" "$write_bytes" || return 1
  done
  observed_at="$(date +%s)" || return 1
  printf 'wukongim_process_collector_last_success_unixtime_seconds %s\n' "$observed_at"
}

collect() {
  install -d -m 0755 "$(dirname "$output")" || return 1
  local temporary="${output}.tmp.$$"
  if emit_snapshot >"$temporary" && chmod 0644 "$temporary" && mv -f "$temporary" "$output"; then
    return 0
  fi
  # Keep the prior timestamp so the host metrics freshness limit still applies.
  rm -f "$temporary"
  return 1
}

if [[ "${1:-}" == --once ]]; then
  collect
  exit 0
fi
while true; do
  collect || true
  sleep "$interval"
done
`,
		"systemd/wukongim.service":             serviceUnit("node.env", "/opt/wukongim/bin/wukongim -config /etc/wukongim/wukongim.toml"),
		"systemd/wkbench-host-metrics.service": serviceUnit("", "/opt/wukongim/bin/wkcli bench host-metrics --listen 0.0.0.0:19101 --path /var/lib/wukongim-cloud --mountpoint /var/lib/wukongim-cloud --device /dev/wukongim-data --system-path / --watch-path /var/lib/wukongim-cloud/prometheus --process-metrics-path /var/lib/wukongim/textfile/processes.prom"),
		"systemd/wkbench-worker@.service":      serviceUnit("load.env", "/opt/wukongim/bin/wkcli bench worker --mode chat-lifecycle --listen 127.0.0.1:1909%i --work-dir /var/lib/wukongim-cloud/workers/%i"),
		"systemd/wkbench-coordinator.service":  coordinatorServiceUnit(),
		"systemd/wkbench-formal.service":       formalServiceUnit(),
		"systemd/wkbench-rehearsal.service":    rehearsalServiceUnit(),
		"systemd/prometheus.service":           serviceUnit("load.env", "/opt/wukongim/bin/prometheus --config.file=/etc/wukongim/prometheus.yml --storage.tsdb.path=/var/lib/wukongim-cloud/prometheus --storage.tsdb.retention.time=96h --storage.tsdb.retention.size=150GB"),
		"systemd/node-exporter.service":        serviceUnit("", "/opt/wukongim/bin/node_exporter --web.listen-address=0.0.0.0:9100 --collector.textfile.directory=/var/lib/wukongim/textfile"),
		"systemd/wkanalysis.service":           analysisServiceUnit(),
		"systemd/caddy.service":                caddyServiceUnit(),
		"systemd/wukongim-process-metrics.service": `[Unit]
Description=WuKongIM independent process resource observations
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/opt/wukongim/scripts/collect-process-metrics.sh
Restart=no
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`,
		"systemd/wukongim-evidence.service": `[Unit]
Description=WuKongIM bounded host evidence

[Service]
Type=oneshot
User=root
ExecStart=/opt/wukongim/scripts/collect-evidence.sh
`,
		"systemd/wukongim-evidence.timer": `[Unit]
Description=Collect WuKongIM host evidence

[Timer]
OnBootSec=30s
OnUnitActiveSec=30s
AccuracySec=1s

[Install]
WantedBy=timers.target
`,
	}
}

func caddyServiceUnit() string {
	return `[Unit]
After=network-online.target time-sync.target
Wants=network-online.target time-sync.target

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/secrets/load.env
ExecStartPre=/opt/wukongim/bin/caddy validate --config /etc/wukongim/Caddyfile --adapter caddyfile
ExecStart=/opt/wukongim/bin/caddy run --config /etc/wukongim/Caddyfile --adapter caddyfile
Restart=no
LimitNOFILE=1048576
TasksMax=infinity
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`
}

func analysisServiceUnit() string {
	return `[Unit]
After=network-online.target time-sync.target
Wants=network-online.target time-sync.target

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/secrets/analysis.env
LoadCredential=analysis-cert.pem:/etc/wukongim/secrets/analysis-cert.pem
LoadCredential=analysis-key.pem:/etc/wukongim/secrets/analysis-key.pem
Environment=WK_ANALYSIS_TLS_CERT_FILE=%d/analysis-cert.pem
Environment=WK_ANALYSIS_TLS_KEY_FILE=%d/analysis-key.pem
ExecStart=/opt/wukongim/bin/wkanalysis
Restart=no
LimitNOFILE=1048576
TasksMax=infinity
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`
}

func coordinatorServiceUnit() string {
	return `[Unit]
After=network-online.target time-sync.target wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service prometheus.service
Wants=network-online.target time-sync.target
Requisite=wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service prometheus.service

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/secrets/load.env
ExecStartPre=/opt/wukongim/scripts/wait-coordinator-dependencies.sh
ExecStart=/opt/wukongim/scripts/run-chat-lifecycle-stage.sh coordinator
TimeoutStartSec=960
Restart=no
LimitNOFILE=1048576
TasksMax=infinity
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`
}

func rehearsalServiceUnit() string {
	return `[Unit]
After=network-online.target time-sync.target wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service prometheus.service
Wants=network-online.target time-sync.target
Requisite=wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service prometheus.service
Conflicts=wkbench-coordinator.service

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/secrets/load.env
Environment=WK_CHAT_LIFECYCLE_CONFIG=/etc/wukongim/chat-lifecycle-rehearsal.yaml
ExecStartPre=/opt/wukongim/scripts/wait-coordinator-dependencies.sh
ExecStart=/opt/wukongim/scripts/run-chat-lifecycle-stage.sh rehearsal
SuccessExitStatus=130
TimeoutStartSec=960
Restart=no
LimitNOFILE=1048576
TasksMax=infinity
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`
}

func formalServiceUnit() string {
	return `[Unit]
After=network-online.target time-sync.target wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service prometheus.service
Wants=network-online.target time-sync.target
Requisite=wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service prometheus.service
Conflicts=wkbench-rehearsal.service wkbench-coordinator.service

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/secrets/load.env
Environment=WK_CHAT_LIFECYCLE_CONFIG=/etc/wukongim/chat-lifecycle.yaml
ExecStartPre=/opt/wukongim/scripts/wait-coordinator-dependencies.sh
ExecStart=/opt/wukongim/scripts/run-chat-lifecycle-stage.sh formal
TimeoutStartSec=960
Restart=no
LimitNOFILE=1048576
TasksMax=infinity
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`
}

func serviceUnit(environment, command string) string {
	unit := `[Unit]
After=network-online.target time-sync.target
Wants=network-online.target time-sync.target

[Service]
Type=simple
User=wukongim
Group=wukongim
`
	if environment != "" {
		unit += "EnvironmentFile=/etc/wukongim/secrets/" + environment + "\n"
	}
	return unit + `ExecStart=` + command + `
Restart=no
LimitNOFILE=1048576
TasksMax=infinity
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`
}
