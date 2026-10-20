#!/usr/bin/env python3
"""Read fixed-host reports from a retained ZIP or directory; no workload or network call."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import statistics
import zipfile


def instant(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def midpoint(cut):
    return (instant(cut["started_at"]) + instant(cut["finished_at"])) / 2


def counters(text):
    return {k: int(v) for k, v in (line.split() for line in text.splitlines())}


def pressure(text):
    return {line.split()[0]: int(line.split("total=")[1])
            for line in text.splitlines()}


def diff(before, after):
    return {k: after[k] - before[k] for k in sorted(before.keys() & after.keys())}


def proc_stat(process):
    values = process["proc_stat"]["content"].rsplit(")", 1)[1].split()
    return {name: int(values[index]) for name, index in {
        "minor_faults": 7, "major_faults": 9, "user_ticks": 11, "system_ticks": 12,
    }.items()}


def metric_delta(window, family, selected=None):
    selected = selected or {}
    result = 0
    for a, b in zip(window["metrics_before"], window["metrics_after"]):
        for key, value in b.items():
            name, _, tail = key.partition("{")
            labels = dict(p.split("=", 1) for p in tail.rstrip("}").split(",") if "=" in p)
            if name == family and all(labels.get(k) == v for k, v in selected.items()):
                result += value - a.get(key, 0)
    return result


def stage(window, scope, name):
    family = "wukongim_conversation_read_stage_duration_seconds"
    labels = {"scope": scope, "stage": name, "result": "ok"}
    count = metric_delta(window, family + "_count", labels)
    seconds = metric_delta(window, family + "_sum", labels)
    return {"count": count, "sum_seconds": seconds,
            "mean_ms": seconds * 1000 / count if count else None}


def resource(before, after, tick_hz):
    elapsed = midpoint(after) - midpoint(before)
    processes = []
    for a, b in zip(before["processes"], after["processes"]):
        d = diff(proc_stat(a), proc_stat(b))
        d.update({"pid": a["pid"], "identity_stable":
                  (a["pid"], a["starttime_ticks"], a["cpu_affinity"], a["cgroup_v2"]["location"]) ==
                  (b["pid"], b["starttime_ticks"], b["cpu_affinity"], b["cgroup_v2"]["location"]),
                  "cpu_seconds": (d["user_ticks"] + d["system_ticks"]) / tick_hz})
        processes.append(d)
    # Verify the observed shared scope before counting it only once.
    for cut in (before, after):
        assert len(cut["processes"]) == 4
        assert all(p["cgroup_v2"]["location"] == cut["processes"][0]["cgroup_v2"]["location"]
                   for p in cut["processes"])
    a, b = (cut["processes"][0]["cgroup_v2"] for cut in (before, after))
    ancestors = {}
    for path, left in a["visible_ancestor_controls"].items():
        right = b["visible_ancestor_controls"][path]
        row = {"before": {}, "after": {}, "delta": {}}
        for name in ["cpu.max", "cpuset.cpus.effective", "memory.max", "memory.high",
                     "memory.current", "memory.swap.max"]:
            row["before"][name] = left[name]
            row["after"][name] = right[name]
        for name, parser in [("cpu.stat", counters), ("memory.events", counters),
                             ("cpu.pressure", pressure), ("io.pressure", pressure),
                             ("memory.pressure", pressure)]:
            if "error" in left[name] or "error" in right[name]:
                row["delta"][name] = {"available": False, "before": left[name], "after": right[name]}
                continue
            values = diff(parser(left[name]["content"]), parser(right[name]["content"]))
            row["delta"][name] = {"available": True, "values": values}
            if name.endswith(".pressure"):
                row["delta"][name]["percent_of_bracket"] = {
                    k: v / 1e6 / elapsed * 100 for k, v in values.items()}
        ancestors[path] = row
    return {"before_name": before["name"], "after_name": after["name"],
            "bracket_seconds": elapsed, "processes_driver_then_nodes": processes,
            "location": a["location"], "ancestors": ancestors}


def sample_key(sample):
    return sample["Name"] + json.dumps(sample["Labels"], sort_keys=True, separators=(",", ":"))


def boundary(cut):
    nodes = []
    for entry in cut["metrics"]:
        values = {s["Name"]: s["Value"] for s in entry["selected_samples"] if not s["Labels"]}
        gc_time = values.get("go_memstats_last_gc_time_seconds", 0)
        storage = {sample_key(s): s["Value"] for s in entry["selected_samples"]
                   if s["Name"].startswith("wukongim_storage_pebble_")}
        nodes.append({
            "node_id": entry["node_id"], "error": entry.get("error"),
            "scrape": entry["scrape"], "gc_count": values.get("go_gc_duration_seconds_count"),
            "gc_pause_seconds": values.get("go_gc_duration_seconds_sum"),
            "last_gc_time_seconds": gc_time,
            "last_gc_age_seconds_bracket": [
                instant(entry["scrape"][key]) - gc_time for key in ("started_at", "finished_at")
            ] if gc_time else None,
            "heap_alloc_bytes": values.get("go_memstats_heap_alloc_bytes"),
            "next_gc_bytes": values.get("go_memstats_next_gc_bytes"),
            "alloc_bytes_total": values.get("go_memstats_alloc_bytes_total"),
            "process_cpu_seconds_total": values.get("process_cpu_seconds_total"),
            "storage": storage,
        })
    return {"name": cut["name"], "nodes": nodes}


def boundary_deltas(cuts):
    result = []
    for before, after in zip(cuts, cuts[1:]):
        nodes = []
        for a, b in zip(before["nodes"], after["nodes"]):
            assert a["node_id"] == b["node_id"]
            delta = {key: b[key] - a[key] for key in (
                "gc_count", "gc_pause_seconds", "alloc_bytes_total", "process_cpu_seconds_total")}
            storage_delta = diff(a["storage"], b["storage"])
            nodes.append({"node_id": a["node_id"], "delta": delta,
                          "storage_matched_series": len(storage_delta),
                          "storage_only_before": sorted(a["storage"].keys() - b["storage"].keys()),
                          "storage_only_after": sorted(b["storage"].keys() - a["storage"].keys()),
                          "storage_signed_nonzero_differences": {k: v for k, v in storage_delta.items() if v}})
        result.append({"before": before["name"], "after": after["name"], "nodes": nodes})
    return result


def analyze(raw):
    report = json.loads(raw)
    fixed = report["fixed_environment"]
    cuts = [boundary(cut) for cut in fixed["boundaries"]]
    windows = []
    for i, window in enumerate(report["windows"]):
        phase_rows = []
        for phase in window["phases"]:
            phase_rows.append({
                "endpoint": phase["case"]["endpoint"],
                **{key: phase[key] for key in ("actual_qps", "completed", "completed_in_window",
                   "dropped", "errors", "p50_ms", "p99_ms", "driver_wait_p99_ms", "request_p99_ms")},
                "timeline": phase["arrival_timeline"],
                "slow_samples_first_at_most_16": phase.get("slow_arrivals", []),
            })
        window_gc = [{
            "node_id": node + 1,
            "count": b["go_gc_duration_seconds_count{}"] - a["go_gc_duration_seconds_count{}"],
            "pause_seconds": b["go_gc_duration_seconds_sum{}"] - a["go_gc_duration_seconds_sum{}"],
            "before_heap_alloc_bytes": a["go_memstats_heap_alloc_bytes{}"],
            "before_next_gc_bytes": a["go_memstats_next_gc_bytes{}"],
            "before_gc_count": a["go_gc_duration_seconds_count{}"],
        } for node, (a, b) in enumerate(zip(window["metrics_before"], window["metrics_after"]))]
        stages = {scope + "/" + name: stage(window, scope, name) for scope, name in (
            ("list", "handler"), ("list", "response"), ("sync", "handler"), ("sync", "response"),
            ("persisted_heads", "metadata"), ("persisted_heads", "heads"),
            ("persisted_heads", "edit_overlay"), ("edit_slot", "barrier"), ("edit_slot", "storage"))}
        host_before, host_after = report["host_snapshots"][2 * i:2 * i + 2]
        cpu = [[int(v) for v in h["/proc/stat"].splitlines()[0].split()[1:9]]
               for h in (host_before, host_after)]
        cpu_delta = [b - a for a, b in zip(*cpu)]
        windows.append({
            "window": i + 1, "start": window["start"], "end": window["end"],
            "phases": phase_rows, "gc": window_gc, "stages": stages,
            "server_cpu_seconds": window["cpu_seconds"],
            "server_allocated_bytes": window["allocated_bytes"],
            "runtime_loads": window["runtime_loads"], "membership_writes": window["membership_writes"],
            "host_cpu_nonidle_percent": (sum(cpu_delta) - cpu_delta[3] - cpu_delta[4]) / sum(cpu_delta) * 100,
            "host_cpu_iowait_percent": cpu_delta[4] / sum(cpu_delta) * 100,
            "host_steal_ticks": cpu_delta[7],
            "resources": resource(*fixed["window_resource_cuts"][2 * i:2 * i + 2], fixed["clock_tick_hz"]),
        })
    return {
        "raw_sha256": hashlib.sha256(raw).hexdigest(),
        "identity": {k: report[k] for k in ("harness_sha", "product_sha", "binary_sha256", "profile_sha256")},
        "control_valid": fixed["valid"], "stages": fixed["stages"],
        "preparation_age_seconds": fixed["preparation_age_seconds"],
        "release_age_seconds": fixed["release_age_seconds"],
        "wait_seconds": fixed["release_age_seconds"] - fixed["preparation_age_seconds"],
        "window_start_age_seconds": fixed["window_start_age_seconds"],
        "clock_tick_hz": fixed["clock_tick_hz"],
        "boundaries": cuts, "boundary_deltas": boundary_deltas(cuts), "windows": windows,
        "medians": {
            endpoint: {metric: statistics.median(p[metric] for w in windows for p in w["phases"]
                                                if p["endpoint"] == endpoint)
                       for metric in ("p99_ms", "driver_wait_p99_ms", "request_p99_ms")}
            for endpoint in ("/conversation/list", "/conversation/sync")},
        "server_cpu_total": sum(w["server_cpu_seconds"] for w in windows),
        "server_allocated_total": sum(w["server_allocated_bytes"] for w in windows),
    }


def summaries(blocks):
    before, after = blocks["04-B"], blocks["05-B"]
    assert before["identity"]["binary_sha256"] == after["identity"]["binary_sha256"]
    stages = {}
    for key in before["windows"][0]["stages"]:
        means = [sum(w["stages"][key]["sum_seconds"] for w in b["windows"]) * 1000 /
                 sum(w["stages"][key]["count"] for w in b["windows"])
                 for b in (before, after)]
        stages[key] = {"B4_mean_ms": means[0], "B5_mean_ms": means[1],
                       "B5_change_percent": (means[1] / means[0] - 1) * 100}
    b1 = blocks["01-B"]["windows"][2]
    late = {}
    for phase in b1["phases"]:
        bins = phase["timeline"]
        late[phase["endpoint"]] = {
            "seconds_0_to_54_max": {k: max(v[k] for v in bins[:55])
                                       for k in ("max_wait_ms", "max_request_ms", "max_latency_ms")},
            "seconds_55_to_59": [{"scheduled_second": i, **bins[i]} for i in range(55, 60)],
            "late_completions": phase["completed"] - phase["completed_in_window"],
        }
    return {"B4_B5": {"same_binary": True, "weighted_server_stage_means": stages,
                       "cpu_change_percent": (after["server_cpu_total"] / before["server_cpu_total"] - 1) * 100,
                       "allocation_change_percent": (after["server_allocated_total"] / before["server_allocated_total"] - 1) * 100},
            "B1_window3_late_timeline": late}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifact", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    source = Path(args.artifact)
    if source.is_dir():
        blocks = {path.parent.name: analyze(path.read_bytes())
                  for path in sorted(source.rglob("report.json"))
                  if path.parent.parent.name == "blocks"}
    else:
        with zipfile.ZipFile(source) as archive:
            blocks = {name.split("/")[-2]: analyze(archive.read(name))
                      for name in sorted(archive.namelist())
                      if "/blocks/" in name and name.endswith("/report.json")}
    assert set(blocks) == {"01-B", "02-B", "03-A", "04-B", "05-B", "06-A", "07-C"}
    output = {
        "schema": "wukongim/fixed-host-resource-analysis/v1", "artifact": args.artifact,
        "notes": [
            "ZIP input checks each selected JSON entry CRC; directory input reads the same report bytes; no workload or network call.",
            "Process and cgroup cuts include warmup and observation overhead.",
            "A shared cgroup is counted once, using the driver's read of its actual resolved ancestry.",
            "Boundary storage differences are signed state differences; only cumulative families represent work.",
            "CPU pressure and latency association is not proof of a unique causal mechanism.",
        ], "summaries": summaries(blocks), "blocks": blocks,
    }
    with open(args.output, "w") as out:
        json.dump(output, out, indent=2)
        out.write("\n")
    print(args.output)


if __name__ == "__main__":
    main()
