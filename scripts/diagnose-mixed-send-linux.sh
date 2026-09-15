#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == --help ]]; then
  echo 'Usage: diagnose-mixed-send-linux.sh OUTPUT_DIR'
  echo 'One fixed 500-QPS unprofiled run and one measured-window profile on the same four-CPU Linux host. No retries or publication.'
  exit 0
fi
[[ $# == 1 && "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && "$(nproc)" == 4 ]]
[[ "$(findmnt -n -o FSTYPE -T /dev/shm)" == tmpfs ]]
[[ -z "$(git status --porcelain --untracked-files=no)" ]]
output_dir="$1"
[[ "$output_dir" == /* && ! -e "$output_dir" ]]
mkdir -m 0700 -p "$output_dir"
export GOWORK=off GOMAXPROCS=4
unset WK_BENCH_SEND_DIAGNOSTICS_DIR
git rev-parse HEAD > "$output_dir/source.sha"
lscpu --json > "$output_dir/host.json"
uname -a > "$output_dir/kernel.txt"
df -Pk /tmp "$output_dir" > "$output_dir/filesystem.txt"
findmnt -J -T /tmp > "$output_dir/data-filesystem.json"
go version > "$output_dir/go-version.txt"
go test -c -tags=integration -o "$output_dir/mixed-send.test" ./internal/app
sha256sum "$output_dir/mixed-send.test" > "$output_dir/binary.sha256"
go version -m "$output_dir/mixed-send.test" > "$output_dir/build.txt"

# The diagnostic always retains the first verdict; profiling never replaces it.
set +e
"$output_dir/mixed-send.test" -test.run '^$' -test.bench '^BenchmarkThreeNodeMixedSendPath500QPS$' -test.benchtime=3000x -test.benchmem -test.count=1 -test.timeout=5m > "$output_dir/unprofiled.txt" 2>&1
unprofiled_status=$?
WK_BENCH_SEND_DIAGNOSTICS_DIR="$output_dir/profile" "$output_dir/mixed-send.test" -test.run '^$' -test.bench '^BenchmarkThreeNodeMixedSendPath500QPS$' -test.benchtime=3000x -test.benchmem -test.count=1 -test.timeout=5m > "$output_dir/profiled.txt" 2>&1
profiled_status=$?
set -e
python3 - "$output_dir" "$unprofiled_status" "$profiled_status" <<'PY'
import json, pathlib, re, sys
p = pathlib.Path(sys.argv[1])
report = {'schema': 'mixed-send-diagnosis/v1', 'source_sha': (p/'source.sha').read_text().strip(),
          'binary_sha256': (p/'binary.sha256').read_text().split()[0], 'qps': 500, 'operations': 3000,
          'threshold_ms': 400, 'release_qualification': False, 'runs': {}}
for name, code in zip(('unprofiled', 'profiled'), sys.argv[2:]):
    text = (p/(name+'.txt')).read_text()
    values = re.findall(r'([0-9.]+)\s+all-p99-ms\b', text)
    p99 = float(values[0]) if len(values) == 1 else None
    report['runs'][name] = {'exit_code': int(code), 'p99_ms': p99,
                           'within_400_ms': int(code) == 0 and p99 is not None and p99 <= 400,
                           'performance_evidence': name == 'unprofiled'}
(p/'report.json').write_text(json.dumps(report, indent=2)+'\n')
print(json.dumps(report, indent=2))
assert all(r['p99_ms'] is not None for r in report['runs'].values()), 'missing or duplicate P99 evidence'
PY
[[ "$unprofiled_status" == 0 && "$profiled_status" == 0 ]]
python3 - "$output_dir/profile/window.json" <<'PY'
import json, sys
r=json.load(open(sys.argv[1]))
assert r['schema']=='mixed-send-window/v1' and r['profile_complete']
assert r['operations']==3000 and r['offered_qps']==500
assert 0 < r['profile_window_seconds'] < 30
assert 0 < r['cpu_bytes'] <= 8<<20 and 0 < r['trace_bytes'] <= 64<<20
PY
go tool pprof -top -nodecount=40 "$output_dir/mixed-send.test" "$output_dir/profile/cpu.pprof" > "$output_dir/cpu-top.txt"
for kind in sched sync syscall net; do
  go tool trace -pprof="$kind" "$output_dir/profile/execution.trace" > "$output_dir/$kind.pprof"
  go tool pprof -top -nodecount=40 "$output_dir/mixed-send.test" "$output_dir/$kind.pprof" > "$output_dir/$kind-top.txt"
done
git diff --exit-code HEAD --
