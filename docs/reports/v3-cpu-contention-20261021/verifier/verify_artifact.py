#!/usr/bin/env python3
"""Independently inspect one CPU-placement artifact without executing its contents."""
import argparse
from datetime import datetime
import hashlib
import json
import math
from pathlib import Path
import re

HARNESS = "07ed85a22bdd2ebf75b776a92e71f21c02d2623f"
PRODUCT = "ef798352c40a94e57f768f76b2f4713712f827fe"
BINARY = "8c3c79f2853b76be7f83dbd606404bf6ab1fdf34ce9370e82c056b389d03c57d"
PROFILE = "1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1"
BUILD = "/home/runner/work/_temp/conversation-fixed-host-build"
BLOCKS = [("01-S", "shared", "S"), ("02-O", "overlap", "O"), ("03-D", "disjoint", "D"), ("04-D", "disjoint", "D"), ("05-O", "overlap", "O"), ("06-S", "shared", "S")]
PREFLIGHTS = {"cleanup": ["TestConversationFixedHostTimeoutCleansChildProcessGroups"], "placement": ["TestCPUPlacementLinuxAllThreads", "TestCPUWindowSamplerLinuxPreflight"]}


def sha(path):
    with path.open("rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def read(path):
    return json.loads(path.read_text())


def utc(value):
    t = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if t.tzinfo is None:
        raise ValueError("UTC offset missing")
    return t


def mask(value):
    if isinstance(value, list):
        values = value
    else:
        values = []
        for item in value.split(","):
            if not re.fullmatch(r"[0-9]+(?:-[0-9]+)?", item):
                raise ValueError("malformed affinity")
            parts = [int(x) for x in item.split("-")]
            if not 0 <= parts[0] <= parts[-1] < 1024:
                raise ValueError("unbounded affinity")
            values.extend(range(parts[0], parts[-1] + 1))
    if not values or len(values) != len(set(values)) or any(type(x) is not int or x < 0 or x >= 1024 for x in values):
        raise ValueError("duplicate, empty or invalid mask")
    return sorted(values)


def proc_identity(raw):
    left, tail = raw.rsplit(") ", 1)
    fields = tail.split()
    return int(left.split(" (", 1)[0]), int(fields[19])


def status_mask(raw):
    return mask(next(line.split(":", 1)[1].strip() for line in raw.splitlines() if line.startswith("Cpus_allowed_list:")))


class Audit:
    def __init__(self):
        self.checks = 0
        self.failures = []
        self.blocks = []
        self.commands = []
        self.preflight = []
        self.manifest = []

    def check(self, condition, message):
        self.checks += 1
        if not condition:
            self.failures.append(message)

    def section(self, name, fn):
        try:
            fn()
        except (OSError, ValueError, TypeError, KeyError, IndexError, AttributeError, ZeroDivisionError, StopIteration) as exc:
            self.failures.append(f"{name}: missing/malformed evidence: {exc}")

    def file(self, path, label):
        self.check(not path.is_symlink(), f"symlink evidence is unsupported: {label}")
        self.manifest.append({"path": label, "bytes": path.stat().st_size, "sha256": sha(path)})

    def elf(self, path):
        with path.open("rb") as stream:
            h = stream.read(20)
        self.check(h[:6] == b"\x7fELF\x02\x01" and int.from_bytes(h[18:20], "little") == 62, f"not Linux AMD64 ELF: {path.name}")


def check_window(a, w, expected_processes, target_masks, profile, label):
    for key in ("runtime_loads", "membership_writes", "active_runtimes_before", "active_runtimes_after"):
        a.check(w.get(key) == 0, f"{label}: {key}")
    samples = w["cpu_samples"]
    points = samples["points"]
    a.check(samples["schema"] == "wukongim/cpu-window-samples/v1" and samples["valid"] is True, f"{label}: sampler valid/schema")
    a.check(samples["duration_ns"] == 60_000_000_000 and samples["start"] == w["start"], f"{label}: sampler window binding")
    a.check(samples["stop_reason"] == "caller_stop" and samples["missed_slots"] == 0, f"{label}: sampler stop/missed")
    a.check(59_995_000_000 <= samples["finished_offset_ns"] <= 75_000_000_000, f"{label}: sampler final coverage")
    a.check(60 <= len(points) <= 70, f"{label}: sampler point count")
    previous_end = 0
    exits = 0
    thread_observations = 0
    for slot, point in enumerate(points):
        prefix = f"{label}/point{slot}"
        begin, duration = point["start_offset_ns"], point["read_duration_ns"]
        a.check(point["valid"] is True and point["slot"] == slot, f"{prefix}: invalid or discontinuous")
        a.check(slot * 10**9 <= begin < (slot+1) * 10**9 and duration >= 0 and previous_end <= begin and begin + duration <= samples["finished_offset_ns"], f"{prefix}: timing bounds")
        a.check(point["deadline_lag_ns"] == begin-slot*10**9, f"{prefix}: inconsistent deadline lag")
        previous_end = begin + duration
        processes = point["processes"]
        a.check(len(processes) == 4, f"{prefix}: four processes missing")
        for index, process in enumerate(processes):
            identity = expected_processes[index]
            a.check(process["valid"] is True, f"{prefix}/p{index}: invalid process")
            for field in ("expected", "stat"):
                a.check(all(process[field][k] == identity[k] for k in ("pid", "starttime_ticks")), f"{prefix}/p{index}: {field} identity")
            a.check(mask(process["expected"]["affinity"]) == target_masks[index] and mask(process["affinity"]) == target_masks[index], f"{prefix}/p{index}: process masks")
            for filekey in ("stat_file", "stat_after_threads"):
                a.check(proc_identity(process[filekey]["content"]) == (identity["pid"], identity["starttime_ticks"]), f"{prefix}/p{index}: raw process lifetime")
            a.check(status_mask(process["status"]["content"]) == target_masks[index], f"{prefix}/p{index}: raw process affinity")
            threads = process["threads"]
            a.check(1 <= len(threads) <= 256 and process["enumerated_threads"] == process["valid_threads"] == len(threads), f"{prefix}/p{index}: all-thread coverage")
            a.check(len({t["tid"] for t in threads}) == len(threads), f"{prefix}/p{index}: duplicate TID")
            exits += len(process.get("exited_or_unobserved_threads", []))
            for t in threads:
                a.check(t["valid"] is True and t["tid"] > 0 and t["starttime_ticks"] > 0 and mask(t["affinity"]) == target_masks[index], f"{prefix}/p{index}/t{t['tid']}: thread mask/identity")
                for filekey in ("stat_before", "stat_after"):
                    a.check(proc_identity(t[filekey]["content"]) == (t["tid"], t["starttime_ticks"]), f"{prefix}/p{index}/t{t['tid']}: raw thread lifetime")
                a.check(status_mask(t["status"]["content"]) == target_masks[index], f"{prefix}/p{index}/t{t['tid']}: raw thread affinity")
                thread_observations += 1
    phases = w["phases"]
    a.check([p["case"]["endpoint"] for p in phases] == ["/conversation/list", "/conversation/sync"], f"{label}: endpoint order")
    numeric = True
    outcomes = []
    cfg = profile["stress_gate"]
    for phase in phases:
        endpoint = phase["case"]["endpoint"]
        qps = cfg["mixed_list_qps"] if endpoint.endswith("list") else cfg["mixed_sync_qps"]
        offered = qps * cfg["mixed_seconds"]
        a.check(phase["case"] == {"endpoint": endpoint, "page_size": 100, "offered_qps": qps}, f"{label}/{endpoint}: case changed")
        a.check(phase["nodes"] == 3 and phase["driver_workers"] == 8 and phase["duration_seconds"] == 60 and phase["queue_capacity"] == max(8, math.ceil(qps*profile["max_p99_ms"]/1000)), f"{label}/{endpoint}: load changed")
        a.check(phase["unexpected_errors"] == 0, f"{label}/{endpoint}: unexpected errors")
        a.check(isinstance(phase["verdict"], str) and bool(phase["verdict"]), f"{label}/{endpoint}: missing numeric verdict")
        for key in ("scheduled", "completed", "completed_in_window", "errors", "dropped", "p99_ms", "actual_qps"):
            a.check(type(phase[key]) in (float, int) and math.isfinite(phase[key]) and phase[key] >= 0, f"{label}/{endpoint}: invalid {key}")
        a.check(phase["completed_in_window"] <= phase["completed"] <= phase["scheduled"], f"{label}/{endpoint}: completion counters")
        passed = (phase["scheduled"] == phase["completed"] == offered and phase["errors"] == phase["dropped"] == 0 and 0 < phase["p99_ms"] <= profile["max_p99_ms"] and phase["actual_qps"] == phase["completed_in_window"]/60 and phase["actual_qps"] >= qps*profile["min_completion_ratio"] and phase["verdict"] == "pass")
        numeric = numeric and passed
        outcomes.append({"endpoint": endpoint, "numeric_pass": passed, **{k: phase.get(k) for k in ("verdict", "actual_qps", "p99_ms", "errors", "dropped")}})
    complete = sum(p["completed"] for p in phases)
    allocated = w["allocated_bytes"]
    a.check(type(allocated) in (int, float) and math.isfinite(allocated) and allocated >= 0, f"{label}: invalid allocation")
    numeric = numeric and complete > 0 and allocated/complete <= cfg["max_allocated_bytes_per_request"]["mixed"]
    return {"numeric_pass": numeric, "points": len(points), "thread_observations": thread_observations, "confirmed_exit_observations": exits, "phases": outcomes}


def verify(args):
    a = Audit()
    root = args.artifact_root.resolve()
    profile = read(Path(__file__).with_name("expected-profile.json"))
    a.check(sha(Path(__file__).with_name("expected-profile.json")) == PROFILE, "local sealed profile changed")
    host = {}
    summary = {}

    def run_identity():
        r = read(args.run_json)
        a.check(str(r.get("id", r.get("databaseId"))) == "39467558163", "run ID")
        a.check(r.get("run_attempt", r.get("attempt")) == 1, "run attempt")
        a.check(r.get("head_sha", r.get("headSha")) == HARNESS, "run harness SHA")
        a.check(r.get("status") == "completed" and r.get("conclusion") == "success", "run not successfully completed; inspect partial evidence")
        path = r.get("path")
        if path is not None:
            a.check(path == ".github/workflows/conversation-qps-diagnose.yml", "run workflow path")
    a.section("run", run_identity)

    def identities():
        nonlocal host, summary
        plan = read(root/"plan/sequence.json")
        a.check(plan["schema"] == "wukongim/conversation-cpu-contention-plan/v1", "plan schema")
        a.check(plan["harness_sha"] == HARNESS and plan["product_sha"] == PRODUCT and plan["expected_binary_sha256"] == BINARY, "plan frozen identities")
        a.check(plan["canonical_source_directory"] == BUILD, "plan canonical path")
        a.check(plan["sequence"] == [{"id": b, "mode": m} for b, m, _ in BLOCKS], "plan sequence")
        a.check(plan["run_release_matrix"] is False and plan["product_builds"] == plan["driver_builds"] == 1, "plan builds/gate")
        for path, expected in ((root/"binaries/C/wukongim-e2e", BINARY), (root/"harness/profile.json", PROFILE)):
            a.check(sha(path) == expected, f"actual bytes mismatch: {path.name}")
        b = read(root/"binaries/C/identity.json")
        a.check(b["product_sha"] == PRODUCT and b["binary_sha256"] == b["expected_binary_sha256"] == BINARY and b["canonical_source_directory"] == BUILD and b["go_version"] == "go1.25.11", "product identity receipt")
        a.check((root/"binaries/C/binary.sha256").read_text().split()[0] == BINARY, "binary.sha256 receipt")
        build = (root/"binaries/C/build.txt").read_text()
        a.check(all(x in build for x in ("go1.25.11", "vcs.revision="+PRODUCT, "vcs.modified=false", "GOARCH=amd64", "GOOS=linux")), "product build metadata")
        h = read(root/"harness/identity.json")
        a.check(h["harness_sha"] == HARNESS and h["profile_sha256"] == PROFILE and h["driver_sha256"] == sha(root/"harness/conversation_qps.test"), "driver identity and actual bytes")
        a.elf(root/"binaries/C/wukongim-e2e")
        a.elf(root/"harness/conversation_qps.test")
        a.check((root/"harness/profile.json").read_bytes() == Path(__file__).with_name("expected-profile.json").read_bytes(), "profile exact bytes")
        host = read(root/"host.json")
        summary = read(root/"summary.json")
        a.check(summary["complete"] is True and summary["release_qualification"] is False and summary["harness_sha"] == HARNESS and summary["product_sha"] == PRODUCT, "summary completion/identity")
        a.check([b["id"] for b in summary["blocks"]] == [b[0] for b in BLOCKS], "summary complete sequence")
        a.check(sorted(p.name for p in (root/"blocks").iterdir() if p.is_dir()) == [b[0] for b in BLOCKS], "actual block directory set")
        a.check(not (root/"gate").exists(), "unexpected release-gate output")
    a.section("identities", identities)

    topology = None
    for index, (block, mode, code) in enumerate(BLOCKS):
        def check_block():
            nonlocal topology
            r = read(root/"blocks"/block/"report.json")
            label = block
            a.check(all(r[k] == v for k,v in {"schema":"wukongim/conversation-cpu-placement/v1", "complete":True, "product_sha":PRODUCT, "harness_sha":HARNESS, "harness_dirty":False, "binary_sha256":BINARY, "profile_sha256":PROFILE, "driver_gomaxprocs":4 if code=="S" else 1, "node_gomaxprocs":2, "config":profile["stress_gate"]}.items()), f"{label}: identity/load report")
            f, p = r["fixed_environment"], r["cpu_placement"]
            a.check(f["schema"] == "wukongim/conversation-cpu-placement-preparation/v1" and f["valid"] is True and f["target_age_seconds"] == 180 and 0 <= f["preparation_age_seconds"] <= 180 and f["release_age_seconds"] >= 180, f"{label}: preparation")
            a.check(p["valid"] is True and p["mode"] == code and 0 <= p["duration_ns"] <= 500_000_000 and 1 <= p["attempts"] <= 8, f"{label}: placement validity/bounds")
            a.check(utc(f["released_at"]) <= utc(p["started_at"]) <= utc(p["finished_at"]), f"{label}: release/application order")
            cpus = mask(p["host_cpus"])
            a.check(cpus == mask(host["affinity"]) and len(cpus) == 4, f"{label}: four-host CPUs")
            rows = p["topology"]
            a.check([row["cpu"] for row in rows] == cpus and len({row["numa_node"] for row in rows}) == 1 and len({row["package_id"] for row in rows}) == 1, f"{label}: socket/NUMA topology")
            cores = {}
            for row in rows:
                cores.setdefault((row["package_id"], row["core_id"]), []).append(row["cpu"])
            a.check(len(cores) == 2 and all(len(v) == 2 for v in cores.values()), f"{label}: two SMT2 cores")
            a.check(all(mask(row["thread_siblings"]) == sorted(cores[(row["package_id"],row["core_id"])]) for row in rows), f"{label}: siblings")
            if topology is not None:
                a.check(topology == rows, f"{label}: topology drift")
            topology = rows
            sibling = next(cpu for cpu in rows[0]["thread_siblings"] if cpu != cpus[0])
            driver = cpus if code=="S" else [cpus[0] if code=="O" else sibling]
            nodes = cpus if code=="S" else [cpu for cpu in cpus if cpu != sibling]
            a.check(p["driver_cpus"] == driver and p["node_cpus"] == nodes, f"{label}: frozen placement masks")
            expected = [f["driver"], *f["nodes"]]
            a.check(len(expected)==4 and len({x["pid"] for x in expected})==4, f"{label}: owned processes")
            for stage in ("initial", "applied"):
                a.check(len(p[stage]) == 4, f"{label}: {stage} process count")
                for i, process in enumerate(p[stage]):
                    target = cpus if stage=="initial" else (driver if i==0 else nodes)
                    a.check(all(process[k] == expected[i][k] for k in ("pid","starttime_ticks")), f"{label}: {stage} process identity")
                    threads=process["threads"]
                    a.check(bool(threads) and len({t["tid"] for t in threads}) == len(threads), f"{label}: {stage} thread count")
                    a.check(all(t["tid"]>0 and t["starttime_ticks"]>0 and mask(t["affinity"])==target for t in threads), f"{label}: {stage} thread masks")
                    a.check(all(proc_identity(t["proc_stat"]) == (t["tid"],t["starttime_ticks"]) for t in threads), f"{label}: {stage} raw thread lifetime")
            a.check(len(r["windows"]) == 3, f"{label}: original window count")
            windows = [check_window(a,w,expected,[driver,nodes,nodes,nodes],profile,f"{label}/w{i+1}") for i,w in enumerate(r["windows"])]
            numeric = all(w["numeric_pass"] for w in windows)
            a.check(summary["blocks"][index]["numeric_pass"] == numeric and summary["blocks"][index]["status"]=="collected", f"{label}: numeric rejection retention/summary")
            a.blocks.append({"id":block,"mode":mode,"numeric_pass":numeric,"windows":windows})
        a.section(block, check_block)

    def preflights():
        for suffix, names in PREFLIGHTS.items():
            path = args.preflight_dir/("conversation-cpu-contention-preflight."+suffix+".jsonl")
            events = [json.loads(line) for line in path.read_text().splitlines() if line.strip()]
            for name in names:
                passed = sum(e.get("Test")==name and e.get("Action")=="pass" for e in events)
                forbidden = [e.get("Action") for e in events if e.get("Test")==name and e.get("Action") in ("skip","fail")]
                a.check(passed==1 and not forbidden, f"preflight: exact non-skipped pass missing: {name}")
                a.preflight.append({"test":name,"pass_count":passed,"rejected_events":forbidden})
            a.check(any(e.get("Action")=="pass" and "Test" not in e for e in events) and not any(e.get("Action")=="fail" for e in events), f"preflight package failure: {suffix}")
    a.section("Linux preflight", preflights)

    def commands():
        receipts = sorted(root.rglob("*.command.json"))
        a.check(len(receipts)==11, "expected 5 build/source and 6 test command receipts")
        builds, drivers = 0, 0
        for path in receipts:
            r=read(path); cmd=r["command"]
            a.check(r["exit_code"]==0 and r["process_exit_code"]==0 and r["timed_out"] is False and r["cleanup_complete"] is True and r["remaining_processes"]==[], f"command failure/leak: {path.relative_to(root)}")
            if cmd[:3]==["go","build","-tags=e2e"]:
                builds+=1; a.check(r["cwd"]==BUILD and cmd[-1]=="./cmd/wukongim", "product build path/flags")
            if cmd[:4]==["go","test","-c","-tags=e2e"]:
                drivers+=1
            if path.parent.parent.name=="blocks":
                a.check(cmd[:2]==["taskset","-c"] and "-test.run=^TestConversationQPSCPUPlacement$" in cmd and "-test.count=1" in cmd and "-test.timeout=12m" in cmd, f"block command changed: {path.parent.name}")
            a.commands.append({"file":str(path.relative_to(root)),"exit_code":r["exit_code"],"cleanup_complete":r["cleanup_complete"]})
        a.check(builds==drivers==1, "product/driver build counts")
        events=[json.loads(line) for line in (root/"events.jsonl").read_text().splitlines() if line.strip()]
        for kind in ("block_start","block_end"):
            a.check([e["id"] for e in events if e["event"]==kind]==[b[0] for b in BLOCKS], f"event sequence: {kind}")
        a.check(sum(e["event"]=="command_start" for e in events)==len(receipts)==sum(e["event"]=="command_end" for e in events), "command event/receipt count")
        a.check(sum(e["event"]=="completed" for e in events)==1 and not any(e["event"] in ("aborted","gate_start") for e in events), "terminal event state")
    a.section("commands", commands)
    for path in sorted(root.rglob("*")):
        if path.is_file():
            a.section("file hash",lambda p=path:a.file(p,str(p.relative_to(root))))
    for path in sorted(args.preflight_dir.glob("conversation-cpu-contention-preflight.*")):
        if path.is_file():
            a.section("preflight hash",lambda p=path:a.file(p,"../"+p.name))
    if len(a.blocks)==6:
        a.check(summary.get("numeric_pass")==all(b["numeric_pass"] for b in a.blocks), "summary overall numeric status")
    return a


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("--artifact-root",type=Path,required=True)
    p.add_argument("--preflight-dir",type=Path,required=True)
    p.add_argument("--run-json",type=Path,required=True)
    p.add_argument("--output-dir",type=Path,required=True)
    args=p.parse_args()
    output=args.output_dir.resolve()
    if output.exists() or output==args.artifact_root.resolve() or args.artifact_root.resolve() in output.parents:
        p.error("output-dir must be new and outside artifact-root")
    output.mkdir(parents=True)
    a=verify(args)
    result={"schema":"wukongim/cpu-contention-offline-integrity/v1","run_id":39467558163,"attempt":1,"harness_sha":HARNESS,"product_sha":PRODUCT,"integrity_pass":not a.failures,"checks":a.checks,"failures":a.failures,"blocks":a.blocks,"preflights":a.preflight,"commands":a.commands,"numeric_pass":all(b["numeric_pass"] for b in a.blocks) if len(a.blocks)==6 else None,"release_qualification":False,"artifact_code_executed":False}
    (output/"verification.json").write_text(json.dumps(result,indent=2)+"\n")
    (output/"file-digests.json").write_text(json.dumps(a.manifest,indent=2)+"\n")
    print(json.dumps({k:result[k] for k in ("integrity_pass","checks","failures","numeric_pass","release_qualification")},indent=2))
    raise SystemExit(0 if not a.failures else 1)

if __name__=="__main__":
    main()
