#!/usr/bin/env python3
"""Offline receipts/bytes audit. Never starts tests, products, or network requests."""
import argparse
import datetime as dt
import hashlib
import json
import math
from pathlib import Path
import re
import statistics
import subprocess

DEFAULT_H = "ef798352c40a94e57f768f76b2f4713712f827fe"
ROLES = {"A": "4d44b39dda6fdaa7dafb637ca6cfb718e9530f77",
         "B": "b469c6e5e2b4ff65611a8fe3389fb5034de3f152"}
ORDER = ["B", "B", "A", "B", "B", "A", "C"]
ENDPOINTS = ["/conversation/list", "/conversation/sync"]
INVARIANTS = ["runtime_loads", "membership_writes",
              "active_runtimes_before", "active_runtimes_after"]


def need(condition, message):
    if not condition:
        raise ValueError(message)


def number(value, name, minimum=0):
    need(type(value) in (int, float) and math.isfinite(value) and value >= minimum,
         f"invalid {name}: {value!r}")
    return value


def count(value, name):
    need(type(value) is int and value >= 0, f"invalid count {name}: {value!r}")
    return value


def stamp(value):
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    need(parsed.tzinfo is not None and parsed.year >= 2026, "invalid UTC timestamp")
    return parsed


def sha(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def read_json(path):
    need(path.stat().st_size <= 32 * 1024 * 1024, f"oversized JSON: {path}")
    return json.loads(path.read_text())


def cpus(value):
    result = set()
    for part in value.split(","):
        bounds = part.split("-")
        need(len(bounds) in (1, 2), "invalid affinity")
        first, last = int(bounds[0]), int(bounds[-1])
        need(0 <= first <= last and last - first <= 3, "invalid affinity range")
        for cpu in range(first, last + 1):
            need(cpu not in result, "duplicate CPU")
            result.add(cpu)
    need(len(result) == 4, "not four allowed CPUs")
    return sorted(result)


def command(path, test=None):
    receipt = read_json(path)
    need(receipt["exit_code"] == 0 and receipt["process_exit_code"] == 0,
         f"command did not succeed: {path}")
    need(receipt["timed_out"] is False and receipt["cleanup_complete"] is True
         and receipt["remaining_processes"] == [], f"timeout/leak: {path}")
    need(receipt["ended_unix"] >= receipt["started_unix"], "invalid command timing")
    if test:
        need(f"-test.run=^{test}$" in receipt["command"]
             and "-test.count=1" in receipt["command"], "wrong test selector/count")
    return receipt


def phase_result(phase, profile):
    """Recompute the actual mixed policy; a genuine numerical rejection is data."""
    cfg = profile["stress_gate"]
    endpoint = phase["case"]["endpoint"]
    need(endpoint in ENDPOINTS, "unknown endpoint")
    rate = cfg["mixed_list_qps" if endpoint == ENDPOINTS[0] else "mixed_sync_qps"]
    duration, workers = cfg["mixed_seconds"], cfg["mixed_workers_per_endpoint"]
    expected = {"case": {"endpoint": endpoint, "page_size": 100, "offered_qps": rate},
                "nodes": 3, "driver_workers": workers, "duration_seconds": duration,
                "queue_capacity": max(workers, math.ceil(rate * profile["max_p99_ms"] / 1000))}
    for key, value in expected.items():
        need(phase.get(key) == value, f"changed mixed workload: {key}")
    for key in ("scheduled", "completed", "completed_in_window", "errors", "unexpected_errors", "dropped"):
        count(phase[key], key)
    need(phase["scheduled"] == rate * duration, "changed arrival count")
    need(phase["completed"] + phase["errors"] + phase["dropped"] == phase["scheduled"],
         "completion/error/drop accounting mismatch")
    need(phase["completed_in_window"] <= phase["completed"], "impossible in-window count")
    need(phase["unexpected_errors"] <= phase["errors"], "unexpected error count exceeds errors")
    need(phase["unexpected_errors"] == 0, "unexpected error invalidates comparison")
    for key in ("actual_qps", "p50_ms", "p95_ms", "p99_ms"):
        number(phase[key], key)
    need(phase["p50_ms"] <= phase["p95_ms"] <= phase["p99_ms"], "unordered quantiles")
    need(math.isclose(phase["actual_qps"], phase["completed_in_window"] / duration,
                      rel_tol=0, abs_tol=1e-9), "QPS not derived from in-window completions")
    for key in INVARIANTS:
        need(number(phase.get(key), key) == 0, f"phase activation/mutation: {key}")
    need(phase.get("cpu_seconds") is None and phase["allocated_bytes"] == phase["heap_bytes"] == 0,
         "shared costs duplicated into an endpoint")
    reason = "pass"
    if phase["completed"] != phase["scheduled"] or phase["errors"] or phase["dropped"]:
        reason = "incomplete or failed requests"
    elif phase["actual_qps"] < rate * profile["min_completion_ratio"]:
        reason = "QPS below fixed floor"
    elif not 0 < phase["p99_ms"] <= profile["max_p99_ms"]:
        reason = "P99 exceeds budget"
    need(phase["verdict"] == reason, f"verdict disagrees with numbers: {phase['verdict']} vs {reason}")
    return reason == "pass"


def cgroup_static(process):
    group = process["cgroup_v2"]
    location = group["location"]
    controls = group["visible_ancestor_controls"]
    need(location["directory"] in controls and location["mount_point"] in controls,
         "missing actual leaf or visible root")
    result = {}
    for directory, files in controls.items():
        result[directory] = {}
        for key in ("cpu.max", "cpuset.cpus.effective", "cpuset.mems.effective",
                    "memory.max", "memory.high"):
            item = files[key]
            if directory != location["mount_point"] and key in ("cpu.max", "memory.max"):
                need(not item.get("error") and "content" in item, f"missing non-root {key}")
            result[directory][key] = (item.get("content"), item.get("error"))
        cpu_max = files["cpu.max"].get("content")
        if cpu_max:
            quota, period = cpu_max.split()
            need(int(period) > 0, "invalid quota period")
            need(quota == "max" or int(quota) / int(period) >= 4, "less than four CPU quota")
    return location, result


def fixed_control(fixed, report, driver_hash, binary_hash, affinity):
    need(fixed["schema"] == "wukongim/conversation-fixed-host/v1"
         and fixed["valid"] is True and not fixed.get("failure"), "invalid fixed control")
    need(fixed["target_age_seconds"] == 180, "wrong age target")
    need(0 <= number(fixed["preparation_age_seconds"], "preparation age") <= 180
         and number(fixed["release_age_seconds"], "release age") >= 180, "invalid age release")
    count(fixed["clock_tick_hz"], "clock tick")
    need(fixed["clock_tick_hz"] > 0, "missing clock rate")
    number(fixed["start_estimate_uncertainty_ns"], "clock uncertainty", 1)
    first, release = stamp(fixed["estimated_first_product_start"]), stamp(fixed["released_at"])
    need(abs((release - first).total_seconds() - fixed["release_age_seconds"]) <= 0.01,
         "UTC and monotonic release-age receipts disagree")
    names = ["resource_preflight", "startup", "process_identity", "readiness",
             "prepare", "eviction", "preparation_age_wait"]
    need([stage["name"] for stage in fixed["stages"]] == names, "missing/reordered setup stage")
    for stage in fixed["stages"]:
        need(stage["failed"] is False and stamp(stage["finished_at"]) >= stamp(stage["started_at"]),
             "failed/incomplete setup stage")
        number(stage["duration_ns"], "stage monotonic duration")
    processes = [fixed["driver"]] + fixed["nodes"]
    need(len(processes) == 4 and len({item["pid"] for item in processes}) == 4,
         "missing distinct driver/three node identities")
    statics = {}
    for index, process in enumerate(processes):
        need(cpus(process["cpu_affinity"]) == affinity, "CPU affinity mismatch")
        need(process["starttime_ticks"] > 0, "missing start ticks")
        need(process["binary_sha256"] == (driver_hash if index == 0 else binary_hash),
             "process executable digest mismatch")
        statics[process["pid"]] = cgroup_static(process)
        if index:
            need(re.fullmatch("[0-9a-f]{64}", process["config_sha256"]) is not None
                 and bool(process["redacted_toml"]), "missing configuration provenance")
    expected_cuts = [f"window_{i}_{when}" for i in (1, 2, 3)
                     for when in ("before_warmup", "after_measurement")]
    cuts = fixed["window_resource_cuts"]
    need([cut["name"] for cut in cuts] == expected_cuts, "missing/reordered resource cut")
    for cut in cuts:
        need(stamp(cut["finished_at"]) >= stamp(cut["started_at"]), "incomplete resource cut")
        need([p["pid"] for p in cut["processes"]] == [p["pid"] for p in processes],
             "changed resource-cut PID inventory")
        for original, current in zip(processes, cut["processes"]):
            need(original["starttime_ticks"] == current["starttime_ticks"]
                 and original["cpu_affinity"] == current["cpu_affinity"], "PID/affinity changed")
            need(cgroup_static(current) == statics[original["pid"]], "visible cgroup controls changed")
            for files in current["cgroup_v2"]["visible_ancestor_controls"].values():
                need("cpu.stat" in files and "cpu.pressure" in files, "missing resolved dynamic cut")
    boundaries = fixed["boundaries"]
    expected_boundaries = ["after_readiness_before_prepare", "after_prepare_before_eviction",
                           "after_eviction_before_age_wait", "after_age_wait_before_original_warmup"]
    need([item["name"] for item in boundaries] == expected_boundaries, "missing preparation boundary")
    for boundary in boundaries:
        need({item["node_id"] for item in boundary["metrics"]} == {1, 2, 3},
             "incomplete metrics-node inventory")
        for item in boundary["metrics"]:
            scrape = item["scrape"]
            need(not item.get("error") and scrape["status_code"] == 200
                 and scrape["requested_encoding"] == "identity", "failed scrape receipt")
            need(number(scrape["body_bytes"], "scrape bytes", 1) > 0
                 and re.fullmatch("[0-9a-f]{64}", scrape["body_sha256"]) is not None,
                 "missing scrape body identity")
            need(stamp(scrape["finished_at"]) >= stamp(scrape["started_at"])
                 and scrape["duration_ns"] >= 0, "invalid scrape timing")
    need(len(fixed["window_start_age_seconds"]) == 3, "missing actual window ages")
    for age, window in zip(fixed["window_start_age_seconds"], report["windows"]):
        actual = (stamp(window["start"]) - first).total_seconds()
        need(age >= fixed["release_age_seconds"] and abs(age - actual) <= 0.01,
             "actual window age missing/mismatched")
    return statics[fixed["driver"]["pid"]]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("artifact_root", type=Path, nargs="?")
    parser.add_argument("--candidate", default=DEFAULT_H)
    parser.add_argument("--expected-run-id", type=int, default=39453556254)
    parser.add_argument("--run-json", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    need(re.fullmatch("[0-9a-f]{40}", args.candidate) is not None, "exact candidate required")
    frozen = lambda path: subprocess.check_output(
        ["git", "show", f"{args.candidate}:{path}"], cwd=args.repo, timeout=30)
    profile_bytes = frozen("test/e2e/message/conversation_qps/profile.json")
    profile = json.loads(profile_bytes)
    profile_hash = hashlib.sha256(profile_bytes).hexdigest()
    if args.self_test:
        phase = {"case": {"endpoint": ENDPOINTS[0], "page_size": 100, "offered_qps": 200},
                 "nodes": 3, "driver_workers": 8, "duration_seconds": 60, "queue_capacity": 100,
                 "scheduled": 12000, "completed": 12000, "completed_in_window": 12000,
                 "errors": 0, "unexpected_errors": 0, "dropped": 0, "actual_qps": 200,
                 "p50_ms": 10, "p95_ms": 20, "p99_ms": 30, "cpu_seconds": None,
                 "allocated_bytes": 0, "heap_bytes": 0, "verdict": "pass",
                 **{key: 0 for key in INVARIANTS}}
        need(phase_result(phase, profile), "correct pass rejected")
        red = dict(phase, p99_ms=600, verdict="P99 exceeds budget")
        need(not phase_result(red, profile), "valid red accepted")
        for changed in (dict(red, verdict="pass"), dict(phase, queue_capacity=101),
                        dict(phase, completed=12001), dict(phase, active_runtimes_after=1)):
            try:
                phase_result(changed, profile)
            except ValueError:
                continue
            raise ValueError("invalid evidence accepted by self-test")
        print("PASS: offline numerical receipt self-test only; no artifact or workload validated")
        return
    need(args.artifact_root is not None and args.output is not None, "artifact_root and --output required")
    root, output = args.artifact_root.resolve(), args.output.resolve()
    need(output != root and root not in output.parents, "output must be outside the immutable artifact root")
    result = {"schema": "wukongim/fixed-host-offline-audit/v1", "candidate": args.candidate,
              "expected_run_id": args.expected_run_id, "issues": [], "blocks": [], "aa": [],
              "candidate_mixed_pass": False, "full_gate_pass": False,
              "historical_root_cause_confirmed": False,
              "limits": ["Median of window P99 values is not pooled P99.",
                         "Resolved resource cuts include warmup and observation overhead.",
                         "Selected metrics plus a body hash do not archive the full response.",
                         "Invisible host constraints and historical causality remain unproved."]}

    def section(name, run):
        try:
            return run()
        except (OSError, ValueError, KeyError, TypeError, IndexError, ZeroDivisionError,
                subprocess.SubprocessError) as error:
            result["issues"].append({"section": name, "error": str(error)[:1500]})
            return None

    def provenance():
        need(args.run_json is not None, "--run-json needed to bind GitHub run/head SHA")
        run = read_json(args.run_json)
        need(run.get("databaseId", run.get("id")) == args.expected_run_id, "wrong run id")
        need(run.get("headSha", run.get("head_sha")) == args.candidate, "wrong Workflow SHA")
        need(run.get("status") == "completed", "run is not terminal")
        roles = dict(ROLES, C=args.candidate)
        plan = read_json(root / "plan/sequence.json")
        need(plan["products"] == roles and plan["sequence"] ==
             [{"id": f"{i:02d}-{role}", "product": role} for i, role in enumerate(ORDER, 1)],
             "wrong fixed plan")
        need((root / "harness/profile.json").read_bytes() == profile_bytes, "profile bytes differ from H")
        harness = read_json(root / "harness/identity.json")
        driver_hash = sha(root / "harness/conversation_qps.test")
        need(harness["harness_sha"] == args.candidate and harness["profile_sha256"] == profile_hash
             and harness["driver_sha256"] == driver_hash, "driver identity mismatch")
        driver_build = command(root / "harness/build.command.json")
        need(driver_build["command"][:4] == ["go", "test", "-c", "-tags=e2e"],
             "wrong compiled driver command")
        events = [json.loads(line) for line in (root / "events.jsonl").read_text().splitlines() if line.strip()]
        commands = [row["command"] for row in events if row["event"] == "command_start"]
        need(sum(cmd[:3] == ["go", "build", "-tags=e2e"] for cmd in commands) == 3,
             "product build count is not three")
        need(sum(cmd[:4] == ["go", "test", "-c", "-tags=e2e"] for cmd in commands) == 1,
             "driver compilation count is not one")
        need([row["id"] for row in events if row["event"] == "block_start"] ==
             [f"{i:02d}-{role}" for i, role in enumerate(ORDER, 1)], "actual block order/count differs")
        binaries, sources = {}, set()
        for role, revision in roles.items():
            folder = root / "binaries" / role
            identity = read_json(folder / "identity.json")
            binaries[role] = sha(folder / "wukongim-e2e")
            need(identity["product_sha"] == revision and identity["binary_sha256"] == binaries[role],
                 f"{role} binary identity mismatch")
            need((folder / "binary.sha256").read_text().split()[0] == binaries[role], "digest file mismatch")
            info = (folder / "build.txt").read_text()
            need(f"vcs.revision={revision}" in info and "vcs.modified=false" in info
                 and "go1.25.11" in info and "GOOS=linux" in info and "GOARCH=amd64" in info,
                 f"{role} build provenance mismatch")
            build = command(folder / "build.command.json")
            need(build["command"][:3] == ["go", "build", "-tags=e2e"], "wrong product build command")
            need(build["cwd"] == identity["canonical_source_directory"], "build cwd differs from receipt")
            sources.add(identity["canonical_source_directory"])
        need(len(sources) == 1, "products built in different source directories")
        preflights = list(root.glob("conversation-fixed-host-preflight.jsonl")) + list(root.parent.glob("conversation-fixed-host-preflight.jsonl"))
        need(len(preflights) == 1, "missing/ambiguous Linux cleanup preflight")
        lines = [json.loads(line) for line in preflights[0].read_text().splitlines() if line.strip()]
        test = "TestConversationFixedHostTimeoutCleansChildProcessGroups"
        need(sum(row.get("Test") == test and row["Action"] == "pass" for row in lines) == 1
             and not any(row.get("Test") == test and row["Action"] in ("skip", "fail") for row in lines),
             "Linux cleanup preflight did not explicitly pass")
        affinity = read_json(root / "host.json")["affinity"]
        need(len(set(affinity)) == 4, "wrong host affinity")
        return roles, binaries, driver_hash, affinity

    identity = section("provenance", provenance)
    previous_end, common_cgroup = None, None
    for ordinal, role in enumerate(ORDER, 1):
        def block():
            nonlocal previous_end, common_cgroup
            need(identity is not None, "provenance not verified")
            roles, binaries, driver_hash, affinity = identity
            folder = root / "blocks" / f"{ordinal:02d}-{role}"
            report = read_json(folder / "report.json")
            expected = {"schema": "wukongim/conversation-mixed-attribution/v1", "complete": True,
                        "harness_sha": args.candidate, "harness_dirty": False,
                        "product_sha": roles[role], "binary_sha256": binaries[role],
                        "profile_sha256": profile_hash, "driver_gomaxprocs": 4,
                        "node_gomaxprocs": 2, "config": profile["stress_gate"]}
            for key, value in expected.items():
                need(report.get(key) == value, f"report identity mismatch: {key}")
            need(len(report["windows"]) == 3, "incomplete three-window diagnostic")
            fixed = report["fixed_environment"]
            need(read_json(folder / "fixed-environment.json") == fixed, "companion control differs")
            current_cgroup = fixed_control(fixed, report, driver_hash, binaries[role], affinity)
            if common_cgroup is not None:
                need(current_cgroup == common_cgroup, "cgroup constraints changed across blocks")
            common_cgroup = current_cgroup
            receipt = command(folder / "test.command.json", "TestConversationQPSMixedAttribution")
            need(previous_end is None or receipt["started_unix"] >= previous_end, "blocks overlap")
            previous_end = receipt["ended_unix"]
            passed, completed, cpu = True, 0, 0
            p99 = {endpoint: [] for endpoint in ENDPOINTS}
            for window in report["windows"]:
                need(abs((stamp(window["end"]) - stamp(window["start"])).total_seconds() - 60) < 1e-5,
                     "wrong window length")
                for key in INVARIANTS:
                    need(number(window.get(key), key) == 0, f"window activation/mutation: {key}")
                cpu += number(window["cpu_seconds"], "shared CPU", 1e-12)
                allocations = number(window["allocated_bytes"], "allocations", 1)
                number(window["heap_bytes"], "heap", 1)
                need([phase["case"]["endpoint"] for phase in window["phases"]] == ENDPOINTS,
                     "missing/reordered endpoints")
                n = 0
                for phase in window["phases"]:
                    accepted = phase_result(phase, profile)
                    passed = accepted and passed
                    n += phase["completed"]
                    p99[phase["case"]["endpoint"]].append(phase["p99_ms"])
                passed = n > 0 and allocations / n <= profile["stress_gate"]["max_allocated_bytes_per_request"]["mixed"] and passed
                completed += n
            need(completed > 0, "no completed requests")
            return {"ordinal": ordinal, "role": role, "numeric_pass": passed,
                    "report_sha256": sha(folder / "report.json"),
                    "server_cpu_ms_per_completed": 1000 * cpu / completed,
                    "window_p99_ms": p99,
                    "median_window_p99_ms": {ep: statistics.median(values) for ep, values in p99.items()}}
        value = section(f"block {ordinal:02d}-{role}", block)
        if value is not None:
            result["blocks"].append(value)
    if len(result["blocks"]) == 7:
        blocks = result["blocks"]
        for left, right in ((1, 2), (4, 5)):
            a, b = blocks[left - 1], blocks[right - 1]
            relative = lambda x, y: abs(x - y) / min(x, y) if min(x, y) > 0 else None
            cpu_difference = relative(a["server_cpu_ms_per_completed"], b["server_cpu_ms_per_completed"])
            differences = {ep: relative(a["median_window_p99_ms"][ep], b["median_window_p99_ms"][ep]) for ep in ENDPOINTS}
            result["aa"].append({"blocks": [left, right], "cpu_relative_difference": cpu_difference,
                                 "p99_relative_differences": differences,
                                 "pass": a["numeric_pass"] and b["numeric_pass"] and cpu_difference <= .10
                                 and all(value is not None and value <= .25 for value in differences.values())})
        result["candidate_mixed_pass"] = blocks[-1]["numeric_pass"]

    def gate():
        need(identity is not None, "provenance not verified")
        path = root / "gate/report.json"
        if not path.exists():
            result["gate_status"] = read_json(root / "summary.json")["gate"]["status"]
            return False
        report = read_json(path)
        need(report["source_sha"] == args.candidate and report["binary_sha256"] == identity[1]["C"],
             "gate did not use the frozen candidate")
        check = subprocess.run(["jq", "-e", "--arg", "sha", args.candidate,
                                "--arg", "profile_sha", profile_hash, "--slurpfile", "profile",
                                str(root / "harness/profile.json"),
                                frozen("scripts/validate-conversation-qps-report.jq").decode(), str(path)],
                               capture_output=True, text=True, timeout=30)
        result["gate_validator_exit"] = check.returncode
        if check.returncode != 0:
            result["gate_status"] = "rejected_or_partial"
            return False
        receipt = command(root / "gate/test.command.json", "TestConversationQPSReleaseGate")
        need(previous_end is not None and receipt["started_unix"] >= previous_end, "gate overlapped diagnostic")
        command(root / "gate/validation.command.json")
        need(len(report["phases"]) + sum(len(window["phases"]) for window in report["stress"]) == 20,
             "not all 20 endpoint results")
        result["gate_status"] = "pass"
        return True
    result["full_gate_pass"] = section("candidate gate", gate) is True
    result["evidence_valid"] = not result["issues"] and len(result["blocks"]) == 7
    result["all_aa_criteria_pass"] = result["evidence_valid"] and len(result["aa"]) == 2 and all(row["pass"] for row in result["aa"])
    result["candidate_regression_floor_pass"] = result["evidence_valid"] and result["candidate_mixed_pass"] and result["full_gate_pass"]
    result["checker_sha256"] = sha(Path(__file__))
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(result, indent=2, allow_nan=False) + "\n")
    print(json.dumps({key: result[key] for key in ("evidence_valid", "all_aa_criteria_pass", "candidate_regression_floor_pass")}))
    raise SystemExit(0 if result["evidence_valid"] else 2)


if __name__ == "__main__":
    main()
