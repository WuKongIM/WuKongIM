#!/usr/bin/env python3
"""Compare interleaved same-runner base/head seam runs.

Both sides ran on one runner in alternating rounds, so runner speed cancels
out of the ratios. Allocation metrics are nearly deterministic and get a tight
limit; latency is noisy and only guards against large regressions.

Usage: compare_ab.py SEAM ROUND_ROOT
  ROUND_ROOT contains base-<n>/summary.json and head-<n>/summary.json.
Writes a Markdown report to stdout. Exit status 1 means a regression was found
and WK_PERF_ENFORCE=1; with WK_PERF_ENFORCE unset the verdict is report-only.
"""
import glob
import json
import os
import statistics
import sys

# Provisional limits; they are calibrated from observation-mode data before
# enforcement is enabled by default.
LIMITS = {
    "allocs_per_op": 1.10,
    "bytes_per_op": 1.10,
    "max_window_p99_ms": 1.50,
}
# Latency ratios between two tiny values are noise; ignore p99 below this.
P99_FLOOR_MS = 20.0


def load(root, side):
    summaries = []
    for path in sorted(glob.glob(os.path.join(root, side + "-*", "summary.json"))):
        with open(path) as handle:
            summaries.append(json.load(handle))
    return summaries


def median(values):
    values = [v for v in values if isinstance(v, (int, float))]
    return statistics.median(values) if values else None


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: compare_ab.py SEAM ROUND_ROOT")
    seam, root = sys.argv[1], sys.argv[2]
    base, head = load(root, "base"), load(root, "head")
    lines = [f"### `{seam}` same-runner A/B", ""]
    if not base or not head:
        lines.append("Missing base or head results; no comparison is possible.")
        print("\n".join(lines))
        sys.exit(1)
    failed_runs = [s for s in base + head if s.get("status") != "pass"]
    cpu = head[0].get("cpu_model") or "unknown"
    lines += [f"Runner CPU: `{cpu}`, data: `{head[0].get('data_mode')}`, rounds: {len(head)}", ""]
    lines += ["| metric | base median | head median | head/base | limit | verdict |", "|---|---|---|---|---|---|"]
    regressions = []
    for metric, limit in LIMITS.items():
        b = median([s.get(metric) for s in base])
        h = median([s.get(metric) for s in head])
        if b is None or h is None or b == 0:
            lines.append(f"| {metric} | {b} | {h} | - | {limit} | no data |")
            continue
        ratio = h / b
        ignored = metric == "max_window_p99_ms" and h < P99_FLOOR_MS
        verdict = "ok"
        if ratio > limit and not ignored:
            verdict = "REGRESSION"
            regressions.append(metric)
        elif ignored:
            verdict = f"ok (< {P99_FLOOR_MS:g} ms)"
        lines.append(f"| {metric} | {b:.2f} | {h:.2f} | {ratio:.3f} | {limit} | {verdict} |")
    if failed_runs:
        regressions.append("failed-runs")
        lines += ["", f"{len(failed_runs)} individual run(s) did not pass; see the uploaded evidence."]
    enforce = os.environ.get("WK_PERF_ENFORCE") == "1"
    lines.append("")
    if regressions:
        mode = "failing" if enforce else "report-only (observation mode)"
        lines.append(f"**Regression detected: {', '.join(regressions)}** ({mode}).")
    else:
        lines.append("No regression detected.")
    print("\n".join(lines))
    sys.exit(1 if regressions and enforce else 0)


if __name__ == "__main__":
    main()
