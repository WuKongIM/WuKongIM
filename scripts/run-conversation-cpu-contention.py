#!/usr/bin/env python3
"""Collect one frozen S/O/D/D/O/S CPU-placement diagnosis; never a release gate."""

import argparse
from datetime import datetime
import importlib.util
import json
import math
import os
from pathlib import Path
import platform
import re
import signal
import sys
import time

# Import only the owned standard-library command/session helpers. Do not create
# Python cache files inside the exact clean harness checkout.
sys.dont_write_bytecode = True
_spec = importlib.util.spec_from_file_location("fixed_host", Path(__file__).with_name("run-conversation-fixed-host.py"))
fixed = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(fixed)

PRODUCT = "ef798352c40a94e57f768f76b2f4713712f827fe"
EXPECTED_BINARY = "8c3c79f2853b76be7f83dbd606404bf6ab1fdf34ce9370e82c056b389d03c57d"
BUILD_DIRECTORY = Path("/home/runner/work/_temp/conversation-fixed-host-build")
SEQUENCE = ("shared", "overlap", "disjoint", "disjoint", "overlap", "shared")
LETTERS = {"shared": "S", "overlap": "O", "disjoint": "D"}
TEST = "TestConversationQPSCPUPlacement"
SCENARIO = "test/e2e/message/conversation_qps"


def make_plan(harness):
    return {
        "schema": "wukongim/conversation-cpu-contention-plan/v1",
        "product_sha": PRODUCT, "expected_binary_sha256": EXPECTED_BINARY,
        "harness_sha": harness, "canonical_source_directory": str(BUILD_DIRECTORY),
        "sequence": [{"id": f"{i:02d}-{LETTERS[mode]}", "mode": mode}
                     for i, mode in enumerate(SEQUENCE, 1)],
        "diagnostic_timeout_seconds": 720, "script_timeout_minutes": 84,
        "job_timeout_minutes": 90, "preparation_age_seconds": 180,
        "run_release_matrix": False, "product_builds": 1, "driver_builds": 1,
        "load": {"list_qps": 200, "sync_qps": 60, "workers_per_endpoint": 8,
                 "windows": 3, "window_seconds": 60, "profile": "byte-identical to product C"},
        "placement": {"preparation": "all four CPUs shared until 180-second release",
                      "S": "driver all four CPUs/GOMAXPROCS 4; nodes same four/GOMAXPROCS 2",
                      "O": "driver one CPU/GOMAXPROCS 1; nodes fixed three CPUs/GOMAXPROCS 2 including driver CPU",
                      "D": "driver sibling CPU/GOMAXPROCS 1; nodes same three CPUs/GOMAXPROCS 2 excluding driver CPU"},
        "analysis": {"pairs": [["02-O", "03-D"], ["05-O", "04-D"]],
                     "primary_screen": "Both pairs, per endpoint: D median of three driver-wait P99 values decreases >=25% and >=1ms, with total P99 in the same direction; a screen, not a causal verdict.",
                     "repeats": "Report S/S, O/O, D/D variation and every window; do not pool P99 or select favorable windows.",
                     "limitations": "Numeric rejects remain. Latency includes attempted requests (including errors), but excludes dropped arrivals. Equal per-role widths do not equalize total eligible CPUs (union 3 vs 4); SMT, cache, visible cgroup and hidden host contention remain. S/D is not a single-variable contrast.",
                     "verdict": "Collection completion is not performance acceptance or historical root-cause confirmation."},
    }


def placement_masks(mode, topology):
    """Check the exact guest-visible two-core/SMT2 topology before interpretation."""
    cpus = sorted(row["cpu"] for row in topology)
    if len(cpus) != 4 or len(set(cpus)) != 4:
        raise RuntimeError("placement requires four distinct CPUs")
    if len({row["package_id"] for row in topology}) != 1 or len({row["numa_node"] for row in topology}) != 1:
        raise RuntimeError("placement requires one known package and NUMA node")
    if any(row["numa_node"] < 0 or row["core_id"] < 0 for row in topology):
        raise RuntimeError("unknown topology")
    cores = {}
    for row in topology:
        cores.setdefault(row["core_id"], []).append(row["cpu"])
    if len(cores) != 2 or any(len(v) != 2 for v in cores.values()):
        raise RuntimeError("placement requires exactly two SMT2 cores")
    for row in topology:
        if sorted(row["thread_siblings"]) != sorted(cores[row["core_id"]]):
            raise RuntimeError("unproven sibling topology")
    if mode == "shared":
        return cpus, cpus
    first = next(row for row in topology if row["cpu"] == cpus[0])
    sibling = next(cpu for cpu in first["thread_siblings"] if cpu != cpus[0])
    nodes = [cpu for cpu in cpus if cpu != sibling]
    if mode not in ("overlap", "disjoint"):
        raise RuntimeError("unknown placement mode")
    return [cpus[0] if mode == "overlap" else sibling], nodes


def timestamp(value):
    result = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if result.tzinfo is None:
        raise RuntimeError("timestamp is missing timezone")
    return result


def affinity_list(value):
    if not isinstance(value, str) or not value or len(value) > 4096:
        raise RuntimeError("invalid sampled affinity")
    cpus = []
    for part in value.split(","):
        bounds = part.split("-")
        if len(bounds) not in (1, 2) or any(not re.fullmatch("[0-9]+", n) for n in bounds):
            raise RuntimeError("invalid sampled affinity range")
        first, last = int(bounds[0]), int(bounds[-1])
        if not 0 <= first <= last < 1024:
            raise RuntimeError("sampled CPU outside bounded topology")
        cpus.extend(range(first, last + 1))
    if len(set(cpus)) != len(cpus):
        raise RuntimeError("duplicate sampled CPU")
    return sorted(cpus)


def validate_samples(window, identities, driver_mask, node_mask):
    """Require original-window coverage, not merely a valid partial collector."""
    sampler = window.get("cpu_samples", {})
    points = sampler.get("points", [])
    finished = sampler.get("finished_offset_ns", 0)
    # The original scheduler's final 200-QPS arrival is at 59.995 s; draining
    # may finish before 60 s. Do not lengthen that workload to satisfy evidence.
    if (sampler.get("schema") != "wukongim/cpu-window-samples/v1" or sampler.get("valid") is not True
            or sampler.get("duration_ns") != 60_000_000_000 or not 60 <= len(points) <= 70
            or not 59_995_000_000 <= finished <= 75_000_000_000):
        raise RuntimeError("missing or incomplete original-window CPU observations")
    if (not window.get("start") or sampler.get("start") != window["start"]
            or sampler.get("stop_reason") != "caller_stop" or sampler.get("missed_slots") != 0):
        raise RuntimeError("CPU observations do not bind the original complete window")
    prior_end = 0
    for slot, point in enumerate(points):
        offset, duration = point.get("start_offset_ns", -1), point.get("read_duration_ns", -1)
        if (point.get("valid") is not True or point.get("slot") != slot or duration < 0
                or not slot * 1_000_000_000 <= offset < (slot + 1) * 1_000_000_000
                or offset < prior_end or offset + duration > finished
                or point.get("deadline_lag_ns") != offset - slot * 1_000_000_000):
            raise RuntimeError("incomplete or inconsistent sample timing coverage")
        prior_end = offset + duration
        processes = point.get("processes", [])
        if len(processes) != 4:
            raise RuntimeError("incomplete CPU observation point")
        for index, process in enumerate(processes):
            if process.get("valid") is not True or any(
                    process.get(section, {}).get(key) != identities[index][key]
                    for section in ("expected", "stat") for key in ("pid", "starttime_ticks")):
                raise RuntimeError("CPU observation process identity changed")
            mask = driver_mask if index == 0 else node_mask
            if affinity_list(process["expected"].get("affinity")) != mask or affinity_list(process.get("affinity")) != mask:
                raise RuntimeError("sampled process mask differs from placement")
            threads = process.get("threads", [])
            if (not 1 <= len(threads) <= 256 or process.get("enumerated_threads") != len(threads)
                    or process.get("valid_threads") != len(threads)
                    or len({t["tid"] for t in threads}) != len(threads)):
                raise RuntimeError("incomplete sampled all-thread coverage")
            for thread in threads:
                if (thread.get("valid") is not True or thread.get("tid", 0) <= 0
                        or thread.get("starttime_ticks", 0) <= 0 or affinity_list(thread.get("affinity")) != mask):
                    raise RuntimeError("sampled thread identity or mask mismatch")


def validate_report(report, mode, harness, profile_sha, profile, affinity):
    """Separate complete controlled collection from the unchanged numeric gates."""
    expected = {"schema": "wukongim/conversation-cpu-placement/v1", "complete": True,
                "product_sha": PRODUCT, "harness_sha": harness, "harness_dirty": False,
                "binary_sha256": EXPECTED_BINARY, "profile_sha256": profile_sha,
                "driver_gomaxprocs": 4 if mode == "shared" else 1,
                "node_gomaxprocs": 2, "config": profile["stress_gate"]}
    for key, value in expected.items():
        if report.get(key) != value:
            raise RuntimeError(f"CPU contention report mismatch: {key}")
    control = report.get("fixed_environment", {})
    if (control.get("schema") != "wukongim/conversation-cpu-placement-preparation/v1"
            or control.get("valid") is not True or control.get("target_age_seconds") != 180
            or not 0 <= control.get("preparation_age_seconds", -1) <= 180
            or not 180 <= control.get("release_age_seconds", 0) < math.inf):
        raise RuntimeError("missing or rejected preparation age control")
    placement = report.get("cpu_placement", {})
    if placement.get("valid") is not True or placement.get("mode") != LETTERS[mode] or placement.get("host_cpus") != affinity:
        raise RuntimeError("invalid placement receipt")
    if not 0 <= placement.get("duration_ns", -1) <= 500_000_000 or not 1 <= placement.get("attempts", 0) <= 8:
        raise RuntimeError("placement exceeded its bounded application interval")
    driver_mask, node_mask = placement_masks(mode, placement.get("topology", []))
    if sorted(row["cpu"] for row in placement["topology"]) != affinity:
        raise RuntimeError("placement topology does not match host affinity")
    if placement.get("driver_cpus") != driver_mask or placement.get("node_cpus") != node_mask:
        raise RuntimeError("placement masks do not match frozen plan")
    if not (timestamp(control["released_at"]) <= timestamp(placement["started_at"]) <= timestamp(placement["finished_at"])):
        raise RuntimeError("placement did not follow preparation release")
    identities = [control["driver"], *control["nodes"]]
    if len(identities) != 4 or len({p["pid"] for p in identities}) != 4:
        raise RuntimeError("four unique owned processes required")
    for field in ("initial", "applied"):
        processes = placement.get(field, [])
        if len(processes) != 4:
            raise RuntimeError("incomplete all-thread placement receipt")
        for index, process in enumerate(processes):
            if process["pid"] <= 0 or process["starttime_ticks"] <= 0 or any(process[key] != identities[index][key] for key in ("pid", "starttime_ticks")):
                raise RuntimeError("placement process identity changed")
            mask = affinity if field == "initial" else (driver_mask if index == 0 else node_mask)
            threads = process.get("threads", [])
            if not threads or len({t["tid"] for t in threads}) != len(threads):
                raise RuntimeError("missing or duplicated placement thread")
            for thread in threads:
                if thread["tid"] <= 0 or thread["starttime_ticks"] <= 0 or thread["affinity"] != mask:
                    raise RuntimeError("placement thread identity or mask mismatch")
    windows = report.get("windows", [])
    if len(windows) != 3:
        raise RuntimeError("missing CPU contention windows")
    numeric_pass = True
    for window in windows:
        for field in ("runtime_loads", "membership_writes", "active_runtimes_before", "active_runtimes_after"):
            if window.get(field) != 0:
                raise RuntimeError(f"unexpected window mutation: {field}")
        validate_samples(window, identities, driver_mask, node_mask)
        phases = window.get("phases", [])
        if [p.get("case", {}).get("endpoint") for p in phases] != ["/conversation/list", "/conversation/sync"]:
            raise RuntimeError("missing or reordered endpoints")
        for phase in phases:
            if phase.get("unexpected_errors") != 0:
                raise RuntimeError("unexpected request failure")
            if not isinstance(phase.get("verdict"), str) or not phase["verdict"]:
                raise RuntimeError("missing numeric verdict")
            numeric = fixed.numeric_phase_pass(phase, profile)
            numeric_pass = numeric_pass and numeric and phase["verdict"] == "pass"
        completed = sum(p["completed"] for p in phases)
        allocated = window.get("allocated_bytes")
        if isinstance(allocated, bool) or not isinstance(allocated, (int, float)) or not math.isfinite(allocated) or allocated < 0:
            raise RuntimeError("missing allocated bytes")
        numeric_pass = numeric_pass and completed > 0 and allocated / completed <= profile["stress_gate"]["max_allocated_bytes_per_request"]["mixed"]
    return numeric_pass


def execute(harness, output):
    repo = Path(__file__).resolve().parent.parent
    if output == repo or repo in output.parents or output == BUILD_DIRECTORY or BUILD_DIRECTORY in output.parents:
        raise RuntimeError("evidence must be outside clean source directories")
    if output.exists():
        raise RuntimeError("refusing to reuse evidence directory")
    output.mkdir(parents=True)
    plan = make_plan(harness)
    fixed.write_json(output / "plan/sequence.json", plan)
    summary = {"schema": "wukongim/conversation-cpu-contention-summary/v1", "product_sha": PRODUCT,
               "harness_sha": harness, "complete": False, "numeric_pass": None,
               "release_qualification": False, "blocks": []}

    def event(kind, **values):
        with (output / "events.jsonl").open("a") as stream:
            stream.write(json.dumps({"time_unix": time.time(), "event": kind, **values}) + "\n")
        fixed.write_json(output / "summary.json", summary)
        if kind in ("plan_frozen", "block_start", "block_end", "completed", "aborted"):
            print(json.dumps({"event": kind, **values}), flush=True)

    def command(args, cwd, log, timeout, extra=None):
        event("command_start", command=args, log=str(log.relative_to(output)))
        result = fixed.run_command(args, cwd, env | (extra or {}), log, timeout)
        event("command_end", result=result)
        if result["exit_code"] != 0 or not result["cleanup_complete"] or result["remaining_processes"]:
            raise RuntimeError(f"command failed or leaked processes: {log.relative_to(output)}")
        return result

    event("plan_frozen")
    try:
        if os.environ.get("CPU_CONTENTION_WORKFLOW_SHA") != harness:
            raise RuntimeError("harness must equal workflow SHA")
        fixed.assert_clean_source(repo, harness)
        if platform.system() != "Linux" or platform.machine() != "x86_64":
            raise RuntimeError("Linux AMD64 required")
        affinity = sorted(os.sched_getaffinity(0))
        if len(affinity) != 4:
            raise RuntimeError("exactly four allowed logical CPUs required")
        os.sched_setaffinity(0, affinity)
        env = {key: value for key, value in os.environ.items()
               if not key.startswith("WK_") and key not in ("GOFLAGS", "GOEXPERIMENT", "GODEBUG", "GOGC", "GOMEMLIMIT", "GOOS", "GOARCH", "GOTOOLCHAIN")}
        env.update({"GOWORK": "off", "GOMAXPROCS": "4", "GOTOOLCHAIN": "local", "GOENV": "off"})
        if fixed.checked_output(["go", "version"], repo) != "go version go1.25.11 linux/amd64":
            raise RuntimeError("exact Go 1.25.11 toolchain required")
        constraints = fixed.cgroup_constraints()
        host = {"affinity": affinity, "kernel": platform.uname()._asdict(), "cgroup": constraints,
                "lscpu": json.loads(fixed.checked_output(["lscpu", "--json"], repo)),
                "topology": fixed.checked_output(["lscpu", "--extended=CPU,CORE,SOCKET,NODE,ONLINE"], repo),
                "exclusive_physical_host": False}
        fixed.write_json(output / "host.json", host)
        if BUILD_DIRECTORY.exists():
            raise RuntimeError("canonical product build directory already exists")
        command(["git", "clone", "--no-hardlinks", "--no-checkout", str(repo), str(BUILD_DIRECTORY)], repo, output / "build-clone.log", 60)
        directory = output / "binaries/C"
        command(["git", "checkout", "--detach", PRODUCT], BUILD_DIRECTORY, directory / "checkout.log", 60)
        if fixed.checked_output(["git", "rev-parse", "HEAD"], BUILD_DIRECTORY) != PRODUCT or fixed.checked_output(["git", "status", "--porcelain"], BUILD_DIRECTORY):
            raise RuntimeError("product checkout identity mismatch")
        command(["git", "merge-base", "--is-ancestor", PRODUCT, harness], repo, output / "source-ancestry.log", 30)
        binary = directory / "wukongim-e2e"
        command(["go", "build", "-tags=e2e", "-o", str(binary), "./cmd/wukongim"], BUILD_DIRECTORY, directory / "build.log", 240)
        binary_sha = fixed.digest(binary)
        (directory / "binary.sha256").write_text(f"{binary_sha}  wukongim-e2e\n")
        build_info = fixed.checked_output(["go", "version", "-m", str(binary)], repo)
        (directory / "build.txt").write_text(build_info + "\n")
        fixed.write_json(directory / "identity.json", {"product_sha": PRODUCT, "binary_sha256": binary_sha,
                         "expected_binary_sha256": EXPECTED_BINARY, "canonical_source_directory": str(BUILD_DIRECTORY), "go_version": "go1.25.11"})
        if binary_sha != EXPECTED_BINARY:
            raise RuntimeError("product binary digest mismatch; no measurement or substitute digest permitted")
        if f"vcs.revision={PRODUCT}" not in build_info or "vcs.modified=false" not in build_info:
            raise RuntimeError("binary does not attest clean exact product")
        fixed.assert_clean_source(repo, harness)
        profile_path = repo / SCENARIO / "profile.json"
        if profile_path.read_bytes() != (BUILD_DIRECTORY / SCENARIO / "profile.json").read_bytes():
            raise RuntimeError("harness profile differs from frozen product C")
        driver = output / "harness/conversation_qps.test"
        command(["go", "test", "-c", "-tags=e2e", "-o", str(driver), "./" + SCENARIO], repo, output / "harness/build.log", 240)
        profile = json.loads(profile_path.read_text())
        profile_sha, driver_sha = fixed.digest(profile_path), fixed.digest(driver)
        fixed.write_json(output / "harness/identity.json", {"harness_sha": harness, "driver_sha256": driver_sha,
                         "profile_sha256": profile_sha, "scenario_directory": str(profile_path.parent)})
        (output / "harness/profile.json").write_bytes(profile_path.read_bytes())
        (output / "harness/build.txt").write_text(fixed.checked_output(["go", "version", "-m", str(driver)], repo) + "\n")

        def check_environment():
            fixed.assert_clean_source(repo, harness)
            if sorted(os.sched_getaffinity(0)) != affinity or fixed.cgroup_constraints() != constraints:
                raise RuntimeError("visible host affinity or cgroup controls changed")
            if fixed.digest(binary) != EXPECTED_BINARY or fixed.digest(driver) != driver_sha or fixed.digest(profile_path) != profile_sha:
                raise RuntimeError("product, harness or profile bytes changed")

        topology = None
        for block in plan["sequence"]:
            check_environment()
            directory = output / "blocks" / block["id"]
            report_path = directory / "report.json"
            status = dict(block, status="started", numeric_pass=None)
            summary["blocks"].append(status)
            event("block_start", **block)
            command(["taskset", "-c", ",".join(map(str, affinity)), str(driver),
                     f"-test.run=^{TEST}$", "-test.count=1", "-test.timeout=12m", "-test.v"],
                    profile_path.parent, directory / "test.log", 730,
                    {"WK_E2E_CONVERSATION_MIXED_ATTRIBUTION": "1", "WK_E2E_CONVERSATION_FIXED_HOST": "1",
                     "WK_E2E_CONVERSATION_CPU_PLACEMENT": block["mode"], "WK_E2E_PRODUCT_SHA": PRODUCT,
                     "WK_E2E_BINARY": str(binary), "WK_E2E_CONVERSATION_MIXED_REPORT": str(report_path)})
            report = json.loads(report_path.read_text())
            status["numeric_pass"] = validate_report(report, block["mode"], harness, profile_sha, profile, affinity)
            observed = report["cpu_placement"]["topology"]
            if topology is not None and topology != observed:
                raise RuntimeError("CPU topology changed between blocks")
            topology = observed
            status.update(status="collected", exit_code=0, product_sha=PRODUCT, harness_sha=harness,
                          binary_sha256=EXPECTED_BINARY, profile_sha256=profile_sha, control_valid=True,
                          windows=[{"cpu_seconds": w["cpu_seconds"], "allocated_bytes": w["allocated_bytes"],
                                    "phases": [{key: phase.get(key) for key in
                                                ("case", "actual_qps", "p99_ms", "request_p99_ms", "driver_wait_p99_ms", "errors", "dropped", "verdict")}
                                               for phase in w["phases"]]} for w in report["windows"]])
            check_environment()
            event("block_end", **status)
        summary.update(complete=True, numeric_pass=all(b["numeric_pass"] for b in summary["blocks"]))
        event("completed", numeric_pass=summary["numeric_pass"], release_qualification=False)
    except BaseException as error:
        if summary["blocks"] and summary["blocks"][-1]["status"] == "started":
            failed = summary["blocks"][-1]
            failed["status"] = "aborted"
            receipt = output / "blocks" / failed["id"] / "test.command.json"
            if receipt.exists():
                failed["exit_code"] = json.loads(receipt.read_text()).get("exit_code")
        summary["failure"] = str(error)
        event("aborted", failure=str(error))
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--harness-sha", required=True)
    parser.add_argument("--output-dir", type=Path)
    parser.add_argument("--plan", action="store_true", help="Print frozen plan without commands or files")
    args = parser.parse_args()
    if not re.fullmatch("[0-9a-f]{40}", args.harness_sha):
        parser.error("harness-sha must be an exact lowercase 40-character SHA")
    if args.plan:
        print(json.dumps(make_plan(args.harness_sha), indent=2))
        return
    if args.output_dir is None or not args.output_dir.is_absolute():
        parser.error("output-dir must be an absolute new directory")

    def interrupted(signum, frame):
        raise RuntimeError(f"orchestrator interrupted by signal {signum}")
    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGALRM):
        signal.signal(sig, interrupted)
    signal.alarm(84 * 60)
    execute(args.harness_sha, args.output_dir.resolve())


if __name__ == "__main__":
    main()
