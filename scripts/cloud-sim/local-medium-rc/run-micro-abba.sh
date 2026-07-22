#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
EVALUATOR="$SCRIPT_DIR/evaluate-micro-abba.sh"
BUILD_MANIFEST_POLICY="$SCRIPT_DIR/validate-build-smoke-manifest.jq"
EXPECTED_SCHEMA="wukongim/local-medium-rc-revision-neutral-build-smoke/v1"
AUTHORITY_BENCHMARK="BenchmarkLocalMediumRCRevisionNeutralAuthorityResolve512x256"
OWNER_BENCHMARK="BenchmarkLocalMediumRCRevisionNeutralOwnerPushAck512x221x55"
EQUIVALENCE_TEST="TestLocalMediumRCWorkloadEquivalence"
CONFIRM_VALUE="RUN_REVISION_NEUTRAL_MICRO_ABBA"
BASELINE_LOCK_PATH="scripts/cloud-sim/local-medium-rc/baseline-lock.json"

usage() {
  cat <<'USAGE'
Usage: run-micro-abba.sh BUILD_SMOKE_DIR OUTPUT_DIR

Runs the two test binaries already produced by build-smoke.sh in the exact
A1/B1/B2/A2 order. This command has no build, server, or cloud side effects.
USAGE
}

die() {
  printf 'local-medium-rc-micro-abba: %s\n' "$*" >&2
  exit 1
}

if [[ $# -ne 2 ]]; then
  usage >&2
  exit 2
fi

BUILD_DIR="$1"
OUTPUT_DIR="$2"
MANIFEST="$BUILD_DIR/build-smoke-manifest.json"

[[ "${WK_LOCAL_RC_CONFIRM_MICRO:-}" == "$CONFIRM_VALUE" ]] ||
  die "set WK_LOCAL_RC_CONFIRM_MICRO=$CONFIRM_VALUE to authorize local timing"
[[ -d "$BUILD_DIR" && ! -L "$BUILD_DIR" ]] || die "build-smoke directory is missing or symlinked"
[[ -f "$MANIFEST" && ! -L "$MANIFEST" ]] || die "build-smoke manifest is missing or symlinked"
[[ ! -e "$OUTPUT_DIR" ]] || die "output already exists: $OUTPUT_DIR"

for command_name in awk cmp date grep jq ps sed sleep sort uname; do
  command -v "$command_name" >/dev/null 2>&1 || die "required timing tool is missing: $command_name"
done
SLEEP_TOOL="$(command -v sleep)"
case "$(uname -s)" in
  Darwin) command -v top >/dev/null 2>&1 || die "required macOS CPU idle tool is missing: top" ;;
  Linux) [[ -r /proc/stat ]] || die "required Linux CPU idle source is missing: /proc/stat" ;;
  *) die "unsupported timing host OS: $(uname -s)" ;;
esac
if command -v shasum >/dev/null 2>&1; then
  SHA256_COMMAND=shasum
elif command -v sha256sum >/dev/null 2>&1; then
  SHA256_COMMAND=sha256sum
else
  die "required SHA-256 tool is missing"
fi

sha256() {
  if [[ "$SHA256_COMMAND" == "shasum" ]]; then
    LC_ALL=C LANG=C shasum -a 256 "$1" | awk '{print $1}'
  else
    LC_ALL=C LANG=C sha256sum "$1" | awk '{print $1}'
  fi
}

BENCHTIME_SECONDS="${WK_LOCAL_RC_MICRO_BENCHTIME_SECONDS:-5}"
SAMPLES_PER_LEG="${WK_LOCAL_RC_MICRO_COUNT:-6}"
COOLDOWN_SECONDS="${WK_LOCAL_RC_MICRO_COOLDOWN_SECONDS:-120}"
MINIMUM_IDLE_PERCENT="${WK_LOCAL_RC_MICRO_MINIMUM_IDLE_PERCENT:-85}"
HIGH_CPU_PERCENT="${WK_LOCAL_RC_MICRO_HIGH_CPU_PERCENT:-20}"
HARNESS_PATHS=(
  "scripts/cloud-sim/local-medium-rc/build-smoke.sh"
  "scripts/cloud-sim/local-medium-rc/baseline-lock.json"
  "scripts/cloud-sim/local-medium-rc/workload_test.go.overlay"
  "scripts/cloud-sim/local-medium-rc/adapter_baseline_test.go.overlay"
  "scripts/cloud-sim/local-medium-rc/adapter_candidate_test.go.overlay"
  "scripts/cloud-sim/local-medium-rc/run-micro-abba.sh"
  "scripts/cloud-sim/local-medium-rc/evaluate-micro-abba.sh"
  "scripts/cloud-sim/local-medium-rc/evaluate-micro-abba.jq"
  "scripts/cloud-sim/local-medium-rc/validate-build-smoke-manifest.jq"
  "scripts/cloud-sim/local-medium-rc/test-micro-abba-static.sh"
)

[[ "$BENCHTIME_SECONDS" =~ ^[0-9]+$ && "$BENCHTIME_SECONDS" -ge 5 ]] || die "benchtime seconds must be an integer >=5"
[[ "$SAMPLES_PER_LEG" =~ ^[0-9]+$ && "$SAMPLES_PER_LEG" -ge 6 ]] || die "sample count must be an integer >=6"
[[ "$COOLDOWN_SECONDS" =~ ^[0-9]+$ && "$COOLDOWN_SECONDS" -ge 120 ]] || die "cooldown seconds must be an integer >=120"
[[ "$MINIMUM_IDLE_PERCENT" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "minimum idle percent must be numeric"
awk -v value="$MINIMUM_IDLE_PERCENT" 'BEGIN {exit !(value >= 85 && value <= 100)}' ||
  die "minimum idle percent must remain within [85,100]"
[[ "$HIGH_CPU_PERCENT" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "high CPU percent must be numeric"
awk -v value="$HIGH_CPU_PERCENT" 'BEGIN {exit !(value > 0 && value <= 20)}' ||
  die "high CPU percent must remain within (0,20]"

[[ -f "$BUILD_MANIFEST_POLICY" && ! -L "$BUILD_MANIFEST_POLICY" ]] || die "build-smoke manifest policy is missing or symlinked"
jq -e -f "$BUILD_MANIFEST_POLICY" "$MANIFEST" >/dev/null || die "build-smoke manifest contract is invalid"

baseline_source="$(jq -er '.baseline_source_sha' "$MANIFEST")"
candidate_source="$(jq -er '.candidate_source_sha' "$MANIFEST")"
expected_baseline="${WK_LOCAL_RC_EXPECTED_BASELINE_SHA:-}"
expected_candidate="${WK_LOCAL_RC_EXPECTED_CANDIDATE_SHA:-}"
[[ "$expected_baseline" =~ ^[0-9a-f]{40}$ ]] || die "WK_LOCAL_RC_EXPECTED_BASELINE_SHA must be an exact lowercase commit SHA"
[[ "$expected_candidate" =~ ^[0-9a-f]{40}$ ]] || die "WK_LOCAL_RC_EXPECTED_CANDIDATE_SHA must be an exact lowercase commit SHA"
[[ "$baseline_source" == "$expected_baseline" ]] || die "build-smoke baseline differs from WK_LOCAL_RC_EXPECTED_BASELINE_SHA"
[[ "$candidate_source" == "$expected_candidate" ]] || die "build-smoke candidate differs from WK_LOCAL_RC_EXPECTED_CANDIDATE_SHA"
[[ -f "$ROOT_DIR/$BASELINE_LOCK_PATH" && ! -L "$ROOT_DIR/$BASELINE_LOCK_PATH" ]] || die "candidate-bound baseline lock is missing or symlinked"
jq -e '
  (keys | sort) == ["baseline_source_sha", "schema", "source_run_identity"] and
  .schema == "wukongim/local-medium-rc-baseline-lock/v1" and
  (.baseline_source_sha | type == "string" and test("^[0-9a-f]{40}$")) and
  (.source_run_identity | type == "string" and test("^gh-[0-9]+-[0-9]+$"))
' "$ROOT_DIR/$BASELINE_LOCK_PATH" >/dev/null || die "candidate-bound baseline lock contract is invalid"
[[ "$(sha256 "$ROOT_DIR/$BASELINE_LOCK_PATH")" == "$(jq -er '.baseline_lock.sha256' "$MANIFEST")" ]] ||
  die "candidate-bound baseline lock SHA-256 mismatch"
[[ "$(jq -er '.baseline_source_sha' "$ROOT_DIR/$BASELINE_LOCK_PATH")" == "$baseline_source" ]] ||
  die "build-smoke baseline differs from candidate-bound baseline lock"
[[ "$(jq -er '.source_run_identity' "$ROOT_DIR/$BASELINE_LOCK_PATH")" == "$(jq -er '.baseline_lock.source_run_identity' "$MANIFEST")" ]] ||
  die "candidate-bound baseline lock run identity mismatch"
git -C "$ROOT_DIR" cat-file -e "$baseline_source^{commit}" 2>/dev/null || die "baseline commit is unavailable"
git -C "$ROOT_DIR" cat-file -e "$candidate_source^{commit}" 2>/dev/null || die "candidate commit is unavailable"
git -C "$ROOT_DIR" merge-base --is-ancestor "$baseline_source" "$candidate_source" ||
  die "candidate commit does not descend from baseline"

resolve_binary() {
  local variant="$1" relative expected_sha path base_real parent_real
  relative="$(jq -er --arg variant "$variant" '.binaries[$variant].path' "$MANIFEST")"
  [[ "$relative" =~ ^bin/[A-Za-z0-9._-]+$ ]] || die "unsafe $variant binary path"
  base_real="$(cd "$BUILD_DIR" && pwd -P)"
  path="$BUILD_DIR/$relative"
  [[ -f "$path" && ! -L "$path" && -x "$path" ]] || die "$variant binary is missing, symlinked, or not executable"
  parent_real="$(cd "$(dirname "$path")" && pwd -P)"
  case "$parent_real/$(basename "$path")" in
    "$base_real"/*) ;;
    *) die "$variant binary escapes the build-smoke directory" ;;
  esac
  expected_sha="$(jq -er --arg variant "$variant" '.binaries[$variant].sha256' "$MANIFEST")"
  [[ "$(sha256 "$path")" == "$expected_sha" ]] || die "$variant binary SHA-256 mismatch"
  printf '%s/%s\n' "$parent_real" "$(basename "$path")"
}

baseline_binary="$(resolve_binary baseline)"
candidate_binary="$(resolve_binary candidate)"
manifest_sha="$(sha256 "$MANIFEST")"
baseline_binary_sha="$(sha256 "$baseline_binary")"
candidate_binary_sha="$(sha256 "$candidate_binary")"

metadata_value() {
  local metadata="$1" key="$2"
  awk -v key="$key" '
    $1 == "build" && index($2, key "=") == 1 {
      value = $2
      sub("^" key "=", "", value)
      print value
      exit
    }
  ' <<<"$metadata"
}

go_tool="$(jq -er '.toolchain.path' "$MANIFEST")"
[[ -x "$go_tool" && ! -d "$go_tool" ]] || die "recorded Go tool is unavailable"
[[ "$(sha256 "$go_tool")" == "$(jq -er '.toolchain.sha256' "$MANIFEST")" ]] || die "Go tool SHA-256 mismatch"
[[ "$(env GOWORK=off GOTOOLCHAIN=local "$go_tool" version)" == "$(jq -er '.toolchain.version' "$MANIFEST")" ]] || die "Go tool version mismatch"
verify_recorded_toolchain() {
  local actual_root host_os host_arch name expected_path actual_path expected_sha
  actual_root="$(env GOWORK=off GOTOOLCHAIN=local "$go_tool" env GOROOT)" || die "could not read recorded toolchain GOROOT"
  [[ -d "$actual_root" ]] || die "recorded toolchain GOROOT is unavailable"
  actual_root="$(cd "$actual_root" && pwd -P)"
  host_os="$(env GOWORK=off GOTOOLCHAIN=local "$go_tool" env GOHOSTOS)" || die "could not read recorded toolchain GOHOSTOS"
  host_arch="$(env GOWORK=off GOTOOLCHAIN=local "$go_tool" env GOHOSTARCH)" || die "could not read recorded toolchain GOHOSTARCH"
  [[ "$actual_root" == "$(jq -er '.toolchain.goroot.path' "$MANIFEST")" ]] || die "recorded toolchain GOROOT mismatch"
  [[ "$host_os" == "$(jq -er '.toolchain.goroot.gohostos' "$MANIFEST")" ]] || die "recorded toolchain GOHOSTOS mismatch"
  [[ "$host_arch" == "$(jq -er '.toolchain.goroot.gohostarch' "$MANIFEST")" ]] || die "recorded toolchain GOHOSTARCH mismatch"
  for name in compile link asm; do
    expected_path="$(jq -er --arg name "$name" '.toolchain.tools[$name].path' "$MANIFEST")"
    expected_sha="$(jq -er --arg name "$name" '.toolchain.tools[$name].sha256' "$MANIFEST")"
    actual_path="$actual_root/pkg/tool/${host_os}_${host_arch}/$name"
    [[ "$expected_path" == "$actual_path" ]] || die "recorded Go $name path mismatch"
    [[ -f "$actual_path" && ! -L "$actual_path" && -x "$actual_path" ]] || die "recorded Go $name tool is unavailable"
    [[ "$(sha256 "$actual_path")" == "$expected_sha" ]] || die "recorded Go $name SHA-256 mismatch"
  done
}
verify_recorded_toolchain

BENCHMARK_REGEX="^(${AUTHORITY_BENCHMARK}|${OWNER_BENCHMARK})$"
EXPECTED_BENCHMARKS="$(printf '%s\n%s\n' "$AUTHORITY_BENCHMARK" "$OWNER_BENCHMARK" | LC_ALL=C sort)"
verify_binary_contract() {
  local variant="$1" binary="$2" source="$3" metadata listed embedded_go expected_go
  metadata="$(env GOWORK=off GOTOOLCHAIN=local "$go_tool" version -m "$binary")" || die "could not read $variant binary metadata"
  embedded_go="$(awk 'NR == 1 {print $NF}' <<<"$metadata")"
  expected_go="$(awk '{print $3}' <<<"$(jq -er '.toolchain.version' "$MANIFEST")")"
  [[ "$embedded_go" == "$expected_go" ]] || die "$variant binary embedded Go version differs from build-smoke toolchain"
  [[ "$(metadata_value "$metadata" vcs.revision)" == "$source" ]] || die "$variant binary revision mismatch"
  [[ "$(metadata_value "$metadata" vcs.modified)" == "false" ]] || die "$variant binary is not clean"
  [[ "$(metadata_value "$metadata" -trimpath)" == "true" ]] || die "$variant binary is not trimpath"
  listed="$(env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin HOME="${HOME:-/tmp}" LANG=C LC_ALL=C TZ=UTC \
    "$binary" -test.list "$BENCHMARK_REGEX" | LC_ALL=C sort)" || die "could not list $variant benchmarks"
  [[ "$listed" == "$EXPECTED_BENCHMARKS" ]] || die "$variant benchmark names differ from the frozen contract"
}
verify_binary_contract baseline "$baseline_binary" "$baseline_source"
verify_binary_contract candidate "$candidate_binary" "$candidate_source"

HARNESS_ENTRIES="$(mktemp "${TMPDIR:-/tmp}/wk-local-medium-rc-harness.XXXXXX")"
trap 'rm -f "${HARNESS_ENTRIES:-}"' EXIT
: >"$HARNESS_ENTRIES"
for harness_path in "${HARNESS_PATHS[@]}"; do
  [[ -f "$ROOT_DIR/$harness_path" && ! -L "$ROOT_DIR/$harness_path" ]] || die "missing or symlinked harness file: $harness_path"
  git -C "$ROOT_DIR" show "$candidate_source:$harness_path" | cmp -s - "$ROOT_DIR/$harness_path" ||
    die "working harness does not match candidate commit: $harness_path"
  jq -cn --arg path "$harness_path" --arg sha "$(sha256 "$ROOT_DIR/$harness_path")" '{path:$path,sha256:$sha}' >>"$HARNESS_ENTRIES"
done
HARNESS_FILES_JSON="$(jq -s . "$HARNESS_ENTRIES")"
HARNESS_BUNDLE_SHA="$(jq -sr '.[] | [.path,.sha256] | @tsv' "$HARNESS_ENTRIES" | sha256 /dev/stdin)"

[[ "$(sha256 "$ROOT_DIR/scripts/cloud-sim/local-medium-rc/workload_test.go.overlay")" == "$(jq -er '.common_workload.sha256' "$MANIFEST")" ]] ||
  die "common workload SHA-256 differs from candidate-bound source"
[[ "$(sha256 "$ROOT_DIR/scripts/cloud-sim/local-medium-rc/adapter_baseline_test.go.overlay")" == "$(jq -er '.adapters.baseline.sha256' "$MANIFEST")" ]] ||
  die "baseline adapter SHA-256 differs from candidate-bound source"
[[ "$(sha256 "$ROOT_DIR/scripts/cloud-sim/local-medium-rc/adapter_candidate_test.go.overlay")" == "$(jq -er '.adapters.candidate.sha256' "$MANIFEST")" ]] ||
  die "candidate adapter SHA-256 differs from candidate-bound source"

EQUIVALENCE_TMP="$(mktemp -d "${TMPDIR:-/tmp}/wk-local-medium-rc-equivalence.XXXXXX")"
trap 'rm -f "${HARNESS_ENTRIES:-}"; rm -rf "${EQUIVALENCE_TMP:-}"' EXIT
verify_equivalence() {
  local variant="$1" binary="$2" relative expected_path expected_sha recorded rerun recorded_marker rerun_marker
  relative="$(jq -er --arg variant "$variant" '.equivalence[$variant].path' "$MANIFEST")"
  expected_path="equivalence/$variant.txt"
  [[ "$relative" == "$expected_path" ]] || die "$variant equivalence path is not canonical"
  recorded="$BUILD_DIR/$relative"
  [[ -f "$recorded" && ! -L "$recorded" ]] || die "$variant equivalence output is missing or symlinked"
  expected_sha="$(jq -er --arg variant "$variant" '.equivalence[$variant].sha256' "$MANIFEST")"
  [[ "$(sha256 "$recorded")" == "$expected_sha" ]] || die "$variant equivalence output SHA-256 mismatch"
  [[ "$(grep -Fc "WKRC-EQUIVALENCE variant=$variant " "$recorded" || true)" == "1" ]] ||
    die "$variant recorded equivalence marker is missing or duplicated"
  [[ "$(grep -Fxc 'PASS' "$recorded" || true)" == "1" ]] || die "$variant recorded equivalence output did not pass"
  recorded_marker="$(sed -n 's/^.*\(WKRC-EQUIVALENCE variant=.*\)$/\1/p' "$recorded")"
  rerun="$EQUIVALENCE_TMP/$variant.txt"
  env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin HOME="${HOME:-/tmp}" LANG=C LC_ALL=C TZ=UTC \
    "$binary" -test.run "^${EQUIVALENCE_TEST}$" -test.count 1 -test.v >"$rerun" 2>&1 ||
    die "$variant equivalence rerun failed"
  [[ "$(grep -Fc "WKRC-EQUIVALENCE variant=$variant " "$rerun" || true)" == "1" ]] ||
    die "$variant equivalence rerun marker is missing or duplicated"
  [[ "$(grep -Fxc 'PASS' "$rerun" || true)" == "1" ]] || die "$variant equivalence rerun did not pass"
  rerun_marker="$(sed -n 's/^.*\(WKRC-EQUIVALENCE variant=.*\)$/\1/p' "$rerun")"
  [[ "$rerun_marker" == "$recorded_marker" ]] || die "$variant equivalence rerun differs from build-smoke evidence"
}
verify_equivalence baseline "$baseline_binary"
verify_equivalence candidate "$candidate_binary"

cpu_idle_percent() {
  case "$(uname -s)" in
    Darwin)
      top -l 2 -n 0 -s 1 | awk -F'[, %]+' '
        /CPU usage:/ {
          for (index = 1; index <= NF; index++) if ($index == "idle") idle = $(index - 1)
        }
        END {if (idle == "") exit 1; printf "%.2f\n", idle}
      '
      ;;
    Linux)
      [[ -r /proc/stat ]] || return 1
      local first second
      first="$(awk '/^cpu / {print; exit}' /proc/stat)"
      sleep 1
      second="$(awk '/^cpu / {print; exit}' /proc/stat)"
      awk -v first="$first" -v second="$second" '
        BEGIN {
          count_a = split(first, a); count_b = split(second, b)
          total_a = 0; total_b = 0
          for (i = 2; i <= count_a; i++) total_a += a[i]
          for (i = 2; i <= count_b; i++) total_b += b[i]
          idle_a = a[5] + a[6]; idle_b = b[5] + b[6]
          total = total_b - total_a; idle = idle_b - idle_a
          if (total <= 0) exit 1
          printf "%.2f\n", 100 * idle / total
        }
      '
      ;;
    *) return 1 ;;
  esac
}

high_cpu_processes() {
  local allowed_pid="${1:-0}"
  ps -Ao pid=,pcpu=,command= | awk \
    -v allowed="$allowed_pid" -v self="$$" -v parent="$PPID" -v limit="$HIGH_CPU_PERCENT" '
      $1 != allowed && $1 != self && $1 != parent && ($2 + 0) >= limit {print}
    '
}

require_clean_host() {
  local leg="$1" idle high_cpu snapshot
  idle="$(cpu_idle_percent)" || die "$leg could not read host CPU idle"
  awk -v idle="$idle" -v minimum="$MINIMUM_IDLE_PERCENT" 'BEGIN {exit !(idle >= minimum)}' ||
    die "$leg host idle ${idle}% is below ${MINIMUM_IDLE_PERCENT}%"
  snapshot="$(ps -Ao pid=,pcpu=,command=)"
  high_cpu="$(awk -v self="$$" -v parent="$PPID" -v limit="$HIGH_CPU_PERCENT" '
    $1 != self && $1 != parent && ($2 + 0) >= limit {print}
  ' <<<"$snapshot")"
  [[ -z "$high_cpu" ]] || die "$leg unrelated process is at or above ${HIGH_CPU_PERCENT}% CPU: $high_cpu"
  printf '%s\n' "$snapshot" >"$OUTPUT_DIR/host-$leg-before.tsv"
  printf '%s\n' "$idle"
}

mkdir -p "$OUTPUT_DIR/raw"
NOISE_FILE="$OUTPUT_DIR/external-high-cpu.tsv"
: >"$NOISE_FILE"
ACTIVE_CHILD_PID=""
ACTIVE_CHILD_COMMAND=""
ACTIVE_CHILD_TOKEN=""
ACTIVE_CHILD_KIND=""
CHILD_TERM_GRACE_SECONDS=10

clear_active_child() {
  ACTIVE_CHILD_PID=""
  ACTIVE_CHILD_COMMAND=""
  ACTIVE_CHILD_TOKEN=""
  ACTIVE_CHILD_KIND=""
}

track_active_child() {
  local pid="$1" kind="$2" command_prefix="$3" attempt child_parent child_command
  ACTIVE_CHILD_PID="$pid"
  ACTIVE_CHILD_COMMAND=""
  ACTIVE_CHILD_TOKEN="$command_prefix"
  ACTIVE_CHILD_KIND="$kind"
  for ((attempt = 0; attempt < 20; attempt++)); do
    child_parent="$(ps -p "$pid" -o ppid= 2>/dev/null | awk '{print $1}')"
    child_command="$(ps -p "$pid" -o command= 2>/dev/null | sed 's/^[[:space:]]*//' || true)"
    if [[ "$child_parent" == "$$" && ("$child_command" == "$command_prefix" || "$child_command" == "$command_prefix "*) ]]; then
      ACTIVE_CHILD_COMMAND="$child_command"
      return 0
    fi
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.05
  done
  die "could not bind $kind child identity"
}

active_child_identity_matches() {
  local child_parent child_command
  [[ -n "$ACTIVE_CHILD_PID" ]] || return 1
  child_parent="$(ps -p "$ACTIVE_CHILD_PID" -o ppid= 2>/dev/null | awk '{print $1}')"
  child_command="$(ps -p "$ACTIVE_CHILD_PID" -o command= 2>/dev/null | sed 's/^[[:space:]]*//' || true)"
  [[ "$child_parent" == "$$" ]] || return 1
  if [[ -n "$ACTIVE_CHILD_COMMAND" ]]; then
    [[ "$child_command" == "$ACTIVE_CHILD_COMMAND" ]]
    return
  fi
  [[ -n "$ACTIVE_CHILD_TOKEN" && "$child_command" == *"$ACTIVE_CHILD_TOKEN"* ]]
}

cleanup_active_test() {
  local attempt child_status
  [[ -n "$ACTIVE_CHILD_PID" ]] || return 0
  if ! active_child_identity_matches; then
    clear_active_child
    return 0
  fi
  kill -TERM "$ACTIVE_CHILD_PID" 2>/dev/null || true
  for ((attempt = 0; attempt < CHILD_TERM_GRACE_SECONDS; attempt++)); do
    if ! kill -0 "$ACTIVE_CHILD_PID" 2>/dev/null; then
      wait "$ACTIVE_CHILD_PID" 2>/dev/null || true
      clear_active_child
      return 0
    fi
    child_status="$(ps -p "$ACTIVE_CHILD_PID" -o stat= 2>/dev/null | awk '{print $1}')"
    if [[ "$child_status" == Z* ]]; then
      wait "$ACTIVE_CHILD_PID" 2>/dev/null || true
      clear_active_child
      return 0
    fi
    if ! active_child_identity_matches; then
      clear_active_child
      return 0
    fi
    sleep 1
  done
  if active_child_identity_matches; then
    kill -KILL "$ACTIVE_CHILD_PID" 2>/dev/null || true
    for ((attempt = 0; attempt < 2; attempt++)); do
      child_status="$(ps -p "$ACTIVE_CHILD_PID" -o stat= 2>/dev/null | awk '{print $1}')"
      if [[ -z "$child_status" || "$child_status" == Z* ]]; then
        wait "$ACTIVE_CHILD_PID" 2>/dev/null || true
        break
      fi
      sleep 1
    done
  fi
  clear_active_child
}

interrupt_run() {
  local exit_code="$1"
  trap - INT TERM HUP
  cleanup_active_test
  exit "$exit_code"
}

trap 'cleanup_active_test; rm -f "${HARNESS_ENTRIES:-}"; rm -rf "${EQUIVALENCE_TMP:-}"' EXIT
trap 'interrupt_run 130' INT
trap 'interrupt_run 143' TERM
trap 'interrupt_run 129' HUP

run_cooldown() {
  local pid rc=0
  "$SLEEP_TOOL" "$COOLDOWN_SECONDS" &
  pid="$!"
  track_active_child "$pid" cooldown "$SLEEP_TOOL"
  wait "$pid" || rc=$?
  clear_active_child
  (( rc == 0 )) || die "cooldown sleep exited $rc"
}

run_leg() {
  local leg="$1" variant="$2" binary="$3" source="$4" binary_sha="$5" idle="$6"
  local raw="$OUTPUT_DIR/raw/$leg.txt" started finished pid rc=0
  started="$(date +%s)"
  {
    printf '# wkrc-micro-leg %s variant=%s\n' "$leg" "$variant"
    printf '# wkrc-source-sha %s\n' "$source"
    printf '# wkrc-test-binary-sha256 %s\n' "$binary_sha"
    printf '# wkrc-started-epoch %s\n' "$started"
    printf '# wkrc-host-idle-before %s\n' "$idle"
    printf '# wkrc-benchtime %ss\n' "$BENCHTIME_SECONDS"
    printf '# wkrc-count %s\n' "$SAMPLES_PER_LEG"
    printf '# wkrc-gomaxprocs 4\n'
    printf '# wkrc-benchmark %s\n' "$AUTHORITY_BENCHMARK"
    printf '# wkrc-benchmark %s\n' "$OWNER_BENCHMARK"
  } >"$raw"
  env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin HOME="${HOME:-/tmp}" LANG=C LC_ALL=C TZ=UTC GOMAXPROCS=4 \
    "$binary" -test.run '^$' -test.bench "^(${AUTHORITY_BENCHMARK}|${OWNER_BENCHMARK})$" \
    -test.benchmem -test.benchtime "${BENCHTIME_SECONDS}s" -test.count "$SAMPLES_PER_LEG" >>"$raw" 2>&1 &
  pid="$!"
  track_active_child "$pid" benchmark "$binary"
  while kill -0 "$pid" 2>/dev/null; do
    high_cpu_processes "$pid" | awk -v epoch="$(date +%s)" '{print epoch "\t" $0}' >>"$NOISE_FILE"
    sleep 1
  done
  wait "$pid" || rc=$?
  clear_active_child
  finished="$(date +%s)"
  printf '# wkrc-finished-epoch %s\n' "$finished" >>"$raw"
  (( rc == 0 )) || die "$leg test binary exited $rc; raw output retained at $raw"
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$leg" "$variant" "$started" "$finished" "$idle" "$(sha256 "$raw")"
}

LEG_ROWS="$OUTPUT_DIR/.leg-rows.tsv"
: >"$LEG_ROWS"
index=0
for leg in A1 B1 B2 A2; do
  if (( index > 0 )); then
    run_cooldown
  fi
  idle="$(require_clean_host "$leg")"
  case "$leg" in
    A1|A2) run_leg "$leg" baseline "$baseline_binary" "$baseline_source" "$baseline_binary_sha" "$idle" >>"$LEG_ROWS" ;;
    B1|B2) run_leg "$leg" candidate "$candidate_binary" "$candidate_source" "$candidate_binary_sha" "$idle" >>"$LEG_ROWS" ;;
  esac
  [[ ! -s "$NOISE_FILE" ]] || die "$leg observed an unrelated process at or above ${HIGH_CPU_PERCENT}% CPU"
  index=$((index + 1))
done

raw_entries="$OUTPUT_DIR/.raw-entries.ndjson"
: >"$raw_entries"
while IFS=$'\t' read -r leg variant started finished idle raw_sha; do
  host_path="host-$leg-before.tsv"
  jq -cn --arg leg "$leg" --arg variant "$variant" --arg path "raw/$leg.txt" --arg sha "$raw_sha" \
    --arg host_path "$host_path" --arg host_sha "$(sha256 "$OUTPUT_DIR/$host_path")" \
    --argjson started "$started" --argjson finished "$finished" --argjson idle "$idle" '
    {leg:$leg,variant:$variant,path:$path,exit_code:0,sha256:$sha,started_epoch:$started,finished_epoch:$finished,host_idle_before:$idle,
     host_process_snapshot:{path:$host_path,sha256:$host_sha}}
  ' >>"$raw_entries"
done <"$LEG_ROWS"

jq -n \
  --arg manifest_sha "$manifest_sha" --arg baseline_source "$baseline_source" --arg candidate_source "$candidate_source" \
  --arg baseline_binary_sha "$baseline_binary_sha" --arg candidate_binary_sha "$candidate_binary_sha" \
  --arg authority "$AUTHORITY_BENCHMARK" --arg owner "$OWNER_BENCHMARK" \
  --arg os "$(uname -s)" --arg arch "$(uname -m)" \
  --arg noise_sha "$(sha256 "$NOISE_FILE")" \
  --arg harness_bundle_sha "$HARNESS_BUNDLE_SHA" --argjson harness_files "$HARNESS_FILES_JSON" \
  --argjson benchtime "$BENCHTIME_SECONDS" --argjson count "$SAMPLES_PER_LEG" \
  --argjson cooldown "$COOLDOWN_SECONDS" --argjson idle "$MINIMUM_IDLE_PERCENT" --argjson high_cpu "$HIGH_CPU_PERCENT" \
  --slurpfile raw "$raw_entries" '
  {
    schema:"wukongim/local-medium-rc-micro-run/v1",status:"completed",
    build_smoke_manifest_sha256:$manifest_sha,
    baseline_source_sha:$baseline_source,candidate_source_sha:$candidate_source,
    binaries:{baseline_sha256:$baseline_binary_sha,candidate_sha256:$candidate_binary_sha},
    executor_bundle:{sha256:$harness_bundle_sha,files:$harness_files},
    host:{os:$os,arch:$arch},
    protocol:{order:["A1","B1","B2","A2"],benchmarks:[$authority,$owner],benchtime_seconds:$benchtime,samples_per_leg:$count,gomaxprocs:4,cooldown_seconds:$cooldown,minimum_host_idle_percent:$idle,external_high_cpu_percent:$high_cpu},
    raw_outputs:$raw,
    external_high_cpu:{path:"external-high-cpu.tsv",samples:0,sha256:$noise_sha}
  }' >"$OUTPUT_DIR/micro-run-manifest.json"
rm -f "$LEG_ROWS" "$raw_entries"
rm -f "$HARNESS_ENTRIES"
HARNESS_ENTRIES=""

"$EVALUATOR" "$BUILD_DIR" "$OUTPUT_DIR"
