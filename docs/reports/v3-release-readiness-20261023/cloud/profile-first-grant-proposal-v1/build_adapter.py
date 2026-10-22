from pathlib import Path
import hashlib,difflib,json
P=Path(__file__).resolve().parent;S=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-loss-diagnosis/scripts/capture-wukongim-local-threshold-pprof.sh')
a=S.read_text();assert hashlib.sha256(a.encode()).hexdigest()=='b17347aea7acdfc6320c7db547065893814b105b7fbe5b6f9655b8b044b0780a';b=a
b=b.replace('wukongim.local_threshold_pprof/v1','wukongim.local_first_grant_profile/v1')
left=b.index("usage() {");right=b.index("\ndie_usage()",left)
b=b[:left]+"""usage() {
  cat <<'USAGE'
Temporary fixed-node2 first-grant adapter. Required: --out-dir, --phase-state-file,
--trigger-kind first_grant_receipt, --trigger-observed-phase measurement,
--previous-utc (actual first grant), --current-utc (receipt detection),
--node (one explicit loopback node2 URL), --cpu-seconds10.
One CPU request then one goroutine request at local submission+5s. No heap/context.
This adapts frozen threshold-helper ownership; it is not an unchanged threshold mode.
USAGE
}
"""+b[right:]
b=b.replace('actual_offered_ratio|sendack_p99|terminal_product_failure','first_grant_receipt')
b=b.replace('actual_offered_ratio|sendack_p99|terminal_product_failure) ;;','first_grant_receipt) ;;')
b=b.replace('[[ "$NODE_COUNT" -eq 1 || "$NODE_COUNT" -eq 3 ]]','[[ "$NODE_COUNT" -eq 1 ]]').replace("'exactly one or three --node values are required'","'exactly one physical node2 --node value is required'")
b=b.replace('NODE_COUNT="${#NODE_URLS[@]}"','[[ "$CPU_SECONDS" -eq 10 ]] || die_usage "temporary mode requires cpu-seconds10"\nNODE_COUNT="${#NODE_URLS[@]}"')
b=b.replace('      CAPTURE_CONTEXT=1','      die_usage "context capture is not permitted in temporary first-grant mode"')
b=b.replace('HEAP_STATUS[$index]=missing','HEAP_STATUS[$index]=not_requested')
b=b.replace('CAPTURE_PIDS=()','TIMER_PID=""\nCAPTURE_PIDS=()',1)
b=b.replace('terminate_children() {\n  local pid','terminate_children() {\n  local pid\n  if [[ -n "$TIMER_PID" ]]; then\n    kill "$TIMER_PID" 2>/dev/null || true\n    wait "$TIMER_PID" 2>/dev/null || true\n    TIMER_PID=""\n  fi')
b=b.replace('"$((node + 1))" "${CPU_STATUS[$node]}"','"2" "${CPU_STATUS[$node]}"')
b=b.replace('if mv "${CAPTURE_TMP_FILES[$capture_index]}" "${CAPTURE_FINAL_FILES[$capture_index]}"; then','limit=8388608\n      [[ "${CAPTURE_KINDS[$capture_index]}" == goroutine ]] && limit=4194304\n      bytes="$(wc -c <"${CAPTURE_TMP_FILES[$capture_index]}")"\n      if (( bytes <= limit )) && mv "${CAPTURE_TMP_FILES[$capture_index]}" "${CAPTURE_FINAL_FILES[$capture_index]}"; then')
start=b.index('for index in "${!NODE_URLS[@]}"; do\n  node_number=$((index + 1))\n  start_profile_request "$index" cpu')
end=b.index('\njoin_profile_requests\n',start)
b=b[:start]+'''# Temporary fixed-role schedule. The original request-registration, auth,
# cancellation and joins remain in use; this is not a threshold trigger.
index=0
start_profile_request "$index" cpu \
  "${NODE_URLS[$index]}/debug/pprof/profile?seconds=${CPU_SECONDS}" \
  "$PROFILE_DIR/node-2-cpu.pb.gz" "$CPU_MAX_TIME_SECONDS"
CAPTURE_REGISTERING=1
sleep 5 &
TIMER_PID=$!
CAPTURE_REGISTERING=0
if (( SIGNAL_PENDING == 1 )); then handle_signal; fi
wait "$TIMER_PID"
TIMER_PID=""
if [[ "$(read_phase_state)" != measurement ]]; then
  handle_signal
fi
start_profile_request "$index" goroutine \
  "${NODE_URLS[$index]}/debug/pprof/goroutine?debug=2" \
  "$PROFILE_DIR/node-2-goroutine.txt" "$SNAPSHOT_MAX_TIME_SECONDS"
'''+b[end:]
b=b.replace('if [[ "${CPU_STATUS[$index]}" != complete || "${HEAP_STATUS[$index]}" != complete ||\n      "${GOROUTINE_STATUS[$index]}" != complete ]]','if [[ "${CPU_STATUS[$index]}" != complete ||\n      "${GOROUTINE_STATUS[$index]}" != complete ]]')
assert 'sleep 5 &' in b and '/debug/pprof/heap' not in b and 'first_grant_receipt) ;;' in b
(P/'capture-first-grant-profiles.sh').write_text(b);(P/'capture-first-grant-profiles.sh').chmod(0o700)
(P/'helper-adaptation.diff').write_text(''.join(difflib.unified_diff(a.splitlines(True),b.splitlines(True),fromfile='frozen79a/capture-wukongim-local-threshold-pprof.sh',tofile='temporary/capture-first-grant-profiles.sh')))
print(json.dumps({'helper_sha256':hashlib.sha256(b.encode()).hexdigest(),'diff_sha256':hashlib.sha256((P/'helper-adaptation.diff').read_bytes()).hexdigest()}))
