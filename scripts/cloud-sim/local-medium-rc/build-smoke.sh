#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
BUILD_SCRIPT="scripts/cloud-sim/local-medium-rc/build-smoke.sh"
COMMON_SOURCE="scripts/cloud-sim/local-medium-rc/workload_test.go.overlay"
BASELINE_ADAPTER="scripts/cloud-sim/local-medium-rc/adapter_baseline_test.go.overlay"
CANDIDATE_ADAPTER="scripts/cloud-sim/local-medium-rc/adapter_candidate_test.go.overlay"
BASELINE_LOCK="scripts/cloud-sim/local-medium-rc/baseline-lock.json"
BUILD_MANIFEST_POLICY="scripts/cloud-sim/local-medium-rc/validate-build-smoke-manifest.jq"
OVERLAY_COMMON_TARGET="internal/app/zz_local_medium_rc_workload_test.go"
OVERLAY_ADAPTER_TARGET="internal/app/zz_local_medium_rc_adapter_test.go"
EQUIVALENCE_TEST='^TestLocalMediumRCWorkloadEquivalence$'
ACTIVE_CHILD_PID=""
ACTIVE_CHILD_PGID=""
CHILD_LAUNCHING=0
PENDING_INTERRUPT=""
CHILD_TERM_GRACE_SECONDS=10

die() {
  printf 'local-medium-rc-build-smoke: %s\n' "$*" >&2
  exit 1
}

sha256() {
  LC_ALL=C LANG=C shasum -a 256 "$1" | awk '{print $1}'
}

clear_active_child() {
  ACTIVE_CHILD_PID=""
  ACTIVE_CHILD_PGID=""
}

process_group_exists() {
  local pgid="$1"
  ps -Ao pgid=,stat= | awk -v pgid="$pgid" '$1 == pgid && $2 !~ /^Z/ {found = 1} END {exit !found}'
}

clone_source() {
  local destination="$1"
  exec git clone --quiet --shared --no-checkout "$ROOT_DIR" "$destination"
}

checkout_source() {
  local source_tree="$1" source_sha="$2"
  exec git -C "$source_tree" checkout --quiet --detach "$source_sha"
}

start_managed_child() {
  local kind="$1" attempt pgid pending_interrupt
  shift
  set -m
  CHILD_LAUNCHING=1
  "$@" &
  ACTIVE_CHILD_PID="$!" ACTIVE_CHILD_PGID="$!"
  CHILD_LAUNCHING=0
  set +m
  if [[ -n "$PENDING_INTERRUPT" ]]; then
    pending_interrupt="$PENDING_INTERRUPT"
    PENDING_INTERRUPT=""
    interrupt_build "$pending_interrupt"
  fi
  for ((attempt = 0; attempt < 20; attempt++)); do
    pgid="$(ps -p "$ACTIVE_CHILD_PID" -o pgid= 2>/dev/null | awk '{print $1}')"
    if [[ "$pgid" == "$ACTIVE_CHILD_PID" ]]; then
      ACTIVE_CHILD_PGID="$pgid"
      return 0
    fi
    kill -0 "$ACTIVE_CHILD_PID" 2>/dev/null || break
    sleep 0.05
  done
  kill -TERM "$ACTIVE_CHILD_PID" 2>/dev/null || true
  wait "$ACTIVE_CHILD_PID" 2>/dev/null || true
  clear_active_child
  die "could not establish an isolated $kind process group"
}

wait_managed_child() {
  local rc=0
  wait "$ACTIVE_CHILD_PID" || rc=$?
  clear_active_child
  return "$rc"
}

cleanup_active_child() {
  local attempt pgid
  [[ -n "$ACTIVE_CHILD_PGID" ]] || return 0
  pgid="$ACTIVE_CHILD_PGID"
  if process_group_exists "$pgid"; then
    kill -TERM -- "-$pgid" 2>/dev/null || true
  fi
  for ((attempt = 0; attempt < CHILD_TERM_GRACE_SECONDS; attempt++)); do
    if ! process_group_exists "$pgid"; then
      wait "$ACTIVE_CHILD_PID" 2>/dev/null || true
      clear_active_child
      return 0
    fi
    sleep 1
  done
  if process_group_exists "$pgid"; then
    kill -KILL -- "-$pgid" 2>/dev/null || true
  fi
  for ((attempt = 0; attempt < 2; attempt++)); do
    process_group_exists "$pgid" || break
    sleep 1
  done
  wait "$ACTIVE_CHILD_PID" 2>/dev/null || true
  clear_active_child
}

cleanup() {
  cleanup_active_child
  [[ -z "${WORK_ROOT:-}" || ! -d "$WORK_ROOT" ]] || rm -rf "$WORK_ROOT"
  if [[ -n "${STAGE_ROOT:-}" && -d "$STAGE_ROOT" ]]; then
    if [[ -n "${failed_output_dir:-}" && ! -e "$failed_output_dir" && ! -L "$failed_output_dir" ]]; then
      if mv "$STAGE_ROOT" "$failed_output_dir"; then
        printf 'local-medium-rc-build-smoke: retained failed evidence at %s\n' "$failed_output_dir" >&2
        STAGE_ROOT=""
      else
        printf 'local-medium-rc-build-smoke: could not move failed evidence; retained stage at %s\n' "$STAGE_ROOT" >&2
      fi
    else
      printf 'local-medium-rc-build-smoke: failed evidence destination is unavailable; retained stage at %s\n' "$STAGE_ROOT" >&2
    fi
  fi
}

interrupt_build() {
  local exit_code="$1"
  if [[ "$CHILD_LAUNCHING" == "1" ]]; then
    [[ -n "$PENDING_INTERRUPT" ]] || PENDING_INTERRUPT="$exit_code"
    return 0
  fi
  trap - INT TERM HUP
  cleanup_active_child
  exit "$exit_code"
}

trap cleanup EXIT
trap 'interrupt_build 130' INT
trap 'interrupt_build 143' TERM
trap 'interrupt_build 129' HUP

if [[ $# -ne 3 ]]; then
  die "usage: build-smoke.sh BASELINE_COMMIT CANDIDATE_COMMIT OUTPUT_DIR"
fi

baseline_source="$(git -C "$ROOT_DIR" rev-parse --verify "$1^{commit}")" || die "baseline commit not found"
candidate_source="$(git -C "$ROOT_DIR" rev-parse --verify "$2^{commit}")" || die "candidate commit not found"
requested_output_dir="$3"
[[ -n "$requested_output_dir" ]] || die "output path is empty"
output_parent="$(dirname "$requested_output_dir")"
mkdir -p "$output_parent" || die "could not create output parent"
output_parent="$(cd "$output_parent" && pwd -P)" || die "could not resolve output parent"
output_name="$(basename "$requested_output_dir")"
[[ -n "$output_name" && "$output_name" != "." && "$output_name" != ".." ]] || die "output name is unsafe"
output_dir="$output_parent/$output_name"
failed_output_dir="${output_dir}.failed"
[[ -f "$ROOT_DIR/$BASELINE_LOCK" && ! -L "$ROOT_DIR/$BASELINE_LOCK" ]] || die "baseline lock is missing or symlinked"
baseline_lock_schema="$(jq -er '.schema' "$ROOT_DIR/$BASELINE_LOCK")" || die "baseline lock schema is missing"
baseline_lock_source="$(jq -er '.baseline_source_sha' "$ROOT_DIR/$BASELINE_LOCK")" || die "baseline lock source is missing"
baseline_lock_run="$(jq -er '.source_run_identity' "$ROOT_DIR/$BASELINE_LOCK")" || die "baseline lock run identity is missing"
jq -e '
  (keys | sort) == ["baseline_source_sha", "schema", "source_run_identity"] and
  .schema == "wukongim/local-medium-rc-baseline-lock/v1" and
  (.baseline_source_sha | type == "string" and test("^[0-9a-f]{40}$")) and
  (.source_run_identity | type == "string" and test("^gh-[0-9]+-[0-9]+$"))
' "$ROOT_DIR/$BASELINE_LOCK" >/dev/null || die "baseline lock contract is invalid"
[[ "$baseline_source" == "$baseline_lock_source" ]] || die "baseline commit differs from candidate-bound baseline lock"
[[ "$baseline_source" != "$candidate_source" ]] || die "baseline and candidate commits are identical"
git -C "$ROOT_DIR" merge-base --is-ancestor "$baseline_source" "$candidate_source" ||
  die "candidate is not a descendant of baseline"
[[ ! -e "$output_dir" && ! -L "$output_dir" ]] || die "output already exists: $output_dir"
[[ ! -e "$failed_output_dir" && ! -L "$failed_output_dir" ]] || die "failed evidence output already exists: $failed_output_dir"

for source in "$BUILD_SCRIPT" "$COMMON_SOURCE" "$BASELINE_ADAPTER" "$CANDIDATE_ADAPTER" "$BASELINE_LOCK" "$BUILD_MANIFEST_POLICY"; do
  [[ -f "$ROOT_DIR/$source" && ! -L "$ROOT_DIR/$source" ]] || die "missing or symlinked harness source: $source"
  git -C "$ROOT_DIR" ls-files --error-unmatch "$source" >/dev/null 2>&1 || die "harness source is not tracked: $source"
  git -C "$ROOT_DIR" show "$candidate_source:$source" | cmp -s - "$ROOT_DIR/$source" ||
    die "working harness source does not match candidate commit: $source"
done

mkdir "$output_dir" || die "could not reserve output directory"
STAGE_ROOT="$output_dir"
mkdir -p "$STAGE_ROOT/bin" "$STAGE_ROOT/equivalence"
WORK_ROOT="$(mktemp -d "${TMPDIR:-/private/tmp}/wk-local-medium-rc-smoke.XXXXXX")"
WORK_ROOT="$(cd "$WORK_ROOT" && pwd -P)" || die "could not canonicalize work root"
BASE_WORKTREE="$WORK_ROOT/baseline"
CANDIDATE_WORKTREE="$WORK_ROOT/candidate"
start_managed_child "baseline clone" clone_source "$BASE_WORKTREE"
wait_managed_child || die "could not clone baseline source"
start_managed_child "candidate clone" clone_source "$CANDIDATE_WORKTREE"
wait_managed_child || die "could not clone candidate source"
start_managed_child "baseline checkout" checkout_source "$BASE_WORKTREE" "$baseline_source"
wait_managed_child || die "could not check out baseline source"
start_managed_child "candidate checkout" checkout_source "$CANDIDATE_WORKTREE" "$candidate_source"
wait_managed_child || die "could not check out candidate source"

go_launcher="${WK_LOCAL_RC_GO:-go}"
go_launcher="$(command -v "$go_launcher")" || die "Go launcher not found"
selected_go_root="$(cd "$CANDIDATE_WORKTREE" && env GOTOOLCHAIN=auto "$go_launcher" env GOROOT)" ||
  die "could not select the candidate repository Go toolchain"
[[ -d "$selected_go_root" ]] || die "selected Go toolchain root is unavailable"
selected_go_root="$(cd "$selected_go_root" && pwd -P)"
go_tool="$selected_go_root/bin/go"
[[ -f "$go_tool" && ! -L "$go_tool" && -x "$go_tool" ]] || die "selected Go tool is missing, symlinked, or not executable"
go_version="$(env GOTOOLCHAIN=local "$go_tool" version)" || die "could not read local Go version"
go_sha="$(sha256 "$go_tool")"
go_root="$(env GOTOOLCHAIN=local "$go_tool" env GOROOT)" || die "could not read local GOROOT"
[[ -d "$go_root" ]] || die "GOROOT is unavailable"
go_root="$(cd "$go_root" && pwd -P)"
go_host_os="$(env GOTOOLCHAIN=local "$go_tool" env GOHOSTOS)" || die "could not read local GOHOSTOS"
go_host_arch="$(env GOTOOLCHAIN=local "$go_tool" env GOHOSTARCH)" || die "could not read local GOHOSTARCH"
go_tool_dir="$go_root/pkg/tool/${go_host_os}_${go_host_arch}"
compile_tool="$go_tool_dir/compile"
link_tool="$go_tool_dir/link"
asm_tool="$go_tool_dir/asm"
for tool in "$compile_tool" "$link_tool" "$asm_tool"; do
  [[ -f "$tool" && ! -L "$tool" && -x "$tool" ]] || die "Go build tool is missing, symlinked, or not executable: $tool"
done
compile_sha="$(sha256 "$compile_tool")"
link_sha="$(sha256 "$link_tool")"
asm_sha="$(sha256 "$asm_tool")"

create_overlay_placeholder() {
  local source="$1" target="$2"
  case "$(uname -s)" in
    Darwin) ln -h "$source" "$target" ;;
    Linux) ln -T "$source" "$target" ;;
    *) die "unsupported host for no-follow overlay placeholder creation" ;;
  esac
}

compile_test_binary() {
  local worktree="$1" overlay="$2" binary="$3"
  cd "$worktree"
  exec env -i PATH="$(dirname "$go_tool"):/usr/bin:/bin:/usr/sbin:/sbin" HOME="$HOME" LANG=C LC_ALL=C TZ=UTC \
    GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=status.showUntrackedFiles GIT_CONFIG_VALUE_0=no \
    GOWORK=off GOTOOLCHAIN=local "$go_tool" test -c -buildvcs=true -trimpath -overlay "$overlay" -o "$binary" ./internal/app
}

run_equivalence_binary() {
  local binary="$1" output="$2"
  exec env -i PATH="/usr/bin:/bin:/usr/sbin:/sbin" HOME="$HOME" LANG=C LC_ALL=C TZ=UTC \
    "$binary" -test.run "$EQUIVALENCE_TEST" -test.count 1 -test.v >"$output" 2>&1
}

build_variant() {
  local variant="$1" worktree="$2" adapter_source="$3" source_sha="$4"
  local overlay="$WORK_ROOT/$variant-overlay.json"
  local binary="$STAGE_ROOT/bin/app-$variant.test"
  local output="$STAGE_ROOT/equivalence/$variant.txt"
  local common_target="$worktree/$OVERLAY_COMMON_TARGET"
  local adapter_target="$worktree/$OVERLAY_ADAPTER_TARGET"
  local placeholder_seed

  # Go overlays replace files discovered by the package loader; they do not
  # introduce a brand-new file into an existing package. Create fixed empty
  # placeholders only in the detached worktree, then remove them immediately
  # after compilation so the source revision remains clean.
  [[ ! -e "$common_target" && ! -L "$common_target" ]] || die "$variant common overlay target already exists"
  [[ ! -e "$adapter_target" && ! -L "$adapter_target" ]] || die "$variant adapter overlay target already exists"
  placeholder_seed="$(mktemp "$worktree/.wkrc-overlay-placeholder.XXXXXX")" ||
    die "$variant could not create overlay placeholder seed"
  if ! create_overlay_placeholder "$placeholder_seed" "$common_target"; then
    rm -f "$placeholder_seed"
    die "$variant could not create common overlay placeholder"
  fi
  if ! create_overlay_placeholder "$placeholder_seed" "$adapter_target"; then
    rm -f "$common_target" "$placeholder_seed"
    die "$variant could not create adapter overlay placeholder"
  fi
  rm -f "$placeholder_seed"

  jq -n \
    --arg common_target "$common_target" \
    --arg common_source "$ROOT_DIR/$COMMON_SOURCE" \
    --arg adapter_target "$adapter_target" \
    --arg adapter_source "$ROOT_DIR/$adapter_source" \
    '{Replace:{($common_target):$common_source,($adapter_target):$adapter_source}}' >"$overlay"

  start_managed_child "$variant build" compile_test_binary "$worktree" "$overlay" "$binary"
  if ! wait_managed_child; then
    rm -f "$common_target" "$adapter_target"
    die "$variant test binary build failed"
  fi
  rm -f "$common_target" "$adapter_target"
  [[ -z "$(git -C "$worktree" status --porcelain=v1 --untracked-files=normal)" ]] ||
    die "$variant worktree changed during build"

  start_managed_child "$variant equivalence" run_equivalence_binary "$binary" "$output"
  wait_managed_child || die "$variant equivalence test failed; see $output"
  grep -F "WKRC-EQUIVALENCE variant=$variant " "$output" >/dev/null ||
    die "$variant equivalence marker missing"

  metadata="$(env GOTOOLCHAIN=local "$go_tool" version -m "$binary")" || die "could not read $variant binary metadata"
  grep -F "vcs.revision=$source_sha" <<<"$metadata" >/dev/null || die "$variant binary revision mismatch"
  grep -F 'vcs.modified=false' <<<"$metadata" >/dev/null || die "$variant binary is marked modified"
  grep -F -- '-trimpath=true' <<<"$metadata" >/dev/null || die "$variant binary is not trimpath"
}

build_variant baseline "$BASE_WORKTREE" "$BASELINE_ADAPTER" "$baseline_source"
build_variant candidate "$CANDIDATE_WORKTREE" "$CANDIDATE_ADAPTER" "$candidate_source"

common_sha="$(sha256 "$ROOT_DIR/$COMMON_SOURCE")"
baseline_adapter_sha="$(sha256 "$ROOT_DIR/$BASELINE_ADAPTER")"
candidate_adapter_sha="$(sha256 "$ROOT_DIR/$CANDIDATE_ADAPTER")"
baseline_binary_sha="$(sha256 "$STAGE_ROOT/bin/app-baseline.test")"
candidate_binary_sha="$(sha256 "$STAGE_ROOT/bin/app-candidate.test")"
baseline_equivalence_sha="$(sha256 "$STAGE_ROOT/equivalence/baseline.txt")"
candidate_equivalence_sha="$(sha256 "$STAGE_ROOT/equivalence/candidate.txt")"
baseline_lock_sha="$(sha256 "$ROOT_DIR/$BASELINE_LOCK")"

jq -n \
  --arg baseline_source "$baseline_source" --arg candidate_source "$candidate_source" \
  --arg go_path "$go_tool" --arg go_version "$go_version" --arg go_sha "$go_sha" \
  --arg go_root "$go_root" --arg go_host_os "$go_host_os" --arg go_host_arch "$go_host_arch" \
  --arg compile_path "$compile_tool" --arg compile_sha "$compile_sha" \
  --arg link_path "$link_tool" --arg link_sha "$link_sha" \
  --arg asm_path "$asm_tool" --arg asm_sha "$asm_sha" \
  --arg baseline_lock_path "$BASELINE_LOCK" --arg baseline_lock_sha "$baseline_lock_sha" \
  --arg baseline_lock_schema "$baseline_lock_schema" --arg baseline_lock_run "$baseline_lock_run" \
  --arg common_path "$COMMON_SOURCE" --arg common_sha "$common_sha" \
  --arg baseline_adapter_path "$BASELINE_ADAPTER" --arg baseline_adapter_sha "$baseline_adapter_sha" \
  --arg candidate_adapter_path "$CANDIDATE_ADAPTER" --arg candidate_adapter_sha "$candidate_adapter_sha" \
  --arg baseline_binary_sha "$baseline_binary_sha" --arg candidate_binary_sha "$candidate_binary_sha" \
  --arg baseline_equivalence_sha "$baseline_equivalence_sha" --arg candidate_equivalence_sha "$candidate_equivalence_sha" '
  {
    schema:"wukongim/local-medium-rc-revision-neutral-build-smoke/v1",
    status:"passed",
    baseline_source_sha:$baseline_source,
    candidate_source_sha:$candidate_source,
    baseline_lock:{path:$baseline_lock_path,sha256:$baseline_lock_sha,schema:$baseline_lock_schema,baseline_source_sha:$baseline_source,source_run_identity:$baseline_lock_run},
    toolchain:{
      path:$go_path,version:$go_version,sha256:$go_sha,gotoolchain:"local",
      goroot:{path:$go_root,gohostos:$go_host_os,gohostarch:$go_host_arch},
      tools:{
        compile:{path:$compile_path,sha256:$compile_sha},
        link:{path:$link_path,sha256:$link_sha},
        asm:{path:$asm_path,sha256:$asm_sha}
      }
    },
    common_workload:{path:$common_path,sha256:$common_sha},
    adapters:{
      baseline:{path:$baseline_adapter_path,sha256:$baseline_adapter_sha},
      candidate:{path:$candidate_adapter_path,sha256:$candidate_adapter_sha}
    },
    shape:{physical_hash_slots:256,logical_slots:10,recipients:512,target_groups:221,online_routes:55,payload_bytes:256},
    benchmarks:["BenchmarkLocalMediumRCRevisionNeutralAuthorityResolve512x256","BenchmarkLocalMediumRCRevisionNeutralOwnerPushAck512x221x55"],
    binaries:{
      baseline:{path:"bin/app-baseline.test",sha256:$baseline_binary_sha},
      candidate:{path:"bin/app-candidate.test",sha256:$candidate_binary_sha}
    },
    equivalence:{
      baseline:{path:"equivalence/baseline.txt",sha256:$baseline_equivalence_sha},
      candidate:{path:"equivalence/candidate.txt",sha256:$candidate_equivalence_sha}
    }
  }' >"$STAGE_ROOT/build-smoke-manifest.json"
jq -e -f "$ROOT_DIR/$BUILD_MANIFEST_POLICY" "$STAGE_ROOT/build-smoke-manifest.json" >/dev/null ||
  die "generated build-smoke manifest contract is invalid"

STAGE_ROOT=""
printf 'local-medium-rc-build-smoke: PASS manifest=%s/build-smoke-manifest.json\n' "$output_dir"
