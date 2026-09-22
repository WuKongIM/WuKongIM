#!/usr/bin/env python3
"""Fail-closed same-version repeatability gate; never accepts an optimization."""
import argparse
import datetime
import json
import math
import statistics
from pathlib import Path


def require(condition, message):
    if not condition:
        raise ValueError(message)


def number(value):
    require(type(value) in (float, int) and math.isfinite(value) and value >= 0,
            "expected finite non-negative number")
    return value


def identity(h):
    require(h["OS"] == "linux" and h["Arch"] == "amd64" and h["KernelArch"] == "x86_64",
            "requires Linux amd64 without architecture emulation")
    require(h["Container"] is False and not h["Ineligible"], "host is not eligible for qualification")
    require(h["CPUCount"] >= 4 and h["Procs"] == 4, "requires four CPUs and GOMAXPROCS=4")
    require(h["MemoryLimit"] == 2**63-1 and h["RuntimeDebug"] == "", "unexpected Go runtime overrides")
    require(h["CPUModel"] and h["HostID"] and h["AllowedCPUs"], "missing host identity or CPU affinity")
    require(h["Source"] != "unspecified" and len(h["Source"]) == 40, "missing committed source identity")
    require(len(h["BinarySHA256"]) == 64 and h["GoVersion"] == "go1.25.11", "binary/toolchain identity mismatch")
    return tuple(h[k] for k in ("HostID", "BinarySHA256", "Source", "GoVersion", "CPUModel", "AllowedCPUs", "CPUCount", "Procs"))


def validate(r):
    require(r["Schema"] == "wkrpc-process-probe/v1", "unsupported schema")
    c = identity(r["Client"])
    before, after = r["ServerBefore"], r["ServerAfter"]
    s = identity(before["Host"])
    require(s == identity(after["Host"]), "server changed during measurement")
    require(c[0] != s[0] and c[4] == s[4], "requires separate hosts with the same CPU model")
    require(before["Instance"] == after["Instance"], "server restarted during measurement")
    require(before["Options"] == after["Options"], "server options changed")
    o = r["Options"]
    require(o.get("TelemetryTail", 0) == 0, "diagnostic host-sampled runs cannot qualify repeatability")
    expected = {"Mode": "client", "Duration": 20_000_000_000, "Warmup": 10_000_000_000,
                "Workers": 16, "Shards": 1, "Bytes": 64, "Samples": 1_000_000,
                "GC": 400, "Budgets": False, "Read": False}
    require(all(o[k] == v for k, v in expected.items()), "not the frozen calibration scenario")
    opts = before["Options"]
    expected_server = {"Concurrency": 64, "QueueSize": 4096, "MaxQueueBytes": 64 << 20,
                       "MaxRetainedBytes": 128 << 20, "MaxPayload": 65537,
                       "Timeout": 30_000_000_000, "QueueTimeout": 5_000_000_000,
                       "CancelRunning": False}
    require(all(opts[k] == v for k, v in expected_server.items()), "server scenario mismatch")
    require(before["GC"] == after["GC"] == 100, "server GC must remain 100")
    calls = number(r["Summary"]["Calls"])
    elapsed = number(r["ElapsedSeconds"])
    rate, p99 = number(r["CallsPerSecond"]), number(r["Summary"]["P99MS"])
    require(r["Errors"] == 0 and r["SampleCapHit"] is False and calls > 0,
            "failed, capped, or empty measurement")
    require(20 <= elapsed <= 22 and math.isclose(rate, calls / elapsed, rel_tol=1e-10),
            "duration or throughput arithmetic mismatch")
    require(len(r["WorkerCalls"]) == 16 and sum(r["WorkerCalls"]) == calls and
            all(type(n) is int and 0 < n < 1_000_000 for n in r["WorkerCalls"]), "worker sample counts invalid")
    require(len(r["WarmupCalls"]) == 16 and all(type(n) is int and n > 0 for n in r["WarmupCalls"]), "warmup incomplete")
    require(len(r["Seconds"]) == 20 and sum(q["Calls"] for q in r["Seconds"]) == calls,
            "per-second sample counts mismatch")
    for q in r["Seconds"]:
        require(number(q["Calls"]) > 0, "empty second")
        number(q["P99MS"])
    require(after["EchoCalls"] - before["EchoCalls"] == calls, "other client activity or server count mismatch")
    for a, b in [(r["ClientBefore"], r["ClientAfter"]), (before["Stats"], after["Stats"])]:
        for k in ("TotalAlloc", "Mallocs", "PauseNS", "NumGC", "CPUSeconds"):
            require(number(b[k]) >= number(a[k]), "resource counter regressed")
    started = datetime.datetime.fromisoformat(r["StartedUTC"].replace("Z", "+00:00"))
    require(started.utcoffset() == datetime.timedelta(0), "measurement start must be UTC")
    return c, s, opts, started, elapsed, rate, p99


def analyze(reports):
    try:
        require(len(reports) >= 6, "requires at least six independent same-version windows")
        rows = [validate(r) for r in reports]
        require(all(row[:3] == rows[0][:3] for row in rows), "mixed hosts, binaries, affinity, or server settings")
        ordered = sorted(rows, key=lambda row: row[3])
        for a, b in zip(ordered, ordered[1:]):
            require((b[3] - a[3]).total_seconds() >= a[4], "duplicate or overlapping measurement windows")
        rates, tails = [r[5] for r in rows], [r[6] for r in rows]
        require(min(tails) > 0, "invalid zero P99")
        spread = lambda xs: (max(xs) - min(xs)) / statistics.median(xs) * 100
        rate_spread, tail_spread = spread(rates), spread(tails)
        passed = rate_spread <= 3 and tail_spread <= 10
        return {"schema": "wkrpc-repeatability/v1", "status": "passed" if passed else "unstable",
                "windows": len(rows), "throughput_spread_percent": rate_spread,
                "p99_spread_percent": tail_spread, "limits_percent": {"throughput": 3, "p99": 10},
                "meaning": "same-version repeatability only; no candidate or production qualification"}
    except (KeyError, TypeError, ValueError, ZeroDivisionError, OverflowError) as e:
        return {"schema": "wkrpc-repeatability/v1", "status": "invalid", "reason": str(e)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("reports", type=Path, nargs="+")
    args = parser.parse_args()
    try:
        result = analyze([json.loads(p.read_text()) for p in args.reports])
    except (OSError, ValueError) as e:
        result = {"schema": "wkrpc-repeatability/v1", "status": "invalid", "reason": str(e)}
    print(json.dumps(result, indent=2))
    return 0 if result["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
