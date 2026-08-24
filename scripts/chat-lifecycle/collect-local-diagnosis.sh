#!/usr/bin/env bash
set -euo pipefail

: "${WK_CHAT_LAB_DIAGNOSIS_DIR:?required}"
: "${WK_CHAT_LAB_SSH_CONFIG:?required}"
: "${WK_CHAT_LAB_REQUEST_ID:?required}"
[[ "$WK_CHAT_LAB_DIAGNOSIS_DIR" == /* && -d "$WK_CHAT_LAB_DIAGNOSIS_DIR" && ! -L "$WK_CHAT_LAB_DIAGNOSIS_DIR" ]]
[[ -f "$WK_CHAT_LAB_SSH_CONFIG" && ! -L "$WK_CHAT_LAB_SSH_CONFIG" ]]
command -v timeout >/dev/null 2>&1 || { echo 'portable timeout command is unavailable' >&2; exit 1; }

umask 077
failed=0
bounded_remote() {
  local maximum_bytes="$1" output="$2"
  shift 2
  local temporary="${output}.next.$$" bytes
  rm -f -- "$temporary"
  if ! timeout 45 ssh -F "$WK_CHAT_LAB_SSH_CONFIG" wukong-load "$@" >"$temporary"; then
    rm -f -- "$temporary"
    failed=$(( failed + 1 ))
    return 1
  fi
  bytes="$(wc -c <"$temporary" | tr -d ' ')"
  if ! [[ "$bytes" =~ ^[0-9]+$ ]] || (( bytes > maximum_bytes )); then
    rm -f -- "$temporary"
    failed=$(( failed + 1 ))
    return 1
  fi
  chmod 0600 "$temporary"
  mv -f -- "$temporary" "$output"
}

bounded_remote 32768 "$WK_CHAT_LAB_DIAGNOSIS_DIR/services.txt" \
  "sudo systemctl show wukongim.service wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service wkbench-rehearsal.service --property=Id --property=ActiveState --property=SubState --property=Result --no-pager" || true
bounded_remote 262144 "$WK_CHAT_LAB_DIAGNOSIS_DIR/stage-journal.txt" \
  "sudo journalctl -u wkbench-rehearsal.service --no-pager -n 2000 -o short-iso" || true
bounded_remote 262144 "$WK_CHAT_LAB_DIAGNOSIS_DIR/prometheus-targets.json" \
  "curl --silent --show-error --fail --max-time 20 http://127.0.0.1:9090/api/v1/targets?state=active" || true

for worker in 1 2 3; do
  port=$(( 19090 + worker ))
  bounded_remote 16384 "$WK_CHAT_LAB_DIAGNOSIS_DIR/worker-${worker}-status.json" \
    "sudo bash -euo pipefail -c 'source /etc/wukongim/secrets/load.env; curl --silent --show-error --fail --max-time 20 -H \"Authorization: Bearer \${WK_BENCH_WORKER_TOKEN}\" http://127.0.0.1:${port}/v1/chat-lifecycle/status'" || true
  bounded_remote 16384 "$WK_CHAT_LAB_DIAGNOSIS_DIR/worker-${worker}-snapshot.json" \
    "sudo bash -euo pipefail -c 'source /etc/wukongim/secrets/load.env; curl --silent --show-error --fail --max-time 20 -H \"Authorization: Bearer \${WK_BENCH_WORKER_TOKEN}\" http://127.0.0.1:${port}/v1/chat-lifecycle/snapshot'" || true
done

classification=captured
if (( failed != 0 )); then
  classification=insufficient_evidence
fi
jq -n --arg schema 'wukongim.chat_lifecycle.local_diagnosis/v1' \
  --arg request_id "$WK_CHAT_LAB_REQUEST_ID" --arg observed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg classification "$classification" --argjson failed_captures "$failed" \
  '{schema:$schema,request_id:$request_id,observed_at:$observed_at,classification:$classification,
    failed_captures:$failed_captures,provider_contacted:false,mutation_performed:false}' \
  >"$WK_CHAT_LAB_DIAGNOSIS_DIR/summary.json"
chmod 0600 "$WK_CHAT_LAB_DIAGNOSIS_DIR/summary.json"
