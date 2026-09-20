#!/usr/bin/env bash
# One clean build and one sustained run; no retry or best-window selection.
set -euo pipefail
[[ $# == 2 ]] || { echo 'usage: run-500qps-seam.sh channel-append|mixed-send|tcp-sendack EVIDENCE_DIR' >&2; exit 2; }
seam="$1"
evidence="$2"
case "$seam" in
  channel-append) package=./pkg/channel/replication; benchmark=BenchmarkThreeNodeChannelAppend500QPS ;;
  mixed-send) package=./internal/app; benchmark=BenchmarkThreeNodeMixedSendPath500QPS ;;
  tcp-sendack) package=./pkg/client; benchmark=BenchmarkRealTCPSendackWithSynchronousRecvackPaced500QPS ;;
  *) echo 'unknown seam' >&2; exit 2 ;;
esac
[[ "$evidence" == /* && ! -e "$evidence" ]] || { echo "evidence directory must be fresh and absolute" >&2; exit 2; }
mkdir -p "$evidence"
evidence="$(cd "$evidence" && pwd)"
git rev-parse HEAD >"$evidence/source.sha"
uname -a >"$evidence/kernel.txt"
lscpu --json >"$evidence/host.json"
go version >"$evidence/go-version.txt"
# Pin concurrency and toolchain. CPU model remains recorded, not assumed fixed
# across GitHub-hosted allocations; pressure during load never excuses failure.
[[ "$(go env GOVERSION)" == go1.25.11 && "$(uname -m)" == x86_64 && "$(nproc)" == 4 ]] || {
  printf '{"status":"environment_invalid","reason":"toolchain_or_cpu_contract"}\n' >"$evidence/result.json"
  exit 2
}
export GOWORK=off GOMAXPROCS=4
mkdir -p "$evidence/data"
export TMPDIR="$evidence/data"
findmnt -J -T "$TMPDIR" >"$evidence/data-filesystem.json"
df -Pk "$TMPDIR" >"$evidence/filesystem-preflight.txt"
read -r blocks available < <(awk 'NR == 2 {print $2, $4}' "$evidence/filesystem-preflight.txt")
if (( blocks <= 0 || available * 100 / blocks < 15 )); then
  printf '{"status":"environment_invalid","reason":"insufficient_free_disk"}\n' >"$evidence/result.json"
  exit 2
fi
go test -c -tags=integration "$package" -o "$evidence/benchmark.test"
sha256sum "$evidence/benchmark.test" >"$evidence/binary.sha256"
export WK_BENCH_QUALIFY=1 WK_BENCH_ARRIVAL_REPORT="$evidence/arrival.json"
flight_stage=""
trap '[[ -z "$flight_stage" ]] || rm -rf -- "$flight_stage"' EXIT
case "$seam" in
 channel-append) export WK_BENCH_APPEND_COUNTERS_DIR="$evidence/counters" ;;
 mixed-send)
   [[ "$(findmnt -n -o FSTYPE -T /dev/shm)" == tmpfs ]]
   flight_stage="$(mktemp -d /dev/shm/wk-send-flight.XXXXXXXX)"
   export WK_BENCH_SEND_COUNTERS_DIR="$evidence/counters"
   export WK_BENCH_SEND_FLIGHT_DIR="$flight_stage/flight"
   ;;
esac
status=0
"$evidence/benchmark.test" -test.run '^$' -test.bench "^${benchmark}$" -test.benchtime=90000x -test.benchmem -test.count=1 -test.timeout=12m >"$evidence/benchmark.txt" 2>&1 || status=$?
cat "$evidence/benchmark.txt"
# Report absence (including setup failure/timeout) cannot become a passing gate.
if [[ "$status" == 0 && -s "$evidence/arrival.json" ]]; then
 printf '{"status":"pass"}\n' >"$evidence/result.json"
else
 printf '{"status":"failed","exit_code":%s}\n' "$status" >"$evidence/result.json"
 status=1
fi
# Even Go's fatal test timeout skips cleanups. The shell still owns the tmpfs
# directory and can retain a captured trace and its last checkpoint after exit.
retain_flight() {
 [[ -n "$flight_stage" ]] || return 0
 mkdir -p "$evidence/flight" || return
 for name in window.json execution.trace; do
   if [[ -f "$flight_stage/flight/$name" ]]; then
     if ! cp "$flight_stage/flight/$name" "$evidence/flight/$name"; then
       printf '%s copy_failed\n' "$name" >>"$evidence/flight/retention-status.txt" || return
     fi
   else
     printf '%s unavailable\n' "$name" >>"$evidence/flight/retention-status.txt" || return
   fi
 done
}
# Derived trace views are diagnostic only; retain the original benchmark exit
# above even when extraction fails. No second fixture or retry is permitted.
analyze_flight() {
 [[ "$seam" == mixed-send && -s "$evidence/flight/execution.trace" ]] || return 0
 for kind in sched sync syscall net; do
   if timeout 30s go tool trace -pprof="$kind" "$evidence/flight/execution.trace" >"$evidence/flight/$kind.pprof" 2>"$evidence/flight/$kind-error.txt" &&
      timeout 30s go tool pprof -top -nodecount=40 "$evidence/benchmark.test" "$evidence/flight/$kind.pprof" >"$evidence/flight/$kind-top.txt" 2>>"$evidence/flight/$kind-error.txt"; then
     printf '%s complete\n' "$kind" >>"$evidence/flight/analysis-status.txt" || return
   else
     printf '%s incomplete\n' "$kind" >>"$evidence/flight/analysis-status.txt" || return
   fi
 done
}
retain_flight || { echo 'rolling SEND trace retention incomplete' >&2 || :; }
analyze_flight || { echo 'rolling SEND trace analysis incomplete' >&2 || :; }
# Large binaries and transient databases are not evidence artifacts.
rm -f "$evidence/benchmark.test"
rm -rf -- "$evidence/data"
exit "$status"
