#!/usr/bin/env python3
"""Summarize one run-500qps-seam.sh evidence directory.

The summary separates hardware-independent signals (allocations and bytes per
operation, which do not depend on runner speed) from latency, and records the
runner class (CPU model, disk write latency, IO pressure) so a slow hosted
allocation is visible instead of silently failing a gate.

Usage: summarize_seam.py EVIDENCE_DIR  -> writes EVIDENCE_DIR/summary.json and
prints it. Missing inputs stay null; this script never decides pass/fail.
"""
import datetime
import json
import os
import re
import sys


def load_json(path):
    try:
        with open(path) as handle:
            return json.load(handle)
    except (OSError, ValueError):
        return None


def read_text(path):
    try:
        with open(path) as handle:
            return handle.read().strip()
    except OSError:
        return None


def cpu_model(evidence):
    host = load_json(os.path.join(evidence, "host.json")) or {}
    for entry in host.get("lscpu", []):
        if entry.get("field", "").strip(":") == "Model name":
            return entry.get("data")
    return None


def benchmem(evidence):
    """Extract ns/op, B/op and allocs/op from the Go benchmark result line."""
    text = read_text(os.path.join(evidence, "benchmark.txt")) or ""
    result = {}
    for key, unit in (("ns_per_op", "ns/op"), ("bytes_per_op", "B/op"), ("allocs_per_op", "allocs/op")):
        match = re.search(r"([0-9.]+) " + re.escape(unit) + r"(\s|$)", text)
        result[key] = float(match.group(1)) if match else None
    return result


def latency(evidence):
    arrival = load_json(os.path.join(evidence, "arrival.json")) or {}
    windows = [w.get("scheduled_to_completion_p99_ms") for w in arrival.get("windows", [])]
    windows = [w for w in windows if isinstance(w, (int, float))]
    return {"window_p99_ms": windows, "max_window_p99_ms": max(windows) if windows else None}


def _timestamp(value):
    value = re.sub(r"(\.\d{6})\d+", r"\1", value).replace("Z", "+00:00")
    return datetime.datetime.fromisoformat(value)


def _disk(text):
    for line in text.splitlines():
        fields = line.split()
        if len(fields) >= 11 and fields[2] in ("sda", "nvme0n1", "vda"):
            return int(fields[7]), int(fields[10])
    return None


def _psi_total(text):
    match = re.search(r"some .*total=(\d+)", text or "")
    return int(match.group(1)) if match else None


def host_pressure(evidence):
    """Disk write latency and IO pressure across the measured counter window."""
    before = load_json(os.path.join(evidence, "counters", "before.json"))
    after = load_json(os.path.join(evidence, "counters", "after.json"))
    result: dict = {"disk_write_ms": None, "io_pressure_pct": None}
    if not before or not after:
        return result
    try:
        seconds = (_timestamp(after["at"]) - _timestamp(before["at"])).total_seconds()
        first = _disk(before["system"]["/proc/diskstats"])
        last = _disk(after["system"]["/proc/diskstats"])
        if first and last and last[0] > first[0]:
            result["disk_write_ms"] = round((last[1] - first[1]) / (last[0] - first[0]), 3)
        start = _psi_total(before["system"].get("/proc/pressure/io"))
        end = _psi_total(after["system"].get("/proc/pressure/io"))
        if start is not None and end is not None and seconds > 0:
            result["io_pressure_pct"] = round((end - start) / 1e6 / seconds * 100, 1)
    except (KeyError, TypeError, ValueError):
        pass
    return result


def summarize(evidence):
    footprint = read_text(os.path.join(evidence, "data-footprint-kib.txt"))
    summary = {
        "schema": "wukongim.perf.seam-summary/v1",
        "status": (load_json(os.path.join(evidence, "result.json")) or {}).get("status"),
        "source_sha": read_text(os.path.join(evidence, "source.sha")),
        "data_mode": read_text(os.path.join(evidence, "data-mode.txt")) or "disk",
        "data_footprint_kib": int(footprint.split()[0]) if footprint else None,
        "cpu_model": cpu_model(evidence),
    }
    summary.update(benchmem(evidence))
    summary.update(latency(evidence))
    summary.update(host_pressure(evidence))
    return summary


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: summarize_seam.py EVIDENCE_DIR")
    evidence = sys.argv[1]
    summary = summarize(evidence)
    with open(os.path.join(evidence, "summary.json"), "w") as handle:
        json.dump(summary, handle, indent=2)
        handle.write("\n")
    print(json.dumps(summary))


if __name__ == "__main__":
    main()
