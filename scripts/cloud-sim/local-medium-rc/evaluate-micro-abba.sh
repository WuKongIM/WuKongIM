#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
POLICY="$SCRIPT_DIR/evaluate-micro-abba.jq"
BUILD_MANIFEST_POLICY="$SCRIPT_DIR/validate-build-smoke-manifest.jq"
EXPECTED_SCHEMA="wukongim/local-medium-rc-revision-neutral-build-smoke/v1"
AUTHORITY_BENCHMARK="BenchmarkLocalMediumRCRevisionNeutralAuthorityResolve512x256"
OWNER_BENCHMARK="BenchmarkLocalMediumRCRevisionNeutralOwnerPushAck512x221x55"
EQUIVALENCE_TEST="TestLocalMediumRCWorkloadEquivalence"
BASELINE_LOCK_PATH="scripts/cloud-sim/local-medium-rc/baseline-lock.json"

usage() {
  cat <<'USAGE'
Usage: evaluate-micro-abba.sh BUILD_SMOKE_DIR RUN_DIR

Revalidates the build-smoke manifest, both test binaries, raw A1/B1/B2/A2
outputs, and the timing policy. It writes RUN_DIR/micro-verdict.json and exits
zero only for a revision-neutral micro_pass. It never authorizes cloud use.
USAGE
}

if [[ $# -ne 2 ]]; then
  usage >&2
  exit 2
fi

BUILD_DIR="$1"
RUN_DIR="$2"
MANIFEST="$BUILD_DIR/build-smoke-manifest.json"
RUN_MANIFEST="$RUN_DIR/micro-run-manifest.json"
VERDICT="$RUN_DIR/micro-verdict.json"
EVALUATION_COMPLETE=0
FAILURE_REASON="evaluation_incomplete"
TMP_ROOT=""
EQUIVALENCE_TMP=""
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

write_validation_failure() {
  local reason="$1"
  command -v jq >/dev/null 2>&1 || return 1
  [[ -d "$RUN_DIR" && ! -L "$RUN_DIR" ]] || return 1
  jq -n --arg reason "$reason" '{
    schema:"wukongim/local-medium-rc-micro-verdict/v1",
    scope:"revision_neutral_micro_only",
    decision:"micro_fail",
    micro_pass:false,
    cloud_authorized:false,
    validation_error:$reason
  }' >"$VERDICT.tmp" 2>/dev/null && mv "$VERDICT.tmp" "$VERDICT"
}

initialize_failure_sentinel() {
  [[ -d "$RUN_DIR" && ! -L "$RUN_DIR" ]] || return 1
  printf '%s\n' '{"schema":"wukongim/local-medium-rc-micro-verdict/v1","scope":"revision_neutral_micro_only","decision":"micro_fail","micro_pass":false,"cloud_authorized":false,"validation_error":"evaluation_started"}' >"$VERDICT"
}

record_unexpected_error() {
  local rc="$?" line="${BASH_LINENO[0]:-unknown}"
  FAILURE_REASON="unexpected evaluator error at line $line (exit $rc)"
  return "$rc"
}

finish_evaluation() {
  local rc="$?"
  trap - ERR EXIT
  if (( EVALUATION_COMPLETE == 0 )); then
    write_validation_failure "$FAILURE_REASON" || rm -f -- "$VERDICT" "$VERDICT.tmp"
  fi
  rm -rf -- "${TMP_ROOT:-}" "${EQUIVALENCE_TMP:-}"
  return "$rc"
}

die() {
  FAILURE_REASON="$*"
  write_validation_failure "$FAILURE_REASON" || rm -f -- "$VERDICT" "$VERDICT.tmp"
  printf 'local-medium-rc-micro-evaluate: %s\n' "$*" >&2
  exit 1
}

if [[ -d "$RUN_DIR" && ! -L "$RUN_DIR" ]]; then
  rm -f -- "$VERDICT" "$VERDICT.tmp"
fi
trap record_unexpected_error ERR
trap finish_evaluation EXIT

[[ -d "$RUN_DIR" && ! -L "$RUN_DIR" ]] || die "run directory is missing or symlinked"
initialize_failure_sentinel || die "could not initialize fail-closed verdict sentinel"

for command_name in awk cmp grep jq mv ps rm sed sort uname; do
  command -v "$command_name" >/dev/null 2>&1 || die "required parser tool is missing: $command_name"
done
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

resolve_regular_artifact() {
  local base="$1" relative="$2" pattern="$3"
  local base_real path parent_real
  [[ "$relative" != /* && "$relative" != *".."* && "$relative" =~ $pattern ]] ||
    die "unsafe artifact path: $relative"
  base_real="$(cd "$base" && pwd -P)"
  path="$base/$relative"
  [[ -f "$path" && ! -L "$path" ]] || die "missing or symlinked artifact: $relative"
  parent_real="$(cd "$(dirname "$path")" && pwd -P)"
  case "$parent_real/$(basename "$path")" in
    "$base_real"/*) printf '%s/%s\n' "$parent_real" "$(basename "$path")" ;;
    *) die "artifact escapes its root: $relative" ;;
  esac
}

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

[[ -d "$BUILD_DIR" && ! -L "$BUILD_DIR" ]] || die "build-smoke directory is missing or symlinked"
[[ -f "$MANIFEST" && ! -L "$MANIFEST" ]] || die "build-smoke manifest is missing or symlinked"
[[ -f "$RUN_MANIFEST" && ! -L "$RUN_MANIFEST" ]] || die "micro run manifest is missing or symlinked"
[[ -f "$POLICY" && ! -L "$POLICY" ]] || die "micro policy is missing or symlinked"
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

go_tool="$(jq -er '.toolchain.path' "$MANIFEST")"
[[ -x "$go_tool" && ! -d "$go_tool" ]] || die "recorded Go tool is unavailable"
[[ "$(sha256 "$go_tool")" == "$(jq -er '.toolchain.sha256' "$MANIFEST")" ]] || die "Go tool SHA-256 mismatch"
[[ "$(env GOTOOLCHAIN=local "$go_tool" version)" == "$(jq -er '.toolchain.version' "$MANIFEST")" ]] || die "Go tool version mismatch"
verify_recorded_toolchain() {
  local actual_root host_os host_arch name expected_path actual_path expected_sha
  actual_root="$(env GOTOOLCHAIN=local "$go_tool" env GOROOT)" || die "could not read recorded toolchain GOROOT"
  [[ -d "$actual_root" ]] || die "recorded toolchain GOROOT is unavailable"
  actual_root="$(cd "$actual_root" && pwd -P)"
  host_os="$(env GOTOOLCHAIN=local "$go_tool" env GOHOSTOS)" || die "could not read recorded toolchain GOHOSTOS"
  host_arch="$(env GOTOOLCHAIN=local "$go_tool" env GOHOSTARCH)" || die "could not read recorded toolchain GOHOSTARCH"
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

verify_binary() {
  local variant="$1" source relative expected_sha binary metadata listed embedded_go expected_go
  source="$(jq -er --arg variant "$variant" '.[$variant + "_source_sha"]' "$MANIFEST")"
  relative="$(jq -er --arg variant "$variant" '.binaries[$variant].path' "$MANIFEST")"
  expected_sha="$(jq -er --arg variant "$variant" '.binaries[$variant].sha256' "$MANIFEST")"
  binary="$(resolve_regular_artifact "$BUILD_DIR" "$relative" '^bin/[A-Za-z0-9._-]+$')"
  [[ "$(sha256 "$binary")" == "$expected_sha" ]] || die "$variant binary SHA-256 mismatch"
  metadata="$(env GOTOOLCHAIN=local "$go_tool" version -m "$binary")" || die "could not read $variant binary metadata"
  embedded_go="$(awk 'NR == 1 {print $NF}' <<<"$metadata")"
  expected_go="$(awk '{print $3}' <<<"$(jq -er '.toolchain.version' "$MANIFEST")")"
  [[ "$embedded_go" == "$expected_go" ]] || die "$variant binary embedded Go version differs from build-smoke toolchain"
  [[ "$(metadata_value "$metadata" vcs.revision)" == "$source" ]] || die "$variant binary revision mismatch"
  [[ "$(metadata_value "$metadata" vcs.modified)" == "false" ]] || die "$variant binary is not clean"
  [[ "$(metadata_value "$metadata" -trimpath)" == "true" ]] || die "$variant binary is not trimpath"
  listed="$(env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin HOME="${HOME:-/tmp}" LANG=C LC_ALL=C TZ=UTC \
    "$binary" -test.list "$BENCHMARK_REGEX" | LC_ALL=C sort)" || die "could not list $variant benchmarks"
  [[ "$listed" == "$EXPECTED_BENCHMARKS" ]] || die "$variant benchmark names differ from the frozen contract"
  printf '%s\n' "$binary"
}

baseline_binary="$(verify_binary baseline)"
candidate_binary="$(verify_binary candidate)"

manifest_sha="$(sha256 "$MANIFEST")"
baseline_binary_sha="$(sha256 "$baseline_binary")"
candidate_binary_sha="$(sha256 "$candidate_binary")"

jq -e \
  --arg manifest_sha "$manifest_sha" --arg baseline_source "$baseline_source" --arg candidate_source "$candidate_source" \
  --arg baseline_binary_sha "$baseline_binary_sha" --arg candidate_binary_sha "$candidate_binary_sha" \
  --arg authority "$AUTHORITY_BENCHMARK" --arg owner "$OWNER_BENCHMARK" \
  --arg os "$(uname -s)" --arg arch "$(uname -m)" '
    .schema == "wukongim/local-medium-rc-micro-run/v1" and .status == "completed" and
    .build_smoke_manifest_sha256 == $manifest_sha and
    .baseline_source_sha == $baseline_source and .candidate_source_sha == $candidate_source and
    .binaries == {baseline_sha256:$baseline_binary_sha,candidate_sha256:$candidate_binary_sha} and
    (.executor_bundle.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    [.executor_bundle.files[].path] == [
      "scripts/cloud-sim/local-medium-rc/build-smoke.sh",
      "scripts/cloud-sim/local-medium-rc/baseline-lock.json",
      "scripts/cloud-sim/local-medium-rc/workload_test.go.overlay",
      "scripts/cloud-sim/local-medium-rc/adapter_baseline_test.go.overlay",
      "scripts/cloud-sim/local-medium-rc/adapter_candidate_test.go.overlay",
      "scripts/cloud-sim/local-medium-rc/run-micro-abba.sh",
      "scripts/cloud-sim/local-medium-rc/evaluate-micro-abba.sh",
      "scripts/cloud-sim/local-medium-rc/evaluate-micro-abba.jq",
      "scripts/cloud-sim/local-medium-rc/validate-build-smoke-manifest.jq",
      "scripts/cloud-sim/local-medium-rc/test-micro-abba-static.sh"
    ] and
    all(.executor_bundle.files[]; (.sha256 | type == "string" and test("^[0-9a-f]{64}$"))) and
    .host.os == $os and .host.arch == $arch and
    .protocol.order == ["A1","B1","B2","A2"] and
    .protocol.benchmarks == [$authority,$owner] and
    (.protocol.benchtime_seconds | type == "number" and floor == . and . >= 5) and
    (.protocol.samples_per_leg | type == "number" and floor == . and . >= 6) and
    .protocol.gomaxprocs == 4 and
    (.protocol.cooldown_seconds | type == "number" and floor == . and . >= 120) and
    (.protocol.minimum_host_idle_percent | type == "number" and . >= 85 and . <= 100) and
    (.protocol.external_high_cpu_percent | type == "number" and . > 0 and . <= 20) and
    (.raw_outputs | type == "array" and length == 4) and
    [.raw_outputs[].leg] == ["A1","B1","B2","A2"] and
    [.raw_outputs[].variant] == ["baseline","candidate","candidate","baseline"] and
    all(.raw_outputs[];
      .host_process_snapshot.path == ("host-" + .leg + "-before.tsv") and
      (.host_process_snapshot.sha256 | type == "string" and test("^[0-9a-f]{64}$"))) and
    .external_high_cpu.path == "external-high-cpu.tsv" and .external_high_cpu.samples == 0 and
    (.external_high_cpu.sha256 | type == "string" and test("^[0-9a-f]{64}$"))
  ' "$RUN_MANIFEST" >/dev/null || die "micro run manifest contract is invalid"

harness_entries="$(mktemp "${TMPDIR:-/tmp}/wk-local-medium-rc-harness-evaluate.XXXXXX")"
: >"$harness_entries"
for harness_path in "${HARNESS_PATHS[@]}"; do
  [[ -f "$ROOT_DIR/$harness_path" && ! -L "$ROOT_DIR/$harness_path" ]] || die "missing or symlinked harness file: $harness_path"
  git -C "$ROOT_DIR" show "$candidate_source:$harness_path" | cmp -s - "$ROOT_DIR/$harness_path" ||
    die "working harness does not match candidate commit: $harness_path"
  actual_sha="$(sha256 "$ROOT_DIR/$harness_path")"
  jq -e --arg path "$harness_path" --arg sha "$actual_sha" \
    '.executor_bundle.files[] | select(.path == $path) | .sha256 == $sha' "$RUN_MANIFEST" >/dev/null ||
    die "run manifest harness SHA-256 mismatch: $harness_path"
  jq -cn --arg path "$harness_path" --arg sha "$actual_sha" '{path:$path,sha256:$sha}' >>"$harness_entries"
done
actual_harness_bundle_sha="$(jq -sr '.[] | [.path,.sha256] | @tsv' "$harness_entries" | sha256 /dev/stdin)"
rm -f "$harness_entries"
[[ "$actual_harness_bundle_sha" == "$(jq -er '.executor_bundle.sha256' "$RUN_MANIFEST")" ]] ||
  die "executor bundle SHA-256 mismatch"

[[ "$(sha256 "$ROOT_DIR/scripts/cloud-sim/local-medium-rc/workload_test.go.overlay")" == "$(jq -er '.common_workload.sha256' "$MANIFEST")" ]] ||
  die "common workload SHA-256 differs from candidate-bound source"
[[ "$(sha256 "$ROOT_DIR/scripts/cloud-sim/local-medium-rc/adapter_baseline_test.go.overlay")" == "$(jq -er '.adapters.baseline.sha256' "$MANIFEST")" ]] ||
  die "baseline adapter SHA-256 differs from candidate-bound source"
[[ "$(sha256 "$ROOT_DIR/scripts/cloud-sim/local-medium-rc/adapter_candidate_test.go.overlay")" == "$(jq -er '.adapters.candidate.sha256' "$MANIFEST")" ]] ||
  die "candidate adapter SHA-256 differs from candidate-bound source"

EQUIVALENCE_TMP="$(mktemp -d "${TMPDIR:-/tmp}/wk-local-medium-rc-equivalence-evaluate.XXXXXX")"
verify_equivalence() {
  local variant="$1" binary="$2" relative expected_path expected_sha recorded rerun recorded_marker rerun_marker
  relative="$(jq -er --arg variant "$variant" '.equivalence[$variant].path' "$MANIFEST")"
  expected_path="equivalence/$variant.txt"
  [[ "$relative" == "$expected_path" ]] || die "$variant equivalence path is not canonical"
  recorded="$(resolve_regular_artifact "$BUILD_DIR" "$relative" '^equivalence/(baseline|candidate)[.]txt$')"
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

samples_per_leg="$(jq -er '.protocol.samples_per_leg' "$RUN_MANIFEST")"
benchtime_seconds="$(jq -er '.protocol.benchtime_seconds' "$RUN_MANIFEST")"
cooldown_seconds="$(jq -er '.protocol.cooldown_seconds' "$RUN_MANIFEST")"
minimum_idle="$(jq -er '.protocol.minimum_host_idle_percent' "$RUN_MANIFEST")"
noise_relative="$(jq -er '.external_high_cpu.path' "$RUN_MANIFEST")"
noise_file="$(resolve_regular_artifact "$RUN_DIR" "$noise_relative" '^external-high-cpu[.]tsv$')"
[[ ! -s "$noise_file" ]] || die "external high-CPU samples were recorded"
[[ "$(sha256 "$noise_file")" == "$(jq -er '.external_high_cpu.sha256' "$RUN_MANIFEST")" ]] ||
  die "external high-CPU evidence SHA-256 mismatch"

parse_samples() {
  local path="$1" benchmark="$2" unit="$3"
  awk -v benchmark="$benchmark" -v unit="$unit" '
    $1 ~ ("^" benchmark "(-[0-9]+)?$") {
      for (index = 2; index <= NF; index++) {
        if ($index == unit && index > 2) print $(index - 1)
      }
    }
  ' "$path" | jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)'
}

require_exact_line() {
  local path="$1" expected="$2"
  [[ "$(awk -v expected="$expected" '$0 == expected {count++} END {print count + 0}' "$path")" == "1" ]] ||
    die "raw output lacks one exact marker: $expected"
}

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/wk-local-medium-rc-evaluate.XXXXXX")"
for leg in A1 B1 B2 A2; do
  case "$leg" in
    A1|A2) expected_variant=baseline; expected_source="$baseline_source"; expected_binary_sha="$baseline_binary_sha" ;;
    B1|B2) expected_variant=candidate; expected_source="$candidate_source"; expected_binary_sha="$candidate_binary_sha" ;;
  esac
  relative="$(jq -er --arg leg "$leg" '.raw_outputs[] | select(.leg == $leg) | .path' "$RUN_MANIFEST")"
  [[ "$relative" == "raw/$leg.txt" ]] || die "$leg raw path is not canonical"
  raw="$(resolve_regular_artifact "$RUN_DIR" "$relative" '^raw/(A1|B1|B2|A2)[.]txt$')"
  [[ "$(sha256 "$raw")" == "$(jq -er --arg leg "$leg" '.raw_outputs[] | select(.leg == $leg) | .sha256' "$RUN_MANIFEST")" ]] ||
    die "$leg raw SHA-256 mismatch"
  host_relative="$(jq -er --arg leg "$leg" '.raw_outputs[] | select(.leg == $leg) | .host_process_snapshot.path' "$RUN_MANIFEST")"
  [[ "$host_relative" == "host-$leg-before.tsv" ]] || die "$leg host snapshot path is not canonical"
  host_snapshot="$(resolve_regular_artifact "$RUN_DIR" "$host_relative" '^host-(A1|B1|B2|A2)-before[.]tsv$')"
  [[ "$(sha256 "$host_snapshot")" == "$(jq -er --arg leg "$leg" '.raw_outputs[] | select(.leg == $leg) | .host_process_snapshot.sha256' "$RUN_MANIFEST")" ]] ||
    die "$leg host snapshot SHA-256 mismatch"
  require_exact_line "$raw" "# wkrc-micro-leg $leg variant=$expected_variant"
  require_exact_line "$raw" "# wkrc-source-sha $expected_source"
  require_exact_line "$raw" "# wkrc-test-binary-sha256 $expected_binary_sha"
  require_exact_line "$raw" "# wkrc-benchtime ${benchtime_seconds}s"
  require_exact_line "$raw" "# wkrc-count $samples_per_leg"
  require_exact_line "$raw" "# wkrc-gomaxprocs 4"
  require_exact_line "$raw" "# wkrc-benchmark $AUTHORITY_BENCHMARK"
  require_exact_line "$raw" "# wkrc-benchmark $OWNER_BENCHMARK"
  require_exact_line "$raw" "PASS"
  [[ "$(grep -Ec '^FAIL([[:space:]]|$)' "$raw" || true)" == "0" ]] || die "$leg raw output contains FAIL"

  unexpected_benchmarks="$(awk -v authority="$AUTHORITY_BENCHMARK" -v owner="$OWNER_BENCHMARK" '
    $1 ~ /^Benchmark/ {
      name = $1
      sub(/-[0-9]+$/, "", name)
      if (name != authority && name != owner) print name
    }
  ' "$raw")"
  [[ -z "$unexpected_benchmarks" ]] || die "$leg contains an unexpected benchmark: $unexpected_benchmarks"

  for benchmark in "$AUTHORITY_BENCHMARK" "$OWNER_BENCHMARK"; do
    for unit in ns/op B/op allocs/op; do
      if ! parsed="$(parse_samples "$raw" "$benchmark" "$unit")"; then
        die "$leg $benchmark $unit could not be parsed as numeric benchmark evidence"
      fi
      [[ "$(jq 'length' <<<"$parsed")" == "$samples_per_leg" ]] ||
        die "$leg $benchmark $unit sample count differs from $samples_per_leg"
      if [[ "$unit" == "ns/op" ]]; then
        jq -e 'all(.[]; type == "number" and . > 0)' <<<"$parsed" >/dev/null ||
          die "$leg $benchmark has non-positive ns/op"
      else
        jq -e 'all(.[]; type == "number" and . >= 0)' <<<"$parsed" >/dev/null ||
          die "$leg $benchmark has negative $unit"
      fi
      benchmark_id=authority
      [[ "$benchmark" == "$OWNER_BENCHMARK" ]] && benchmark_id=owner
      unit_id="${unit//\//_}"
      printf '%s\n' "$parsed" >"$TMP_ROOT/$leg-$benchmark_id-$unit_id.json"
    done
  done

  started="$(awk '$1 == "#" && $2 == "wkrc-started-epoch" {print $3}' "$raw")"
  finished="$(awk '$1 == "#" && $2 == "wkrc-finished-epoch" {print $3}' "$raw")"
  idle="$(awk '$1 == "#" && $2 == "wkrc-host-idle-before" {print $3}' "$raw")"
  [[ "$started" =~ ^[0-9]+$ && "$finished" =~ ^[0-9]+$ && "$finished" -ge "$started" ]] ||
    die "$leg has invalid start/finish timestamps"
  minimum_duration=$((2 * samples_per_leg * benchtime_seconds - 2))
  (( finished - started >= minimum_duration )) ||
    die "$leg duration is shorter than two benchmarks x count x benchtime minus two seconds"
  [[ "$idle" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "$leg has invalid host idle evidence"
  awk -v idle="$idle" -v minimum="$minimum_idle" 'BEGIN {exit !(idle >= minimum && idle <= 100)}' ||
    die "$leg host idle $idle is below $minimum_idle"
  jq -e --arg leg "$leg" --argjson started "$started" --argjson finished "$finished" --argjson idle "$idle" '
    .raw_outputs[] | select(.leg == $leg) |
    .started_epoch == $started and .finished_epoch == $finished and .host_idle_before == $idle and .exit_code == 0
  ' "$RUN_MANIFEST" >/dev/null || die "$leg raw timing evidence differs from the run manifest"
done

for transition in 'A1 B1' 'B1 B2' 'B2 A2'; do
  read -r previous next <<<"$transition"
  previous_finished="$(jq -er --arg leg "$previous" '.raw_outputs[] | select(.leg == $leg) | .finished_epoch' "$RUN_MANIFEST")"
  next_started="$(jq -er --arg leg "$next" '.raw_outputs[] | select(.leg == $leg) | .started_epoch' "$RUN_MANIFEST")"
  (( next_started - previous_finished >= cooldown_seconds )) ||
    die "$previous to $next cooldown is shorter than $cooldown_seconds seconds"
done

metric_json() {
  local benchmark_id="$1" metric_id="$2" unit="$3" file_unit="$4"
  jq -n --arg unit "$unit" \
    --slurpfile A1 "$TMP_ROOT/A1-$benchmark_id-$file_unit.json" \
    --slurpfile B1 "$TMP_ROOT/B1-$benchmark_id-$file_unit.json" \
    --slurpfile B2 "$TMP_ROOT/B2-$benchmark_id-$file_unit.json" \
    --slurpfile A2 "$TMP_ROOT/A2-$benchmark_id-$file_unit.json" \
    '{unit:$unit,samples_by_leg:{A1:$A1[0],B1:$B1[0],B2:$B2[0],A2:$A2[0]}}'
}

case_json() {
  local id="$1" benchmark_id="$2"
  jq -n --arg id "$id" \
    --argjson ns "$(metric_json "$benchmark_id" ns_per_op ns/op ns_op)" \
    --argjson bytes "$(metric_json "$benchmark_id" bytes_per_op B/op B_op)" \
    --argjson allocs "$(metric_json "$benchmark_id" allocs_per_op allocs/op allocs_op)" \
    '{id:$id,metrics:{ns_per_op:$ns,bytes_per_op:$bytes,allocs_per_op:$allocs}}'
}

jq -n \
  --arg manifest_sha "$manifest_sha" --arg baseline_source "$baseline_source" --arg candidate_source "$candidate_source" \
  --arg run_manifest_sha "$(sha256 "$RUN_MANIFEST")" \
  --argjson samples_per_leg "$samples_per_leg" --argjson benchtime_seconds "$benchtime_seconds" \
  --argjson raw_outputs "$(jq '.raw_outputs' "$RUN_MANIFEST")" \
  --argjson external_high_cpu "$(jq '.external_high_cpu' "$RUN_MANIFEST")" \
  --argjson authority "$(case_json authority_resolve authority)" \
  --argjson owner "$(case_json owner_push_ack owner)" '
  {
    schema:"wukongim/local-medium-rc-micro-samples/v1",
    build_smoke:{manifest_sha256:$manifest_sha,baseline_source_sha:$baseline_source,candidate_source_sha:$candidate_source},
    run:{manifest_sha256:$run_manifest_sha,raw_outputs:$raw_outputs,external_high_cpu:$external_high_cpu},
    protocol:{order:["A1","B1","B2","A2"],samples_per_leg:$samples_per_leg,benchtime_seconds:$benchtime_seconds,gomaxprocs:4},
    thresholds:{maximum_cv:0.05,maximum_pair_drift_ratio:0.05,maximum_authority_ns_ratio:0.5574,maximum_non_regression_ratio:1.05},
    cases:[$authority,$owner]
  }' >"$TMP_ROOT/samples.json"

jq -f "$POLICY" "$TMP_ROOT/samples.json" >"$VERDICT.tmp" || die "micro policy evaluation failed"
mv "$VERDICT.tmp" "$VERDICT"
jq -e '
  .schema == "wukongim/local-medium-rc-micro-verdict/v1" and
  .scope == "revision_neutral_micro_only" and
  .cloud_authorized == false and
  (.decision == "micro_pass" or .decision == "micro_fail") and
  (.micro_pass | type == "boolean")
' "$VERDICT" >/dev/null || die "micro policy produced an invalid verdict contract"
EVALUATION_COMPLETE=1
if jq -e '.micro_pass == true and .decision == "micro_pass" and .cloud_authorized == false' "$VERDICT" >/dev/null; then
  printf 'local-medium-rc-micro-evaluate: micro_pass verdict=%s\n' "$VERDICT"
  exit 0
fi
printf 'local-medium-rc-micro-evaluate: micro_fail verdict=%s\n' "$VERDICT" >&2
exit 1
