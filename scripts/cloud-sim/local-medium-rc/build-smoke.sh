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

die() {
  printf 'local-medium-rc-build-smoke: %s\n' "$*" >&2
  exit 1
}

sha256() {
  LC_ALL=C LANG=C shasum -a 256 "$1" | awk '{print $1}'
}

cleanup() {
  if [[ -n "${BASE_WORKTREE:-}" && -d "$BASE_WORKTREE" ]]; then
    git -C "$ROOT_DIR" worktree remove --force "$BASE_WORKTREE" >/dev/null 2>&1 || true
  fi
  if [[ -n "${CANDIDATE_WORKTREE:-}" && -d "$CANDIDATE_WORKTREE" ]]; then
    git -C "$ROOT_DIR" worktree remove --force "$CANDIDATE_WORKTREE" >/dev/null 2>&1 || true
  fi
  [[ -z "${WORK_ROOT:-}" || ! -d "$WORK_ROOT" ]] || rm -rf "$WORK_ROOT"
  [[ -z "${STAGE_ROOT:-}" || ! -d "$STAGE_ROOT" ]] || rm -rf "$STAGE_ROOT"
}
trap cleanup EXIT

if [[ $# -ne 3 ]]; then
  die "usage: build-smoke.sh BASELINE_COMMIT CANDIDATE_COMMIT OUTPUT_DIR"
fi

baseline_source="$(git -C "$ROOT_DIR" rev-parse --verify "$1^{commit}")" || die "baseline commit not found"
candidate_source="$(git -C "$ROOT_DIR" rev-parse --verify "$2^{commit}")" || die "candidate commit not found"
output_dir="$3"
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
[[ ! -e "$output_dir" ]] || die "output already exists: $output_dir"

for source in "$BUILD_SCRIPT" "$COMMON_SOURCE" "$BASELINE_ADAPTER" "$CANDIDATE_ADAPTER" "$BASELINE_LOCK" "$BUILD_MANIFEST_POLICY"; do
  [[ -f "$ROOT_DIR/$source" && ! -L "$ROOT_DIR/$source" ]] || die "missing or symlinked harness source: $source"
  git -C "$ROOT_DIR" ls-files --error-unmatch "$source" >/dev/null 2>&1 || die "harness source is not tracked: $source"
  git -C "$ROOT_DIR" show "$candidate_source:$source" | cmp -s - "$ROOT_DIR/$source" ||
    die "working harness source does not match candidate commit: $source"
done

WORK_ROOT="$(mktemp -d "${TMPDIR:-/private/tmp}/wk-local-medium-rc-smoke.XXXXXX")"
BASE_WORKTREE="$WORK_ROOT/baseline"
CANDIDATE_WORKTREE="$WORK_ROOT/candidate"
STAGE_ROOT="$(mktemp -d "${TMPDIR:-/private/tmp}/wk-local-medium-rc-output.XXXXXX")"
mkdir -p "$STAGE_ROOT/bin" "$STAGE_ROOT/equivalence"
git -C "$ROOT_DIR" worktree add --detach "$BASE_WORKTREE" "$baseline_source" >/dev/null
git -C "$ROOT_DIR" worktree add --detach "$CANDIDATE_WORKTREE" "$candidate_source" >/dev/null

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

build_variant() {
  local variant="$1" worktree="$2" adapter_source="$3" source_sha="$4"
  local overlay="$WORK_ROOT/$variant-overlay.json"
  local binary="$STAGE_ROOT/bin/app-$variant.test"
  local output="$STAGE_ROOT/equivalence/$variant.txt"

  jq -n \
    --arg common_target "$worktree/$OVERLAY_COMMON_TARGET" \
    --arg common_source "$ROOT_DIR/$COMMON_SOURCE" \
    --arg adapter_target "$worktree/$OVERLAY_ADAPTER_TARGET" \
    --arg adapter_source "$ROOT_DIR/$adapter_source" \
    '{Replace:{($common_target):$common_source,($adapter_target):$adapter_source}}' >"$overlay"

  (
    cd "$worktree"
    env -i PATH="$(dirname "$go_tool"):/usr/bin:/bin:/usr/sbin:/sbin" HOME="$HOME" LANG=C LC_ALL=C TZ=UTC \
      GOWORK=off GOTOOLCHAIN=local "$go_tool" test -c -trimpath -overlay "$overlay" -o "$binary" ./internal/app
  )
  [[ -z "$(git -C "$worktree" status --porcelain=v1 --untracked-files=normal)" ]] ||
    die "$variant worktree changed during build"

  env -i PATH="/usr/bin:/bin:/usr/sbin:/sbin" HOME="$HOME" LANG=C LC_ALL=C TZ=UTC \
    "$binary" -test.run "$EQUIVALENCE_TEST" -test.count 1 -test.v >"$output" 2>&1 ||
    die "$variant equivalence test failed; see $output"
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

mkdir -p "$(dirname "$output_dir")"
mv "$STAGE_ROOT" "$output_dir"
STAGE_ROOT=""
printf 'local-medium-rc-build-smoke: PASS manifest=%s/build-smoke-manifest.json\n' "$output_dir"
