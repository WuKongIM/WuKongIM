set -euo pipefail
analysis_bridge_pid="";analysis_bridge_url="";session_open=true;repository=fixture/project;request_id=fixture-request;run_id=fixture-run
close_attempted_request_id="";analysis_token="";analysis_worktree_added=false;analysis_worktree="";analysis_temp="";result_temp=""
analysis_temp=/private/tmp/wukongim-v3-readiness-20261021/scripts-signal-analysis-diagnosis/bash5-v2/analysis-original.temp
dispatch_close_once() { printf "closed\n" >> /private/tmp/wukongim-v3-readiness-20261021/scripts-signal-analysis-diagnosis/bash5-v2/analysis-original.closed; return 0; }
wk_cleanup_local_cloud_tools() { printf "done\n" >> /private/tmp/wukongim-v3-readiness-20261021/scripts-signal-analysis-diagnosis/bash5-v2/analysis-original.done; }
dispatch_close_best_effort() {
  local close_status=0
  if [[ "$session_open" != true || -z "$repository" || -z "$request_id" || -z "$run_id" ]]; then
    return
  fi
  if [[ "$close_attempted_request_id" == "$request_id" ]]; then
    printf 'Warning: Analysis close for request %s was already attempted; access-window expiry remains the backstop.\n' \
      "$request_id" >&2
    return
  fi
  if dispatch_close_once >/dev/null; then
    session_open=false
    return
  else
    close_status=$?
  fi
  printf 'Warning: Analysis close for request %s could not be confirmed (status %d); the bounded access-window expiry and scheduled cleanup remain the backstop.\n' \
    "$request_id" "$close_status" >&2
}
stop_analysis_bridge() {
  local bridge_wait
  if [[ -z "$analysis_bridge_pid" ]]; then
    return
  fi
  kill "$analysis_bridge_pid" 2>/dev/null || true
  for ((bridge_wait = 0; bridge_wait < 50; bridge_wait += 1)); do
    kill -0 "$analysis_bridge_pid" 2>/dev/null || break
    sleep 0.1
  done
  if kill -0 "$analysis_bridge_pid" 2>/dev/null; then
    kill -KILL "$analysis_bridge_pid" 2>/dev/null || true
  fi
  wait "$analysis_bridge_pid" 2>/dev/null || true
  analysis_bridge_pid=""
  analysis_bridge_url=""
}
cleanup() {
  local exit_status=$?
  trap - EXIT
  stop_analysis_bridge
  dispatch_close_best_effort
  analysis_token=""
  if [[ "$analysis_worktree_added" == true && -n "$analysis_worktree" ]]; then
    run_git_checkout_bounded -C "$REPO_ROOT" worktree remove --force "$analysis_worktree" >/dev/null 2>&1 || true
  fi
  if [[ -n "$analysis_temp" && -d "$analysis_temp" ]]; then
    rm -rf "$analysis_temp"
  fi
  if [[ -n "$result_temp" ]]; then
    rm -f "$result_temp"
  fi
  wk_cleanup_local_cloud_tools
  exit "$exit_status"
}
trap cleanup EXIT
exit 17
