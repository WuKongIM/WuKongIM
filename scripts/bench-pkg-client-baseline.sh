#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${OUT_DIR:-$ROOT_DIR/tmp/pkg-client-baseline/$(date +%Y%m%d-%H%M%S)}"
BENCHTIME="${BENCHTIME:-2s}"
COUNT="${COUNT:-3}"

guard_regex='Test(PrepareSendAllocationBudget|WriteBatchEncodeAllocationBudget|PendingTrackerTimeoutAllocationBudget|SendFutureWaitAllocationBudget|SendFutureWaitOnceAllocationBudget|ClientSendBatchRoundTripAllocationBudget)$'
bench_regex='Benchmark(PrepareSend|PendingTrackerResolve|PendingTrackerResolveWithTimeout|SendFutureWaitReady|SendFutureWaitOnceReady|WriteBatchEncode|ClientSendBatchRoundTrip)$'

mkdir -p "$OUT_DIR"

{
  echo "# pkg/client benchmark baseline"
  echo "date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "git: $(git -C "$ROOT_DIR" rev-parse --short HEAD)"
  echo "go: $(go env GOVERSION)"
  echo "benchtime: $BENCHTIME"
  echo "count: $COUNT"
} >"$OUT_DIR/metadata.txt"

echo "[pkg-client-baseline] output: $OUT_DIR"
echo "[pkg-client-baseline] running allocation guards"
(
  cd "$ROOT_DIR"
  GOWORK=off go test -count=1 -run "$guard_regex" ./pkg/client
) | tee "$OUT_DIR/allocation-guards.txt"

echo "[pkg-client-baseline] running benchmarks"
(
  cd "$ROOT_DIR"
  GOWORK=off go test -run '^$' -bench "$bench_regex" -benchmem -benchtime="$BENCHTIME" -count="$COUNT" ./pkg/client
) | tee "$OUT_DIR/bench.txt"

echo "[pkg-client-baseline] done"
echo "[pkg-client-baseline] benchmark output: $OUT_DIR/bench.txt"
