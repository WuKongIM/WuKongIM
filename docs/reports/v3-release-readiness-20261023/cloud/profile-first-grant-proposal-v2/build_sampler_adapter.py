from pathlib import Path
import hashlib,difflib
p=Path(__file__).absolute().parent;s=p.parent/'profile-first-grant-proposal-v1';a=(s/'capture-first-grant-profiles.sh').read_text();assert hashlib.sha256(a.encode()).hexdigest()=='197b86be9f5f245a9b4c0dccc78b660094e2399cf19d1b9aba26b5e5ea319448'
b=a.replace('wukongim.local_first_grant_profile/v1','wukongim.local_first_grant_profile/v2').replace('goroutine?debug=2','goroutine?debug=0').replace('node-2-goroutine.txt','node-2-goroutine.pb.gz')
b=b.replace('PROFILE_DIR="$OUT_DIR/profiles"','PROFILE_DIR="$OUT_DIR/profiles"\nREQUEST_DIR="$OUT_DIR/requests"')
b=b.replace('[[ -e "$METADATA_FILE" || -e "$PROFILE_DIR"','[[ -e "$REQUEST_DIR" || -L "$REQUEST_DIR" || -e "$METADATA_FILE" || -e "$PROFILE_DIR"')
b=b.replace('if ! mkdir "$PROFILE_DIR" 2>/dev/null; then','if ! mkdir "$PROFILE_DIR" "$REQUEST_DIR" 2>/dev/null; then')
b=b.replace('CAPTURE_STARTED=()','CAPTURE_STARTED=()\nCAPTURE_HTTP_FILES=()\nCAPTURE_RESULTS_RECORDED=()')
marker='terminate_children() {\n'
fn='''# Store only finite curl/HTTP evidence; raw URL, header and stderr are excluded.
record_request_result() {
  local index="$1" curl_code="$2" http_code http_json=null error_class=none bytes=0 limit=8388608
  [[ "${CAPTURE_RESULTS_RECORDED[$index]}" == 0 ]] || return 0
  http_code="$(LC_ALL=C head -c 4 "${CAPTURE_HTTP_FILES[$index]}" 2>/dev/null || true)"
  if [[ "$http_code" =~ ^[0-9][0-9][0-9]$ ]]; then http_json="\\\"$http_code\\\""; else http_code=unknown; fi
  if [[ -f "${CAPTURE_TMP_FILES[$index]}" ]]; then bytes="$(wc -c <"${CAPTURE_TMP_FILES[$index]}")"; fi
  [[ "${CAPTURE_KINDS[$index]}" == goroutine ]] && limit=4194304
  case "$curl_code" in
    0) if [[ "$http_code" != 200 ]]; then error_class=http_status; elif (( bytes == 0 )); then error_class=empty_body; elif (( bytes > limit )); then error_class=max_filesize; fi ;;
    22) error_class=http_error ;;
    28) error_class=timeout ;;
    63) error_class=max_filesize ;;
    7) error_class=connect_error ;;
    129|130|143) error_class=interrupted ;;
    *) error_class=curl_error ;;
  esac
  printf '{"schema":"wukongim/local-profile-request-status/v1","node_id":2,"kind":"%s","curl_exit_code":%s,"http_status":%s,"error_class":"%s","retained_body_bytes":%s,"max_bytes":%s}\\n' \\
    "${CAPTURE_KINDS[$index]}" "$curl_code" "$http_json" "$error_class" "$bytes" "$limit" \\
    >"$REQUEST_DIR/node-2-${CAPTURE_KINDS[$index]}.json"
  CAPTURE_RESULTS_RECORDED[$index]=1
}

'''
b=b.replace(marker,fn+marker,1)
b=b.replace('terminate_children() {\n  local pid','terminate_children() {\n  local pid index code')
old='''  for pid in "${CAPTURE_PIDS[@]}"; do
    [[ -n "$pid" ]] || continue
    wait "$pid" 2>/dev/null || true
  done'''
new='''  for index in "${!CAPTURE_PIDS[@]}"; do
    pid="${CAPTURE_PIDS[$index]}"
    [[ -n "$pid" ]] || continue
    code=0
    wait "$pid" 2>/dev/null || code=$?
    record_request_result "$index" "$code"
    CAPTURE_PIDS[$index]=''
  done'''
assert b.count(old)==1;b=b.replace(old,new)
b=b.replace('local temporary="$destination.next.$$" max_bytes=8388608','local temporary="$destination.next.$$" max_bytes=8388608\n  local http_file="$REQUEST_DIR/node-2-$kind-http-code"')
b=b.replace('"$url" >"$temporary" 2>/dev/null &','--output "$temporary" --write-out \'%{http_code}\' "$url" >"$http_file" 2>/dev/null &')
b=b.replace('  CAPTURE_KINDS+=("$kind")','  CAPTURE_KINDS+=("$kind")\n  CAPTURE_HTTP_FILES+=("$http_file")\n  CAPTURE_RESULTS_RECORDED+=(0)')
b=b.replace('local capture_index capture_status node_index','local capture_index capture_status node_index curl_code http_code')
old='''    if wait "${CAPTURE_PIDS[$capture_index]}" && [[ -s "${CAPTURE_TMP_FILES[$capture_index]}" ]]; then'''
new='''    curl_code=0
    wait "${CAPTURE_PIDS[$capture_index]}" || curl_code=$?
    record_request_result "$capture_index" "$curl_code"
    http_code="$(LC_ALL=C head -c 4 "${CAPTURE_HTTP_FILES[$capture_index]}" 2>/dev/null || true)"
    if [[ "$curl_code" -eq 0 && "$http_code" == 200 && -s "${CAPTURE_TMP_FILES[$capture_index]}" ]]; then'''
assert b.count(old)==1;b=b.replace(old,new)
(p/'capture-first-grant-profiles.sh').write_text(b);(p/'capture-first-grant-profiles.sh').chmod(0o700)
(p/'watch-first-grant.py').write_bytes((s/'watch-first-grant.py').read_bytes());(p/'watch-first-grant.py').chmod(0o700)
(p/'helper-v1-to-v2.diff').write_text(''.join(difflib.unified_diff(a.splitlines(True),b.splitlines(True),fromfile='frozen-proposal-v1/helper',tofile='proposal-v2/helper')))
print(hashlib.sha256(b.encode()).hexdigest())
