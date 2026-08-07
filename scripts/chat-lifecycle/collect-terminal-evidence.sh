#!/usr/bin/env bash
set -euo pipefail
umask 077

if (( $# != 2 )); then
  echo 'usage: collect-terminal-evidence.sh STAGE OUTPUT_DIR' >&2
  exit 2
fi

stage="$1"
output="$2"
[[ "$stage" == rehearsal || "$stage" == formal ]]
: "${WK_CLOUD_SSH_CONFIG:?required}"
: "${WK_CLOUD_SERVICE1_IP:?required}"
: "${WK_CLOUD_SERVICE2_IP:?required}"
: "${WK_CLOUD_SERVICE3_IP:?required}"

timeout_seconds="${WK_CHAT_EVIDENCE_TIMEOUT:-30}"
[[ "$timeout_seconds" =~ ^[1-9][0-9]*$ && "$timeout_seconds" -le 120 ]]
max_bytes=4194304
capture_failures=0
install -d -m 0700 "$output/logs" "$output/metrics" "$output/profiles" "$output/system"
status_rows="$(mktemp)"
trap 'rm -f "$status_rows"' EXIT

record_status() {
  local class="$1" name="$2" state="$3" bytes="$4"
  jq -cn --arg class "$class" --arg name "$name" --arg state "$state" --argjson bytes "$bytes" \
    '{class:$class,name:$name,state:$state,bytes:$bytes}' >>"$status_rows"
}

capture_remote() {
  local host="$1" class="$2" name="$3" destination="$4" command="$5" temporary size state
  temporary="${destination}.tmp"
  rm -f "$temporary"
  if timeout "$timeout_seconds" ssh -F "$WK_CLOUD_SSH_CONFIG" "$host" \
    "set +o pipefail; $command | head -c $(( max_bytes + 1 ))" >"$temporary" 2>/dev/null; then
    size="$(wc -c <"$temporary" | tr -d '[:space:]')"
    if (( size > 0 && size <= max_bytes )); then
      install -m 0600 "$temporary" "$destination"
      rm -f "$temporary"
      record_status "$class" "$name" collected "$size"
      return 0
    fi
    state=empty
    (( size > max_bytes )) && state=oversized
    record_status "$class" "$name" "$state" "$size"
  else
    record_status "$class" "$name" unavailable 0
  fi
  rm -f "$temporary"
  capture_failures=$((capture_failures + 1))
  return 1
}

validate_prometheus_capture() {
  local name="$1" path="$2" filter="$3"
  if jq -e "$filter" "$path" >/dev/null 2>&1; then
    return 0
  fi
  record_status metrics "${name}-validation" invalid 0
  capture_failures=$((capture_failures + 1))
  return 1
}

service_units='wukongim.service wkbench-host-metrics.service node-exporter.service wukongim-process-metrics.service wukongim-evidence.timer'
for index in 1 2 3; do
  host="WK_CLOUD_SERVICE${index}_IP"
  address="${!host}"
  capture_remote "$address" logs "service-${index}" "$output/logs/service-${index}.log" \
    "sudo journalctl --no-pager --output=short-iso -n 500 $(for unit in $service_units; do printf -- '-u %q ' "$unit"; done)" || true
  capture_remote "$address" system "service-${index}" "$output/system/service-${index}.txt" \
    "sudo systemctl show $service_units --property=Id,ActiveState,SubState,Result,ExecMainCode,ExecMainStatus,NRestarts" || true
  for profile in heap goroutine; do
    query=''
    [[ "$profile" == heap ]] && query='?gc=1'
    [[ "$profile" == goroutine ]] && query='?debug=0'
    capture_remote "$address" profiles "service-${index}-${profile}" "$output/profiles/service-${index}-${profile}.pprof" \
      "sudo bash -c '. /etc/wukongim/secrets/node.env; exec curl --fail --silent --show-error --max-time 20 -H \"Authorization: Bearer \${WK_BENCH_API_TOKEN}\" \"http://127.0.0.1:5001/debug/pprof/${profile}${query}\"'" || true
  done
done

stage_unit="wkbench-${stage}.service"
load_units="wkbench-host-metrics.service wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service ${stage_unit} prometheus.service wkanalysis.service caddy.service node-exporter.service wukongim-process-metrics.service wukongim-evidence.timer"
capture_remote wukong-load logs load "$output/logs/load.log" \
  "sudo journalctl --no-pager --output=short-iso -n 1000 $(for unit in $load_units; do printf -- '-u %q ' "$unit"; done)" || true
capture_remote wukong-load system load "$output/system/load.txt" \
  "sudo systemctl show $load_units --property=Id,ActiveState,SubState,Result,ExecMainCode,ExecMainStatus,NRestarts" || true
capture_remote wukong-load metrics targets "$output/metrics/prometheus-targets.json" \
  "curl --fail --silent --show-error --max-time 20 'http://127.0.0.1:9090/api/v1/targets?state=active'" || true
if [[ -s "$output/metrics/prometheus-targets.json" ]]; then
  validate_prometheus_capture targets "$output/metrics/prometheus-targets.json" \
    '.status == "success" and (.data.activeTargets | type == "array")' || true
fi

end_epoch="$(date -u +%s)"
start_epoch=$((end_epoch - 900))
metric_query='{__name__=~"up|wukongim_(runtime_.*|channel_worker_.*|process_.*|metadata_create_total|activation_rejected_total|go_.*)"}'
capture_remote wukong-load metrics range "$output/metrics/prometheus-range.json" \
  "curl --get --fail --silent --show-error --max-time 30 --data-urlencode 'query=${metric_query}' --data-urlencode 'start=${start_epoch}' --data-urlencode 'end=${end_epoch}' --data-urlencode 'step=15' 'http://127.0.0.1:9090/api/v1/query_range'" || true
if [[ -s "$output/metrics/prometheus-range.json" ]]; then
  validate_prometheus_capture range "$output/metrics/prometheus-range.json" \
    '.status == "success" and (.data.result | type == "array")' || true
fi

jq -s --arg schema 'wukongim.chat_lifecycle.terminal_evidence/v1' --arg stage "$stage" \
  --arg observed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson failures "$capture_failures" \
  '{schema:$schema,stage:$stage,observed_at:$observed_at,max_file_bytes:4194304,
    capture_failures:$failures,complete:($failures == 0),captures:.,
    diagnosis_references:{window:"../diagnosis-window.json",result:"../diagnosis-result.json",optional:true}}' \
  "$status_rows" >"$output/manifest.json"

(( capture_failures == 0 ))
