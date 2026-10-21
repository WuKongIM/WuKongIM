PROBE_MAIN_PID=$BASHPID
PROBE_FIRED=0
set -T
probe_boundary() {
 [[ "$BASHPID" == "$PROBE_MAIN_PID" && "$PROBE_FIRED" == 0 ]] || return 0
 local hit=0
 case "$PROBE_MODE:$BASH_COMMAND" in
  'before:CAPTURE_PIDS+=("$!")') hit=1 ;;
  after:start_profile_request*heap*) hit=1 ;;
 esac
 [[ "$hit" == 1 ]] || return 0
 PROBE_FIRED=1
 local ready=0
 for ((probe_i=0; probe_i<200; probe_i++)); do
  if [[ -f "$PROBE_DIR/request-started" ]]; then ready=1; break; fi
  sleep .01
 done
 [[ "$ready" == 1 ]] || exit 97
 printf 'helper_pid=%s\nchild_pid=%s\nregistered=%s\n' "$BASHPID" "$!" "${#CAPTURE_PIDS[@]}" >"$PROBE_DIR/boundary.txt"
 kill -TERM "$PROBE_MAIN_PID"
}
trap probe_boundary DEBUG
