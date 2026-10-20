#!/usr/bin/env python3
"""Run one predeclared, credential-free conversation comparison and candidate gate."""

import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import re
import signal
import subprocess
import time

BASELINE = "4d44b39dda6fdaa7dafb637ca6cfb718e9530f77"
PRIOR = "b469c6e5e2b4ff65611a8fe3389fb5034de3f152"
SEQUENCE = ("B", "B", "A", "B", "B", "A", "C")
DIAGNOSTIC_TEST = "TestConversationQPSMixedAttribution"
GATE_TEST = "TestConversationQPSReleaseGate"


def make_plan(candidate):
    return {
        "schema": "wukongim/conversation-fixed-host-plan/v1",
        "products": {"A": BASELINE, "B": PRIOR, "C": candidate},
        "sequence": [{"id": f"{i:02d}-{product}", "product": product}
                     for i, product in enumerate(SEQUENCE, 1)],
        "diagnostic_timeout_seconds": 720, "gate_timeout_seconds": 1080,
        "job_timeout_minutes": 120, "preparation_age_seconds": 180,
        "driver_gomaxprocs": 4, "node_gomaxprocs": 2,
        "gate": "candidate only; all three candidate windows must pass first",
        "interpretation": "A/A then ABBA; preserve all windows, never select or retry",
    }


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
    temporary.replace(path)


def digest(path):
    with path.open("rb") as handle:
        return hashlib.file_digest(handle, "sha256").hexdigest()


def session_members(session_id):
    """Find live members even when suite nodes use separate process groups."""
    members = []
    for entry in Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        try:
            fields = (entry / "stat").read_text().rsplit(") ", 1)[1].split()
            if int(fields[3]) == session_id and fields[0] != "Z":
                members.append(int(entry.name))
        except (FileNotFoundError, ProcessLookupError, PermissionError):
            pass
    return members


def cleanup_session(session_id):
    """Only signal this command's new session; never the runner's own session."""
    if session_id == os.getsid(0):
        raise RuntimeError("refusing to clean the orchestrator session")
    for sig in (signal.SIGTERM, signal.SIGKILL):
        deadline = time.monotonic() + 5
        while True:
            members = session_members(session_id)
            if not members:
                return True
            for pid in members:
                try:
                    # Recheck identity to avoid signalling a reused PID.
                    if os.getsid(pid) == session_id:
                        os.kill(pid, sig)
                except ProcessLookupError:
                    pass
            if time.monotonic() >= deadline:
                break
            time.sleep(0.05)
    return not session_members(session_id)


def run_command(command, cwd, env, log, timeout):
    """Bound one command and reclaim its session on success, failure or cancel."""
    log.parent.mkdir(parents=True, exist_ok=True)
    started = time.time()
    with log.open("w") as stream:
        process = subprocess.Popen(command, cwd=cwd, env=env, stdout=stream,
                                   stderr=subprocess.STDOUT, start_new_session=True)
        result = {"command": command, "cwd": str(cwd), "started_unix": started,
                  "session_id": process.pid, "timed_out": False, "exit_code": None}
        try:
            try:
                result["exit_code"] = process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                result["timed_out"] = True
                result["exit_code"] = 124
        finally:
            result["remaining_processes"] = session_members(process.pid)
            result["cleanup_complete"] = cleanup_session(process.pid)
            if process.poll() is None:
                process.wait(timeout=2)
            result["process_exit_code"] = process.returncode
            result["ended_unix"] = time.time()
            write_json(log.with_suffix(".command.json"), result)
    return result


def checked_output(command, cwd):
    return subprocess.check_output(command, cwd=cwd, text=True, timeout=30).strip()


def assert_clean_source(repo, candidate):
    if checked_output(["git", "rev-parse", "HEAD"], repo) != candidate:
        raise RuntimeError("candidate does not equal harness HEAD")
    if checked_output(["git", "status", "--porcelain"], repo):
        raise RuntimeError("harness tree is dirty")
    subprocess.run(["git", "merge-base", "--is-ancestor", "origin/main", candidate],
                   cwd=repo, check=True, timeout=30)


def cgroup_constraints():
    """Resolve cgroup-v2 membership and all visible inherited static limits."""
    membership = Path("/proc/self/cgroup").read_text()
    mountinfo = Path("/proc/self/mountinfo").read_text()
    unified = [line.split(":", 2)[2] for line in membership.splitlines()
               if line.startswith("0::")]
    mounts = [line.split() for line in mountinfo.splitlines() if " - cgroup2 " in line]
    if len(unified) != 1 or len(mounts) != 1:
        raise RuntimeError("fixed-host validation requires one resolvable cgroup-v2 mount")
    root, mount = Path(mounts[0][3]), Path(mounts[0][4])
    relative = Path(unified[0]).relative_to(root)
    if ".." in relative.parts:
        raise RuntimeError("cgroup membership escapes visible mount")
    current = mount / relative
    controls = {}
    for directory in (current, *current.parents):
        if directory != mount and mount not in directory.parents:
            break
        values = {}
        for name in ("cpu.max", "cpu.weight", "cpuset.cpus.effective", "cpuset.mems.effective",
                     "memory.max", "memory.high", "pids.max"):
            path = directory / name
            values[name] = path.read_text().strip() if path.exists() else None
        if directory != mount and (values["cpu.max"] is None or values["memory.max"] is None):
            raise RuntimeError("missing non-root cgroup CPU or memory limit")
        if values["cpu.max"] is not None:
            quota, period = values["cpu.max"].split()
            if quota != "max" and int(quota) / int(period) < 4:
                raise RuntimeError("visible CPU quota is below four CPUs")
        controls[str(directory)] = values
        if directory == mount:
            break
    if not controls or not controls[str(current)].get("cpuset.cpus.effective"):
        raise RuntimeError("missing effective cgroup CPU constraints")
    return {"membership": membership, "mountinfo": mountinfo, "resolved": controls,
            "scope": "Visible mount ancestry only; root absent limits are unknown, not zero; hidden host constraints are not inferred."}


def validate_diagnostic(report, product, candidate, binary_sha, profile_sha, profile):
    """Reject incomplete/unsafe evidence; return numeric verdict without hiding red windows."""
    expected = {"schema": "wukongim/conversation-mixed-attribution/v1",
                "complete": True, "product_sha": product, "harness_sha": candidate,
                "harness_dirty": False, "binary_sha256": binary_sha,
                "profile_sha256": profile_sha, "driver_gomaxprocs": 4,
                "node_gomaxprocs": 2, "config": profile["stress_gate"]}
    for key, value in expected.items():
        if report.get(key) != value:
            raise RuntimeError(f"diagnostic receipt mismatch: {key}")
    fixed = report.get("fixed_environment", {})
    if (fixed.get("schema") != "wukongim/conversation-fixed-host/v1"
            or fixed.get("valid") is not True or fixed.get("target_age_seconds") != 180
            or not 0 <= fixed.get("preparation_age_seconds", -1) <= 180
            or fixed.get("release_age_seconds", 0) < 180):
        raise RuntimeError("missing or rejected fixed environment control")
    windows = report.get("windows", [])
    if len(windows) != 3:
        raise RuntimeError("missing diagnostic windows")
    numeric_pass = True
    for window in windows:
        for field in ("runtime_loads", "membership_writes", "active_runtimes_before", "active_runtimes_after"):
            if window.get(field) != 0:
                raise RuntimeError(f"unexpected window mutation: {field}")
        phases = window.get("phases", [])
        if [p.get("case", {}).get("endpoint") for p in phases] != ["/conversation/list", "/conversation/sync"]:
            raise RuntimeError("missing or reordered endpoints")
        for phase in phases:
            if phase.get("unexpected_errors") != 0:
                raise RuntimeError("unexpected request failure")
            if not isinstance(phase.get("verdict"), str) or not phase["verdict"]:
                raise RuntimeError("missing numeric verdict")
            numeric_pass = numeric_phase_pass(phase, profile) and phase["verdict"] == "pass" and numeric_pass
        completed = sum(phase["completed"] for phase in phases)
        ceiling = profile["stress_gate"]["max_allocated_bytes_per_request"]["mixed"]
        numeric_pass = numeric_pass and completed > 0 and window["allocated_bytes"] / completed <= ceiling
    return numeric_pass



def numeric_phase_pass(phase, profile):
    """Recompute fixed numeric floors, separately from the driver's reason text."""
    cfg = profile["stress_gate"]
    endpoint = phase["case"]["endpoint"]
    rate = cfg["mixed_list_qps"] if endpoint == "/conversation/list" else cfg["mixed_sync_qps"]
    expected = {"case": {"endpoint": endpoint, "page_size": 100, "offered_qps": rate},
                "nodes": 3, "driver_workers": cfg["mixed_workers_per_endpoint"],
                "queue_capacity": max(cfg["mixed_workers_per_endpoint"], math.ceil(rate * profile["max_p99_ms"] / 1000)),
                "duration_seconds": cfg["mixed_seconds"]}
    for key, value in expected.items():
        if phase.get(key) != value:
            raise RuntimeError(f"mixed workload contract mismatch: {key}")
    for field in ("scheduled", "completed", "completed_in_window", "actual_qps", "p99_ms", "errors", "dropped"):
        value = phase.get(field)
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0:
            raise RuntimeError(f"missing/invalid numeric field: {field}")
    if phase["completed"] > phase["scheduled"] or phase["completed_in_window"] > phase["completed"]:
        raise RuntimeError("inconsistent completion counters")
    return (phase["scheduled"] == rate * cfg["mixed_seconds"]
            and phase["completed"] == phase["scheduled"]
            and phase["errors"] == 0 and phase["dropped"] == 0
            and phase["actual_qps"] == phase["completed_in_window"] / cfg["mixed_seconds"]
            and phase["actual_qps"] >= rate * profile["min_completion_ratio"]
            and 0 < phase["p99_ms"] <= profile["max_p99_ms"])


def execute(candidate, output):
    repo = Path(__file__).resolve().parent.parent
    if output == repo or repo in output.parents:
        raise RuntimeError("evidence directory must be outside the clean harness checkout")
    if output.exists():
        raise RuntimeError("refusing to reuse an evidence directory")
    output.mkdir(parents=True)
    plan = make_plan(candidate)
    write_json(output / "plan/sequence.json", plan)
    summary = {"schema": "wukongim/conversation-fixed-host-summary/v1", "candidate_sha": candidate,
               "complete": False, "passed": False, "blocks": [], "gate": {"status": "not_started"}}

    def event(kind, **values):
        with (output / "events.jsonl").open("a") as handle:
            handle.write(json.dumps({"time_unix": time.time(), "event": kind, **values}) + "\n")
        write_json(output / "summary.json", summary)
        if kind in ("plan_frozen", "block_start", "block_end", "gate_start", "completed", "aborted"):
            print(json.dumps({"event": kind, **values}), flush=True)

    def command(args, cwd, log, timeout, extra=None):
        event("command_start", command=args, log=str(log.relative_to(output)))
        result = run_command(args, cwd, env | (extra or {}), log, timeout)
        event("command_end", result=result)
        if result["exit_code"] != 0 or not result["cleanup_complete"] or result["remaining_processes"]:
            raise RuntimeError(f"command failed or leaked processes: {log.relative_to(output)}")
        return result

    event("plan_frozen")
    try:
        if os.environ.get("FIXED_HOST_WORKFLOW_SHA") != candidate:
            raise RuntimeError("candidate must equal the workflow source SHA")
        assert_clean_source(repo, candidate)
        if platform.system() != "Linux" or platform.machine() != "x86_64":
            raise RuntimeError("Linux AMD64 required")
        affinity = sorted(os.sched_getaffinity(0))
        if len(affinity) != 4:
            raise RuntimeError("exactly four allowed logical CPUs required")
        os.sched_setaffinity(0, affinity)
        env = {key: value for key, value in os.environ.items()
               if not key.startswith("WK_") and key not in
               ("GOFLAGS", "GOEXPERIMENT", "GODEBUG", "GOGC", "GOMEMLIMIT", "GOOS", "GOARCH", "GOTOOLCHAIN")}
        env.update({"GOWORK": "off", "GOMAXPROCS": "4", "GOTOOLCHAIN": "local", "GOENV": "off"})
        if checked_output(["go", "version"], repo) != "go version go1.25.11 linux/amd64":
            raise RuntimeError("exact Go 1.25.11 toolchain required")
        constraints = cgroup_constraints()
        host = {"affinity": affinity, "kernel": platform.uname()._asdict(), "cgroup": constraints,
                "lscpu": json.loads(checked_output(["lscpu", "--json"], repo)),
                "topology": checked_output(["lscpu", "--extended=CPU,CORE,SOCKET,NODE,ONLINE"], repo),
                "taskset": checked_output(["taskset", "-pc", str(os.getpid())], repo),
                "exclusive_physical_host": False}
        write_json(output / "host.json", host)
        build = output.parent / (output.name + "-build")
        if build.exists():
            raise RuntimeError("canonical product build directory already exists")
        command(["git", "clone", "--no-hardlinks", "--no-checkout", str(repo), str(build)], repo, output / "build-clone.log", 60)
        binaries = {}
        for role, sha in plan["products"].items():
            directory = output / "binaries" / role
            directory.mkdir(parents=True)
            command(["git", "checkout", "--detach", sha], build, directory / "checkout.log", 60)
            if checked_output(["git", "rev-parse", "HEAD"], build) != sha or checked_output(["git", "status", "--porcelain"], build):
                raise RuntimeError("product checkout identity mismatch")
            binary = directory / "wukongim-e2e"
            command(["go", "build", "-tags=e2e", "-o", str(binary), "./cmd/wukongim"], build, directory / "build.log", 240)
            binary_sha = digest(binary)
            (directory / "binary.sha256").write_text(f"{binary_sha}  wukongim-e2e\n")
            build_info = checked_output(["go", "version", "-m", str(binary)], repo)
            (directory / "build.txt").write_text(build_info + "\n")
            if f"vcs.revision={sha}" not in build_info or "vcs.modified=false" not in build_info:
                raise RuntimeError("built binary does not attest clean exact product")
            write_json(directory / "identity.json", {"product_sha": sha, "binary_sha256": binary_sha,
                       "canonical_source_directory": str(build), "go_version": "go1.25.11"})
            binaries[role] = (binary, binary_sha)
        assert_clean_source(repo, candidate)
        driver = output / "harness/conversation_qps.test"
        driver.parent.mkdir(parents=True)
        command(["go", "test", "-c", "-tags=e2e", "-o", str(driver), "./test/e2e/message/conversation_qps"], repo, output / "harness/build.log", 240)
        profile_path = repo / "test/e2e/message/conversation_qps/profile.json"
        profile = json.loads(profile_path.read_text())
        profile_sha = digest(profile_path)
        driver_sha = digest(driver)
        write_json(output / "harness/identity.json", {"harness_sha": candidate, "driver_sha256": driver_sha,
                   "profile_sha256": profile_sha, "scenario_directory": str(profile_path.parent)})
        (output / "harness/build.txt").write_text(checked_output(["go", "version", "-m", str(driver)], repo) + "\n")
        (output / "harness/profile.json").write_bytes(profile_path.read_bytes())

        def check_environment():
            assert_clean_source(repo, candidate)
            if sorted(os.sched_getaffinity(0)) != affinity or cgroup_constraints() != constraints:
                raise RuntimeError("CPU affinity or cgroup constraints changed")
            if digest(driver) != driver_sha or digest(profile_path) != profile_sha:
                raise RuntimeError("driver or profile changed")
            for binary, sha in binaries.values():
                if digest(binary) != sha:
                    raise RuntimeError("product binary changed")

        for block in plan["sequence"]:
            check_environment()
            role = block["product"]
            directory = output / "blocks" / block["id"]
            report_path = directory / "report.json"
            status = dict(block, status="started", numeric_pass=None)
            summary["blocks"].append(status)
            event("block_start", **block)
            command(["taskset", "-c", ",".join(map(str, affinity)), str(driver),
                     f"-test.run=^{DIAGNOSTIC_TEST}$", "-test.count=1", "-test.timeout=12m", "-test.v"],
                    profile_path.parent, directory / "test.log", 730,
                    {"WK_E2E_CONVERSATION_MIXED_ATTRIBUTION": "1", "WK_E2E_CONVERSATION_FIXED_HOST": "1",
                     "WK_E2E_PRODUCT_SHA": plan["products"][role], "WK_E2E_BINARY": str(binaries[role][0]),
                     "WK_E2E_CONVERSATION_MIXED_REPORT": str(report_path)})
            report = json.loads(report_path.read_text())
            status["numeric_pass"] = validate_diagnostic(report, plan["products"][role], candidate,
                                                         binaries[role][1], profile_sha, profile)
            status.update(status="collected", exit_code=0, product_sha=plan["products"][role],
                          harness_sha=candidate, binary_sha256=binaries[role][1],
                          profile_sha256=profile_sha, control_valid=True,
                          windows=[{"cpu_seconds": w["cpu_seconds"], "allocated_bytes": w["allocated_bytes"],
                                    "phases": [{key: phase.get(key) for key in
                                                ("case", "actual_qps", "p99_ms", "request_p99_ms",
                                                 "driver_wait_p99_ms", "errors", "dropped", "verdict")}
                                               for phase in w["phases"]]} for w in report["windows"]])
            check_environment()
            event("block_end", **status)
        summary["diagnostics_complete"] = True
        if not summary["blocks"][-1]["numeric_pass"]:
            summary["gate"]["status"] = "candidate_windows_rejected"
            raise RuntimeError("candidate_windows_rejected")
        summary["gate"]["status"] = "started"
        event("gate_start")
        gate_report = output / "gate/report.json"
        command(["taskset", "-c", ",".join(map(str, affinity)), str(driver),
                 f"-test.run=^{GATE_TEST}$", "-test.count=1", "-test.timeout=18m", "-test.v"],
                profile_path.parent, output / "gate/test.log", 1090,
                {"WK_E2E_CONVERSATION_QPS": "1", "WK_E2E_BINARY": str(binaries["C"][0]),
                 "WK_E2E_CONVERSATION_QPS_REPORT": str(gate_report)})
        command(["jq", "-e", "--arg", "sha", candidate, "--arg", "profile_sha", profile_sha,
                 "--slurpfile", "profile", str(profile_path), "-f",
                 str(repo / "scripts/validate-conversation-qps-report.jq"), str(gate_report)],
                repo, output / "gate/validation.log", 30)
        if json.loads(gate_report.read_text())["binary_sha256"] != binaries["C"][1]:
            raise RuntimeError("gate binary identity mismatch")
        check_environment()
        summary.update(complete=True, passed=True)
        summary["gate"]["status"] = "pass"
        event("completed")
    except BaseException as error:
        if summary["blocks"] and summary["blocks"][-1]["status"] == "started":
            failed = summary["blocks"][-1]
            failed["status"] = "aborted"
            receipt = output / "blocks" / failed["id"] / "test.command.json"
            if receipt.exists():
                failed["exit_code"] = json.loads(receipt.read_text()).get("exit_code")
        if summary["gate"]["status"] == "started":
            summary["gate"]["status"] = "failed"
        summary["failure"] = str(error)
        event("aborted", failure=str(error))
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--output-dir", type=Path)
    parser.add_argument("--plan", action="store_true", help="Print the fixed plan without commands or files")
    args = parser.parse_args()
    if not re.fullmatch("[0-9a-f]{40}", args.candidate_sha):
        parser.error("candidate-sha must be an exact lowercase 40-character commit SHA")
    if args.plan:
        print(json.dumps(make_plan(args.candidate_sha), indent=2))
        return
    if args.output_dir is None or not args.output_dir.is_absolute():
        parser.error("output-dir must be an absolute new directory")

    def interrupted(signum, frame):
        raise RuntimeError(f"orchestrator interrupted by signal {signum}")
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGALRM, interrupted)
    # Leave six minutes inside the job limit for bounded cleanup and artifact upload.
    signal.alarm(114 * 60)
    execute(args.candidate_sha, args.output_dir.resolve())


if __name__ == "__main__":
    main()
