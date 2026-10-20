"""Read only existing qualification receipts; derive bounded resource/stage costs."""
import datetime
import hashlib
import json
from collections import defaultdict
from pathlib import Path

ROOT = Path("/tmp/wukongim-v3-rc-20261020")
OUT = Path("/tmp/wukongim-performance-followup-20261020")
INPUTS = {
    "reverse_beta24": OUT / "reverse/report.json",
    "reverse_candidate": OUT / "reverse/comparison/report.json",
}


def labels(key):
    return dict(part.split("=", 1) for part in key.partition("{")[2].rstrip("}").split(",") if "=" in part)


def aggregate(window, prefix, keep=()):
    totals = defaultdict(float)
    for before, after in zip(window["metrics_before"], window["metrics_after"]):
        for key, value in after.items():
            if key.partition("{")[0] == prefix:
                tags = labels(key)
                group = ",".join(f"{k}={tags[k]}" for k in keep if k in tags)
                totals[group] += value - before.get(key, 0)
    return dict(totals)


def ratios(window, prefix, keep):
    counts = aggregate(window, prefix + "_count", keep)
    sums = aggregate(window, prefix + "_sum", keep)
    return {k: {"count": v, "sum": sums.get(k, 0), "mean": sums.get(k, 0) / v if v else None}
            for k, v in counts.items() if v}


def instant(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def counters(raw):
    result = {}
    for line in raw.splitlines():
        fields = line.split()
        if len(fields) == 2:
            try:
                result[fields[0]] = int(fields[1])
            except ValueError:
                pass
    return result


def proc_ticks(raw):
    values = raw.rsplit(")", 1)[1].split()
    return int(values[11]) + int(values[12])


def resource(report, index, window):
    completed = sum(p["completed"] for p in window["phases"])
    row = {
        "window": index + 1,
        "completed": completed,
        "server_cpu_seconds": window["cpu_seconds"],
        "server_cpu_ms_per_completed_mixed_request": window["cpu_seconds"] * 1000 / completed,
        "server_allocated_bytes": window["allocated_bytes"],
        "server_allocated_bytes_per_completed_mixed_request": window["allocated_bytes"] / completed,
        "server_average_cpu_cores_over_60s": window["cpu_seconds"] / 60,
        "stages": ratios(window, "wukongim_conversation_read_stage_duration_seconds", ("scope", "stage", "result")),
        "persisted_hold": ratios(window, "wukongim_conversation_persisted_hold_seconds", ("kind", "result")),
        "persisted_occupancy": ratios(window, "wukongim_conversation_persisted_occupancy", ("kind", "result")),
        "persisted_admission": aggregate(window, "wukongim_conversation_persisted_admission_total", ("kind", "result")),
        "rpc_client_exact_calls": aggregate(window, "wukongim_transport_rpc_client_total", ("service", "result")),
        "rpc_client_sampled_latency": ratios(window, "wukongim_transport_rpc_client_duration_seconds", ("service",)),
        "observer_dropped": {k: sum(after.get(k, 0)-before.get(k, 0) for before, after in zip(window["metrics_before"], window["metrics_after"]))
                             for k in window["metrics_after"][0] if "observer_dropped" in k},
    }
    occ = aggregate(window, "wukongim_conversation_persisted_occupancy_bucket", ("kind", "result", "le"))
    row["persisted_occupancy_buckets"] = {k: v for k, v in occ.items() if v}
    a, b = report["host_snapshots"][2 * index:2 * index + 2]
    elapsed = (instant(b["time"]) - instant(a["time"])).total_seconds()
    ac, bc = counters(a["/sys/fs/cgroup/cpu.stat"]), counters(b["/sys/fs/cgroup/cpu.stat"])
    usage = (bc["usage_usec"] - ac["usage_usec"]) / 1e6
    ticks = proc_ticks(b["/proc/self/stat"]) - proc_ticks(a["/proc/self/stat"])
    sa, sb = ([int(x) for x in t["/proc/stat"].splitlines()[0].split()[1:9]] for t in (a, b))
    delta = [y-x for x, y in zip(sa, sb)]
    total = sum(delta)
    row["host"] = {
        "bracket_seconds": elapsed,
        "cgroup_usage_seconds": usage,
        "cgroup_four_cpu_utilization": usage / elapsed / 4,
        "server_cpu_over_cgroup_cpu_approximate_ratio": window["cpu_seconds"] / usage,
        "cgroup_minus_server_cpu_seconds_different_brackets": usage - window["cpu_seconds"],
        "driver_cpu_ticks": ticks,
        "driver_cpu_ticks_per_completed_request": ticks / completed,
        "host_cpu_nonidle_fraction": (total-delta[3]-delta[4]) / total,
        "host_cpu_iowait_fraction": delta[4] / total,
        "host_cpu_steal_fraction": delta[7] / total,
        "cpu_psi_some_fraction": (
            int(b["/proc/pressure/cpu"].splitlines()[0].split("total=")[1]) -
            int(a["/proc/pressure/cpu"].splitlines()[0].split("total=")[1])
        ) / 1e6 / elapsed,
        "throttled_usec_delta": bc["throttled_usec"] - ac["throttled_usec"],
        "nr_throttled_delta": bc["nr_throttled"] - ac["nr_throttled"],
        "cpu_max": a["/sys/fs/cgroup/cpu.max"],
        "driver_clock_tick_hz": None,
    }
    return row


out = {"notes": [
    "Resource CPU/allocation include all three servers and bounded window scrapes, not per-endpoint CPU.",
    "Host brackets include per-window warmup and observation; cgroup minus server CPU is not an exact driver CPU measure.",
    "Driver ticks are deliberately not converted to seconds because remote CLK_TCK was not captured.",
    "Stage wall time sums overlap, have different populations and are not CPU time or additive request latency.",
    "RPC total counters are exact call counts; duration histogram populations are sampled.",
    "Three per-window ratios are descriptive, not statistical confidence bounds.",
], "inputs": {}}
for name, path in INPUTS.items():
    raw = path.read_bytes()
    report = json.loads(raw)
    windows = [resource(report, i, w) for i, w in enumerate(report["windows"])]
    n = sum(w["completed"] for w in windows)
    cpu = sum(w["server_cpu_seconds"] for w in windows)
    alloc = sum(w["server_allocated_bytes"] for w in windows)
    ticks = sum(w["host"]["driver_cpu_ticks"] for w in windows)
    stage_sums, stage_counts = defaultdict(float), defaultdict(float)
    calls = defaultdict(float)
    for w in windows:
        for key, value in w["stages"].items():
            stage_sums[key] += value["sum"]
            stage_counts[key] += value["count"]
        for key, value in w["rpc_client_exact_calls"].items():
            calls[key] += value
    out["inputs"][name] = {
        "path": str(path), "sha256": hashlib.sha256(raw).hexdigest(),
        "identity": {k: report[k] for k in ["product_sha", "harness_sha", "harness_dirty", "binary_sha256", "profile_sha256", "driver_gomaxprocs", "node_gomaxprocs"]},
        "windows": windows,
        "aggregate": {
            "completed": n, "server_cpu_seconds": cpu, "server_cpu_ms_per_completed_mixed_request": cpu * 1000 / n,
            "server_allocated_bytes": alloc, "server_allocated_bytes_per_completed_mixed_request": alloc / n,
            "driver_cpu_ticks": ticks, "driver_cpu_ticks_per_completed_request": ticks / n,
            "stages": {k: {"count": stage_counts[k], "sum_seconds": stage_sums[k], "mean_ms": stage_sums[k] * 1000 / stage_counts[k]} for k in stage_counts},
            "rpc_client_exact_calls": dict(calls),
            "rpc_client_calls_per_completed_request": {k: v/n for k,v in calls.items()},
        },
        "profile_window": {
            "start": report["profile_window"]["start"], "end": report["profile_window"]["end"],
            "cpu_seconds": report["profile_window"]["cpu_seconds"],
            "allocated_bytes": report["profile_window"]["allocated_bytes"],
            "phases": [{k: p[k] for k in ["case", "completed", "actual_qps", "p99_ms", "driver_wait_p99_ms", "request_p99_ms", "dropped", "verdict"]} for p in report["profile_window"]["phases"]],
        },
    }
base, cand = (out["inputs"][k]["aggregate"] for k in ["reverse_beta24", "reverse_candidate"])
out["matched_candidate_delta_percent"] = {
    key: (cand[key]/base[key]-1)*100 for key in [
        "server_cpu_ms_per_completed_mixed_request",
        "server_allocated_bytes_per_completed_mixed_request",
        "driver_cpu_ticks_per_completed_request",
    ]}
OUT.mkdir(exist_ok=True)
(OUT/"reverse-cost-data.json").write_text(json.dumps(out, indent=2) + "\n")
for name, result in out["inputs"].items():
    print(name, json.dumps(result["aggregate"], indent=2))
print("delta", out["matched_candidate_delta_percent"])
