#!/usr/bin/env python3
"""Align bounded host intervals with RPC seconds without inventing per-second counts."""
import argparse
import json
import math
from pathlib import Path
import sys

from host_sample import NS, OUTPUT_CAP, READ_CAP, SAMPLE_CAP, SCHEMA, THREAD_CAP, Invalid

IDENTITY = ("HostID", "BootID", "PID", "StartTicks", "BinarySHA256")
DRIFT_PPM = 1000  # Explicit assumption, not a measured guarantee across hosts.
MAX_CLOCK_WIDTH_NS = 100_000_000
MAX_LATENESS_NS = 250_000_000


def require(ok, message):
    if not ok:
        raise Invalid(message)


def positive(n):
    return type(n) is int and n > 0


def counter_map(values):
    require(isinstance(values, dict) and len(values) <= 10000, "counter map bound")
    require(all(isinstance(k, str) and type(v) is int and v >= 0 for k, v in values.items()), "invalid counter")


def strict_json(raw):
    def bad_constant(_):
        raise Invalid("non-finite JSON value")

    def finite_float(value):
        result = float(value)
        require(math.isfinite(result), "non-finite JSON value")
        return result

    return json.loads(raw, parse_constant=bad_constant, parse_float=finite_float)


def load_samples(path):
    require(path.stat().st_size <= OUTPUT_CAP, "input file bound")
    records = []
    with path.open() as stream:
        for _ in range(SAMPLE_CAP + 3):
            line = stream.readline(READ_CAP + 1)
            if not line:
                break
            require(len(line.encode()) <= READ_CAP and line.endswith("\n"), "record bound/truncation")
            records.append(strict_json(line))
        require(not stream.read(1), "record count bound")
    return records


def span(value):
    low, high = value["LowNS"], value["HighNS"]
    require(positive(low) and positive(high) and low <= high, "missing/reversed monotonic anchor")
    require(high - low <= MAX_CLOCK_WIDTH_NS, "clock anchor wider than 100ms")
    return low, high


def clock_mapping(probe, role):
    timeline = probe["Timeline"]
    start = span(timeline["Start"])
    before, after = span(timeline["BeforeRPC"]), span(timeline["AfterRPC"])
    duration = probe["Options"]["Duration"]
    require(positive(duration) and duration <= 60 * NS, "invalid probe duration")
    require(before[1] <= start[0] and start[1] + duration <= after[0], "probe anchors out of order")
    require(after[1] - before[0] <= 65 * NS, "boundary span exceeds diagnostic limit")
    if role == "client":
        return start, (0, 0), {"basis": "same_process_clock_monotonic", "drift_assumption_ppm": 0}
    b, a = probe["ServerBefore"]["MonotonicNS"], probe["ServerAfter"]["MonotonicNS"]
    require(positive(b) and positive(a) and a > b, "invalid server clock")
    blo, bhi = b - before[1], b - before[0]
    alo, ahi = a - after[1], a - after[0]
    margin = math.ceil((after[1] - before[0]) * DRIFT_PPM / 1_000_000)
    require(max(blo - ahi, alo - bhi) <= margin, "server clock discontinuity/excess drift")
    # Enclose both RPC offset brackets and possible drift between boundaries.
    offset = min(blo, alo) - margin, max(bhi, ahi) + margin
    require(offset[1] - offset[0] <= MAX_CLOCK_WIDTH_NS, "server alignment uncertainty exceeds 100ms")
    return start, offset, {"basis": "existing_boundary_rpc_brackets", "drift_assumption_ppm": DRIFT_PPM,
                           "before_offset_ns": [blo, bhi], "after_offset_ns": [alo, ahi]}


def delta(before, after):
    counter_map(before)
    counter_map(after)
    reset = sorted(k for k in before.keys() & after.keys() if after[k] < before[k])
    return {"delta": {k: after[k] - before[k] for k in sorted(before.keys() & after.keys()) if k not in reset},
            "reset": reset, "missing_before": sorted(after.keys() - before.keys()),
            "missing_after": sorted(before.keys() - after.keys())}


def thread_delta(before, after):
    require(isinstance(before, dict) and isinstance(after, dict), "thread maps required")
    require(len(before) <= THREAD_CAP and len(after) <= THREAD_CAP, "thread bound")
    surviving, removed, added, values, resets, incomplete = [], [], [], {}, [], []
    for tid in sorted(before.keys() | after.keys()):
        a, b = before.get(tid), after.get(tid)
        if a and b and a["start_ticks"] == b["start_ticks"]:
            d = delta(a["counters"], b["counters"])
            surviving.append(tid)
            resets.extend(tid + "." + key for key in d["reset"])
            if d["missing_before"] or d["missing_after"]:
                incomplete.append(tid)
            for key, value in d["delta"].items():
                values[key] = values.get(key, 0) + value
        else:
            if a:
                removed.append(tid)
            if b:
                added.append(tid)
    return {"surviving_thread_delta_lower_bound": values, "surviving": surviving,
            "removed_or_reused": removed, "added_or_reused": added, "reset": resets,
            "incomplete_threads": incomplete,
            "complete_observation": not (removed or added or resets or incomplete)}


def correlate(probe, records, role):
    require(role in ("client", "server"), "unknown role")
    require(isinstance(probe, dict), "probe object required")
    require(probe["Schema"] == "wkrpc-process-probe/v1", "probe schema")
    require(3 <= len(records) <= SAMPLE_CAP + 2, "missing/too many records")
    require(all(isinstance(r, dict) for r in records), "invalid record type")
    header, end = records[0], records[-1]
    require(header["kind"] == "header" and header["schema"] == SCHEMA and header["diagnostic_only"] is True,
            "sample header")
    require(header["interval_ns"] == NS and end["kind"] == "end", "missing terminal record/interval")
    host = probe["Client"] if role == "client" else probe["ServerBefore"]["Host"]
    require(host["OS"] == "linux", "Linux clock required")
    require(all(host.get(k) and host[k] == header["identity"][k] for k in IDENTITY), "process identity mismatch")
    if role == "server":
        require(all(host[k] == probe["ServerAfter"]["Host"][k] for k in IDENTITY)
                and probe["ServerBefore"]["Instance"] == probe["ServerAfter"]["Instance"], "server restart")
    start, offset, mapping = clock_mapping(probe, role)
    duration = probe["Options"]["Duration"]
    bins = probe["Seconds"]
    require(len(bins) == math.ceil(duration / NS), "probe second count")
    samples = records[1:-1]
    require(len(samples) == end["samples"] and len(samples) >= 2, "insufficient sample boundaries")
    intervals, issues = [], []
    if end["exit_code"] != 0:
        issues.append("sampler_failed:" + str(end["outcome"]))
    for i, s in enumerate(samples):
        require(s["kind"] == "sample" and s["index"] == i, "sample ordering")
        require(positive(s["begin_ns"]) and s["end_ns"] >= s["begin_ns"], "sample clock")
        require(type(s["slot"]) is int and s["slot"] >= 0, "invalid slot")
        require(s["begin_ns"] - s["scheduled_ns"] == s["late_ns"] >= 0, "sample schedule")
        counter_map(s["counters"])
        if s["missing"]:
            issues.append("missing_metrics")
        if s["gauges"]["schedstats_enabled"] != 1:
            issues.append("schedstats_disabled_or_unavailable")
        if not i:
            continue
        a = samples[i - 1]
        require(a["end_ns"] <= s["begin_ns"] and s["slot"] > a["slot"], "non-monotonic samples")
        elapsed = s["begin_ns"] - a["begin_ns"]
        wall_drift = (s["wall_ns"] - a["wall_ns"]) - elapsed
        flags = []
        if s["slot"] != a["slot"] + 1 or elapsed > NS + MAX_LATENESS_NS:
            flags.append("sampling_gap")
        if max(a["late_ns"], s["late_ns"], a["end_ns"] - a["begin_ns"], s["end_ns"] - s["begin_ns"]) > MAX_LATENESS_NS:
            flags.append("late_or_long_sample")
        if abs(wall_drift) > 100_000_000:
            flags.append("wall_clock_jump_monotonic_alignment_retained")
        d, t = delta(a["counters"], s["counters"]), thread_delta(a["threads"], s["threads"])
        if d["reset"] or d["missing_before"] or d["missing_after"]:
            flags.append("counter_reset_or_membership_change")
        if not t["complete_observation"]:
            flags.append("thread_churn_or_partial_counters")
        # An increment occurred somewhere between the two reads. Keep it ONCE in
        # this interval; never divide or copy it into each overlapping RPC second.
        lo, hi = a["begin_ns"] - offset[1], s["end_ns"] - offset[0]
        overlap = [j for j in range(len(bins)) if lo < start[1] + min((j + 1) * NS, duration)
                   and hi > start[0] + j * NS]
        intervals.append({"from_sample": i - 1, "to_sample": i,
                          "client_clock_outer_ns": [lo, hi], "possible_rpc_seconds": overlap,
                          "elapsed_ns": elapsed, "wall_minus_monotonic_delta_ns": wall_drift,
                          "flags": flags, "counters": d, "threads": t,
                          "missing": {"before": a["missing"], "after": s["missing"]}})
        if overlap:
            issues.extend(flags)
    seconds = []
    for j, rpc in enumerate(bins):
        lo, hi = start[0] + j * NS, start[1] + min((j + 1) * NS, duration)
        bracketed = samples[0]["end_ns"] - offset[0] <= lo and samples[-1]["begin_ns"] - offset[1] >= hi
        adjacent = [k for k, interval in enumerate(intervals) if j in interval["possible_rpc_seconds"]]
        gaps = any(set(intervals[k]["flags"]) & {"sampling_gap", "late_or_long_sample"} for k in adjacent)
        if not bracketed or gaps:
            issues.append("incomplete_time_coverage")
        seconds.append({"second": j, "rpc": rpc, "bracketed": bracketed,
                        "regular_sampling": not gaps, "interval_indexes": adjacent})
    return {"schema": "wkrpc-host-correlation/v1", "role": role, "diagnostic_only": True,
            "status": "partial" if issues else "aligned", "issues": sorted(set(issues)),
            "clock": dict(mapping, start_ns=list(start), server_minus_client_ns=list(offset)),
            "scope": header["scope"], "seconds": seconds, "intervals": intervals,
            "sampler": end,
            "limitations": ["TCP/interface counters include all traffic in the target network namespace.",
                            "Surviving-thread deltas exclude terminated threads and transient threads between samples.",
                            "One-second counter intervals overlap RPC seconds; no exact attribution or root cause is inferred."]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--probe", type=Path, required=True)
    parser.add_argument("--samples", type=Path, required=True)
    parser.add_argument("--role", choices=("client", "server"), required=True)
    args = parser.parse_args()
    try:
        require(args.probe.stat().st_size <= 1024 * 1024, "probe file bound")
        result = correlate(strict_json(args.probe.read_text()), load_samples(args.samples), args.role)
    except (OSError, ValueError, KeyError, TypeError, IndexError, AttributeError) as exc:
        result = {"schema": "wkrpc-host-correlation/v1", "status": "invalid", "error": str(exc)}
    print(json.dumps(result, indent=2, allow_nan=False))
    return 1 if result["status"] == "invalid" else 0


if __name__ == "__main__":
    sys.exit(main())
