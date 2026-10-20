#!/usr/bin/env python3
"""Independently audit archived S/O/D/D/O/S receipts, without running binaries.

This script imports no repository validator. Missing values remain null/issues.
Window P99 medians are explicitly not pooled-request P99. Numeric rejection and
invalid evidence are separate; no automatic causal or release verdict is made.
"""
import argparse
import csv
from datetime import datetime
import hashlib
import json
import math
from pathlib import Path
import re
import statistics

PRODUCT = "ef798352c40a94e57f768f76b2f4713712f827fe"
BINARY = "8c3c79f2853b76be7f83dbd606404bf6ab1fdf34ce9370e82c056b389d03c57d"
PROFILE = "1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1"
BLOCKS = ["01-S", "02-O", "03-D", "04-D", "05-O", "06-S"]
MODES = ["shared", "overlap", "disjoint", "disjoint", "overlap", "shared"]
ENDPOINTS = ["/conversation/list", "/conversation/sync"]
METRICS = ["total_p99_ms", "driver_wait_p99_ms", "request_p99_ms",
           "producer_p99_ms", "queued_worker_p99_ms", "actual_qps"]
NS = 1_000_000_000


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def numeric(value):
    return isinstance(value, (float, int)) and not isinstance(value, bool) and math.isfinite(value)


def ratio(a, b):
    return a / b if numeric(a) and numeric(b) and b > 0 else None


def median(values):
    return statistics.median(values) if values and all(numeric(v) for v in values) else None


def date(value):
    dt = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if dt.tzinfo is None:
        raise ValueError("timezone missing")
    return dt


def affinity(value):
    if not isinstance(value, str) or not value or len(value) > 4096:
        raise ValueError("missing affinity")
    result = []
    for section in value.split(","):
        limits = section.split("-")
        if len(limits) not in (1, 2) or any(not re.fullmatch(r"[0-9]+", v) for v in limits):
            raise ValueError("invalid CPU range")
        first, last = int(limits[0]), int(limits[-1])
        if not 0 <= first <= last < 1024:
            raise ValueError("CPU range outside bound")
        result.extend(range(first, last + 1))
    if len(set(result)) != len(result):
        raise ValueError("duplicated CPU")
    return sorted(result)


class Audit:
    def __init__(self):
        self.issues = []

    def check(self, condition, where, message):
        if not condition:
            self.issues.append({"where": where, "issue": message})
        return bool(condition)

    def read(self, path):
        try:
            return json.loads(path.read_text(), parse_constant=lambda x: (_ for _ in ()).throw(ValueError(x)))
        except (OSError, ValueError) as e:
            self.check(False, str(path), str(e))
            return {}


def parse_schedstat(file):
    if file.get("error") or file.get("truncated"):
        raise ValueError("schedstat source unavailable")
    parts = file["content"].split()
    if len(parts) != 3 or any(not re.fullmatch(r"[0-9]+", v) for v in parts):
        raise ValueError("schedstat must have three unsigned counters")
    return dict(zip(["runtime_ns", "runqueue_wait_ns", "timeslices"], map(int, parts)))


def parse_stat(file):
    if file.get("error") or file.get("truncated"):
        raise ValueError("stat source unavailable")
    raw = file["content"]
    opening, ending = raw.index(" ("), raw.rindex(") ")
    fields = raw[ending + 2:].split()
    return {"pid": int(raw[:opening]), "starttime_ticks": int(fields[19]),
            "user_ticks": int(fields[11]), "system_ticks": int(fields[12]),
            "minor_faults": int(fields[7]), "major_faults": int(fields[9])}


def parse_status_affinity(file):
    if file.get("error") or file.get("truncated"):
        raise ValueError("status source unavailable")
    values = [line.split(":", 1)[1].strip() for line in file["content"].splitlines()
              if line.startswith("Cpus_allowed_list:")]
    if len(values) != 1:
        raise ValueError("status affinity absent/duplicated")
    return affinity(values[0])


def parse_counter_file(file):
    """Parse monotone totals, preserving each scope and never summing ancestors."""
    if file.get("error") or file.get("truncated"):
        raise ValueError(file.get("error", "truncated"))
    name = Path(file["path"]).name
    output = {}
    for line in file.get("content", "").splitlines():
        parts = line.split()
        if name.endswith("pressure") or "/pressure/" in file["path"]:
            if parts and parts[0] in ("some", "full"):
                for item in parts[1:]:
                    key, value = item.split("=", 1)
                    if key == "total":
                        output[parts[0] + "_total_usec"] = int(value)
        elif name == "cpu.stat":
            if len(parts) == 2:
                output[parts[0]] = int(parts[1])
        elif file["path"] == "/proc/stat" and parts and parts[0] == "cpu":
            keys = ["user_ticks", "nice_ticks", "system_ticks", "idle_ticks", "iowait_ticks",
                    "irq_ticks", "softirq_ticks", "steal_ticks", "guest_ticks", "guest_nice_ticks"]
            output = dict(zip(keys, map(int, parts[1:])))
    if not output:
        raise ValueError("counter source empty or unknown")
    return output


def delta(before, after):
    if before.keys() != after.keys():
        raise ValueError("counter fields changed")
    values = {k: after[k] - before[k] for k in before}
    if any(v < 0 for v in values.values()):
        raise ValueError("counter regressed")
    return values


def source_counters(audit, points, where):
    observed = {}
    for point in points:
        files = [(f["path"], f) for f in point.get("host", [])]
        for directory, entries in point.get("cgroups", {}).items():
            files.extend((f["path"], f) for f in entries)
        for path, file in files:
            row = {"slot": point["slot"], "offset_ns": point["start_offset_ns"], "values": None}
            try:
                row["values"] = parse_counter_file(file)
            except (ValueError, KeyError, TypeError) as e:
                row["error"] = str(e)
                audit.check(False, where + ":" + path, str(e))
            observed.setdefault(path, []).append(row)
    result = {}
    for path, rows in observed.items():
        intervals = []
        for first, last in zip(rows, rows[1:]):
            d = None
            if first["values"] is not None and last["values"] is not None:
                try:
                    d = delta(first["values"], last["values"])
                except ValueError as e:
                    audit.check(False, where + ":" + path, str(e))
            intervals.append({"from_slot": first["slot"], "to_slot": last["slot"],
                              "interval_ns": last["offset_ns"] - first["offset_ns"], "delta": d})
        full_delta = None
        if len(rows) == len(points) and rows[0]["values"] is not None and rows[-1]["values"] is not None:
            try:
                full_delta = delta(rows[0]["values"], rows[-1]["values"])
            except ValueError as e:
                audit.check(False, where + ":" + path, str(e))
        audit.check(len(rows) == len(points), where + ":" + path, "counter scope missing at some sample points")
        result[path] = {"points": len(rows), "valid_points": sum(r["values"] is not None for r in rows),
                        "first_offset_ns": rows[0]["offset_ns"], "last_offset_ns": rows[-1]["offset_ns"],
                        "first_to_last_delta": full_delta, "intervals": intervals}
    return result


def summarize_samples(audit, samples, control, placement, where):
    points = samples.get("points", [])
    n = len(points)
    audit.check(samples.get("schema") == "wukongim/cpu-window-samples/v1" and samples.get("valid") is True,
                where, "invalid sampler schema/control")
    audit.check(60 <= n <= 70 and samples.get("duration_ns") == 60 * NS
                and 59_995_000_000 <= samples.get("finished_offset_ns", -1) <= 75 * NS
                and samples.get("stop_reason") == "caller_stop" and samples.get("missed_slots") == 0,
                where, "original window coverage incomplete")
    roles = {}
    identities = [control["driver"], *control["nodes"]]
    groups = set().union(*(p["cgroup_v2"]["visible_ancestor_controls"].keys() for p in identities))
    audit.check(bool(groups), where, "resolved visible cgroup ancestors missing")
    prior = [None] * 4
    previous_point_end = 0
    elapsed_finish = samples.get("finished_offset_ns")
    for slot, point in enumerate(points):
        loc = f"{where}/slot{slot}"
        start, duration = point.get("start_offset_ns", -1), point.get("read_duration_ns", -1)
        audit.check(point.get("valid") is True and point.get("slot") == slot
                    and slot * NS <= start < (slot + 1) * NS and duration >= 0
                    and point.get("deadline_lag_ns") == start - slot * NS
                    and start >= previous_point_end and start + duration <= elapsed_finish
                    and point.get("missed_before") == 0, loc, "invalid point timing/coverage")
        previous_point_end = start + duration
        audit.check(set(point.get("cgroups", {})) == groups
                    and {f["path"] for f in point.get("host", [])} == {"/proc/stat", "/proc/pressure/cpu"},
                    loc, "resolved cgroup/host scope incomplete")
        for group, files in point.get("cgroups", {}).items():
            audit.check(len(files) == 2 and {f["path"] for f in files} ==
                        {group.rstrip("/") + "/cpu.stat", group.rstrip("/") + "/cpu.pressure"},
                        loc, "cgroup sample files incomplete")
        processes = point.get("processes", [])
        audit.check(len(processes) == 4, loc, "requires driver plus three nodes")
        for index, process in enumerate(processes):
            if index >= 4:
                break
            role = "driver" if index == 0 else f"node{index}"
            ploc = loc + "/" + role
            expected = process.get("expected", {})
            identity = {key: identities[index][key] for key in ("pid", "starttime_ticks")}
            mask = placement["driver_cpus"] if index == 0 else placement["node_cpus"]
            audit.check(process.get("valid") is True and expected.get("role") == role and
                        all(expected.get(k) == v and process.get("stat", {}).get(k) == v for k, v in identity.items()),
                        ploc, "invalid process identity/role/control")
            audit.check(affinity(expected.get("affinity")) == mask and affinity(process.get("affinity")) == mask,
                        ploc, "process mask differs from applied plan")
            audit.check(parse_status_affinity(process["status"]) == mask,
                        ploc, "raw process status affinity differs")
            audit.check(not process["cgroup_membership"].get("error")
                        and process["cgroup_membership"].get("content") == identities[index]["proc_cgroup"].get("content"),
                        ploc, "process cgroup membership changed or unavailable")
            raw = parse_stat(process["stat_file"])
            audit.check(raw == process.get("stat"), ploc, "parsed process stat differs from raw source")
            final = parse_stat(process["stat_after_threads"])
            audit.check(all(final[k] == identity[k] for k in identity), ploc, "process changed during sample")
            threads = process.get("threads", [])
            audit.check(1 <= len(threads) <= 256 and process.get("enumerated_threads") == len(threads)
                        and process.get("valid_threads") == len(threads), ploc, "incomplete task inventory")
            current = {}
            old = prior[index] or {}
            sums = {"runtime_ns": 0, "runqueue_wait_ns": 0, "timeslices": 0}
            matched = 0
            for thread in threads:
                key = (thread["tid"], thread["starttime_ticks"])
                audit.check(key not in current and thread.get("valid") is True
                            and affinity(thread.get("affinity")) == mask, ploc, "invalid task identity or mask")
                audit.check(parse_status_affinity(thread["status"]) == mask, ploc, "raw task status affinity differs")
                raw_first, raw_last = parse_stat(thread["stat_before"]), parse_stat(thread["stat_after"])
                audit.check((raw_first["pid"], raw_first["starttime_ticks"]) == key
                            and (raw_last["pid"], raw_last["starttime_ticks"]) == key,
                            ploc, "task lifetime changed within read")
                parsed = parse_schedstat(thread["schedstat_file"])
                audit.check(parsed == thread.get("schedstat"), ploc, "parsed task schedstat differs from raw source")
                current[key] = parsed
                if key in old:
                    change = delta(old[key], parsed)
                    audit.check(change == thread.get("matched_previous_delta"), ploc,
                                "stored matched task delta differs from independent raw subtraction")
                    for name, value in change.items():
                        sums[name] += value
                    matched += 1
                else:
                    audit.check("matched_previous_delta" not in thread, ploc, "new task has previous delta")
            audit.check(process.get("matched_threads") == matched, ploc, "matched task count mismatch")
            new = {f"{key[0]}:{key[1]}" for key in current.keys() - old.keys()}
            exited = {f"{key[0]}:{key[1]}" for key in old.keys() - current.keys()}
            report_new, report_exited = process.get("new_threads", []), process.get("exited_or_unobserved_threads", [])
            audit.check(set(report_new) == new and set(report_exited) == exited,
                        ploc, "task birth/exit coverage counts mismatch")
            data = roles.setdefault(role, {"pid": identity["pid"], "starttime_ticks": identity["starttime_ticks"],
                "samples": [], "matched_runtime_ns": 0, "matched_runqueue_wait_ns": 0, "matched_timeslices": 0,
                "matched_task_intervals": 0, "union_task_intervals": 0, "new_after_first": 0,
                "exited_or_unobserved": 0, "initial_threads": len(threads)})
            if slot:
                data["matched_task_intervals"] += matched
                data["union_task_intervals"] += len(current.keys() | old.keys())
                data["new_after_first"] += len(new)
                data["exited_or_unobserved"] += len(exited)
            data["matched_runtime_ns"] += sums["runtime_ns"]
            data["matched_runqueue_wait_ns"] += sums["runqueue_wait_ns"]
            data["matched_timeslices"] += sums["timeslices"]
            data["samples"].append({"slot": slot, "offset_ns": start, "valid_threads": len(threads),
                "matched_threads": matched, "new_threads": report_new, "exited_or_unobserved_threads": report_exited,
                "matched_runtime_ns": sums["runtime_ns"] if slot else None,
                "matched_runqueue_wait_ns": sums["runqueue_wait_ns"] if slot else None,
                "matched_timeslices": sums["timeslices"] if slot else None,
                "process_user_ticks": raw["user_ticks"], "process_system_ticks": raw["system_ticks"]})
            prior[index] = current
    hz = control.get("clock_tick_hz")
    audit.check(numeric(hz) and hz > 0, where, "missing process CPU clock tick rate")
    for role, data in roles.items():
        first, last = data["samples"][0], data["samples"][-1]
        ticks = (last["process_user_ticks"] + last["process_system_ticks"] -
                 first["process_user_ticks"] - first["process_system_ticks"])
        audit.check(ticks >= 0, where + "/" + role, "process CPU ticks regressed")
        data["sampled_elapsed_ns"] = last["offset_ns"] - first["offset_ns"]
        data["process_cpu_ticks_delta"] = ticks
        data["process_cpu_seconds_sampled_span"] = ratio(ticks, hz)
        data["matched_runtime_seconds"] = data["matched_runtime_ns"] / NS
        data["matched_runqueue_wait_seconds"] = data["matched_runqueue_wait_ns"] / NS
        data["matched_over_union_task_interval_ratio"] = ratio(data["matched_task_intervals"], data["union_task_intervals"])
        data["coverage_note"] = "Count coverage, not wall-time coverage; unmatched birth/exit edges and before-first/after-last intervals are unknown, never zero."
    point_cpu = sum(p["sampler_cpu_end_ns"] - p["sampler_cpu_start_ns"] for p in points)
    read_ns = sum(p["read_duration_ns"] for p in points)
    overhead = {k: samples.get(k) for k in ("sampler_tid", "sampler_cpu_ns", "read_bytes",
        "conservative_receipt_bytes", "missed_slots", "clock", "sched_schedstats")}
    overhead.update({"point_read_cpu_ns": point_cpu, "sum_read_wall_ns": read_ns,
        "max_read_wall_ns": max((p["read_duration_ns"] for p in points), default=None),
        "sampler_cpu_fraction_of_one_cpu_window": ratio(samples.get("sampler_cpu_ns"), 60 * NS),
        "read_wall_fraction_of_window": ratio(read_ns, 60 * NS),
        "note": "Sampler CPU is already included in driver CPU; do not subtract or double-count without a separate sensitivity analysis."})
    audit.check(numeric(overhead["sampler_cpu_ns"]) and overhead["sampler_cpu_ns"] >= point_cpu >= 0,
                where, "sampler CPU accounting invalid")
    audit.check(numeric(overhead["read_bytes"]) and 0 <= overhead["read_bytes"] <= 64 << 20
                and 0 < overhead["conservative_receipt_bytes"] <= 64 << 20, where, "sampler byte bounds invalid")
    return {"points": n, "first_offset_ns": points[0]["start_offset_ns"] if points else None,
            "last_offset_ns": points[-1]["start_offset_ns"] if points else None,
            "finished_offset_ns": elapsed_finish, "roles": roles, "overhead": overhead,
            "scoped_counter_deltas": source_counters(audit, points, where)}


def phase_summary(audit, phase, endpoint, profile, where):
    qps = 200 if endpoint.endswith("/list") else 60
    case = phase.get("case", {})
    audit.check(case == {"endpoint": endpoint, "page_size": 100, "offered_qps": qps}
                and phase.get("nodes") == 3 and phase.get("duration_seconds") == 60
                and phase.get("driver_workers") == 8
                and phase.get("queue_capacity") == max(8, math.ceil(qps * profile["max_p99_ms"] / 1000)),
                where, "workload or topology changed")
    integers = ["scheduled", "completed", "completed_in_window", "errors", "unexpected_errors", "dropped"]
    audit.check(all(isinstance(phase.get(k), int) and not isinstance(phase[k], bool) and phase[k] >= 0 for k in integers),
                where, "invalid request counts")
    scheduled, completed, errors, dropped = (phase[k] for k in ("scheduled", "completed", "errors", "dropped"))
    audit.check(scheduled == qps * 60 and completed + errors + dropped == scheduled
                and 0 <= phase["completed_in_window"] <= completed, where, "arrival accounting incomplete")
    audit.check(phase["unexpected_errors"] == 0, where, "unexpected request errors")
    audit.check(abs(phase["actual_qps"] - phase["completed_in_window"] / 60) < .001, where, "QPS arithmetic mismatch")
    timing = phase.get("driver_timing", {})
    audit.check(timing.get("producer_arrivals") == scheduled and timing.get("observed") == completed + errors
                and timing.get("dropped_arrivals") == dropped, where, "driver timing populations incomplete")
    timeline = phase.get("arrival_timeline", [])
    audit.check(len(timeline) == 60 and sum(v.get("completed", 0) for v in timeline) == completed + errors
                and sum(v.get("dropped", 0) for v in timeline) == dropped, where, "arrival timeline accounting incomplete")
    result = {k: phase.get(k) for k in integers + ["actual_qps", "verdict", "error_samples"]}
    result.update({"endpoint": endpoint, "offered_qps": qps, "total_p99_ms": phase.get("p99_ms"),
        "request_p99_ms": phase.get("request_p99_ms"), "driver_wait_p99_ms": phase.get("driver_wait_p99_ms"),
        "producer_p99_ms": timing.get("producer_p99_ms"), "queued_worker_p99_ms": timing.get("queued_worker_p99_ms"),
        "arrival_timeline_by_scheduled_second": timeline, "slow_arrivals": phase.get("slow_arrivals", []),
        "timing_population": "total/request/driver-wait and queued-worker: attempted requests including errors; producer: all scheduled including dropped"})
    audit.check(all(numeric(result[k]) and result[k] >= 0 for k in METRICS), where, "invalid latency/QPS metric")
    numeric_pass = (completed == scheduled and errors == dropped == 0
                    and phase["actual_qps"] >= qps * profile["min_completion_ratio"]
                    and 0 < phase["p99_ms"] <= profile["max_p99_ms"])
    audit.check((phase.get("verdict") == "pass") == numeric_pass, where, "numeric verdict disagrees with recomputation")
    result["numeric_pass_recomputed"] = numeric_pass
    return result


def validate_placement(audit, receipt, control, mode, host_cpus, where):
    audit.check(receipt.get("valid") is True and receipt.get("mode") == mode
                and receipt.get("host_cpus") == host_cpus and 1 <= receipt.get("attempts", 0) <= 8
                and 0 <= receipt.get("duration_ns", -1) <= 500_000_000, where, "placement bounded control invalid")
    rows = receipt["topology"]
    audit.check(sorted(r["cpu"] for r in rows) == host_cpus and len(host_cpus) == len(set(host_cpus)) == 4
                and len({r["package_id"] for r in rows}) == 1 and len({r["numa_node"] for r in rows}) == 1
                and all(min(r["package_id"], r["numa_node"], r["core_id"]) >= 0 for r in rows),
                where, "placement host topology invalid")
    cores = {}
    for row in rows:
        cores.setdefault((row["package_id"], row["core_id"]), []).append(row["cpu"])
    audit.check(len(cores) == 2 and all(len(v) == 2 for v in cores.values()), where, "requires SMT2 on two cores")
    for row in rows:
        audit.check(sorted(row["thread_siblings"]) == sorted(cores[(row["package_id"], row["core_id"])]),
                    where, "inconsistent sibling topology")
    first = min(rows, key=lambda row: row["cpu"])
    sibling = next(cpu for cpu in first["thread_siblings"] if cpu != first["cpu"])
    driver = host_cpus if mode == "S" else [first["cpu"] if mode == "O" else sibling]
    nodes = host_cpus if mode == "S" else [cpu for cpu in host_cpus if cpu != sibling]
    audit.check(receipt["driver_cpus"] == driver and receipt["node_cpus"] == nodes, where, "wrong O/D/S assignment")
    audit.check(date(control["released_at"]) <= date(receipt["started_at"]) <= date(receipt["finished_at"]),
                where, "placement was not after common preparation release")
    identities = [control["driver"], *control["nodes"]]
    audit.check(len(identities) == 4 and len({p["pid"] for p in identities}) == 4, where, "invalid process identity set")
    for stage in ("initial", "applied"):
        processes = receipt[stage]
        audit.check(len(processes) == 4, where, "incomplete placement inventory")
        for index, process in enumerate(processes):
            audit.check(all(process[k] == identities[index][k] for k in ("pid", "starttime_ticks")),
                        where, "placement process lifetime mismatch")
            mask = host_cpus if stage == "initial" else driver if index == 0 else nodes
            threads = process["threads"]
            audit.check(1 <= len(threads) <= 128 and len({t["tid"] for t in threads}) == len(threads)
                        and any(t["tid"] == process["pid"] and t["starttime_ticks"] == process["starttime_ticks"] for t in threads)
                        and all(t["tid"] > 0 and t["starttime_ticks"] > 0 and t["affinity"] == mask for t in threads),
                        where, "placement missing/wrong task masks or lifetimes")
    return rows


def summarize_report(audit, report, block, mode, harness, profile, host_cpus):
    where = block
    start_issues = len(audit.issues)
    expected = {"schema": "wukongim/conversation-cpu-placement/v1", "complete": True,
                "product_sha": PRODUCT, "harness_sha": harness, "harness_dirty": False,
                "binary_sha256": BINARY, "profile_sha256": PROFILE, "node_gomaxprocs": 2,
                "driver_gomaxprocs": 4 if mode == "S" else 1, "config": profile["stress_gate"]}
    for key, want in expected.items():
        audit.check(report.get(key) == want, where, "wrong report " + key)
    control, placement = report["fixed_environment"], report["cpu_placement"]
    audit.check(control.get("schema") == "wukongim/conversation-cpu-placement-preparation/v1"
                and control.get("valid") is True and control.get("target_age_seconds") == 180
                and 0 <= control.get("preparation_age_seconds", -1) <= 180
                and 180 <= control.get("release_age_seconds", 0) < math.inf,
                where, "common preparation age control invalid")
    topology = validate_placement(audit, placement, control, mode, host_cpus, where)
    for node in control["nodes"]:
        audit.check(node.get("binary_sha256") == BINARY, where, "running node product bytes differ")
    windows = report.get("windows", [])
    audit.check(len(windows) == 3, where, "three original windows required")
    result = {"block": block, "mode": mode, "windows": [], "topology": topology,
              "placement": placement, "release_age_seconds": control.get("release_age_seconds")}
    previous_end = None
    for index, window in enumerate(windows, 1):
        loc = f"{where}/window{index}"
        begin, end = date(window["start"]), date(window["end"])
        audit.check((end - begin).total_seconds() == 60 and window.get("name") == "mixed"
                    and begin >= date(placement["finished_at"])
                    and (previous_end is None or begin >= previous_end), loc, "wrong/reordered shared window")
        previous_end = end
        audit.check(all(window.get(k) == 0 for k in ("runtime_loads", "membership_writes",
            "active_runtimes_before", "active_runtimes_after")), loc, "runtime/membership mutation")
        phases = window["phases"]
        audit.check(len(phases) == 2, loc, "two endpoint phases required")
        summaries = [phase_summary(audit, p, ep, profile, loc + ep) for p, ep in zip(phases, ENDPOINTS)]
        completed = sum(p["completed"] for p in summaries)
        samples = window["cpu_samples"]
        audit.check(samples.get("start") == window["start"], loc, "sampler not bound to original window")
        sampling = summarize_samples(audit, samples, control, placement, loc)
        cpu, allocated = window.get("cpu_seconds"), window.get("allocated_bytes")
        audit.check(numeric(cpu) and cpu > 0 and numeric(allocated) and allocated > 0, loc, "invalid resource metrics")
        allocation_pass = ratio(allocated, completed) is not None and allocated / completed <= profile["stress_gate"]["max_allocated_bytes_per_request"]["mixed"]
        result["windows"].append({"index": index, "start": window["start"], "end": window["end"],
            "phases": summaries, "cpu_seconds_product_metrics": cpu, "allocated_bytes_product_metrics": allocated,
            "cpu_seconds_per_completed_request": ratio(cpu, completed),
            "allocated_bytes_per_completed_request": ratio(allocated, completed),
            "completed": completed, "list_completion_share": ratio(summaries[0]["completed"], completed),
            "numeric_pass_recomputed": all(p["numeric_pass_recomputed"] for p in summaries) and allocation_pass,
            "allocation_pass_recomputed": allocation_pass, "sampling": sampling})
    result["control_and_evidence_valid"] = len(audit.issues) == start_issues
    result["numeric_all_windows_pass"] = len(result["windows"]) == 3 and all(w["numeric_pass_recomputed"] for w in result["windows"])
    result["endpoint_window_medians"] = {}
    for endpoint in ENDPOINTS:
        phases = [next(p for p in w["phases"] if p["endpoint"] == endpoint) for w in result["windows"]]
        result["endpoint_window_medians"][endpoint] = {key: median([p[key] for p in phases]) for key in METRICS}
    result["median_cpu_seconds_per_completed_request"] = median([w["cpu_seconds_per_completed_request"] for w in result["windows"]])
    return result


def paired_screen(blocks):
    output = []
    for overlap, disjoint in (("02-O", "03-D"), ("05-O", "04-D")):
        if overlap not in blocks or disjoint not in blocks:
            output.append({"O": overlap, "D": disjoint, "evaluated": False})
            continue
        one = {"O": overlap, "D": disjoint, "endpoints": {}, "evaluated": True}
        a, b = blocks[overlap], blocks[disjoint]
        no_loss = all(p["dropped"] == p["errors"] == 0 for block in (a, b) for w in block["windows"] for p in w["phases"])
        one["all_windows_error_and_drop_free"] = no_loss
        one["controls_valid"] = a["control_and_evidence_valid"] and b["control_and_evidence_valid"]
        for endpoint in ENDPOINTS:
            before, after = a["endpoint_window_medians"][endpoint], b["endpoint_window_medians"][endpoint]
            wait_a, wait_b = before["driver_wait_p99_ms"], after["driver_wait_p99_ms"]
            absolute = wait_a - wait_b if numeric(wait_a) and numeric(wait_b) else None
            relative = ratio(absolute, wait_a)
            total_decreased = numeric(before["total_p99_ms"]) and numeric(after["total_p99_ms"]) and after["total_p99_ms"] < before["total_p99_ms"]
            one["endpoints"][endpoint] = {"O_medians": before, "D_medians": after,
                "driver_wait_decrease_ms": absolute, "driver_wait_decrease_fraction": relative,
                "total_p99_decreased": total_decreased,
                "arithmetic_screen_met": absolute is not None and absolute >= 1 and relative is not None and relative >= .25 and total_decreased}
        one["screen_interpretation_allowed"] = one["controls_valid"] and no_loss
        one["arithmetic_screen_all_endpoints"] = all(p["arithmetic_screen_met"] for p in one["endpoints"].values())
        output.append(one)
    return output


def repeats(blocks):
    output = []
    for first, last in (("01-S", "06-S"), ("02-O", "05-O"), ("03-D", "04-D")):
        if first not in blocks or last not in blocks:
            continue
        row = {"first": first, "last": last, "metrics": {}}
        for endpoint in ENDPOINTS:
            values = {}
            for key in METRICS:
                a = blocks[first]["endpoint_window_medians"][endpoint][key]
                b = blocks[last]["endpoint_window_medians"][endpoint][key]
                values[key] = {"first_median": a, "last_median": b,
                              "absolute_difference": abs(b - a) if numeric(a) and numeric(b) else None,
                              "relative_absolute_difference_over_smaller": ratio(abs(b - a), min(a, b)) if numeric(a) and numeric(b) else None}
            row["metrics"][endpoint] = values
        row["cpu_per_completed_request"] = {k: blocks[v]["median_cpu_seconds_per_completed_request"] for k, v in (("first", first), ("last", last))}
        row["all_window_numeric_verdicts"] = {key: [w["numeric_pass_recomputed"] for w in blocks[key]["windows"]] for key in (first, last)}
        row["note"] = "Descriptive repeat variation only; no invented pass threshold or retrospective relaxation."
        output.append(row)
    return output


def emit_csv(path, rows):
    if not rows:
        return
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("artifact", type=Path, help="Extracted conversation-cpu-contention artifact root")
    parser.add_argument("--expected-harness-sha", required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    if not re.fullmatch("[0-9a-f]{40}", args.expected_harness_sha):
        parser.error("expected harness SHA must be exact")
    if args.output_dir.exists():
        parser.error("output directory must be new, to preserve prior evidence")
    args.output_dir.mkdir(parents=True)
    root, audit = args.artifact.resolve(), Audit()
    summary, plan = audit.read(root / "summary.json"), audit.read(root / "plan/sequence.json")
    audit.check(summary.get("complete") is True and summary.get("release_qualification") is False,
                "summary", "collection is incomplete or incorrectly claims release qualification")
    audit.check(plan.get("product_sha") == PRODUCT and plan.get("expected_binary_sha256") == BINARY
                and plan.get("harness_sha") == args.expected_harness_sha
                and [(v.get("id"), v.get("mode")) for v in plan.get("sequence", [])] == list(zip(BLOCKS, MODES)),
                "plan", "frozen product/harness/sequence mismatch")
    audit.check([b.get("id") for b in summary.get("blocks", [])] == BLOCKS
                and all(b.get("status") == "collected" and b.get("control_valid") is True for b in summary.get("blocks", [])),
                "summary", "missing/reordered/uncollected blocks")
    profile = audit.read(root / "harness/profile.json")
    for path, expected in ((root / "harness/profile.json", PROFILE), (root / "binaries/C/wukongim-e2e", BINARY)):
        audit.check(path.is_file() and digest(path) == expected, str(path), "frozen bytes differ or missing")
    harness_identity = audit.read(root / "harness/identity.json")
    driver = root / "harness/conversation_qps.test"
    audit.check(harness_identity.get("harness_sha") == args.expected_harness_sha
                and harness_identity.get("profile_sha256") == PROFILE
                and driver.is_file() and digest(driver) == harness_identity.get("driver_sha256"),
                "harness", "compiled driver identity missing/mismatched")
    host = audit.read(root / "host.json")
    results, hashes = {}, []
    topology = None
    for block in BLOCKS:
        path = root / "blocks" / block / "report.json"
        if path.exists():
            hashes.append({"path": str(path.relative_to(root)), "sha256": digest(path), "bytes": path.stat().st_size})
        report = audit.read(path)
        try:
            result = summarize_report(audit, report, block, block[-1], args.expected_harness_sha, profile, host["affinity"])
            if topology is not None:
                audit.check(topology == result["topology"], block, "topology changed across fixed sequence")
            topology = result["topology"]
            results[block] = result
            receipt = audit.read(root / "blocks" / block / "test.command.json")
            audit.check(receipt.get("exit_code") == 0 and receipt.get("cleanup_complete") is True
                        and not receipt.get("remaining_processes"), block, "test process failure or leak")
        except (KeyError, TypeError, ValueError, ZeroDivisionError, StopIteration) as e:
            audit.check(False, block, f"malformed/incomplete report: {type(e).__name__}: {e}")
    paired = paired_screen(results)
    for pair in paired:
        pair["whole_collection_evidence_valid"] = not audit.issues and len(results) == 6
        pair["screen_interpretation_allowed"] = pair.get("screen_interpretation_allowed", False) and pair["whole_collection_evidence_valid"]
    output = {"schema": "wukongim/independent-cpu-contention-audit/v1", "artifact_root": str(root),
        "expected_product_sha": PRODUCT, "expected_binary_sha256": BINARY,
        "expected_harness_sha": args.expected_harness_sha, "profile_sha256": PROFILE,
        "evidence_valid": not audit.issues and len(results) == 6,
        "all_original_windows_numeric_pass": len(results) == 6 and all(b["numeric_all_windows_pass"] for b in results.values()),
        "issues": audit.issues, "input_reports": hashes, "blocks": results,
        "predeclared_pair_screen": paired, "repeat_variation": repeats(results),
        "limitations": [
            "This diagnostic does not rerun the 20-item release gate and does not qualify a new product.",
            "All medians are medians of three window P99s, never pooled-request P99.",
            "Pair screens are descriptive, not statistical significance, historical root-cause proof or a no-regression claim.",
            "O/D changes logical overlap and total CPU union 3 versus 4; D still shares an SMT physical core, cache, cgroups and hidden host.",
            "Same-mode repeats and S sentinels expose some drift, not all carryover or host interference.",
            "Missing task lifetime edges are unknown; matched task deltas cannot give total process off-CPU or goroutine waiting.",
            "Cgroup ancestors overlap: never sum their CPU/throttle/pressure counters as separate workloads.",
            "Arrival timeline is keyed to planned arrival second, sample intervals to observation offsets; maxima and bucket alignment do not prove per-request causality.",
            "Component quantiles are different populations and must not be added.",
            "Post-window pprof is excluded from original-window causal claims."
        ]}
    (args.output_dir / "audit.json").write_text(json.dumps(output, ensure_ascii=False, indent=2, allow_nan=False) + "\n")
    endpoint_rows, role_rows, window_rows = [], [], []
    for block, result in results.items():
        for window in result["windows"]:
            base = {"block": block, "window": window["index"]}
            window_rows.append(base | {k: window[k] for k in ("cpu_seconds_product_metrics", "allocated_bytes_product_metrics",
                "cpu_seconds_per_completed_request", "allocated_bytes_per_completed_request", "completed",
                "list_completion_share", "numeric_pass_recomputed")} | {
                    "sampler_cpu_ns": window["sampling"]["overhead"]["sampler_cpu_ns"],
                    "sampler_read_bytes": window["sampling"]["overhead"]["read_bytes"],
                    "sampler_read_wall_ns": window["sampling"]["overhead"]["sum_read_wall_ns"]})
            for phase in window["phases"]:
                endpoint_rows.append(base | {k: phase[k] for k in ["endpoint", *METRICS, "scheduled", "completed",
                    "completed_in_window", "errors", "unexpected_errors", "dropped", "numeric_pass_recomputed", "verdict"]})
            for role, item in window["sampling"]["roles"].items():
                role_rows.append(base | {"role": role} | {k: item[k] for k in ("pid", "process_cpu_seconds_sampled_span",
                    "matched_runtime_seconds", "matched_runqueue_wait_seconds", "matched_timeslices",
                    "matched_task_intervals", "union_task_intervals", "matched_over_union_task_interval_ratio",
                    "new_after_first", "exited_or_unobserved")})
    emit_csv(args.output_dir / "endpoints.csv", endpoint_rows)
    emit_csv(args.output_dir / "windows.csv", window_rows)
    emit_csv(args.output_dir / "roles.csv", role_rows)
    print(json.dumps({"evidence_valid": output["evidence_valid"], "issues": len(audit.issues),
                      "blocks": len(results), "numeric_all_windows_pass": output["all_original_windows_numeric_pass"],
                      "output": str(args.output_dir / "audit.json")}))
    return 0 if output["evidence_valid"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
