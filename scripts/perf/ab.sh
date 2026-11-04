#!/usr/bin/env bash
# Run one 500-QPS seam for a base and a head revision on the same runner,
# alternating base/head for ROUNDS rounds, then compare with compare_ab.py.
# The head checkout's run-500qps-seam.sh is the harness for both sides, so
# only the product code differs between base and head.
set -euo pipefail
[[ $# == 4 ]] || { echo 'usage: ab.sh SEAM BASE_REF ROUNDS OUT_DIR' >&2; exit 2; }
seam="$1" base_ref="$2" rounds="$3" out="$4"
[[ "$rounds" =~ ^[1-9]$ ]] || { echo 'ROUNDS must be 1..9' >&2; exit 2; }
[[ "$out" == /* && ! -e "$out" ]] || { echo 'OUT_DIR must be fresh and absolute' >&2; exit 2; }
head_root="$(git rev-parse --show-toplevel)"
harness="$head_root/scripts/run-500qps-seam.sh"
base_root="$(mktemp -d "${RUNNER_TEMP:-/tmp}/wk-perf-base.XXXXXXXX")"
trap 'git -C "$head_root" worktree remove --force "$base_root" >/dev/null 2>&1 || :' EXIT
git -C "$head_root" worktree add --detach "$base_root" "$base_ref" >/dev/null
mkdir -p "$out"
export WK_PERF_DATA="${WK_PERF_DATA:-tmpfs}"
for round in $(seq 1 "$rounds"); do
  for side in base head; do
    root="$head_root"
    [[ "$side" == head ]] || root="$base_root"
    # A failed single run is retained as evidence and reported by the comparison.
    (cd "$root" && bash "$harness" "$seam" "$out/$side-$round") || :
    python3 "$head_root/scripts/perf/summarize_seam.py" "$out/$side-$round" >/dev/null || :
  done
done
python3 "$head_root/scripts/perf/compare_ab.py" "$seam" "$out"
